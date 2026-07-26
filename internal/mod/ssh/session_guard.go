package ssh

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// ErrSessionOpenTimeout is returned when opening an SSH session or SFTP client
// did not complete within the allowed window.
var ErrSessionOpenTimeout = errors.New("timed out opening SSH session")

// sessionOpenTimeout bounds a channel-open round-trip.
//
// Opening a session or an SFTP client talks to the peer, but those calls carry
// no deadline of their own and sit OUTSIDE the per-command watchdog — so on a
// wedged transport (channel-open sent, no reply, connection not yet detected
// as dead) a nominally 30s-bounded command blocked indefinitely before its
// timeout ever started counting. 20s is generous for a channel open on any
// link where the TCP connection is already established.
const sessionOpenTimeout = 20 * time.Second

// openWithContext runs a blocking open and bounds it by both `timeout` and the
// caller's context.
//
// The open runs in a goroutine because there is no cancellable variant in the
// underlying library. When we stop waiting, that goroutine is left to finish on
// its own — it unblocks when the connection is closed or errors out, it holds
// no locks, and the buffered channel means it never blocks on send. Leaking it
// briefly is strictly better than blocking the caller forever.
func openWithContext[T any](ctx context.Context, timeout time.Duration, open func() (T, error)) (T, error) {
	var zero T

	type result struct {
		val T
		err error
	}
	done := make(chan result, 1)
	go func() {
		v, err := open()
		done <- result{v, err}
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case r := <-done:
		if r.err != nil {
			return zero, r.err
		}
		return r.val, nil
	case <-ctx.Done():
		return zero, ctx.Err()
	case <-timer.C:
		return zero, fmt.Errorf("%w after %s (transport wedged?)", ErrSessionOpenTimeout, timeout)
	}
}

// newSessionContext opens an SSH session bounded by ctx and sessionOpenTimeout,
// after reserving a slot against the per-connection session cap.
//
// The returned session owns its slot: the caller MUST close it via the
// returned release func (session.Close alone does not free the slot), which is
// why this returns both.
func (c *Client) newSessionContext(ctx context.Context) (*ssh.Session, error) {
	sess, release, err := c.newSessionContextTracked(ctx)
	if err != nil {
		return nil, err
	}
	// Callers that do not track the slot release it as soon as the session is
	// handed over; the cap then only bounds the OPEN burst, which is what
	// protects sshd's MaxSessions during fan-out.
	release()
	return sess, nil
}

// newSessionContextTracked is newSessionContext for callers that hold the
// session open for a long time (streaming, shells) and should hold their slot
// for that whole period.
func (c *Client) newSessionContextTracked(ctx context.Context) (*ssh.Session, func(), error) {
	if c == nil || c.conn == nil {
		return nil, nil, errors.New("ssh client is not connected")
	}
	release, err := c.acquireSession(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("waiting for an SSH session slot: %w", err)
	}
	sess, err := openWithContext(ctx, sessionOpenTimeout, func() (*ssh.Session, error) {
		return c.conn.NewSession()
	})
	if err != nil {
		release()
		return nil, nil, err
	}
	return sess, release, nil
}

// newSFTPContext opens an SFTP client bounded by ctx and sessionOpenTimeout.
func (c *Client) newSFTPContext(ctx context.Context) (*sftp.Client, error) {
	if c == nil || c.conn == nil {
		return nil, errors.New("ssh client is not connected")
	}
	return openWithContext(ctx, sessionOpenTimeout, func() (*sftp.Client, error) {
		return sftp.NewClient(c.conn)
	})
}
