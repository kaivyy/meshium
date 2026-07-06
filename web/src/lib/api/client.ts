const BASE = '/api';

export class APIError extends Error {
  code: string;
  constructor(message: string, code: string) {
    super(message);
    this.code = code;
  }
}

function getSessionToken(): string | null {
  if (typeof localStorage === 'undefined') return null;
  return localStorage.getItem('meshium_session_token');
}

export function setSessionToken(token: string) {
  if (typeof localStorage === 'undefined') return;
  localStorage.setItem('meshium_session_token', token);
}

export function clearSessionToken() {
  if (typeof localStorage === 'undefined') return;
  localStorage.removeItem('meshium_session_token');
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const token = getSessionToken();
  const headers: Record<string, string> = {};
  if (body) {
    headers['Content-Type'] = 'application/json';
  }
  if (token) {
    headers['Authorization'] = `Bearer ${token}`;
  }

  const res = await fetch(`${BASE}${path}`, {
    method,
    headers,
    body: body ? JSON.stringify(body) : undefined
  });

  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: 'Unknown error', code: 'UNKNOWN' }));
    // On 403 (forbidden) or 401 (unauthorized), clear the stale session token
    // — the layout's reactive block will handle the redirect to /login.
    if (res.status === 403 || res.status === 401) {
      clearSessionToken();
    }
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

export const api = {
  get: <T>(path: string) => request<T>('GET', path),
  post: <T>(path: string, body?: unknown) => request<T>('POST', path, body),
  put: <T>(path: string, body?: unknown) => request<T>('PUT', path, body),
  delete: <T>(path: string) => request<T>('DELETE', path),
  patch: <T>(path: string, body?: unknown) => request<T>('PATCH', path, body)
};

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

export interface AuthPriorityEntry {
  method: string;
  priority: number;
  enabled: boolean;
}

export interface ConnectionHistoryEntry {
  id: number;
  serverId: number;
  success: boolean;
  durationMs: number;
  reason: string;
  remoteIp: string;
  fingerprint: string;
  authMethod: string;
  createdAt: string;
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

export interface CredentialHealthScore {
  score: number;
  grade: string;
  passwordExists: boolean;
  keyInstalled: boolean;
  fingerprintVerified: boolean;
  knownHost: boolean;
  recentSuccess: boolean;
  recentFailure: boolean;
  keyAge?: string;
  passphraseEnabled: boolean;
  bastionHealthy: boolean;
  agentHealthy: boolean;
}

export interface HostKeyChange {
  id: number;
  host: string;
  port: number;
  oldFingerprint: string;
  newFingerprint: string;
  riskLevel: string;
  actionTaken: string;
  serverId: number;
  timestamp: string;
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
  serverId: number;
  createdAt: string;
  updatedAt: string;
}

export interface SSHAgentStatus {
  available: boolean;
  socketPath?: string;
  identities: Array<{ fingerprintSha256: string; type: string; comment?: string }>;
}

export const sshApi = {
  listKnownHosts: () => api.get<KnownHostEntry[]>('/known-hosts'),
  removeKnownHost: (host: string, port: number) => api.delete(`/known-hosts/${encodeURIComponent(host)}/${port}`),
  listConnectionProfiles: () => api.get<ConnectionProfile[]>('/connection-profiles'),
  createConnectionProfile: (profile: ConnectionProfile) => api.post<ConnectionProfile>('/connection-profiles', profile),
  updateConnectionProfile: (id: number, profile: ConnectionProfile) => api.put(`/connection-profiles/${id}`, profile),
  deleteConnectionProfile: (id: number) => api.delete(`/connection-profiles/${id}`),
  getAuthPriority: () => api.get<AuthPriorityEntry[]>('/auth-priority'),
  setAuthPriority: (entries: AuthPriorityEntry[]) => api.put('/auth-priority', entries),
  getDashboard: () => api.get<AuthDashboard>('/dashboard'),
  getSSHAgentStatus: () => api.get<SSHAgentStatus>('/ssh-agent/status'),
  listHostKeyChanges: (limit = 100) => api.get<HostKeyChange[]>(`/host-key-changes?limit=${limit}`)
};

export const serverApi = {
  getConnectionHistory: (serverId: number, limit = 100) =>
    api.get<ConnectionHistoryEntry[]>(`/servers/${serverId}/history?limit=${limit}`),
  getCredentialHealth: (serverId: number) =>
    api.get<CredentialHealthScore>(`/servers/${serverId}/credential-health`)
};
