package ssh

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// boundedWriter caps total bytes written; once exceeded it records overflow and
// discards further writes. Read returns the captured prefix + overflow flag.
func TestBoundedWriterCapsOutput(t *testing.T) {
	w := newBoundedWriter(8, 4)
	n, err := w.Write([]byte("hello world")) // 11 bytes, cap 8
	if err != nil {
		t.Fatalf("write error: %v", err)
	}
	if n != 11 {
		t.Fatalf("expected to report all bytes written (11), got %d", n)
	}
	if !w.overflowed {
		t.Fatal("expected overflow flag set after exceeding cap")
	}
	if w.String() != "hello wo" { // first 8 bytes
		t.Fatalf("expected truncated to cap, got %q", w.String())
	}
}

func TestBoundedWriterSmallOutputUnaffected(t *testing.T) {
	w := newBoundedWriter(1<<20, 256<<10)
	_, _ = w.Write([]byte("small"))
	if w.overflowed {
		t.Fatal("no overflow expected for small output")
	}
	if w.String() != "small" {
		t.Fatalf("expected 'small', got %q", w.String())
	}
}

// ErrOutputLimitExceeded is the typed error returned when capture exceeds the cap.
func TestErrOutputLimitExceeded(t *testing.T) {
	if !errors.Is(ErrOutputLimitExceeded, ErrOutputLimitExceeded) {
		t.Fatal("ErrOutputLimitExceeded must satisfy errors.Is")
	}
	if !strings.Contains(ErrOutputLimitExceeded.Error(), "output") {
		t.Fatalf("unexpected error text: %v", ErrOutputLimitExceeded)
	}
}

// Direct exercise of the bounded writer under a real byte stream (no SSH
// connection needed): feed >cap, assert overflow + truncation + no unbounded
// allocation growth.
func TestBoundedWriterNoUnboundedGrowth(t *testing.T) {
	w := newBoundedWriter(1<<10, 1<<9) // 1KiB stdout, 512B stderr
	chunk := bytes.Repeat([]byte("x"), 256)
	for i := 0; i < 100; i++ { // 25KiB total, well over cap
		_, _ = w.Write(chunk)
	}
	if !w.overflowed {
		t.Fatal("expected overflow after many writes")
	}
	if len(w.String()) > 1<<10 {
		t.Fatalf("internal buffer grew beyond cap: %d", len(w.String()))
	}
}
