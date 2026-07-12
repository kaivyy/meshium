package transfer

import (
	"context"
	"errors"
	"testing"
)

func TestVerifyIntoPersistsOnMatch(t *testing.T) {
	// Source and dest carry identical content → verify succeeds AND the
	// checkpoint is marked verified before returning.
	src := newMockSSH()
	dst := newMockSSH()
	content := []byte("identical payload")
	src.setFile("/s/file", content)
	dst.setFile("/d/file", content)

	v := NewChecksumVerifier()
	cp := &TransferCheckpoint{}
	sum, err := v.VerifyInto(context.Background(),
		TransferTarget{IsLocal: false, Path: "/s/file", SSHClient: src},
		TransferTarget{IsLocal: false, Path: "/d/file", SSHClient: dst},
		cp,
	)
	if err != nil {
		t.Fatalf("verify failed: %v", err)
	}
	if sum == "" {
		t.Fatal("expected non-empty checksum")
	}
	if cp.ChecksumSource != sum || cp.ChecksumTarget != sum {
		t.Fatalf("checksums not captured: src=%q tgt=%q", cp.ChecksumSource, cp.ChecksumTarget)
	}
	if cp.LastVerifiedPhase != "verified" {
		t.Fatalf("checkpoint must be marked verified on success, got %q", cp.LastVerifiedPhase)
	}
}

func TestVerifyIntoNeverMarksVerifiedOnMismatch(t *testing.T) {
	// Mismatched content → error, and the checkpoint must NOT be marked
	// verified (so a later step cannot report success off it).
	src := newMockSSH()
	dst := newMockSSH()
	src.setFile("/s/file", []byte("aaaa"))
	dst.setFile("/d/file", []byte("bbbb"))

	v := NewChecksumVerifier()
	v.MaxRetries = 1
	cp := &TransferCheckpoint{}
	_, err := v.VerifyInto(context.Background(),
		TransferTarget{IsLocal: false, Path: "/s/file", SSHClient: src},
		TransferTarget{IsLocal: false, Path: "/d/file", SSHClient: dst},
		cp,
	)
	if err == nil || !errors.Is(err, ErrTransferChecksumMismatch) {
		t.Fatalf("expected ErrTransferChecksumMismatch, got %v", err)
	}
	if cp.LastVerifiedPhase == "verified" {
		t.Fatal("checkpoint must NOT be marked verified on mismatch")
	}
}

func TestVerifyIntoNilCheckpointNoOp(t *testing.T) {
	// No checkpoint persisted: VerifyInto behaves like Verify, no panic.
	src := newMockSSH()
	dst := newMockSSH()
	src.setFile("/s/file", []byte("same"))
	dst.setFile("/d/file", []byte("same"))

	v := NewChecksumVerifier()
	if _, err := v.VerifyInto(context.Background(),
		TransferTarget{IsLocal: false, Path: "/s/file", SSHClient: src},
		TransferTarget{IsLocal: false, Path: "/d/file", SSHClient: dst},
		nil,
	); err != nil {
		t.Fatalf("verify with nil cp failed: %v", err)
	}
}

func TestTerminalStateForError(t *testing.T) {
	cases := []struct {
		err    error
		want   TransferTerminalState
	}{
		{nil, TerminalOK},
		{ErrTransferChecksumMismatch, TerminalManualIntervention},
		{ErrTransferReconcileFailed, TerminalManualIntervention},
		{ErrTransferStrategyUnavailable, TerminalFailed},
		{ErrTransferSourceUnreachable, TerminalFailed},
		{ErrTransferTargetUnreachable, TerminalFailed},
		{ErrTransferAuth, TerminalFailed},
		{ErrTransferInactivity, TerminalFailed},
		{ErrTransferProtocol, TerminalFailed},
		{ErrTransferPartialTransfer, TerminalFailed},
		{ErrTransferRemoteFailure, TerminalFailed},
		{ErrTransferInvalidPath, TerminalFailed},
		{ErrTransferOutputCap, TerminalFailed},
		{ErrTransferTopologyUnsupported, TerminalFailed},
		{errors.New("some unknown failure"), TerminalManualIntervention}, // fail closed
	}
	for _, c := range cases {
		if got := TerminalStateForError(c.err); got != c.want {
			t.Fatalf("err=%v: got %q want %q", c.err, got, c.want)
		}
	}
}
