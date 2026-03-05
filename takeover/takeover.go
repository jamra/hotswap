// Package takeover provides hot module reloading support for Go HTTP servers
// using socket takeover via SCM_RIGHTS file descriptor passing.
package takeover

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

const (
	// EnvTakeover is set to "1" when the process should perform a takeover
	EnvTakeover = "HMR_TAKEOVER"

	// EnvSocketPath can override the default socket path
	EnvSocketPath = "HMR_SOCKET_PATH"

	// DefaultSocketPath is the default Unix socket path for takeover
	DefaultSocketPath = "/tmp/gohmr-takeover.sock"

	// DefaultDrainTimeout is the default time to wait for connections to drain
	DefaultDrainTimeout = 30 * time.Second
)

// Options configures the takeover behavior
type Options struct {
	// DrainTimeout is how long to wait for connections to drain before force closing
	DrainTimeout time.Duration

	// SocketPath is the Unix socket path for takeover communication
	SocketPath string

	// OnTakeover is called when a takeover is initiated (new process is ready)
	OnTakeover func()

	// OnDrainStart is called when draining begins
	OnDrainStart func()

	// Logger is an optional logger for takeover events
	Logger *log.Logger
}

func (o *Options) defaults() {
	if o.DrainTimeout == 0 {
		o.DrainTimeout = DefaultDrainTimeout
	}
	if o.SocketPath == "" {
		o.SocketPath = os.Getenv(EnvSocketPath)
		if o.SocketPath == "" {
			o.SocketPath = DefaultSocketPath
		}
	}
	if o.Logger == nil {
		o.Logger = log.Default()
	}
}

// Server wraps an HTTP server with takeover support
type Server struct {
	httpServer     *http.Server
	listener       net.Listener
	takeoverServer *TakeoverServer
	opts           Options
	wg             sync.WaitGroup
	shutdownCh     chan struct{}
}

// NewServer creates a new server with takeover support
func NewServer(addr string, handler http.Handler, opts Options) *Server {
	opts.defaults()

	return &Server{
		httpServer: &http.Server{
			Addr:    addr,
			Handler: handler,
		},
		opts:       opts,
		shutdownCh: make(chan struct{}),
	}
}

// IsTakeoverMode returns true if the process should perform a takeover
func IsTakeoverMode() bool {
	return os.Getenv(EnvTakeover) == "1"
}

// ListenAndServe starts an HTTP server with takeover support.
// If HMR_TAKEOVER=1, it will connect to an existing process and take over its socket.
// Otherwise, it starts normally and listens for incoming takeover requests.
func ListenAndServe(addr string, handler http.Handler, opts Options) error {
	s := NewServer(addr, handler, opts)
	return s.ListenAndServe()
}

// ListenAndServe starts the server
func (s *Server) ListenAndServe() error {
	if IsTakeoverMode() {
		return s.runWithTakeover()
	}
	return s.runNormal()
}

func (s *Server) runNormal() error {
	// Create a new listener
	listener, err := net.Listen("tcp", s.httpServer.Addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", s.httpServer.Addr, err)
	}
	s.listener = listener

	log.Printf("[gohmr] listening on %s", s.httpServer.Addr)

	// Start takeover server in background
	s.takeoverServer = NewTakeoverServer(s.opts.SocketPath)
	s.takeoverServer.RegisterListener(listener)
	s.takeoverServer.SetDrainCallback(s.startDrain)

	if err := s.takeoverServer.Start(); err != nil {
		listener.Close()
		return fmt.Errorf("failed to start takeover server: %w", err)
	}

	log.Printf("[gohmr] takeover server listening on %s", s.opts.SocketPath)

	// Handle signals
	s.setupSignalHandler()

	// Serve requests
	err = s.httpServer.Serve(listener)
	if err == http.ErrServerClosed {
		// Wait for shutdown to complete
		<-s.shutdownCh
		return nil
	}
	return err
}

func (s *Server) runWithTakeover() error {
	log.Printf("[gohmr] takeover mode: connecting to existing process")

	// Connect to existing process
	client := NewTakeoverClient(s.opts.SocketPath)
	if err := client.Connect(); err != nil {
		return fmt.Errorf("failed to connect to existing process: %w", err)
	}

	// Perform handshake and receive listeners
	listeners, err := client.PerformTakeover()
	if err != nil {
		client.Close()
		return fmt.Errorf("takeover failed: %w", err)
	}

	if len(listeners) == 0 {
		client.Close()
		return fmt.Errorf("no listeners received during takeover")
	}

	// Use the first listener (for single-address servers)
	s.listener = listeners[0]
	log.Printf("[gohmr] took over listener: %s", s.listener.Addr())

	// Start takeover server for future takeovers
	s.takeoverServer = NewTakeoverServer(s.opts.SocketPath)
	s.takeoverServer.RegisterListener(s.listener)
	s.takeoverServer.SetDrainCallback(s.startDrain)

	if err := s.takeoverServer.Start(); err != nil {
		s.listener.Close()
		client.Close()
		return fmt.Errorf("failed to start takeover server: %w", err)
	}

	// Handle signals
	s.setupSignalHandler()

	// Start accepting in a goroutine
	serveCh := make(chan error, 1)
	go func() {
		serveCh <- s.httpServer.Serve(s.listener)
	}()

	// Signal old process to drain
	if err := client.SignalDrain(); err != nil {
		log.Printf("[gohmr] warning: failed to signal drain: %v", err)
	}
	client.Close()

	log.Printf("[gohmr] takeover complete, serving requests")

	// Wait for server to finish
	err = <-serveCh
	if err == http.ErrServerClosed {
		<-s.shutdownCh
		return nil
	}
	return err
}

func (s *Server) startDrain() {
	log.Printf("[gohmr] starting graceful shutdown (timeout: %s)", s.opts.DrainTimeout)

	if s.opts.OnDrainStart != nil {
		s.opts.OnDrainStart()
	}

	// Stop the takeover server
	if s.takeoverServer != nil {
		s.takeoverServer.Stop()
	}

	// Graceful shutdown with timeout
	ctx, cancel := context.WithTimeout(context.Background(), s.opts.DrainTimeout)
	defer cancel()

	if err := s.httpServer.Shutdown(ctx); err != nil {
		log.Printf("[gohmr] shutdown error: %v", err)
	}

	close(s.shutdownCh)
	log.Printf("[gohmr] shutdown complete")
}

func (s *Server) setupSignalHandler() {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigCh
		log.Printf("[gohmr] received signal: %v", sig)
		s.startDrain()
	}()
}

// Shutdown gracefully shuts down the server
func (s *Server) Shutdown(ctx context.Context) error {
	if s.takeoverServer != nil {
		s.takeoverServer.Stop()
	}
	return s.httpServer.Shutdown(ctx)
}
