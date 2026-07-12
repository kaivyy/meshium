package transfer

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"meshium/internal/db"
)

func newCheckpointDB(t *testing.T) *sql.DB {
	t.Helper()
	d, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.Migrate(d); err != nil {
		t.Fatalf("migrate db: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func TestTransferCheckpointRoundTrip(t *testing.T) {
	d := newCheckpointDB(t)
	store := NewSQLiteCheckpointStore(d)

	cp := TransferCheckpoint{
		TransferID:       "tx-1",
		MigrationID:      1,
		Category:         "configs",
		FileName:         "etc",
		Strategy:         "rsync",
		Mode:             "direct",
		SourceHost:       "src",
		SourcePath:       "/etc",
		TargetHost:       "tgt",
		TargetPath:       "/etc",
		TotalBytes:       1024,
		BytesTransferred: 512,
		Resumable:        true,
		SourceSnapshot:    "src-snap-a",
		TargetPartialState: "partial-a",
		ChecksumSource:    "abc",
		LastVerifiedPhase: "transferred",
	}
	if err := store.SaveCheckpoint(cp); err != nil {
		t.Fatalf("save: %v", err)
	}
	// Re-save (upsert) to confirm ON CONFLICT works and updates.
	cp.BytesTransferred = 768
	if err := store.SaveCheckpoint(cp); err != nil {
		t.Fatalf("resave: %v", err)
	}

	got, err := store.GetCheckpoint("tx-1", "etc")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.BytesTransferred != 768 {
		t.Fatalf("bytes=%d want 768", got.BytesTransferred)
	}
	if !got.Resumable {
		t.Fatalf("resumable should be true")
	}
	if got.SourceSnapshot != "src-snap-a" || got.TargetPartialState != "partial-a" {
		t.Fatalf("snapshot mismatch: %+v", got)
	}
	if got.LastVerifiedPhase != "transferred" {
		t.Fatalf("phase=%q", got.LastVerifiedPhase)
	}

	if err := store.DeleteCheckpoint("tx-1", "etc"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := store.GetCheckpoint("tx-1", "etc"); err == nil {
		t.Fatalf("expected not-found after delete")
	}
}

func TestReconcileMatrix(t *testing.T) {
	base := &TransferCheckpoint{
		TransferID:        "tx-1",
		SourceSnapshot:    "src-snap-a",
		TargetPartialState: "partial-a",
		BytesTransferred:  512,
	}

	cases := []struct {
		name   string
		cp     *TransferCheckpoint
		ev     ReconcileEvidence
		verdict ReconcileVerdict
		failed bool
	}{
		{
			name: "partial matches + source unchanged -> resume",
			cp:   base,
			ev: ReconcileEvidence{
				CurrentSourceSnapshot:    "src-snap-a",
				PartialTargetExists:      true,
				CurrentTargetPartialState: "partial-a",
				StrategyStillValid:        true,
			},
			verdict: VerdictResume,
		},
		{
			name: "no partial + nothing transferred -> fresh start",
			cp:   &TransferCheckpoint{TransferID: "tx-1"},
			ev: ReconcileEvidence{
				StrategyStillValid: true,
			},
			verdict: VerdictFreshStart,
		},
		{
			name: "source changed -> manual intervention",
			cp:   base,
			ev: ReconcileEvidence{
				CurrentSourceSnapshot: "src-snap-b",
				PartialTargetExists:   true,
				StrategyStillValid:     true,
			},
			verdict: VerdictManualIntervention,
			failed:  true,
		},
		{
			name: "partial fingerprint mismatch -> manual intervention",
			cp:   base,
			ev: ReconcileEvidence{
				CurrentSourceSnapshot:    "src-snap-a",
				PartialTargetExists:      true,
				CurrentTargetPartialState: "partial-foreign",
				StrategyStillValid:        true,
			},
			verdict: VerdictManualIntervention,
			failed:  true,
		},
		{
			name: "strategy invalid -> manual intervention",
			cp:   base,
			ev: ReconcileEvidence{
				CurrentSourceSnapshot:    "src-snap-a",
				PartialTargetExists:      true,
				CurrentTargetPartialState: "partial-a",
				StrategyStillValid:        false,
			},
			verdict: VerdictManualIntervention,
			failed:  true,
		},
		{
			name: "partial missing after transfer -> manual intervention",
			cp:   base,
			ev: ReconcileEvidence{
				CurrentSourceSnapshot: "src-snap-a",
				PartialTargetExists:   false,
				StrategyStillValid:     true,
			},
			verdict: VerdictManualIntervention,
			failed:  true,
		},
		{
			name:   "nil checkpoint -> fresh start",
			cp:     nil,
			ev:     ReconcileEvidence{StrategyStillValid: true},
			verdict: VerdictFreshStart,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v, err := Reconcile(c.cp, c.ev)
			if v != c.verdict {
				t.Fatalf("verdict=%q want %q", v, c.verdict)
			}
			if c.failed != (err != nil) {
				t.Fatalf("fail expectation: err=%v want-fail=%v", err, c.failed)
			}
			if c.failed && !errors.Is(err, ErrTransferReconcileFailed) {
				t.Fatalf("expected ErrTransferReconcileFailed, got %v", err)
			}
		})
	}
}

func TestNoopCheckpointStoreStillSatisfies(t *testing.T) {
	// Guard against interface drift: the noop store must still compile against
	// the interface used by the engine.
	var _ CheckpointStore = NoopCheckpointStore{}
	_ = context.Background()
}
