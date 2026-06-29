package jobengine

import (
	"time"
)

// JobType identifies the kind of work a job performs.
type JobType string

const (
	JobTypeMigration   JobType = "migration"
	JobTypeDiscovery    JobType = "discovery"
	JobTypeCompatCheck  JobType = "compat_check"
)

// JobStatus represents the lifecycle state of a job.
type JobStatus string

const (
	JobStatusQueued    JobStatus = "queued"
	JobStatusRunning   JobStatus = "running"
	JobStatusPaused    JobStatus = "paused"
	JobStatusDone      JobStatus = "done"
	JobStatusFailed    JobStatus = "failed"
	JobStatusCancelled JobStatus = "cancelled"
)

// IsTerminal returns true if the status is a final state (no further
// transitions are possible).
func (s JobStatus) IsTerminal() bool {
	return s == JobStatusDone || s == JobStatusFailed || s == JobStatusCancelled
}

// IsActive returns true if the job is currently being processed or
// waiting to be processed.
func (s JobStatus) IsActive() bool {
	return s == JobStatusQueued || s == JobStatusRunning || s == JobStatusPaused
}

// LogLevel is the severity level for a job log entry.
type LogLevel string

const (
	LogLevelInfo  LogLevel = "info"
	LogLevelWarn  LogLevel = "warn"
	LogLevelError LogLevel = "error"
)

// Job is the top-level orchestrator unit. Each job represents a unit
// of work (migration, discovery, or compatibility check) that is
// submitted to the Job Engine, queued, and executed by a worker.
type Job struct {
	ID          string     `json:"id"`
	Type        JobType    `json:"type"`
	Status      JobStatus  `json:"status"`
	CreatedAt   time.Time  `json:"createdAt"`
	StartedAt   *time.Time `json:"startedAt,omitempty"`
	FinishedAt  *time.Time `json:"finishedAt,omitempty"`
	PlanID      string     `json:"planId,omitempty"`     // for Migration jobs
	MigrationID int        `json:"migrationId,omitempty"` // from Phase 2 Engine
	SourceID    int        `json:"sourceId,omitempty"`   // for Discovery/CompatCheck
	TargetID    int        `json:"targetId,omitempty"`   // for CompatCheck
	Progress    JobProgress `json:"progress"`
	Logs        []JobLog   `json:"logs,omitempty"`
	Error       string     `json:"error,omitempty"`
}

// JobProgress tracks the real-time progress of a running job.
type JobProgress struct {
	CurrentStep  int           `json:"currentStep"`
	TotalSteps   int           `json:"totalSteps"`
	CurrentName  string        `json:"currentName"`
	Percentage   float64       `json:"percentage"`
	BytesDone    int64         `json:"bytesDone"`
	BytesTotal   int64         `json:"bytesTotal"`
	SpeedBPS     int64         `json:"speedBps"`
	ETA          time.Duration `json:"eta"`
}

// JobLog is a single log entry emitted during job execution.
type JobLog struct {
	Timestamp time.Time `json:"timestamp"`
	Level     LogLevel  `json:"level"`
	Step      string    `json:"step"`
	Message   string    `json:"message"`
}

// JobRequest is submitted by the caller to create a new job.
type JobRequest struct {
	Type        JobType  `json:"type"`
	PlanID      string   `json:"planId,omitempty"`      // for Migration jobs
	SourceID    int      `json:"sourceId,omitempty"`     // for Discovery/CompatCheck
	TargetID    int      `json:"targetId,omitempty"`     // for CompatCheck
	MigrationID int      `json:"migrationId,omitempty"`  // for Migration jobs
}

// JobFilter is used to filter jobs when listing.
type JobFilter struct {
	Type   JobType   `json:"type,omitempty"`
	Status JobStatus `json:"status,omitempty"`
	Limit  int       `json:"limit,omitempty"`
}

// generateJobID creates a unique job ID based on the current time.
func generateJobID() string {
	return "job-" + time.Now().UTC().Format("20060102150405.000000000")
}
