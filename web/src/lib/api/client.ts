const BASE = '/api';

type QueryValue = string | number | boolean | undefined | null;

// Session token management — stored in memory (not localStorage for security)
let sessionToken: string | null = null;

export function setSessionToken(token: string | null) {
  sessionToken = token;
}

export function getSessionToken(): string | null {
  return sessionToken;
}

export class APIError extends Error {
  code: string;

  constructor(message: string, code: string) {
    super(message);
    this.code = code;
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = {};
  if (body) {
    headers['Content-Type'] = 'application/json';
  }
  if (sessionToken) {
    headers['X-Session-Token'] = sessionToken;
  }

  const res = await fetch(`${BASE}${path}`, {
    method,
    headers,
    body: body ? JSON.stringify(body) : undefined
  });

  // If we get a 401, clear the session token and redirect to login
  if (res.status === 401) {
    sessionToken = null;
    if (typeof window !== 'undefined' && window.location.pathname !== '/login') {
      window.location.href = '/login';
    }
  }

  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: 'Unknown error', code: 'UNKNOWN' }));
    throw new APIError(err.error, err.code);
  }

  const contentLength = res.headers.get('content-length');
  const contentType = res.headers.get('content-type')?.toLowerCase();

  if (res.status === 204 || contentLength === '0') {
    return undefined as T;
  }

  if (!contentType || !contentType.includes('application/json')) {
    return undefined as T;
  }

  return res.json();
}

function buildPath(path: string, query?: Record<string, QueryValue>) {
  if (!query) {
    return path;
  }

  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(query)) {
    if (value === undefined || value === null || value === '') {
      continue;
    }
    params.set(key, String(value));
  }

  const qs = params.toString();
  return qs ? `${path}?${qs}` : path;
}

export interface StatusResponse {
  status: string;
}

export interface ServerListFilters {
  environment?: string;
  region?: string;
  tag?: string;
  q?: string;
}

export interface ServerResponse {
  id: number;
  name: string;
  description: string;
  host: string;
  port: number;
  username: string;
  authMethod: string;
  credentialStatus: string;
  fingerprint?: string;
  keyType?: string;
  bastionId?: number;
  tags: string[];
  environment: string;
  region: string;
  icon: string;
  color: string;
  favorite: boolean;
  lastSuccess: string;
  lastFailure: string;
  failureCount: number;
  successCount: number;
  createdAt: string;
  updatedAt: string;
}

export interface ServerCreateRequest {
  name: string;
  description: string;
  host: string;
  port: number;
  username: string;
  password?: string;
  sshKey?: string;
  passphrase?: string;
  tags: string[];
  environment: string;
  region: string;
  icon: string;
  color: string;
  keyType?: string;
  bastionId?: number;
}

export interface ServerUpdateRequest {
  name?: string;
  description?: string;
  host?: string;
  port?: number;
  username?: string;
  password?: string;
  sshKey?: string;
  passphrase?: string;
  tags?: string[];
  environment?: string;
  region?: string;
  icon?: string;
  color?: string;
  keyType?: string;
  bastionId?: number;
}

export interface ServerInfo {
  sshStatus: string;
  latencyMs: number;
  hostname: string;
  os: string;
  kernel: string;
  architecture: string;
  cpuModel: string;
  cpuCores: number;
  ramTotalMb: number;
  diskTotalGb: number;
  virtualization: string;
  provider: string;
  publicIp: string;
  privateIp: string;
  timezone: string;
}

export interface AuthStatus {
  authMethod: string;
  credentialStatus: string;
  hasPassword: boolean;
  hasSSHKey: boolean;
  hasPassphrase: boolean;
  fingerprint?: string;
  keyType?: string;
}

export interface CredentialHealthScore {
  score: number;
  grade: string;
  passwordExists: boolean;
  keyInstalled: boolean;
  fingerprintVerified: boolean;
  knownHost: boolean;
  recentSuccess: boolean;
  recentFailure: boolean;
  keyAge: string;
  passphraseEnabled: boolean;
  bastionHealthy: boolean;
  agentHealthy: boolean;
}

export interface ConnectionHistoryEntry {
  id: number;
  serverId: number;
  hostname: string;
  ip: string;
  username: string;
  authMethod: string;
  keyFingerprint?: string;
  agentUsed: boolean;
  bastionId?: number;
  success: boolean;
  durationMs: number;
  latencyMs?: number;
  exitStatus?: number;
  failureReason?: string;
  cipher?: string;
  kex?: string;
  compression?: string;
  remoteBanner?: string;
  timestamp: string;
}

export interface ConnectionMetrics {
  totalAttempts: number;
  successCount: number;
  failureCount: number;
  successRate: number;
  failureRate: number;
  lastSuccess?: string;
  lastFailure?: string;
}

export interface RetryConfig {
  serverId: number;
  retryCount: number;
  retryDelayMs: number;
  backoffStrategy: string;
  jitterMs: number;
  reconnectPolicy: string;
  authRetryOrder: string[];
}

export interface ServerKey {
  id: number;
  serverId: number;
  label: string;
  keyType: string;
  publicKey: string;
  fingerprint: string;
  notes?: string;
  enabled: boolean;
  isDefault: boolean;
  priority: number;
  lastUsed?: string;
  createdAt: string;
  updatedAt: string;
}

export interface ServerKeyRequest {
  label: string;
  keyType: string;
  privateKey: string;
  passphrase?: string;
  notes?: string;
}

export interface KeyInstallResult {
  success: boolean;
  message: string;
  fingerprint?: string;
  keyType?: string;
  alreadyInstalled: boolean;
}

export interface KeyVerifyResult {
  success: boolean;
  message: string;
  fingerprint?: string;
  installed: boolean;
}

export interface KeyRotationResult {
  success: boolean;
  message: string;
  oldFingerprint?: string;
  newFingerprint?: string;
  keyType?: string;
}

export interface FingerprintResult {
  sha256: string;
  md5: string;
  keyType?: string;
}

export interface AuthTestResult {
  success: boolean;
  authMethod: string;
  latencyMs: number;
  message?: string;
}

export interface SSHAgentConfig {
  useAgent: boolean;
  preferredIdentity: string;
  agentForwarding: boolean;
}

export interface AuthPriorityEntry {
  method: string;
  priority: number;
  enabled: boolean;
}

export interface ConnectionProfile {
  id: number;
  name: string;
  description: string;
  timeoutSeconds: number;
  retryCount: number;
  retryDelayMs: number;
  backoffStrategy: string;
  keepaliveSeconds: number;
  reconnectEnabled: boolean;
  bufferSizeKb: number;
  compression: boolean;
  parallelism: number;
  isBuiltin: boolean;
}

export interface KnownHostEntry {
  host: string;
  port: number;
  hostKey: string;
  fingerprintSha256: string;
  fingerprintMd5: string;
  algorithm: string;
  bits: number;
  status: string;
  verified: boolean;
  serverId?: number;
  createdAt: string;
  updatedAt: string;
}

export interface HostKeyChange {
  id: number;
  host: string;
  port: number;
  oldFingerprint?: string;
  newFingerprint?: string;
  riskLevel: string;
  actionTaken: string;
  serverId?: number;
  timestamp: string;
}

export interface AuthDashboard {
  passwordServers: number;
  keyServers: number;
  agentServers: number;
  bastionServers: number;
  fingerprintChanged: number;
  unknownHosts: number;
  credentialWarnings: number;
  expiredKeys: number;
  authFailures: number;
  authSuccessRate: number;
  connectionSuccessRate: number;
  averageLatency: number;
  medianLatency: number;
  p95Latency: number;
  p99Latency: number;
}

export interface SSHAgentIdentity {
  fingerprintSha256: string;
  type: string;
  comment: string;
}

export interface SSHAgentStatus {
  available: boolean;
  socketPath: string;
  identities: SSHAgentIdentity[];
}

export interface ExportData {
  servers: ServerResponse[];
  knownHosts: KnownHostEntry[];
  profiles: ConnectionProfile[];
  authPriority: AuthPriorityEntry[];
  version: string;
  exportedAt: string;
}

export type ImportData = Record<string, unknown>;

export const api = {
  get: <T>(path: string) => request<T>('GET', path),
  post: <T>(path: string, body?: unknown) => request<T>('POST', path, body),
  put: <T>(path: string, body?: unknown) => request<T>('PUT', path, body),
  delete: <T>(path: string) => request<T>('DELETE', path),
  patch: <T>(path: string, body?: unknown) => request<T>('PATCH', path, body)
};

export const serverApi = {
  list: (filters?: ServerListFilters) =>
    api.get<ServerResponse[]>(buildPath('/servers', filters as Record<string, QueryValue> | undefined)),
  get: (id: number) => api.get<ServerResponse>(`/servers/${id}`),
  create: (data: ServerCreateRequest) => api.post<ServerResponse>('/servers', data),
  update: (id: number, data: ServerUpdateRequest) => api.put<ServerResponse>(`/servers/${id}`, data),
  delete: (id: number) => api.delete<StatusResponse>(`/servers/${id}`),
  toggleFavorite: (id: number) => api.patch<StatusResponse>(`/servers/${id}/favorite`),
  getInfo: (id: number) => api.get<ServerInfo>(`/servers/${id}/info`),
  getAuthStatus: (id: number) => api.get<AuthStatus>(`/servers/${id}/auth-status`),
  getCredentialHealth: (id: number) => api.get<CredentialHealthScore>(`/servers/${id}/credential-health`),
  getConnectionHistory: (id: number, limit = 100) =>
    api.get<ConnectionHistoryEntry[]>(buildPath(`/servers/${id}/connection-history`, { limit })),
  getConnectionMetrics: (id: number) => api.get<ConnectionMetrics>(`/servers/${id}/connection-metrics`),
  getRetryConfig: (id: number) => api.get<RetryConfig>(`/servers/${id}/retry-config`),
  setRetryConfig: (id: number, config: RetryConfig) => api.put<StatusResponse>(`/servers/${id}/retry-config`, config),
  listKeys: (id: number) => api.get<ServerKey[]>(`/servers/${id}/keys`),
  addKey: (id: number, key: ServerKeyRequest) => api.post<ServerKey>(`/servers/${id}/keys`, key),
  deleteKey: (serverId: number, keyId: number) => api.delete<StatusResponse>(`/servers/${serverId}/keys/${keyId}`),
  setDefaultKey: (serverId: number, keyId: number) =>
    api.patch<StatusResponse>(`/servers/${serverId}/keys/${keyId}/default`),
  installKey: (id: number) => api.post<KeyInstallResult>(`/servers/${id}/install-key`),
  verifyKey: (id: number) => api.post<KeyVerifyResult>(`/servers/${id}/verify-key`),
  rotateKey: (id: number, keyType?: string) =>
    api.post<KeyRotationResult>(`/servers/${id}/rotate-key`, keyType ? { keyType } : undefined),
  getFingerprint: (id: number) => api.get<FingerprintResult>(`/servers/${id}/fingerprint`),
  testAuth: (id: number) => api.post<AuthTestResult>(`/servers/${id}/test-auth`),
  removePassword: (id: number) => api.post<StatusResponse>(`/servers/${id}/remove-password`),
  clearKey: (id: number) => api.post<StatusResponse>(`/servers/${id}/clear-key`),
  getAgentConfig: (id: number) => api.get<SSHAgentConfig>(`/servers/${id}/agent-config`),
  setAgentConfig: (id: number, config: SSHAgentConfig) => api.put<StatusResponse>(`/servers/${id}/agent-config`, config)
};

export const sshApi = {
  getAuthPriority: () => api.get<AuthPriorityEntry[]>('/auth-priority'),
  setAuthPriority: (entries: AuthPriorityEntry[]) => api.put<StatusResponse>('/auth-priority', entries),
  listConnectionProfiles: () => api.get<ConnectionProfile[]>('/connection-profiles'),
  getConnectionProfile: (id: number) => api.get<ConnectionProfile>(`/connection-profiles/${id}`),
  createConnectionProfile: (profile: ConnectionProfile) =>
    api.post<ConnectionProfile>('/connection-profiles', profile),
  updateConnectionProfile: (id: number, profile: ConnectionProfile) =>
    api.put<StatusResponse>(`/connection-profiles/${id}`, profile),
  deleteConnectionProfile: (id: number) => api.delete<StatusResponse>(`/connection-profiles/${id}`),
  listKnownHosts: () => api.get<KnownHostEntry[]>('/known-hosts'),
  removeKnownHost: (host: string, port: number) =>
    api.delete<StatusResponse>(`/known-hosts/${encodeURIComponent(host)}/${port}`),
  listHostKeyChanges: (limit = 100) => api.get<HostKeyChange[]>(buildPath('/host-key-changes', { limit })),
  getDashboard: () => api.get<AuthDashboard>('/dashboard'),
  getSSHAgentStatus: () => api.get<SSHAgentStatus>('/ssh-agent/status'),
  exportData: () => api.get<ExportData>('/export'),
  importData: (data: ImportData) => api.post<StatusResponse>('/import', data)
};
