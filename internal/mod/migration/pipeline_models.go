package migration

import "time"

// --- Pipeline Stage Models ---

// StageState represents the lifecycle state of a pipeline stage.
type StageState string

const (
	StageStatePending    StageState = "pending"
	StageStateRunning    StageState = "running"
	StageStateCompleted  StageState = "completed"
	StageStateFailed     StageState = "failed"
	StageStateSkipped    StageState = "skipped"
	StageStateRetrying   StageState = "retrying"
	StageStateRolledBack StageState = "rolled_back"
)

// PipelineStage represents a single stage in the 14-stage zero-downtime pipeline.
type PipelineStage struct {
	ID             int64      `json:"id"`
	MigrationID    int        `json:"migrationId"`
	StageName      string     `json:"stageName"`
	StageIndex     int        `json:"stageIndex"`
	State          StageState `json:"state"`
	AttemptCount   int        `json:"attemptCount"`
	CheckpointData string     `json:"checkpointData,omitempty"`
	ResultData     string     `json:"resultData,omitempty"`
	Error          string     `json:"error,omitempty"`
	StartedAt      string     `json:"startedAt,omitempty"`
	CompletedAt    string     `json:"completedAt,omitempty"`
}

// PipelineStageName is a typed enum for the 14 pipeline stages.
type PipelineStageName string

const (
	StageDiscovery              PipelineStageName = "discovery"
	StageAnalysis               PipelineStageName = "analysis"
	StagePlanning               PipelineStageName = "planning"
	StageValidation             PipelineStageName = "validation"
	StagePreparation            PipelineStageName = "preparation"
	StageInitialSync            PipelineStageName = "initial_sync"
	StageLiveReplication        PipelineStageName = "live_replication"
	StageHealthVerification     PipelineStageName = "health_verification"
	StagePreCutoverValidation   PipelineStageName = "pre_cutover_validation"
	StageTrafficSwitch          PipelineStageName = "traffic_switch"
	StagePostCutoverObservation PipelineStageName = "post_cutover_observation"
	StageFinalization           PipelineStageName = "finalization"
	StageArchive                PipelineStageName = "archive"
)

// AllStages returns the ordered list of pipeline stages.
func AllStages() []PipelineStageName {
	return []PipelineStageName{
		StageDiscovery,
		StageAnalysis,
		StagePlanning,
		StageValidation,
		StagePreparation,
		StageInitialSync,
		StageLiveReplication,
		StageHealthVerification,
		StagePreCutoverValidation,
		StageTrafficSwitch,
		StagePostCutoverObservation,
		StageFinalization,
		StageArchive,
	}
}

// --- Replication Models ---

// ReplicationMode represents the type of database replication.
type ReplicationMode string

const (
	ReplicationModeNone      ReplicationMode = "none"
	ReplicationModeStreaming ReplicationMode = "streaming"
	ReplicationModeLogical   ReplicationMode = "logical"
	ReplicationModeReplica   ReplicationMode = "replica"
	ReplicationModeDump      ReplicationMode = "dump"
)

// ReplicationStatus tracks the replication state for a database or Redis instance.
type ReplicationStatus struct {
	ID              int64           `json:"id"`
	MigrationID     int             `json:"migrationId"`
	DatabaseType    string          `json:"databaseType"`
	DatabaseName    string          `json:"databaseName,omitempty"`
	SourceHost      string          `json:"sourceHost,omitempty"`
	TargetHost      string          `json:"targetHost,omitempty"`
	ReplicationMode ReplicationMode `json:"replicationMode"`
	ReplicationLag  int64           `json:"replicationLag"`
	Status          string          `json:"status"`
	LastError       string          `json:"lastError,omitempty"`
	CreatedAt       string          `json:"createdAt"`
	UpdatedAt       string          `json:"updatedAt"`
}

// --- Traffic Switch Models ---

// TrafficProvider represents the traffic routing provider.
type TrafficProvider string

const (
	TrafficProviderCloudflare TrafficProvider = "cloudflare"
	TrafficProviderNginx      TrafficProvider = "nginx"
	TrafficProviderTraefik    TrafficProvider = "traefik"
	TrafficProviderHAProxy    TrafficProvider = "haproxy"
	TrafficProviderCaddy      TrafficProvider = "caddy"
	TrafficProviderDocker     TrafficProvider = "docker"
	TrafficProviderDNS        TrafficProvider = "dns"
)

// TrafficSwitchConfig stores the configuration for a traffic switch operation.
type TrafficSwitchConfig struct {
	ID             int64           `json:"id"`
	MigrationID    int             `json:"migrationId"`
	Provider       TrafficProvider `json:"provider"`
	OriginalConfig string          `json:"originalConfig,omitempty"`
	NewConfig      string          `json:"newConfig,omitempty"`
	SwitchState    string          `json:"switchState"`
	HealthCheckURL string          `json:"healthCheckUrl,omitempty"`
	RollbackConfig string          `json:"rollbackConfig,omitempty"`
	CreatedAt      string          `json:"createdAt"`
	UpdatedAt      string          `json:"updatedAt"`
}

// --- Health Check Models ---

// HealthCheckType represents the type of health check.
type HealthCheckType string

const (
	HealthCheckHTTP      HealthCheckType = "http"
	HealthCheckHTTPS     HealthCheckType = "https"
	HealthCheckTCP       HealthCheckType = "tcp"
	HealthCheckDatabase  HealthCheckType = "database"
	HealthCheckRedis     HealthCheckType = "redis"
	HealthCheckQueue     HealthCheckType = "queue"
	HealthCheckContainer HealthCheckType = "container"
	HealthCheckSystemd   HealthCheckType = "systemd"
	HealthCheckDNS       HealthCheckType = "dns"
	HealthCheckSSL       HealthCheckType = "ssl"
	HealthCheckDisk      HealthCheckType = "disk"
)

// HealthCheckResult stores the result of a single health check.
type HealthCheckResult struct {
	ID             int64           `json:"id"`
	MigrationID    int             `json:"migrationId"`
	ServerID       int             `json:"serverId"`
	CheckType      HealthCheckType `json:"checkType"`
	CheckTarget    string          `json:"checkTarget"`
	Status         string          `json:"status"`
	ResponseTimeMs int64           `json:"responseTimeMs,omitempty"`
	StatusCode     int             `json:"statusCode,omitempty"`
	ErrorMessage   string          `json:"errorMessage,omitempty"`
	HealthScore    float64         `json:"healthScore"`
	CreatedAt      string          `json:"createdAt"`
}

// HealthScore is a composite health score for a server.
type HealthScore struct {
	Score         float64 `json:"score"`
	ChecksTotal   int     `json:"checksTotal"`
	ChecksPassed  int     `json:"checksPassed"`
	ChecksFailed  int     `json:"checksFailed"`
	AvgResponseMs float64 `json:"avgResponseMs,omitempty"`
	ErrorRate     float64 `json:"errorRate,omitempty"`
}

// --- Cutover Models ---

// CutoverRecord records a cutover event.
type CutoverRecord struct {
	ID                int64  `json:"id"`
	MigrationID       int    `json:"migrationId"`
	CutoverType       string `json:"cutoverType"`
	PreviousState     string `json:"previousState,omitempty"`
	NewState          string `json:"newState,omitempty"`
	FreezeWrite       bool   `json:"freezeWrite"`
	QueueDrained      bool   `json:"queueDrained"`
	DeltaSynced       bool   `json:"deltaSynced"`
	HealthVerified    bool   `json:"healthVerified"`
	TrafficSwitched   bool   `json:"trafficSwitched"`
	RollbackTriggered bool   `json:"rollbackTriggered"`
	Error             string `json:"error,omitempty"`
	StartedAt         string `json:"startedAt"`
	CompletedAt       string `json:"completedAt,omitempty"`
}

// --- Rollback Models ---

// RollbackRecord records a rollback event.
type RollbackRecord struct {
	ID                 int64  `json:"id"`
	MigrationID        int    `json:"migrationId"`
	RollbackType       string `json:"rollbackType"`
	StageName          string `json:"stageName,omitempty"`
	Category           string `json:"category,omitempty"`
	TrafficReverted    bool   `json:"trafficReverted"`
	DNSReverted        bool   `json:"dnsReverted"`
	DBRoleReverted     bool   `json:"dbRoleReverted"`
	RedisRoleReverted  bool   `json:"redisRoleReverted"`
	QueueResumed       bool   `json:"queueResumed"`
	ContainersReverted bool   `json:"containersReverted"`
	ConfigsReverted    bool   `json:"configsReverted"`
	Success            bool   `json:"success"`
	Error              string `json:"error,omitempty"`
	StartedAt          string `json:"startedAt"`
	CompletedAt        string `json:"completedAt,omitempty"`
}

// --- Sync/Transfer Models ---

// SyncSession tracks a data sync operation (rsync, tar, etc.).
type SyncSession struct {
	ID               int64  `json:"id"`
	MigrationID      int    `json:"migrationId"`
	SyncType         string `json:"syncType"`
	SourcePath       string `json:"sourcePath,omitempty"`
	TargetPath       string `json:"targetPath,omitempty"`
	BytesTransferred int64  `json:"bytesTransferred"`
	BytesTotal       int64  `json:"bytesTotal"`
	FilesTransferred int    `json:"filesTransferred"`
	FilesTotal       int    `json:"filesTotal"`
	SpeedBytesSec    int64  `json:"speedBytesSec"`
	ChecksumVerified bool   `json:"checksumVerified"`
	Status           string `json:"status"`
	Error            string `json:"error,omitempty"`
	StartedAt        string `json:"startedAt"`
	CompletedAt      string `json:"completedAt,omitempty"`
}

// TransferSession tracks an individual file transfer.
type TransferSession struct {
	ID               int64  `json:"id"`
	MigrationID      int    `json:"migrationId"`
	SyncSessionID    int64  `json:"syncSessionId,omitempty"`
	FilePath         string `json:"filePath"`
	FileSize         int64  `json:"fileSize"`
	BytesTransferred int64  `json:"bytesTransferred"`
	ChecksumSource   string `json:"checksumSource,omitempty"`
	ChecksumTarget   string `json:"checksumTarget,omitempty"`
	TransferMethod   string `json:"transferMethod,omitempty"`
	Status           string `json:"status"`
	Error            string `json:"error,omitempty"`
	StartedAt        string `json:"startedAt"`
	CompletedAt      string `json:"completedAt,omitempty"`
}

// --- Verification Models ---

// VerificationResult stores the result of a verification check.
type VerificationResult struct {
	ID               int64  `json:"id"`
	MigrationID      int    `json:"migrationId"`
	VerificationType string `json:"verificationType"`
	Target           string `json:"target,omitempty"`
	Expected         string `json:"expected,omitempty"`
	Actual           string `json:"actual,omitempty"`
	Passed           bool   `json:"passed"`
	ErrorMessage     string `json:"errorMessage,omitempty"`
	CreatedAt        string `json:"createdAt"`
}

// --- Risk Models ---

// RiskClass represents the risk classification.
type RiskClass string

const (
	RiskClassLow      RiskClass = "low"
	RiskClassMedium   RiskClass = "medium"
	RiskClassHigh     RiskClass = "high"
	RiskClassCritical RiskClass = "critical"
)

// RiskReport stores the risk assessment for a migration.
type RiskReport struct {
	ID                 int64     `json:"id"`
	MigrationID        int       `json:"migrationId"`
	RiskScore          float64   `json:"riskScore"`
	RiskClass          RiskClass `json:"riskClass"`
	DowntimeEstimate   string    `json:"downtimeEstimate,omitempty"`
	DataSizeBytes      int64     `json:"dataSizeBytes"`
	DatabaseSizeBytes  int64     `json:"databaseSizeBytes"`
	ContainerCount     int       `json:"containerCount"`
	VolumeCount        int       `json:"volumeCount"`
	RollbackComplexity string    `json:"rollbackComplexity,omitempty"`
	Details            string    `json:"details,omitempty"`
	CreatedAt          string    `json:"createdAt"`
}

// --- Audit Models ---

// AuditEntry records a significant event in the migration lifecycle.
type AuditEntry struct {
	ID            int64  `json:"id"`
	MigrationID   int    `json:"migrationId"`
	EventType     string `json:"eventType"`
	EventData     string `json:"eventData,omitempty"`
	PreviousState string `json:"previousState,omitempty"`
	NewState      string `json:"newState,omitempty"`
	Actor         string `json:"actor,omitempty"`
	CreatedAt     string `json:"createdAt"`
}

// --- Metrics Models ---

// MigrationMetric stores a time-series metric for a migration.
type MigrationMetric struct {
	ID          int64   `json:"id"`
	MigrationID int     `json:"migrationId"`
	MetricName  string  `json:"metricName"`
	MetricValue float64 `json:"metricValue"`
	MetricUnit  string  `json:"metricUnit,omitempty"`
	StageName   string  `json:"stageName,omitempty"`
	CreatedAt   string  `json:"createdAt"`
}

// --- Queue State Models ---

// QueueState tracks the state of a message queue during cutover.
type QueueState struct {
	ID          int64  `json:"id"`
	MigrationID int    `json:"migrationId"`
	QueueType   string `json:"queueType"`
	QueueName   string `json:"queueName,omitempty"`
	Paused      bool   `json:"paused"`
	ActiveJobs  int    `json:"activeJobs"`
	Drained     bool   `json:"drained"`
	Synced      bool   `json:"synced"`
	Verified    bool   `json:"verified"`
	Error       string `json:"error,omitempty"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
}

// --- Provision State Models ---

// ProvisionState tracks the provisioning state of a target component.
type ProvisionState struct {
	ID          int64  `json:"id"`
	MigrationID int    `json:"migrationId"`
	Component   string `json:"component"`
	Installed   bool   `json:"installed"`
	Configured  bool   `json:"configured"`
	Verified    bool   `json:"verified"`
	Version     string `json:"version,omitempty"`
	Error       string `json:"error,omitempty"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
}

// --- Migration Config ---

// MigrationConfig is the configuration for a zero-downtime migration.
type MigrationConfig struct {
	// Categories to migrate (packages, configs, services, users, docker, databases).
	Categories []string `json:"categories"`

	// ConfigPaths for the configs category.
	ConfigPaths []string `json:"configPaths,omitempty"`

	// TrafficSwitch configuration.
	TrafficProvider TrafficProvider `json:"trafficProvider,omitempty"`
	TrafficConfig   string          `json:"trafficConfig,omitempty"`
	HealthCheckURL  string          `json:"healthCheckUrl,omitempty"`

	// Database replication configuration.
	ReplicationEnabled bool            `json:"replicationEnabled"`
	ReplicationMode    ReplicationMode `json:"replicationMode,omitempty"`

	// Sync configuration.
	SyncMethod        string `json:"syncMethod,omitempty"`
	BandwidthLimit    int64  `json:"bandwidthLimit,omitempty"`
	ParallelTransfers int    `json:"parallelTransfers,omitempty"`

	// Cutover configuration.
	FreezeWriteOnCutover bool          `json:"freezeWriteOnCutover"`
	DrainQueuesOnCutover bool          `json:"drainQueuesOnCutover"`
	ObservationDuration  time.Duration `json:"observationDuration,omitempty"`
	AutoRollbackOnError  bool          `json:"autoRollbackOnError"`
	MaxErrorRate         float64       `json:"maxErrorRate,omitempty"`
	MaxLatencyMs         int64         `json:"maxLatencyMs,omitempty"`

	// Retry configuration.
	MaxRetries int           `json:"maxRetries,omitempty"`
	RetryDelay time.Duration `json:"retryDelay,omitempty"`
}

// DefaultMigrationConfig returns a sensible default configuration.
func DefaultMigrationConfig() *MigrationConfig {
	return &MigrationConfig{
		Categories:           []string{"docker", "configs", "services", "users", "packages"},
		TrafficProvider:      TrafficProviderNginx,
		ReplicationEnabled:   true,
		ReplicationMode:      ReplicationModeStreaming,
		SyncMethod:           "rsync",
		ParallelTransfers:    4,
		FreezeWriteOnCutover: true,
		DrainQueuesOnCutover: true,
		ObservationDuration:  10 * time.Minute,
		AutoRollbackOnError:  true,
		MaxErrorRate:         0.05,
		MaxLatencyMs:         5000,
		MaxRetries:           3,
		RetryDelay:           5 * time.Second,
	}
}

// --- Extended WSMessage ---

// WSMessageExtended is the enriched WebSocket message format for the
// zero-downtime pipeline. It includes metrics, ETA, and stage information.
type WSMessageExtended struct {
	// Base fields (compatible with WSMessage).
	Step   string `json:"step"`
	Status string `json:"status"`
	Value  string `json:"value,omitempty"`
	Error  string `json:"error,omitempty"`

	// Pipeline stage information.
	Stage      string `json:"stage,omitempty"`
	StageIndex int    `json:"stageIndex,omitempty"`
	StageTotal int    `json:"stageTotal,omitempty"`

	// Progress metrics.
	Progress   float64 `json:"progress,omitempty"`
	BytesDone  int64   `json:"bytesDone,omitempty"`
	BytesTotal int64   `json:"bytesTotal,omitempty"`
	SpeedBytes int64   `json:"speedBytes,omitempty"`
	ETA        string  `json:"eta,omitempty"`

	// Replication metrics.
	ReplicationLag int64 `json:"replicationLag,omitempty"`

	// Health metrics.
	HealthScore float64 `json:"healthScore,omitempty"`

	// Risk metrics.
	RiskScore float64 `json:"riskScore,omitempty"`
	RiskClass string  `json:"riskClass,omitempty"`

	// State machine.
	CurrentState string `json:"currentState,omitempty"`

	// Event sequence for reconnect replay.
	Sequence int64 `json:"sequence,omitempty"`

	// Timestamp.
	Timestamp string `json:"timestamp,omitempty"`
}

// ToWSMessage converts an extended message to the base WSMessage format.
func (m WSMessageExtended) ToWSMessage() WSMessage {
	return WSMessage{
		Step:   m.Step,
		Status: m.Status,
		Value:  m.Value,
		Error:  m.Error,
	}
}

// --- Migration Session ---

// MigrationSession is the top-level object representing a zero-downtime
// migration session. It wraps the existing Migration model with the
// extended pipeline state, config, and metadata.
type MigrationSession struct {
	Migration            *Migration                 `json:"migration"`
	Config               *MigrationConfig           `json:"config,omitempty"`
	State                MigrationState             `json:"state"`
	Stages               []PipelineStage            `json:"stages,omitempty"`
	ReplicationStatus    []ReplicationStatus        `json:"replicationStatus,omitempty"`
	TrafficSwitch        *TrafficSwitchConfig       `json:"trafficSwitch,omitempty"`
	RiskReport           *RiskReport                `json:"riskReport,omitempty"`
	CompatibilityResults []CompatibilityCheckResult `json:"compatibilityResults,omitempty"`
	VerificationResults  []VerificationResult       `json:"verificationResults,omitempty"`
	HealthHistory        []HealthCheckResult        `json:"healthHistory,omitempty"`
	CutoverHistory       []CutoverRecord            `json:"cutoverHistory,omitempty"`
	RollbackHistory      []RollbackRecord           `json:"rollbackHistory,omitempty"`
	SyncSessions         []SyncSession              `json:"syncSessions,omitempty"`
	QueueStates          []QueueState               `json:"queueStates,omitempty"`
	ProvisionStates      []ProvisionState           `json:"provisionStates,omitempty"`
	AuditTrail           []AuditEntry               `json:"auditTrail,omitempty"`
	Events               []MigrationEvent           `json:"events,omitempty"`
}
