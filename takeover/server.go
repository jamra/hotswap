package takeover

import (
	"fmt"
	"log"
	"net"
	"os"
	"sync"
)

// TakeoverServer handles incoming takeover requests from new processes
type TakeoverServer struct {
	socketPath string
	listener   net.Listener
	listeners  []net.Listener
	mu         sync.Mutex
	onDrain    func()
	done       chan struct{}
}

// NewTakeoverServer creates a new takeover server
func NewTakeoverServer(socketPath string) *TakeoverServer {
	return &TakeoverServer{
		socketPath: socketPath,
		done:       make(chan struct{}),
	}
}

// RegisterListener registers a listener to be transferred during takeover
func (s *TakeoverServer) RegisterListener(l net.Listener) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listeners = append(s.listeners, l)
}

// SetDrainCallback sets the callback to invoke when draining should start
func (s *TakeoverServer) SetDrainCallback(fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onDrain = fn
}

// Start begins listening for takeover connections
func (s *TakeoverServer) Start() error {
	// Remove any existing socket file
	os.Remove(s.socketPath)

	listener, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", s.socketPath, err)
	}
	s.listener = listener

	go s.acceptLoop()
	return nil
}

// Stop stops the takeover server
func (s *TakeoverServer) Stop() {
	close(s.done)
	if s.listener != nil {
		s.listener.Close()
	}
	os.Remove(s.socketPath)
}

func (s *TakeoverServer) acceptLoop() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.done:
				return
			default:
				log.Printf("[takeover] accept error: %v", err)
				continue
			}
		}

		go s.handleConnection(conn.(*net.UnixConn))
	}
}

func (s *TakeoverServer) handleConnection(conn *net.UnixConn) {
	defer conn.Close()

	log.Printf("[takeover] new process connected")

	// Step 1: Receive Hello
	msg, err := recvMessage(conn)
	if err != nil {
		log.Printf("[takeover] failed to receive hello: %v", err)
		return
	}
	if msg.Type != MsgHello {
		log.Printf("[takeover] expected Hello, got %s", msg.Type)
		return
	}

	hello, err := decodeHello(msg.Payload)
	if err != nil {
		log.Printf("[takeover] failed to decode hello: %v", err)
		return
	}

	// Check version compatibility
	if hello.Version != ProtocolVersion {
		log.Printf("[takeover] version mismatch: got %d, want %d", hello.Version, ProtocolVersion)
		return
	}

	// Step 2: Send HelloAck
	err = sendMessage(conn, Message{
		Type:    MsgHelloAck,
		Version: ProtocolVersion,
		Payload: encodeHello(HelloPayload{Version: ProtocolVersion}),
	})
	if err != nil {
		log.Printf("[takeover] failed to send hello ack: %v", err)
		return
	}

	// Step 3: Receive RequestSockets
	msg, err = recvMessage(conn)
	if err != nil {
		log.Printf("[takeover] failed to receive request: %v", err)
		return
	}
	if msg.Type != MsgRequestSockets {
		log.Printf("[takeover] expected RequestSockets, got %s", msg.Type)
		return
	}

	// Step 4: Send SocketsTransfer with FDs
	s.mu.Lock()
	listeners := s.listeners
	s.mu.Unlock()

	// Collect file descriptors
	fds := make([]int, 0, len(listeners))
	for _, l := range listeners {
		fd, err := ListenerToFD(l)
		if err != nil {
			log.Printf("[takeover] failed to get listener fd: %v", err)
			continue
		}
		fds = append(fds, fd)
	}

	// Send count first
	err = sendMessage(conn, Message{
		Type:    MsgSocketsTransfer,
		Version: ProtocolVersion,
		Payload: encodeSocketsTransfer(SocketsTransferPayload{Count: uint32(len(fds))}),
	})
	if err != nil {
		log.Printf("[takeover] failed to send sockets transfer: %v", err)
		return
	}

	// Send the actual file descriptors via SCM_RIGHTS
	if len(fds) > 0 {
		err = SendFDs(conn, fds)
		if err != nil {
			log.Printf("[takeover] failed to send fds: %v", err)
			return
		}
	}

	log.Printf("[takeover] sent %d file descriptors", len(fds))

	// Step 5: Receive SocketsAck
	msg, err = recvMessage(conn)
	if err != nil {
		log.Printf("[takeover] failed to receive sockets ack: %v", err)
		return
	}
	if msg.Type != MsgSocketsAck {
		log.Printf("[takeover] expected SocketsAck, got %s", msg.Type)
		return
	}

	// Step 6: Receive StartDrain
	msg, err = recvMessage(conn)
	if err != nil {
		log.Printf("[takeover] failed to receive start drain: %v", err)
		return
	}
	if msg.Type != MsgStartDrain {
		log.Printf("[takeover] expected StartDrain, got %s", msg.Type)
		return
	}

	// Step 7: Send DrainStarted and begin draining
	err = sendMessage(conn, Message{
		Type:    MsgDrainStarted,
		Version: ProtocolVersion,
	})
	if err != nil {
		log.Printf("[takeover] failed to send drain started: %v", err)
		return
	}

	log.Printf("[takeover] starting drain")

	// Invoke drain callback
	s.mu.Lock()
	onDrain := s.onDrain
	s.mu.Unlock()

	if onDrain != nil {
		onDrain()
	}
}
