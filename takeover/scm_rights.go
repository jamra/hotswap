//go:build unix

package takeover

import (
	"fmt"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

// SendFDs sends file descriptors over a Unix socket using SCM_RIGHTS
func SendFDs(conn *net.UnixConn, fds []int) error {
	if len(fds) == 0 {
		return nil
	}

	// Get the raw connection
	rawConn, err := conn.SyscallConn()
	if err != nil {
		return fmt.Errorf("failed to get raw conn: %w", err)
	}

	// Build the control message with SCM_RIGHTS
	rights := unix.UnixRights(fds...)

	// Send a dummy byte along with the control message
	// (at least one byte of data is required when sending ancillary data)
	data := []byte{0}

	var sendErr error
	err = rawConn.Write(func(fd uintptr) bool {
		sendErr = unix.Sendmsg(int(fd), data, rights, nil, 0)
		return true
	})

	if err != nil {
		return fmt.Errorf("raw conn write: %w", err)
	}
	if sendErr != nil {
		return fmt.Errorf("sendmsg: %w", sendErr)
	}

	return nil
}

// RecvFDs receives file descriptors from a Unix socket using SCM_RIGHTS
func RecvFDs(conn *net.UnixConn, maxFDs int) ([]int, error) {
	if maxFDs <= 0 {
		return nil, nil
	}

	rawConn, err := conn.SyscallConn()
	if err != nil {
		return nil, fmt.Errorf("failed to get raw conn: %w", err)
	}

	// Buffer for the dummy byte
	data := make([]byte, 1)
	// Buffer for the control message
	// Use unix.CmsgSpace to calculate the proper size for SCM_RIGHTS
	oob := make([]byte, unix.CmsgSpace(maxFDs*4))

	var fds []int
	var recvErr error

	err = rawConn.Read(func(fd uintptr) bool {
		var n, oobn int
		n, oobn, _, _, recvErr = unix.Recvmsg(int(fd), data, oob, 0)
		if recvErr != nil {
			return true
		}
		if n == 0 {
			recvErr = fmt.Errorf("connection closed")
			return true
		}

		// Parse the control message
		msgs, parseErr := unix.ParseSocketControlMessage(oob[:oobn])
		if parseErr != nil {
			recvErr = fmt.Errorf("parse control message: %w", parseErr)
			return true
		}

		for _, msg := range msgs {
			parsedFDs, parseErr := unix.ParseUnixRights(&msg)
			if parseErr != nil {
				recvErr = fmt.Errorf("parse unix rights: %w", parseErr)
				return true
			}
			fds = append(fds, parsedFDs...)
		}
		return true
	})

	if err != nil {
		return nil, fmt.Errorf("raw conn read: %w", err)
	}
	if recvErr != nil {
		return nil, recvErr
	}

	return fds, nil
}

// FileToFD extracts the file descriptor from an *os.File
func FileToFD(f *os.File) int {
	return int(f.Fd())
}

// FDToFile creates an *os.File from a file descriptor
func FDToFile(fd int, name string) *os.File {
	return os.NewFile(uintptr(fd), name)
}

// ListenerToFD gets the file descriptor from a net.Listener
func ListenerToFD(l net.Listener) (int, error) {
	// Get the underlying file
	file, err := listenerFile(l)
	if err != nil {
		return -1, err
	}
	return int(file.Fd()), nil
}

// listenerFile gets the *os.File from various listener types
func listenerFile(l net.Listener) (*os.File, error) {
	switch v := l.(type) {
	case *net.TCPListener:
		return v.File()
	case *net.UnixListener:
		return v.File()
	default:
		return nil, fmt.Errorf("unsupported listener type: %T", l)
	}
}

// FDToListener creates a net.Listener from a file descriptor
func FDToListener(fd int) (net.Listener, error) {
	file := os.NewFile(uintptr(fd), "listener")
	if file == nil {
		return nil, fmt.Errorf("invalid file descriptor: %d", fd)
	}
	defer file.Close()

	return net.FileListener(file)
}
