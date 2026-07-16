package migration

import (
	"context"
	"fmt"

	"meshium/internal/mod/discovery"
	modssh "meshium/internal/mod/ssh"
	"meshium/internal/mod/transport"

	xssh "golang.org/x/crypto/ssh"
)

// --- Status constants ---

const (
	StatusPlanned        = "planned"
	StatusRunning        = "running"
	StatusCompleted      = "completed"
	StatusFailed         = "failed"
	StatusRollingBack    = "rolling_back"
	StatusRolledBack     = "rolled_back"
	StatusRollbackFailed = "rollback_failed"
	StatusInterrupted    = "interrupted" // set when a running migration is detected after a crash
	StatusResuming       = "resuming"    // set when an interrupted migration is being resumed

	// Phase 6B (selective apply + verification) adds migration-level outcome
	// statuses that honestly distinguish "done but not identical" from "done".
	// These are ADDITIVE: the legacy StatusCompleted is still emitted when a
	// migration has no unresolved drift, no manual gaps, and no failed items.
	// None of these may be claimed when an app-health layer is unverified for a
	// selected app item (see parity spec §D).
	StatusCompletedWithDrift       = "completed_with_drift"        // selected applied but parity shows unresolved infra diffs
	StatusCompletedWithManualGaps  = "completed_with_manual_gaps"  // done; skip/review_manual/L4 items remain
	StatusCompletedPartial         = "completed_partial"          // some selected applied, some failed-but-nonfatal
	StatusVerificationFailed       = "verification_failed"        // apply ok but runtime/app verification failed
	StatusVerificationPartial      = "verification_partial"       // some checks passed, some unresolved
	StatusManualFollowupRequired   = "manual_followup_required"   // L4 items present and unhandled
)

const (
	StepStatusPending   = "pending"
	StepStatusRunning   = "running"
	StepStatusCompleted = "completed"
	StepStatusFailed    = "failed"
	StepStatusApplied   = "applied" // step has been applied to the target (checkpoint marker)
	// StepStatusSkipped marks a step the operator explicitly chose NOT to apply
	// (selection action keep_target or skip). It is a terminal, non-applied
	// state: rollback must leave it untouched (see initialSyncStage selective
	// apply, parity spec §C.2/§I.4). Distinct from StepStatusFailed: a skipped
	// step is an intentional operator decision, not an error.
	StepStatusSkipped = "skipped"
)

// --- SSH and progress types ---

// SSHExecuter is the interface for executing commands and transferring
// files over an SSH connection. It is an alias for transport.SSHExecuter.
type SSHExecuter = transport.SSHExecuter

// StepCallback is called for each step result during migration.
type StepCallback func(msg WSMessage)

// WSMessage is the WebSocket message format for migration progress.
//
// The structured fields below (BytesCompleted, BytesTotal, ResumeState, …) are
// optional and additive: older consumers that only read Step/Status/Value keep
// working. They exist so the FE can render honest, non-collapsed transfer state
// (Phase 5E, D1/D2) — distinguishing a fresh transfer, a resuming upload, a
// restarting download, a refused resume, and a verified-complete — without
// parsing the free-text Value.
type WSMessage struct {
	Step   string `json:"step"`
	Status string `json:"status"`
	Value  string `json:"value,omitempty"`
	Error  string `json:"error,omitempty"`

	// MigrationID ties progress to a specific migration (for multi-pipeline UIs).
	MigrationID int `json:"migrationId,omitempty"`
	// TransferID identifies one resumable transfer (db:<id>:<db>, config path, …).
	TransferID string `json:"transferId,omitempty"`
	// Direction distinguishes source→local download vs local→target upload legs.
	Direction string `json:"direction,omitempty"` // "download" | "upload"
	// TransferMethod is the real mechanism in use.
	TransferMethod string `json:"transferMethod,omitempty"` // "scp" | "rsync" | "stream" | "sftp"
	// BytesCompleted / BytesTotal are the live progress for this leg.
	BytesCompleted int64 `json:"bytesCompleted,omitempty"`
	BytesTotal     int64 `json:"bytesTotal,omitempty"`
	// ThroughputBPS / ETASeconds are best-effort, emitted only when calculable.
	ThroughputBPS int64 `json:"throughputBps,omitempty"`
	ETASeconds    int64 `json:"etaSeconds,omitempty"`
	// CheckpointStatus records whether a persisted checkpoint drove this leg.
	CheckpointStatus string `json:"checkpointStatus,omitempty"` // "loaded" | "none" | "deleted"
	// ResumeState is the explicit, non-collapsed resume classification (D2).
	ResumeState string `json:"resumeState,omitempty"`
	// ResumeReason explains a refused/non-resumable decision.
	ResumeReason string `json:"resumeReason,omitempty"`
	// SourceFingerprint is the size:mtime (or hash) used to detect source drift.
	SourceFingerprint string `json:"sourceFingerprint,omitempty"`
	// Attempt is the 1-based retry attempt for this transfer.
	Attempt int `json:"attempt,omitempty"`
	// IsResumable reports whether THIS engine/path supports resume.
	IsResumable bool `json:"isResumable,omitempty"`
	// DowntimeClass is the honest downtime model for this category/engine.
	DowntimeClass string `json:"downtimeClass,omitempty"`
	// EstimatedBytes is the collector's best-effort total data size for a category
	// (e.g. summed DB sizes). 0 = unknown; the FE shows "size unknown", never
	// "0 MB" (Phase 5E, J).
	EstimatedBytes int64 `json:"estimatedBytes,omitempty"`
}

// ResumeState values (Phase 5E, D2). These are explicit so the FE never has to
// collapse every transfer into a generic "running" state.
const (
	ResumeFreshTransfer            = "fresh_transfer"
	ResumeResumingUpload           = "resuming_upload"
	ResumeRestartingDownload       = "restarting_download"
	ResumeRefusedSourceChanged     = "resume_refused_source_changed"
	ResumeRefusedPartialInvalid    = "resume_refused_partial_invalid"
	ResumeNotSupported             = "resume_not_supported"
	ResumeManualIntervention       = "manual_intervention_required"
	ResumeVerificationInProgress    = "verification_in_progress"
	ResumeVerifiedComplete          = "verified_complete"
)


// --- Data types ---

// UserData holds user information collected from source.
type UserData struct {
	Name     string `json:"name"`
	UID      int    `json:"uid"`
	GID      int    `json:"gid"`
	HomeDir  string `json:"homeDir"`
	Shell    string `json:"shell"`
	Password string `json:"password,omitempty"`
}

// GroupData holds group information collected from source.
type GroupData struct {
	Name string `json:"name"`
	GID  int    `json:"gid"`
}

// --- Plan types ---

// MigrationPlan is the plan generated by the planner and persisted to DB.
type MigrationPlan struct {
	ID              int                  `json:"id"`
	SourceServerID  int                  `json:"sourceServerId"`
	TargetServerID  int                  `json:"targetServerId"`
	Status          string               `json:"status"`
	Categories      []string             `json:"categories"`
	Source          discovery.SystemInfo `json:"source,omitempty"`
	Target          discovery.SystemInfo `json:"target,omitempty"`
	Steps           []PlanStep           `json:"steps,omitempty"`
	Warnings        []string             `json:"warnings,omitempty"`
	EstimatedTime   string               `json:"estimatedTime,omitempty"`
}

type PlanStep struct {
	Category    string `json:"category"`
	Action      string `json:"action"`
	Description string `json:"description"`
	ItemCount   int    `json:"itemCount"`
}

// --- DB models ---

// Migration is the DB model for a migration record.
type Migration struct {
	ID           int            `json:"id"`
	SourceID     int            `json:"sourceId"`
	TargetID     int            `json:"targetId"`
	Categories   []string       `json:"categories"`
	Status       string         `json:"status"`
	Plan         *MigrationPlan `json:"plan,omitempty"`
	Error        string         `json:"error,omitempty"`
	CreatedAt    string         `json:"createdAt"`
	CompletedAt  string         `json:"completedAt,omitempty"`
	RolledBackAt string         `json:"rolledBackAt,omitempty"`
	OperationID  string         `json:"operationId,omitempty"`
}

// MigrationStepRecord is the DB model for a migration step.
// (Renamed from MigrationStep to avoid collision with the MigrationStep
// interface used by the Phase 2 engine.)
type MigrationStepRecord struct {
	ID          int    `json:"id"`
	MigrationID int    `json:"migrationId"`
	Category    string `json:"category"`
	Action      string `json:"action"`
	Status      string `json:"status"`
	Data        string `json:"data,omitempty"`     // JSON CategoryData
	Output      string `json:"output,omitempty"`
	Error       string `json:"error,omitempty"`
	StartedAt   string `json:"startedAt,omitempty"`
	CompletedAt string `json:"completedAt,omitempty"`
}

// MigrationBackup is the DB model for a migration backup.
type MigrationBackup struct {
	ID          int    `json:"id"`
	MigrationID int    `json:"migrationId"`
	ServerID    int    `json:"serverId"`
	Category    string `json:"category"`
	Data        string `json:"data"`              // JSON BackupData
	CreatedAt   string `json:"createdAt"`
}

// --- DTOs ---

type PlanRequest struct {
	SourceServerID  int             `json:"sourceServerId"`
	TargetServerID  int             `json:"targetServerId"`
	Categories      []string        `json:"categories"`
	ConfigPaths     []string        `json:"configPaths,omitempty"`
	DatabaseConfig  *DatabaseConfig `json:"databaseConfig,omitempty"`
	// OperationID is a client-generated idempotency key for the whole create-plan
	// attempt. The backend dedups against an existing recoverable migration with
	// the same key, so a refresh/retry/reconnect never inserts a second plan.
	OperationID string `json:"operationId,omitempty"`
}

type MigrationResponse struct {
	ID          int             `json:"id"`
	SourceID    int             `json:"sourceId"`
	TargetID    int             `json:"targetId"`
	Categories  []string        `json:"categories"`
	Status      string          `json:"status"`
	Plan        *MigrationPlan  `json:"plan,omitempty"`
	Error       string          `json:"error,omitempty"`
	CreatedAt   string          `json:"createdAt"`
	CompletedAt string          `json:"completedAt,omitempty"`
	OperationID string          `json:"operationId,omitempty"`
	Steps       []MigrationStepRecord `json:"steps,omitempty"`
}

// --- Interfaces (reused from transport) ---

// ConnectionPool provides SSH clients for a server.
type ConnectionPool = transport.ConnectionPool

// PoolAdapter wraps a transport.ConnectionPool to satisfy migration.ConnectionPool.
type PoolAdapter struct {
	Inner transport.ConnectionPool
}

func (a *PoolAdapter) Get(serverID int, cfg modssh.ServerConfig, hostKeyCallback xssh.HostKeyCallback) (SSHExecuter, error) {
	client, err := a.Inner.Get(serverID, cfg, hostKeyCallback)
	if err != nil {
		return nil, err
	}
	// The underlying *ssh.Client implements Upload/Download, so type-assert
	if s, ok := client.(SSHExecuter); ok {
		return s, nil
	}
	return nil, fmt.Errorf("SSH client does not support SFTP operations")
}

func (a *PoolAdapter) GetContext(ctx context.Context, serverID int, cfg modssh.ServerConfig, hostKeyCallback xssh.HostKeyCallback) (SSHExecuter, error) {
	client, err := a.Inner.GetContext(ctx, serverID, cfg, hostKeyCallback)
	if err != nil {
		return nil, err
	}
	if s, ok := client.(SSHExecuter); ok {
		return s, nil
	}
	return nil, fmt.Errorf("SSH client does not support SFTP operations")
}

// AESKeyProvider exposes the AES key needed to decrypt stored credentials.
type AESKeyProvider = transport.AESKeyProvider

// HostKeyStore provides host key verification callbacks.
type HostKeyStore = transport.HostKeyStore

// Collector and Applier interfaces are defined in categories.go
// to avoid circular dependency with CategoryData/BackupData types.
