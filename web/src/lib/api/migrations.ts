import { api } from '$lib/api/client';

export interface MigrationPlan {
  id: number;
  sourceServerId: number;
  targetServerId: number;
  status: string;
  categories: string[];
  errorMessage: string;
  createdAt: string;
  completedAt: string;
  rolledBackAt: string;
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

export interface PlanRequest {
  sourceServerId: number;
  targetServerId: number;
  categories: string[];
  configPaths?: string[];
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

export function wsPlan(req: PlanRequest, onMessage: (msg: WSMessage) => void, onClose?: () => void, onError?: () => void): WebSocket {
  const ws = new WebSocket(`ws://${window.location.host}/ws/plan`);
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
  const ws = new WebSocket(`ws://${window.location.host}/ws/migrate/${migrationId}`);
  ws.onmessage = (event) => {
    const msg = JSON.parse(event.data) as WSMessage;
    onMessage(msg);
  };
  ws.onclose = () => onClose?.();
  ws.onerror = () => onError?.();
  return ws;
}

export function wsRollback(migrationId: number, onMessage: (msg: WSMessage) => void, onClose?: () => void, onError?: () => void): WebSocket {
  const ws = new WebSocket(`ws://${window.location.host}/ws/migrate/${migrationId}/rollback`);
  ws.onmessage = (event) => {
    const msg = JSON.parse(event.data) as WSMessage;
    onMessage(msg);
  };
  ws.onclose = () => onClose?.();
  ws.onerror = () => onError?.();
  return ws;
}

export function wsDryRun(migrationId: number, onMessage: (msg: WSMessage) => void, onClose?: () => void, onError?: () => void): WebSocket {
  const ws = new WebSocket(`ws://${window.location.host}/ws/dryrun/${migrationId}`);
  ws.onmessage = (event) => {
    const msg = JSON.parse(event.data) as WSMessage;
    onMessage(msg);
  };
  ws.onclose = () => onClose?.();
  ws.onerror = () => onError?.();
  return ws;
}
