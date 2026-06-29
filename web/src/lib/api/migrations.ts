import { api, getSessionToken } from '$lib/api/client';

export interface MigrationPlan {
  id: number;
  sourceId: number;
  targetId: number;
  status: string;
  categories: string[];
  error?: string;
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

export const migrationApi = {
  list: () => api.get('/migrations') as Promise<MigrationPlan[]>,
  get: (id: number) => api.get(`/migrations/${id}`) as Promise<MigrationPlan>,
  delete: (id: number) => api.delete(`/migrations/${id}`),
  getSteps: (id: number) => api.get(`/migrations/${id}/steps`) as Promise<MigrationStep[]>,
  rollback: (id: number) => api.post(`/migrations/${id}/rollback`, {}),
};

function wsUrl(path: string): string {
  const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
  const token = getSessionToken();
  const tokenParam = token ? `?token=${encodeURIComponent(token)}` : '';
  return `${proto}//${window.location.host}${path}${tokenParam}`;
}

function parseWsMessage(data: string): WSMessage | null {
  try {
    return JSON.parse(data) as WSMessage;
  } catch (error) {
    console.error('Failed to parse migration WebSocket message', error);
    return null;
  }
}

export function wsPlan(req: PlanRequest, onMessage: (msg: WSMessage) => void, onClose?: () => void, onError?: () => void): WebSocket {
  const ws = new WebSocket(wsUrl('/ws/plan'));
  ws.onopen = () => {
    ws.send(JSON.stringify(req));
  };
  ws.onmessage = (event) => {
    const msg = parseWsMessage(event.data);
    if (msg) {
      onMessage(msg);
    }
  };
  ws.onclose = () => onClose?.();
  ws.onerror = () => onError?.();
  return ws;
}

export function wsExecute(migrationId: number, onMessage: (msg: WSMessage) => void, onClose?: () => void, onError?: () => void): WebSocket {
  const ws = new WebSocket(wsUrl(`/ws/migrate/${migrationId}`));
  ws.onmessage = (event) => {
    const msg = parseWsMessage(event.data);
    if (msg) {
      onMessage(msg);
    }
  };
  ws.onclose = () => onClose?.();
  ws.onerror = () => onError?.();
  return ws;
}

export function wsRollback(migrationId: number, onMessage: (msg: WSMessage) => void, onClose?: () => void, onError?: () => void): WebSocket {
  const ws = new WebSocket(wsUrl(`/ws/migrate/${migrationId}/rollback`));
  ws.onmessage = (event) => {
    const msg = parseWsMessage(event.data);
    if (msg) {
      onMessage(msg);
    }
  };
  ws.onclose = () => onClose?.();
  ws.onerror = () => onError?.();
  return ws;
}
