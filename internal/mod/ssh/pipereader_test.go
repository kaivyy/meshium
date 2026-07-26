package ssh

import (
	"context"
	"testing"
)

// pipeReader.Close does `close(p.done)`. ExecPipe used to build the struct with
// a literal that omitted the `done` field, leaving it nil — and closing a nil
// channel panics. Both callers (migration/database.go, migration/replication.go)
// `defer r.Close()`, so the panic fired on the ordinary success path of every
// streamed database dump and took the process down with it.
//
// Constructing through newPipeReader is what guarantees the channel exists.
func TestNewPipeReaderInitializesDone(t *testing.T) {
	pr := newPipeReader(context.Background(), nil, nil, nil)
	if pr.done == nil {
		t.Fatal("newPipeReader left done nil; Close would panic closing a nil channel")
	}
	// close(nil) panics, so reaching the far side of this proves the fix.
	close(pr.done)
}

// The watchdog goroutine ExecPipe starts selects on <-pr.done to know when to
// stop. A nil done channel is never selectable, so the goroutine (and the SSH
// session it holds) leaked until the context expired.
func TestPipeReaderDoneIsSelectable(t *testing.T) {
	pr := newPipeReader(context.Background(), nil, nil, nil)
	close(pr.done)

	select {
	case <-pr.done:
	default:
		t.Fatal("done channel not selectable after close; watchdog goroutine would leak")
	}
}
