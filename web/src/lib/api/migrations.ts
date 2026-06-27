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

export const migrationApi = {
  list: () => api.get('/migrations') as Promise<MigrationPlan[]>,
  get: (id: number) => api.get(`/migrations/${id}`) as Promise<MigrationPlan>,
  delete: (id: number) => api.delete(`/migrations/${id}`),
  getSteps: (id: number) => api.get(`/migrations/${id}/steps`) as Promise<MigrationStep[]>,
  rollback: (id: number) => api.post(`/migrations/${id}/rollback`, {}),
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
