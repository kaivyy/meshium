package ssh

import (
	"errors"
	"io"
	"net"
	"strings"
	"syscall"
)

// Sentinel errors for common transient SSH connection failures.
// These allow callers to use errors.Is for type-safe error checking
// instead of string matching.
var (
	// ErrConnectionRefused indicates the target server refused the connection.
	ErrConnectionRefused = errors.New("connection refused")

	// ErrConnectionReset indicates the connection was reset by the peer.
	ErrConnectionReset = errors.New("connection reset")

	// ErrConnectionTimeout indicates the connection attempt timed out.
	ErrConnectionTimeout = errors.New("connection timeout")

	// ErrConnectionEOF indicates the connection was closed by the peer
	// before the SSH handshake completed.
	ErrConnectionEOF = errors.New("connection closed (EOF)")

	// ErrBrokenPipe indicates a write to a broken connection.
	ErrBrokenPipe = errors.New("broken pipe")

	// ErrTemporarilyUnavailable indicates a resource is temporarily
	// unavailable (e.g., too many open files).
	ErrTemporarilyUnavailable = errors.New("temporarily unavailable")
)

// isTransientError returns true for errors that may resolve on retry
// (e.g. connection refused, timeout, EOF).
//
// It uses typed error checking via errors.As and errors.Is first,
// falling back to string matching for errors from the SSH library
// that don't unwrap to standard library types.
func isTransientError(err error) bool {
	if err == nil {
		return false
	}

	// Check for net.Error interface (includes timeout errors)
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return true
		}
	}

	// Check for standard sentinel errors
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return true
	}
	if errors.Is(err, syscall.ECONNRESET) {
		return true
	}
	if errors.Is(err, syscall.EPIPE) {
		return true
	}
	if errors.Is(err, syscall.EAGAIN) {
		return true
	}

	// Fall back to string matching for SSH library errors that wrap
	// network errors in ways that don't unwrap to standard types.
	// The x/crypto/ssh library may format errors as strings without
	// preserving the underlying error type.
	msg := strings.ToLower(err.Error())
	for _, needle := range []string{
		"connection refused",
		"connection reset",
		"timeout",
		"timed out",
		"eof",
		"broken pipe",
		"temporarily unavailable",
		"i/o timeout",
	} {
		if strings.Contains(msg, needle) {
			return true
		}
	}

	return false
}
