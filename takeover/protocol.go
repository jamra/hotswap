package takeover

import (
	"encoding/binary"
	"fmt"
	"io"
)

// Protocol version for compatibility checking
const ProtocolVersion = 1

// Message types for the takeover protocol
type MessageType uint8

const (
	MsgHello          MessageType = 1
	MsgHelloAck       MessageType = 2
	MsgRequestSockets MessageType = 3
	MsgSocketsTransfer MessageType = 4
	MsgSocketsAck     MessageType = 5
	MsgStartDrain     MessageType = 6
	MsgDrainStarted   MessageType = 7
)

func (m MessageType) String() string {
	switch m {
	case MsgHello:
		return "Hello"
	case MsgHelloAck:
		return "HelloAck"
	case MsgRequestSockets:
		return "RequestSockets"
	case MsgSocketsTransfer:
		return "SocketsTransfer"
	case MsgSocketsAck:
		return "SocketsAck"
	case MsgStartDrain:
		return "StartDrain"
	case MsgDrainStarted:
		return "DrainStarted"
	default:
		return fmt.Sprintf("Unknown(%d)", m)
	}
}

// Message represents a protocol message
type Message struct {
	Type    MessageType
	Version uint8
	Payload []byte
}

// sendMessage writes a message to the connection
func sendMessage(w io.Writer, msg Message) error {
	// Message format: [type:1][version:1][length:4][payload:N]
	header := make([]byte, 6)
	header[0] = byte(msg.Type)
	header[1] = msg.Version
	binary.BigEndian.PutUint32(header[2:6], uint32(len(msg.Payload)))

	if _, err := w.Write(header); err != nil {
		return fmt.Errorf("failed to write header: %w", err)
	}

	if len(msg.Payload) > 0 {
		if _, err := w.Write(msg.Payload); err != nil {
			return fmt.Errorf("failed to write payload: %w", err)
		}
	}

	return nil
}

// recvMessage reads a message from the connection
func recvMessage(r io.Reader) (Message, error) {
	header := make([]byte, 6)
	if _, err := io.ReadFull(r, header); err != nil {
		return Message{}, fmt.Errorf("failed to read header: %w", err)
	}

	msg := Message{
		Type:    MessageType(header[0]),
		Version: header[1],
	}

	payloadLen := binary.BigEndian.Uint32(header[2:6])
	if payloadLen > 0 {
		msg.Payload = make([]byte, payloadLen)
		if _, err := io.ReadFull(r, msg.Payload); err != nil {
			return Message{}, fmt.Errorf("failed to read payload: %w", err)
		}
	}

	return msg, nil
}

// Hello message payload
type HelloPayload struct {
	Version uint8
}

func encodeHello(p HelloPayload) []byte {
	return []byte{p.Version}
}

func decodeHello(data []byte) (HelloPayload, error) {
	if len(data) < 1 {
		return HelloPayload{}, fmt.Errorf("hello payload too short")
	}
	return HelloPayload{Version: data[0]}, nil
}

// SocketsTransfer payload contains the number of sockets being transferred
type SocketsTransferPayload struct {
	Count uint32
}

func encodeSocketsTransfer(p SocketsTransferPayload) []byte {
	buf := make([]byte, 4)
	binary.BigEndian.PutUint32(buf, p.Count)
	return buf
}

func decodeSocketsTransfer(data []byte) (SocketsTransferPayload, error) {
	if len(data) < 4 {
		return SocketsTransferPayload{}, fmt.Errorf("sockets transfer payload too short")
	}
	return SocketsTransferPayload{Count: binary.BigEndian.Uint32(data)}, nil
}
