// Command gohmr provides hot module reloading for Go web servers.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jamra/hotswap/builder"
	"github.com/jamra/hotswap/takeover"
	"github.com/jamra/hotswap/watcher"
)

func main() {
	// Parse flags
	watchDirs := flag.String("watch", ".", "Directories to watch (comma-separated)")
	buildCmd := flag.String("build", "", "Build command (default: go build -o ./tmp/main .)")
	output := flag.String("o", "./tmp/main", "Output binary path")
	socketPath := flag.String("socket", takeover.DefaultSocketPath, "Takeover socket path")
	verbose := flag.Bool("v", false, "Verbose output")
	flag.Parse()

	// Get build args from remaining arguments
	buildArgs := flag.Args()

	// Setup logger
	logger := log.New(os.Stdout, "", log.LstdFlags)
	if !*verbose {
		logger.SetFlags(0)
	}

	// Create the CLI
	cli := &CLI{
		watchDirs:  strings.Split(*watchDirs, ","),
		buildCmd:   *buildCmd,
		output:     *output,
		buildArgs:  buildArgs,
		socketPath: *socketPath,
		logger:     logger,
		verbose:    *verbose,
	}

	if err := cli.Run(); err != nil {
		logger.Fatalf("Error: %v", err)
	}
}

// CLI orchestrates the watch-build-reload loop
type CLI struct {
	watchDirs  []string
	buildCmd   string
	output     string
	buildArgs  []string
	socketPath string
	logger     *log.Logger
	verbose    bool

	builder *builder.Builder
	watcher *watcher.Watcher
	process *Process

	mu sync.Mutex
}

// Run starts the CLI main loop
func (c *CLI) Run() error {
	c.logger.Println("[gohmr] Starting hot reload...")

	// Initialize builder - use first watch dir as the package to build
	buildArgs := c.buildArgs
	if len(buildArgs) == 0 {
		// Default to building the first watch directory
		buildArgs = []string{"./" + c.watchDirs[0]}
	}

	c.builder = builder.New(builder.Options{
		Output:    c.output,
		BuildArgs: buildArgs,
		Logger:    c.logger,
	})

	// Initial build
	c.logger.Println("[gohmr] Initial build...")
	if err := c.builder.Build(); err != nil {
		return fmt.Errorf("initial build failed: %w", err)
	}

	// Start the process
	c.logger.Println("[gohmr] Starting process...")
	if err := c.startProcess(false); err != nil {
		return fmt.Errorf("failed to start process: %w", err)
	}

	// Initialize watcher
	w, err := watcher.New(watcher.Options{
		Paths:  c.watchDirs,
		Logger: c.logger,
	})
	if err != nil {
		return fmt.Errorf("failed to create watcher: %w", err)
	}
	c.watcher = w

	w.SetOnChange(c.onFileChange)

	if err := w.Start(); err != nil {
		return fmt.Errorf("failed to start watcher: %w", err)
	}
	defer w.Stop()

	c.logger.Printf("[gohmr] Watching for changes in: %s", strings.Join(c.watchDirs, ", "))

	// Wait for signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	c.logger.Println("[gohmr] Shutting down...")

	// Stop the process
	c.mu.Lock()
	if c.process != nil {
		c.process.Stop()
	}
	c.mu.Unlock()

	return nil
}

func (c *CLI) onFileChange() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.logger.Println("[gohmr] File change detected, rebuilding...")

	// Rebuild
	if err := c.builder.Build(); err != nil {
		c.logger.Printf("[gohmr] Build failed: %v", err)
		return
	}

	// Check if there's an existing process to takeover from
	_, err := os.Stat(c.socketPath)
	hasTakeover := err == nil

	if hasTakeover {
		c.logger.Println("[gohmr] Starting new process with takeover...")
	} else {
		c.logger.Println("[gohmr] Starting new process...")
	}

	// Start new process
	if err := c.startProcess(hasTakeover); err != nil {
		c.logger.Printf("[gohmr] Failed to start process: %v", err)
		return
	}
}

func (c *CLI) startProcess(doTakeover bool) error {
	// Get absolute path to binary
	binPath, err := filepath.Abs(c.output)
	if err != nil {
		return fmt.Errorf("failed to get absolute path: %w", err)
	}

	// Build environment
	env := os.Environ()
	if doTakeover {
		env = append(env, fmt.Sprintf("%s=1", takeover.EnvTakeover))
	}
	env = append(env, fmt.Sprintf("%s=%s", takeover.EnvSocketPath, c.socketPath))

	// Create the process
	proc := &Process{
		cmd:    exec.Command(binPath),
		logger: c.logger,
	}
	proc.cmd.Env = env
	proc.cmd.Stdout = os.Stdout
	proc.cmd.Stderr = os.Stderr

	if err := proc.Start(); err != nil {
		return err
	}

	// If this is a takeover, the old process will drain and exit
	// We keep reference to the new process
	c.process = proc

	return nil
}

// Process manages a running server process
type Process struct {
	cmd    *exec.Cmd
	logger *log.Logger
	done   chan struct{}
}

// Start starts the process
func (p *Process) Start() error {
	p.done = make(chan struct{})

	if err := p.cmd.Start(); err != nil {
		return fmt.Errorf("failed to start: %w", err)
	}

	// Monitor process in background
	go func() {
		err := p.cmd.Wait()
		if err != nil {
			p.logger.Printf("[gohmr] Process exited: %v", err)
		} else {
			p.logger.Println("[gohmr] Process exited cleanly")
		}
		close(p.done)
	}()

	return nil
}

// Stop stops the process
func (p *Process) Stop() {
	if p.cmd.Process == nil {
		return
	}

	// Send SIGTERM
	p.cmd.Process.Signal(syscall.SIGTERM)

	// Wait with timeout
	select {
	case <-p.done:
		return
	case <-time.After(5 * time.Second):
		// Force kill
		p.cmd.Process.Kill()
	}
}

// Wait waits for the process to exit
func (p *Process) Wait() error {
	<-p.done
	return nil
}
