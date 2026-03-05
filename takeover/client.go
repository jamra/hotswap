package takeover

import (
	"fmt"
	"log"
	"net"
)

// TakeoverClient connects to an existing process to perform takeover
type TakeoverClient struct {
	socketPath string
	conn       *net.UnixConn
}

// NewTakeoverClient creates a new takeover client
func NewTakeoverClient(socketPath string) *TakeoverClient {
	return &TakeoverClient{
		socketPath: socketPath,
	}
}

// Connect connects to the existing process
func (c *TakeoverClient) Connect() error {
	conn, err := net.Dial("unix", c.socketPath)
	if err != nil {
		return fmt.Errorf("failed to connect to %s: %w", c.socketPath, err)
	}
	c.conn = conn.(*net.UnixConn)
	return nil
}

// Close closes the connection
func (c *TakeoverClient) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// PerformTakeover executes the takeover protocol and returns the received listeners
func (c *TakeoverClient) PerformTakeover() ([]net.Listener, error) {
	if c.conn == nil {
		return nil, fmt.Errorf("not connected")
	}

	log.Printf("[takeover] starting takeover handshake")

	// Step 1: Send Hello
	err := sendMessage(c.conn, Message{
		Type:    MsgHello,
		Version: ProtocolVersion,
		Payload: encodeHello(HelloPayload{Version: ProtocolVersion}),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to send hello: %w", err)
	}

	// Step 2: Receive HelloAck
	msg, err := recvMessage(c.conn)
	if err != nil {
		return nil, fmt.Errorf("failed to receive hello ack: %w", err)
	}
	if msg.Type != MsgHelloAck {
		return nil, fmt.Errorf("expected HelloAck, got %s", msg.Type)
	}

	ack, err := decodeHello(msg.Payload)
	if err != nil {
		return nil, fmt.Errorf("failed to decode hello ack: %w", err)
	}
	if ack.Version != ProtocolVersion {
		return nil, fmt.Errorf("version mismatch: server=%d, client=%d", ack.Version, ProtocolVersion)
	}

	// Step 3: Send RequestSockets
	err = sendMessage(c.conn, Message{
		Type:    MsgRequestSockets,
		Version: ProtocolVersion,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to send request sockets: %w", err)
	}

	// Step 4: Receive SocketsTransfer
	msg, err = recvMessage(c.conn)
	if err != nil {
		return nil, fmt.Errorf("failed to receive sockets transfer: %w", err)
	}
	if msg.Type != MsgSocketsTransfer {
		return nil, fmt.Errorf("expected SocketsTransfer, got %s", msg.Type)
	}

	transfer, err := decodeSocketsTransfer(msg.Payload)
	if err != nil {
		return nil, fmt.Errorf("failed to decode sockets transfer: %w", err)
	}

	log.Printf("[takeover] expecting %d file descriptors", transfer.Count)

	// Receive FDs via SCM_RIGHTS
	var listeners []net.Listener
	if transfer.Count > 0 {
		fds, err := RecvFDs(c.conn, int(transfer.Count))
		if err != nil {
			return nil, fmt.Errorf("failed to receive fds: %w", err)
		}

		log.Printf("[takeover] received %d file descriptors", len(fds))

		// Convert FDs to listeners
		for i, fd := range fds {
			l, err := FDToListener(fd)
			if err != nil {
				log.Printf("[takeover] failed to convert fd %d to listener: %v", fd, err)
				continue
			}
			log.Printf("[takeover] created listener %d from fd %d", i, fd)
			listeners = append(listeners, l)
		}
	}

	// Step 5: Send SocketsAck
	err = sendMessage(c.conn, Message{
		Type:    MsgSocketsAck,
		Version: ProtocolVersion,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to send sockets ack: %w", err)
	}

	return listeners, nil
}

// SignalDrain tells the old process to start draining
func (c *TakeoverClient) SignalDrain() error {
	if c.conn == nil {
		return fmt.Errorf("not connected")
	}

	// Step 6: Send StartDrain
	err := sendMessage(c.conn, Message{
		Type:    MsgStartDrain,
		Version: ProtocolVersion,
	})
	if err != nil {
		return fmt.Errorf("failed to send start drain: %w", err)
	}

	// Step 7: Receive DrainStarted
	msg, err := recvMessage(c.conn)
	if err != nil {
		return fmt.Errorf("failed to receive drain started: %w", err)
	}
	if msg.Type != MsgDrainStarted {
		return fmt.Errorf("expected DrainStarted, got %s", msg.Type)
	}

	log.Printf("[takeover] old process is draining")
	return nil
}
