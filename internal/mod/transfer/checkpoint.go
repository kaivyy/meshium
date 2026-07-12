package transfer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrTransferReconcileFailed indicates a checkpoint could not be trusted to
// resume (source changed, partial target inconsistent, or strategy invalid).
// Callers map this to NeedsManualIntervention rather than blind-resuming.
var ErrTransferReconcileFailed = errors.New("transfer reconcile failed")

// ReconcileVerdict is the decision after comparing a persisted checkpoint
// against the live source/target state on restart.
type ReconcileVerdict string

const (
	// VerdictResume means the partial target is consistent and the source
	// unchanged, so the transfer may continue from the checkpoint.
	VerdictResume ReconcileVerdict = "resume"
	// VerdictFreshStart means no usable partial state exists; start over.
	VerdictFreshStart ReconcileVerdict = "fresh_start"
	// VerdictManualIntervention means reconciliation is ambiguous/inconsistent
	// and an operator must decide (fail closed).
	VerdictManualIntervention ReconcileVerdict = "manual_intervention"
)

// TransferCheckpoint is the durable, persisted state of one transfer (a file
// or tree) so it can resume or be reconciled after a backend restart. It is
// written BEFORE and DURING a transfer, and BEFORE the step is considered
// resumable. Never emits success without a persisted verification phase.
type TransferCheckpoint struct {
	ID               int64  `json:"id"`
	TransferID       string `json:"transferId"`       // unique ID for this transfer
	MigrationID      int    `json:"migrationId"`       // owning migration
	Category         string `json:"category"`          // e.g. "configs", "docker-volume"
	FileName         string `json:"fileName"`          // file or tree root name
	Strategy         string `json:"strategy"`          // "rsync" | "scp" | "tar-over-ssh"
	Mode             string `json:"mode"`              // "direct" | "degraded"
	SourceHost       string `json:"sourceHost"`
	SourcePath       string `json:"sourcePath"`
	TargetHost       string `json:"targetHost"`
	TargetPath       string `json:"targetPath"`
	TotalBytes       int64  `json:"totalBytes"`
	BytesTransferred int64  `json:"bytesTransferred"`
	Resumable        bool   `json:"resumable"`
	// SourceSnapshot is an opaque, strategy-defined fingerprint of the source
	// object/path at checkpoint time (e.g. size+mtime+checksum). Used to detect
	// a source that changed after the checkpoint (which invalidates resume).
	SourceSnapshot string `json:"sourceSnapshot"`
	// TargetPartialState is an opaque fingerprint of the partial target file
	// (e.g. size of the rsync partial). Used to confirm the partial is the one
	// we were building, not a stale/foreign file.
	TargetPartialState string `json:"targetPartialState"`
	ChecksumSource     string `json:"checksumSource"`
	ChecksumTarget     string `json:"checksumTarget"`
	// LastVerifiedPhase is the furthest phase proven: "transferred" | "verified".
	// A checkpoint is never reported complete without "verified".
	LastVerifiedPhase string `json:"lastVerifiedPhase"`
	StartedAt         string `json:"startedAt"`
	UpdatedAt         string `json:"updatedAt"`
}

// ReconcileEvidence is the live state gathered at restart time to decide
// whether an existing checkpoint can be trusted to resume.
type ReconcileEvidence struct {
	// CurrentSourceSnapshot is the live source fingerprint. If it differs from
	// cp.SourceSnapshot, the source moved under us → cannot blindly resume.
	CurrentSourceSnapshot string
	// PartialTargetExists is true if a partial target file/dir is present.
	PartialTargetExists bool
	// CurrentTargetPartialState is the live partial target fingerprint.
	CurrentTargetPartialState string
	// StrategyStillValid is false if the configured strategy is no longer
	// available (e.g. rsync removed from either side).
	StrategyStillValid bool
}

// Reconcile compares a persisted checkpoint against live evidence and returns
// a verdict. It fails closed: any ambiguity → ManualIntervention. It never
// returns Resume when the source has changed or the partial target is missing/
// inconsistent, because resuming onto an unverified partial risks corrupting
// or duplicating data.
func Reconcile(cp *TransferCheckpoint, ev ReconcileEvidence) (ReconcileVerdict, error) {
	if cp == nil {
		return VerdictFreshStart, nil
	}
	if !ev.StrategyStillValid {
		return VerdictManualIntervention, fmt.Errorf("%w: strategy no longer valid for transfer %s", ErrTransferReconcileFailed, cp.TransferID)
	}
	// Source changed after checkpoint → resuming would transfer against a moving
	// or different source. Do not blind-resume; require operator decision.
	if cp.SourceSnapshot != "" && ev.CurrentSourceSnapshot != "" &&
		cp.SourceSnapshot != ev.CurrentSourceSnapshot {
		return VerdictManualIntervention, fmt.Errorf("%w: source snapshot changed for transfer %s", ErrTransferReconcileFailed, cp.TransferID)
	}
	// No partial target and nothing transferred yet → safe fresh start.
	if !ev.PartialTargetExists && cp.BytesTransferred == 0 {
		return VerdictFreshStart, nil
	}
	// Partial exists but its fingerprint does not match → foreign/stale file.
	if ev.PartialTargetExists && cp.TargetPartialState != "" &&
		ev.CurrentTargetPartialState != "" &&
		cp.TargetPartialState != ev.CurrentTargetPartialState {
		return VerdictManualIntervention, fmt.Errorf("%w: target partial state mismatch for transfer %s", ErrTransferReconcileFailed, cp.TransferID)
	}
	// Partial matches (or we have no partial fingerprint to compare) and the
	// source is unchanged → safe to resume.
	if ev.PartialTargetExists {
		return VerdictResume, nil
	}
	// Partial gone but bytes were transferred → ambiguous; let operator decide.
	return VerdictManualIntervention, fmt.Errorf("%w: partial target missing after %d bytes transferred for transfer %s", ErrTransferReconcileFailed, cp.BytesTransferred, cp.TransferID)
}

// sqliteCheckpointStore persists checkpoints in SQLite. It satisfies the
// CheckpointStore interface from progress.go.
type sqliteCheckpointStore struct {
	db *sql.DB
}

// NewSQLiteCheckpointStore builds a persisted store over a *sql.DB. The
// transfer_checkpoints table must exist (created in internal/db/migrations.go).
func NewSQLiteCheckpointStore(db *sql.DB) CheckpointStore {
	return &sqliteCheckpointStore{db: db}
}

func (s *sqliteCheckpointStore) SaveCheckpoint(cp TransferCheckpoint) error {
	now := time.Now().UTC().Format(time.RFC3339)
	if cp.StartedAt == "" {
		cp.StartedAt = now
	}
	_, err := s.db.ExecContext(context.Background(), `
		INSERT INTO transfer_checkpoints
			(transfer_id, migration_id, category, file_name, strategy, mode,
			 source_host, source_path, target_host, target_path,
			 total_bytes, bytes_transferred, resumable,
			 source_snapshot, target_partial_state, checksum_source, checksum_target,
			 last_verified_phase, started_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(transfer_id, file_name) DO UPDATE SET
			strategy=excluded.strategy, mode=excluded.mode,
			total_bytes=excluded.total_bytes, bytes_transferred=excluded.bytes_transferred,
			resumable=excluded.resumable, source_snapshot=excluded.source_snapshot,
			target_partial_state=excluded.target_partial_state, checksum_source=excluded.checksum_source,
			checksum_target=excluded.checksum_target, last_verified_phase=excluded.last_verified_phase,
			updated_at=excluded.updated_at;`,
		cp.TransferID, cp.MigrationID, cp.Category, cp.FileName, cp.Strategy, cp.Mode,
		cp.SourceHost, cp.SourcePath, cp.TargetHost, cp.TargetPath,
		cp.TotalBytes, cp.BytesTransferred, boolToInt(cp.Resumable),
		cp.SourceSnapshot, cp.TargetPartialState, cp.ChecksumSource, cp.ChecksumTarget,
		cp.LastVerifiedPhase, cp.StartedAt, now,
	)
	return err
}

func (s *sqliteCheckpointStore) GetCheckpoint(transferID, fileName string) (*TransferCheckpoint, error) {
	row := s.db.QueryRowContext(context.Background(), `
		SELECT id, transfer_id, migration_id, category, file_name, strategy, mode,
			source_host, source_path, target_host, target_path,
			total_bytes, bytes_transferred, resumable,
			source_snapshot, target_partial_state, checksum_source, checksum_target,
			last_verified_phase, started_at, updated_at
		FROM transfer_checkpoints WHERE transfer_id = ? AND file_name = ?;`,
		transferID, fileName,
	)
	cp := &TransferCheckpoint{}
	var resumable int
	err := row.Scan(
		&cp.ID, &cp.TransferID, &cp.MigrationID, &cp.Category, &cp.FileName, &cp.Strategy, &cp.Mode,
		&cp.SourceHost, &cp.SourcePath, &cp.TargetHost, &cp.TargetPath,
		&cp.TotalBytes, &cp.BytesTransferred, &resumable,
		&cp.SourceSnapshot, &cp.TargetPartialState, &cp.ChecksumSource, &cp.ChecksumTarget,
		&cp.LastVerifiedPhase, &cp.StartedAt, &cp.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	cp.Resumable = resumable != 0
	return cp, nil
}

func (s *sqliteCheckpointStore) DeleteCheckpoint(transferID, fileName string) error {
	_, err := s.db.ExecContext(context.Background(),
		`DELETE FROM transfer_checkpoints WHERE transfer_id = ? AND file_name = ?;`,
		transferID, fileName)
	return err
}

func (s *sqliteCheckpointStore) DeleteCheckpoints(transferID string) error {
	_, err := s.db.ExecContext(context.Background(),
		`DELETE FROM transfer_checkpoints WHERE transfer_id = ?;`, transferID)
	return err
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
