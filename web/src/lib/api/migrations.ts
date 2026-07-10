import { api } from '$lib/api/client';

export interface MigrationPlan {
  id: number;
  // The backend Migration record serializes these as sourceId/targetId.
  // sourceServerId/targetServerId are kept as optional aliases for older callers.
  sourceId: number;
  targetId: number;
  sourceServerId?: number;
  targetServerId?: number;
  status: string;
  categories: string[];
  error?: string;
  errorMessage?: string;
  createdAt: string;
  completedAt?: string;
  rolledBackAt?: string;
}

export interface MigrationStep {
  id: number;
  migrationId: number;
  category: string;
  action: string;
  status: string;
  data: string;
  error: string;
  createdAt: string;
  completedAt: string;
}

export interface DatabaseConfig {
  engine: string; // postgres | mysql | mongodb | redis
  databaseName: string; // empty = all user databases
  username: string;
  password: string;
  host: string;
  port: number;
}

export interface PlanRequest {
  sourceServerId: number;
  targetServerId: number;
  categories: string[];
  configPaths?: string[];
  databaseConfig?: DatabaseConfig;
}

export interface WSMessage {
  step: string;
  status: string;
  value?: string;
  error?: string;
}

export interface DryRunChange {
  type: string;
  resource: string;
  detail: string;
}

export interface DryRunCategory {
  category: string;
  changes: DryRunChange[];
  summary: string;
}

export interface DryRunResult {
  migrationId: number;
  categories: DryRunCategory[];
  summary: {
    totalChanges: number;
    addCount: number;
    modifyCount: number;
    removeCount: number;
  };
}

export interface DiffCategory {
  category: string;
  onlyInSource: string[];
  onlyInTarget: string[];
  different: string[];
  same: number;
}

export interface DiffResult {
  sourceId: number;
  targetId: number;
  categories: DiffCategory[];
}

export const migrationApi = {
  list: () => api.get('/migrations') as Promise<MigrationPlan[]>,
  get: (id: number) => api.get(`/migrations/${id}`) as Promise<MigrationPlan>,
  delete: (id: number) => api.delete(`/migrations/${id}`),
  getSteps: (id: number) => api.get(`/migrations/${id}/steps`) as Promise<MigrationStep[]>,
  rollback: (id: number) => api.post(`/migrations/${id}/rollback`, {}),
  dryRun: (id: number) => api.get(`/migrations/${id}/dryrun`) as Promise<DryRunResult>,
  diff: (sourceId: number, targetId: number, categories?: string[]) =>
    api.post('/diff', { sourceId, targetId, categories: categories || [] }) as Promise<DiffResult>,
};

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

function createWs(path: string): WebSocket {
  const subprotocols = wsSubprotocols();
  return subprotocols.length > 0
    ? new WebSocket(wsUrl(path), subprotocols)
    : new WebSocket(wsUrl(path));
}

export function wsPlan(req: PlanRequest, onMessage: (msg: WSMessage) => void, onClose?: () => void, onError?: () => void): WebSocket {
  const ws = createWs('/ws/plan');
  ws.onopen = () => {
    ws.send(JSON.stringify(req));
  };
  ws.onmessage = (event) => {
    const msg = JSON.parse(event.data) as WSMessage;
    onMessage(msg);
  };
  ws.onclose = () => onClose?.();
  ws.onerror = () => onError?.();
  return ws;
}

export function wsExecute(migrationId: number, onMessage: (msg: WSMessage) => void, onClose?: () => void, onError?: () => void): WebSocket {
  const ws = createWs(`/ws/migrate/${migrationId}`);
  ws.onmessage = (event) => {
    const msg = JSON.parse(event.data) as WSMessage;
    onMessage(msg);
  };
  ws.onclose = () => onClose?.();
  ws.onerror = () => onError?.();
  return ws;
}

export function wsRollback(migrationId: number, onMessage: (msg: WSMessage) => void, onClose?: () => void, onError?: () => void): WebSocket {
  const ws = createWs(`/ws/migrate/${migrationId}/rollback`);
  ws.onmessage = (event) => {
    const msg = JSON.parse(event.data) as WSMessage;
    onMessage(msg);
  };
  ws.onclose = () => onClose?.();
  ws.onerror = () => onError?.();
  return ws;
}

export function wsDryRun(migrationId: number, onMessage: (msg: WSMessage) => void, onClose?: () => void, onError?: () => void): WebSocket {
  const ws = createWs(`/ws/dryrun/${migrationId}`);
  ws.onmessage = (event) => {
    const msg = JSON.parse(event.data) as WSMessage;
    onMessage(msg);
  };
  ws.onclose = () => onClose?.();
  ws.onerror = () => onError?.();
  return ws;
}

// wsCompatibility streams per-check progress for the compatibility preflight
// over /ws/compatibility/{id}. Same shape as wsDryRun; the result itself is
// persisted server-side and surfaced via loadSession on close.
export function wsCompatibility(migrationId: number, onMessage: (msg: WSMessage) => void, onClose?: () => void, onError?: () => void): WebSocket {
  const ws = createWs(`/ws/compatibility/${migrationId}`);
  ws.onmessage = (event) => {
    const msg = JSON.parse(event.data) as WSMessage;
    onMessage(msg);
  };
  ws.onclose = () => onClose?.();
  ws.onerror = () => onError?.();
  return ws;
}
