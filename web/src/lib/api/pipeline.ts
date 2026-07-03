import { api } from '$lib/api/client';

// --- Pipeline Stage Types ---

export interface PipelineStage {
  id: number;
  migrationId: number;
  stageName: string;
  stageIndex: number;
  state: string;
  attemptCount: number;
  checkpointData?: string;
  resultData?: string;
  error?: string;
  startedAt?: string;
  completedAt?: string;
}

// --- Replication ---

export interface ReplicationStatus {
  id: number;
  migrationId: number;
  databaseType: string;
  databaseName?: string;
  sourceHost?: string;
  targetHost?: string;
  replicationMode: string;
  replicationLag: number;
  status: string;
  lastError?: string;
}

// --- Traffic Switch ---

export interface TrafficSwitchConfig {
  id: number;
  migrationId: number;
  provider: string;
  originalConfig?: string;
  newConfig?: string;
  switchState: string;
  healthCheckUrl?: string;
  rollbackConfig?: string;
}

// --- Health Check ---

export interface HealthCheckResult {
  id: number;
  migrationId: number;
  serverId: number;
  checkType: string;
  checkTarget: string;
  status: string;
  responseTimeMs?: number;
  statusCode?: number;
  errorMessage?: string;
  healthScore: number;
}

// --- Risk ---

export interface RiskReport {
  id: number;
  migrationId: number;
  riskScore: number;
  riskClass: string;
  downtimeEstimate?: string;
  dataSizeBytes: number;
  databaseSizeBytes: number;
  containerCount: number;
  volumeCount: number;
  rollbackComplexity?: string;
  details?: string;
}

// --- Compatibility ---

export interface CompatibilityCheckResult {
  checkName: string;
  severity: 'info' | 'warning' | 'high' | 'critical';
  passed: boolean;
  message: string;
}

// --- Sync ---

export interface SyncSession {
  id: number;
  migrationId: number;
  syncType: string;
  sourcePath?: string;
  targetPath?: string;
  bytesTransferred: number;
  bytesTotal: number;
  filesTransferred: number;
  filesTotal: number;
  speedBytesSec: number;
  checksumVerified: boolean;
  status: string;
  error?: string;
}

// --- Queue ---

export interface QueueState {
  id: number;
  migrationId: number;
  queueType: string;
  queueName?: string;
  paused: boolean;
  activeJobs: number;
  drained: boolean;
  synced: boolean;
  verified: boolean;
  error?: string;
}

// --- Provision ---

export interface ProvisionState {
  id: number;
  migrationId: number;
  component: string;
  installed: boolean;
  configured: boolean;
  verified: boolean;
  version?: string;
  error?: string;
}

// --- Server Resource Metrics ---

export interface ServerResourceMetrics {
  timestamp: number;
  serverId?: number;
  status: string;                // "ok", "degraded", "unknown"
  source?: string;
  target?: string;
  cpuUsagePercent: number;
  ramUsedBytes: number;
  ramTotalBytes: number;
  diskUsedBytes: number;
  diskTotalBytes: number;
  networkRxBytesSec: number;
  networkTxBytesSec: number;
  loadAvg1m: number;
  loadAvg5m: number;
  loadAvg15m: number;
  uptimeSeconds: number;
  // Extended metrics (Phase 2)
  containers?: ContainerMetrics[];
  services?: ServiceMetric[];
  transfer?: TransferMetric;
  database?: DatabaseMetric[];
  redis?: RedisMetric;
  queue?: QueueMetricInfo[];
}

// --- Extended Monitoring Types ---

export interface ContainerMetrics {
  name: string;
  image: string;
  status: string;
  healthy: boolean;
  restartCount: number;
  cpuPercent: number;
  memoryUsage: number;
  memoryLimit: number;
  healthScore: number;
}

export interface ServiceMetric {
  name: string;
  status: string;
  subState: string;
  uptime: number;
  pid?: number;
  port?: number;
}

export interface TransferMetric {
  bytesTransferred: number;
  bytesTotal: number;
  speedBytesSec: number;
  eta: string;
  progress: number;
}

export interface DatabaseMetric {
  type: string;
  name: string;
  status: string;
  connections: number;
  replicationLag: number;
  sizeBytes: number;
  healthScore: number;
}

export interface RedisMetric {
  status: string;
  version: string;
  memoryUsed: number;
  memoryMax: number;
  connected: number;
  keys: number;
  uptime: number;
  replication: string;
  healthScore: number;
}

export interface QueueMetricInfo {
  type: string;
  name: string;
  status: string;
  activeJobs: number;
  queueLength: number;
  workers: number;
  drained: boolean;
}

// --- Container Health ---

export interface ContainerHealthInfo {
  name: string;
  image: string;
  status: string;
  healthy: boolean;
  restartCount: number;
  cpuPercent: number;
  memoryUsageBytes: number;
  memoryLimitBytes: number;
  networkRxBytes: number;
  networkTxBytes: number;
}

// --- Migration Config ---

export interface MigrationConfig {
  categories: string[];
  configPaths?: string[];
  trafficProvider?: string;
  trafficConfig?: string;
  healthCheckUrl?: string;
  replicationEnabled: boolean;
  replicationMode?: string;
  syncMethod?: string;
  bandwidthLimit?: number;
  parallelTransfers?: number;
  freezeWriteOnCutover: boolean;
  drainQueuesOnCutover: boolean;
  observationDuration?: number;
  autoRollbackOnError: boolean;
  maxErrorRate?: number;
  maxLatencyMs?: number;
  maxRetries?: number;
  retryDelay?: number;
}

export interface PipelineActionResponse {
  status: string;
  message?: string;
}

// --- Migration Session ---

export interface MigrationSession {
  migration: {
    id: number;
    sourceId: number;
    targetId: number;
    categories: string;
    status: string;
    state: string;
    config?: string;
    riskScore: number;
    riskClass: string;
    error?: string;
    createdAt: string;
    completedAt?: string;
  };
  config?: MigrationConfig;
  state: string;
  stages?: PipelineStage[];
  replicationStatus?: ReplicationStatus[];
  trafficSwitch?: TrafficSwitchConfig;
  riskReport?: RiskReport;
  healthHistory?: HealthCheckResult[];
  syncSessions?: SyncSession[];
  queueStates?: QueueState[];
  provisionStates?: ProvisionState[];
}

// --- Extended WebSocket Message ---

export interface WSMessageExtended {
  step: string;
  status: string;
  value?: string;
  error?: string;
  stage?: string;
  stageIndex?: number;
  stageTotal?: number;
  progress?: number;
  bytesDone?: number;
  bytesTotal?: number;
  speedBytes?: number;
  eta?: string;
  replicationLag?: number;
  healthScore?: number;
  riskScore?: number;
  riskClass?: string;
  currentState?: string;
  // Resource metrics
  cpuUsagePercent?: number;
  ramUsedBytes?: number;
  ramTotalBytes?: number;
  diskUsedPercent?: number;
  networkRxBytesSec?: number;
  networkTxBytesSec?: number;
  // Container & queue info
  containerHealth?: ContainerHealthInfo[];
  queueInfo?: QueueState[];
  // Event sequence for reconnect replay
  sequence?: number;
  timestamp?: string;
}

// --- Metrics & Audit ---

export interface MigrationMetric {
  id: number;
  migrationId: number;
  metricName: string;
  metricValue: number;
  metricUnit?: string;
  stageName?: string;
  createdAt: string;
}

export interface AuditEntry {
  id: number;
  migrationId: number;
  eventType: string;
  eventData?: string;
  previousState?: string;
  newState?: string;
  actor?: string;
  createdAt: string;
}

// --- Migration Event (for event replay) ---

export interface MigrationEvent {
  id: number;
  migrationId: number;
  sequence: number;
  timestamp: string;
  level: string;
  stage: string;
  type: string;
  message: string;
  details?: string;
  source: string;
  correlationId?: string;
}

// --- Batch 2: Workload, Dependency, Compatibility, Strategy, Warning Types ---

export type WorkloadType =
  | 'stateless_application'
  | 'stateful_application'
  | 'database'
  | 'cache'
  | 'queue'
  | 'worker'
  | 'scheduler'
  | 'reverse_proxy'
  | 'monitoring'
  | 'logging'
  | 'storage'
  | 'messaging'
  | 'search'
  | 'ai_service'
  | 'object_storage'
  | 'unknown';

export interface WorkloadClassification {
  name: string;
  type: WorkloadType;
  confidence: number;
  reasons: string[];
  source: string;
  sourceName: string;
  dependencies?: string[];
  port?: number;
}

export interface DependencyNode {
  id: string;
  name: string;
  type: string;
  workloadType?: WorkloadType;
  port?: number;
  metadata?: Record<string, string>;
}

export interface DependencyEdge {
  from: string;
  to: string;
  reason: string;
}

export interface DependencyGraph {
  nodes: DependencyNode[];
  edges: DependencyEdge[];
}

export interface CompatibilityIssue {
  category: string;
  severity: string;
  message: string;
  recommendation?: string;
  blocking: boolean;
  riskScore: number;
  manualAction?: string;
  sourceValue?: string;
  targetValue?: string;
}

export type MigrationStrategy =
  | 'cold_migration'
  | 'warm_migration'
  | 'live_sync'
  | 'shadow_deployment'
  | 'blue_green_preparation'
  | 'manual_cutover'
  | 'reverse_proxy_cutover'
  | 'dns_cutover'
  | 'database_replication'
  | 'queue_drain';

export interface StrategySelection {
  strategy: MigrationStrategy;
  reasoning: string;
  estimatedDowntime: string;
  riskLevel: string;
  rollbackAvailable: boolean;
  requirements?: string[];
  prerequisites?: string[];
}

export type PlannerWarningType =
  | 'risk'
  | 'recommendation'
  | 'blocking'
  | 'manual_step'
  | 'verification_step'
  | 'rollback_note'
  | 'unsupported_workload';

export interface PlannerWarning {
  type: PlannerWarningType;
  severity: string;
  message: string;
  recommendation?: string;
  blocking: boolean;
  riskScore: number;
  manualAction?: string;
  workloadName?: string;
  category?: string;
}

export interface PlannerResult {
  workloads: WorkloadClassification[];
  dependencyGraph: DependencyGraph;
  compatibilityIssues: CompatibilityIssue[];
  strategy: StrategySelection;
  warnings: PlannerWarning[];
  riskScore: number;
  blockingIssues: number;
  recommendationCount: number;
}

// --- Pipeline API ---

export const pipelineApi = {
  // Session
  getSession: (id: number) => api.get(`/pipeline/migrations/${id}`) as Promise<MigrationSession>,
  getStages: (id: number) => api.get(`/pipeline/migrations/${id}/stages`) as Promise<PipelineStage[]>,

  // Risk
  getRisk: (id: number) => api.get(`/pipeline/migrations/${id}/risk`) as Promise<RiskReport>,
  assessRisk: (id: number) => api.post(`/pipeline/migrations/${id}/risk`, {}) as Promise<RiskReport>,

  // Compatibility
  getCompatibility: (id: number) => api.get(`/pipeline/migrations/${id}/compatibility`) as Promise<CompatibilityCheckResult[]>,
  checkCompatibility: (id: number) => api.post(`/pipeline/migrations/${id}/compatibility`, {}) as Promise<CompatibilityCheckResult[]>,

  // Health
  getHealth: (id: number) => api.get(`/pipeline/migrations/${id}/health`) as Promise<HealthCheckResult[]>,
  checkHealth: (id: number) => api.post(`/pipeline/migrations/${id}/health`, {}) as Promise<HealthCheckResult[]>,

  // Replication
  getReplication: (id: number) => api.get(`/pipeline/migrations/${id}/replication`) as Promise<ReplicationStatus[]>,

  // Sync
  getSyncSessions: (id: number) => api.get(`/pipeline/migrations/${id}/sync`) as Promise<SyncSession[]>,

  // Queue
  getQueueStates: (id: number) => api.get(`/pipeline/migrations/${id}/queue`) as Promise<QueueState[]>,

  // Provision
  getProvisionStates: (id: number) => api.get(`/pipeline/migrations/${id}/provision`) as Promise<ProvisionState[]>,
  provision: (id: number, components?: string[]) => api.post(`/pipeline/migrations/${id}/provision`, { components }),

  // Metrics & Audit
  getMetrics: (id: number) => api.get(`/pipeline/migrations/${id}/metrics`) as Promise<MigrationMetric[]>,
  getAuditTrail: (id: number) => api.get(`/pipeline/migrations/${id}/audit`) as Promise<AuditEntry[]>,

  // Event replay (for WS reconnect)
  getEvents: (id: number, afterSeq: number = 0, limit: number = 200) =>
    api.get(`/pipeline/migrations/${id}/events?after_seq=${afterSeq}&limit=${limit}`) as Promise<MigrationEvent[]>,

  // Planner results (Batch 2)
  getWorkloads: (id: number) => api.get(`/pipeline/migrations/${id}/workloads`) as Promise<WorkloadClassification[]>,
  getDependencyGraph: (id: number) => api.get(`/pipeline/migrations/${id}/dependency-graph`) as Promise<DependencyGraph>,
  getCompatibility: (id: number) => api.get(`/pipeline/migrations/${id}/compatibility`) as Promise<CompatibilityIssue[]>,
  getStrategy: (id: number) => api.get(`/pipeline/migrations/${id}/strategy`) as Promise<StrategySelection>,
  getWarnings: (id: number) => api.get(`/pipeline/migrations/${id}/warnings`) as Promise<PlannerWarning[]>,
  getPlannerResult: (id: number) => api.get(`/pipeline/migrations/${id}/planner-result`) as Promise<PlannerResult>,

  // Config
  configure: (id: number, config: MigrationConfig) => api.put(`/pipeline/migrations/${id}/config`, config),

  // Actions
  cutover: (id: number) => api.post(`/pipeline/migrations/${id}/actions/cutover`, {}) as Promise<PipelineActionResponse>,
  commit: (id: number) => api.post(`/pipeline/migrations/${id}/actions/commit`, {}) as Promise<PipelineActionResponse>,
  rollbackMigration: (id: number) => api.post(`/pipeline/migrations/${id}/actions/rollback`, {}) as Promise<PipelineActionResponse>,
  pause: (id: number) => api.post(`/pipeline/migrations/${id}/actions/pause`, {}) as Promise<PipelineActionResponse>,
  resume: (id: number) => api.post(`/pipeline/migrations/${id}/actions/resume`, {}) as Promise<PipelineActionResponse>,
  cancel: (id: number) => api.post(`/pipeline/migrations/${id}/actions/cancel`, {}) as Promise<PipelineActionResponse>,
  retry: (id: number) => api.post(`/pipeline/migrations/${id}/actions/retry`, {}) as Promise<PipelineActionResponse>,

  // Export
  exportReport: (id: number) => api.get(`/pipeline/migrations/${id}/export`) as Promise<Blob>,
};

// --- Pipeline WebSocket ---

export type WSConnectionState = 'connecting' | 'connected' | 'disconnected' | 'reconnecting' | 'failed';

function getWsToken(): string {
  return typeof localStorage !== 'undefined' ? localStorage.getItem('meshium_session_token') ?? '' : '';
}

function wsUrl(path: string): string {
  const proto = location.protocol === 'https:' ? 'wss' : 'ws';
  return `${proto}://${location.host}${path}`;
}

function wsSubprotocols(): string[] {
  const token = getWsToken();
  return token ? [`meshium-auth.${token}`] : [];
}

export interface WSPipelineOptions {
  maxRetries?: number;
  initialDelay?: number;
  maxDelay?: number;
  heartbeatInterval?: number;
}

/**
 * wsPipelineConnect creates a resilient WebSocket connection with:
 * - Automatic reconnect with exponential backoff
 * - Heartbeat (client ping / server pong)
 * - Event sequence tracking for replay on reconnect
 * - Connection state callbacks
 *
 * Returns a control handle with close() method.
 */
export function wsPipelineConnect(
  migrationId: number,
  onMessage: (msg: WSMessageExtended) => void,
  onStatusChange: (status: WSConnectionState) => void,
  opts?: WSPipelineOptions
): { close: () => void } {
  const maxRetries = opts?.maxRetries ?? 10;
  const initialDelay = opts?.initialDelay ?? 1000;
  const maxDelay = opts?.maxDelay ?? 30000;
  const heartbeatInterval = opts?.heartbeatInterval ?? 30000;

  let retries = 0;
  let lastSequence = 0;
  let ws: WebSocket | null = null;
  let heartbeatTimer: ReturnType<typeof setInterval> | null = null;
  let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  let closed = false;

  function clearTimers() {
    if (heartbeatTimer) { clearInterval(heartbeatTimer); heartbeatTimer = null; }
    if (reconnectTimer) { clearTimeout(reconnectTimer); reconnectTimer = null; }
  }

  function connect() {
    if (closed) return;

    onStatusChange(retries > 0 ? 'reconnecting' : 'connecting');
    const subprotocols = wsSubprotocols();
    ws = subprotocols.length > 0
      ? new WebSocket(wsUrl(`/ws/pipeline/${migrationId}`), subprotocols)
      : new WebSocket(wsUrl(`/ws/pipeline/${migrationId}`));

    ws.onopen = () => {
      retries = 0;
      onStatusChange('connected');

      // Replay missed events after reconnect
      if (lastSequence > 0) {
        replayEvents(migrationId, lastSequence).then((events) => {
          for (const event of events) {
            onMessage(event);
            if (event.sequence && event.sequence > lastSequence) {
              lastSequence = event.sequence;
            }
          }
        });
      }

      // Start heartbeat
      heartbeatTimer = setInterval(() => {
        if (ws && ws.readyState === WebSocket.OPEN) {
          ws.send(JSON.stringify({ action: 'ping' }));
        }
      }, heartbeatInterval);
    };

    ws.onmessage = (event) => {
      try {
        const msg = JSON.parse(event.data) as WSMessageExtended;
        // Track sequence for replay
        if (msg.sequence && msg.sequence > lastSequence) {
          lastSequence = msg.sequence;
        }
        onMessage(msg);
      } catch {
        // Ignore non-JSON messages
      }
    };

    ws.onclose = () => {
      clearTimers();
      if (closed) {
        onStatusChange('disconnected');
        return;
      }
      if (retries < maxRetries) {
        retries++;
        const delay = Math.min(initialDelay * Math.pow(2, retries - 1), maxDelay);
        onStatusChange('reconnecting');
        reconnectTimer = setTimeout(connect, delay);
      } else {
        onStatusChange('failed');
      }
    };

    ws.onerror = () => {
      // onclose will fire after onerror, so reconnect logic is handled there
    };
  }

  connect();

  return {
    close: () => {
      closed = true;
      clearTimers();
      if (ws) {
        ws.onclose = null; // Prevent reconnect
        ws.close();
        ws = null;
      }
      onStatusChange('disconnected');
    }
  };
}

/**
 * Replay missed events from the server after a reconnect.
 * Calls the event replay REST endpoint.
 */
async function replayEvents(migrationId: number, afterSequence: number): Promise<WSMessageExtended[]> {
  try {
    const events = await api.get(`/pipeline/migrations/${migrationId}/events?after_seq=${afterSequence}&limit=200`) as MigrationEvent[];
    return events.map((e) => ({
      step: e.stage || e.type,
      status: e.level === 'critical' || e.level === 'error' ? 'error' : e.level === 'warning' ? 'warning' : 'info',
      value: e.message,
      stage: e.stage,
      sequence: e.sequence,
      timestamp: e.timestamp,
    }));
  } catch {
    return [];
  }
}

// MigrationEvent type for replay
interface MigrationEvent {
  id: number;
  migrationId: number;
  sequence: number;
  timestamp: string;
  level: string;
  stage: string;
  type: string;
  message: string;
  details?: string;
  source: string;
  correlationId?: string;
}

/**
 * Legacy wsPipeline function for backward compatibility.
 * Prefer wsPipelineConnect for new code.
 */
export function wsPipeline(
  migrationId: number,
  onMessage: (msg: WSMessageExtended) => void,
  onClose?: () => void,
  onError?: () => void
): WebSocket {
  const subprotocols = wsSubprotocols();
  const ws = subprotocols.length > 0
    ? new WebSocket(wsUrl(`/ws/pipeline/${migrationId}`), subprotocols)
    : new WebSocket(wsUrl(`/ws/pipeline/${migrationId}`));
  ws.onmessage = (event) => {
    const msg = JSON.parse(event.data) as WSMessageExtended;
    onMessage(msg);
  };
  ws.onclose = () => onClose?.();
  ws.onerror = () => onError?.();
  return ws;
}

// --- Wizard Step Definition ---

export interface WizardStep {
  id: number;
  name: string;
  description: string;
  requiresAction: boolean;
  validationGate: string;
}

export const WIZARD_STEPS: WizardStep[] = [
  { id: 0,  name: 'Discovery',       description: 'Discover services, containers, and databases on source & target', requiresAction: true, validationGate: 'Discovery must complete successfully' },
  { id: 1,  name: 'Compatibility',   description: 'Check source/target compatibility for blockers', requiresAction: true, validationGate: 'No critical compatibility failures' },
  { id: 2,  name: 'Risk',            description: 'Assess migration risk score and downtime estimate', requiresAction: true, validationGate: 'Risk assessment must complete' },
  { id: 3,  name: 'Plan',            description: 'Review migration plan and configure options', requiresAction: true, validationGate: 'At least one category must be selected' },
  { id: 4,  name: 'Dry Run',         description: 'Preview changes without applying them', requiresAction: true, validationGate: 'Dry run must complete' },
  { id: 5,  name: 'Provision',       description: 'Install dependencies on target server', requiresAction: true, validationGate: 'Provisioning must complete' },
  { id: 6,  name: 'Execute',         description: 'Start initial sync and replication', requiresAction: true, validationGate: 'Pipeline must be started' },
  { id: 7,  name: 'Live Monitoring', description: 'Monitor sync progress and replication lag', requiresAction: false, validationGate: 'Replication lag must be below threshold' },
  { id: 8,  name: 'Cutover',         description: 'Switch traffic from source to target', requiresAction: true, validationGate: 'Cutover must be confirmed' },
  { id: 9,  name: 'Observation',     description: 'Monitor post-cutover health and stability', requiresAction: false, validationGate: 'Observation period must pass' },
  { id: 10, name: 'Finish',          description: 'Migration complete — review summary', requiresAction: false, validationGate: '' },
];

// --- Default Config ---

export function defaultMigrationConfig(): MigrationConfig {
  return {
    categories: [],
    replicationEnabled: true,
    replicationMode: 'streaming',
    syncMethod: 'rsync',
    freezeWriteOnCutover: true,
    drainQueuesOnCutover: true,
    observationDuration: 300,
    autoRollbackOnError: true,
    maxErrorRate: 5,
    maxLatencyMs: 2000,
    maxRetries: 3,
    retryDelay: 30,
    parallelTransfers: 4,
  };
}
