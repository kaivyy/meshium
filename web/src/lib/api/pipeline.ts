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

function getWsToken(): string {
  return typeof localStorage !== 'undefined' ? localStorage.getItem('meshium_session_token') ?? '' : '';
}

function wsUrl(path: string): string {
  const proto = location.protocol === 'https:' ? 'wss' : 'ws';
  const token = getWsToken();
  const tokenParam = token ? `?token=${encodeURIComponent(token)}` : '';
  return `${proto}://${location.host}${path}${tokenParam}`;
}

export function wsPipeline(
  migrationId: number,
  onMessage: (msg: WSMessageExtended) => void,
  onClose?: () => void,
  onError?: () => void
): WebSocket {
  const ws = new WebSocket(wsUrl(`/ws/pipeline/${migrationId}`));
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
