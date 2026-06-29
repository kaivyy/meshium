import { api } from '$lib/api/client';

export type JobType = 'migration' | 'discovery' | 'compat_check';
export type JobStatus = 'queued' | 'running' | 'paused' | 'done' | 'failed' | 'cancelled';
export type LogLevel = 'info' | 'warn' | 'error';

export interface JobProgress {
  currentStep: number;
  totalSteps: number;
  currentName: string;
  percentage: number;
  bytesDone: number;
  bytesTotal: number;
  speedBps: number; // bytes per second
  eta: number; // seconds remaining (backend sends nanoseconds, we convert)
}

export interface JobLog {
  timestamp: string;
  level: LogLevel;
  step: string;
  message: string;
}

export interface Job {
  id: string;
  type: JobType;
  status: JobStatus;
  createdAt: string;
  startedAt?: string;
  finishedAt?: string;
  planId?: string;
  migrationId?: number;
  sourceId?: number;
  targetId?: number;
  progress: JobProgress;
  logs?: JobLog[];
  error?: string;
}

export interface JobRequest {
  type: JobType;
  planId?: string;
  sourceId?: number;
  targetId?: number;
  migrationId?: number;
}

export interface JobFilter {
  type?: JobType;
  status?: JobStatus;
  limit?: number;
}

export interface JobWSMessage {
  type: 'progress' | 'ping' | 'error';
  progress?: JobProgress;
  error?: string;
}

export function isTerminal(status: JobStatus): boolean {
  return status === 'done' || status === 'failed' || status === 'cancelled';
}

export function isActive(status: JobStatus): boolean {
  return status === 'queued' || status === 'running' || status === 'paused';
}

export const jobsApi = {
  list: (filter?: JobFilter) => {
    const params = new URLSearchParams();
    if (filter?.type) params.set('type', filter.type);
    if (filter?.status) params.set('status', filter.status);
    if (filter?.limit) params.set('limit', String(filter.limit));
    const qs = params.toString();
    return api.get(`/jobs${qs ? `?${qs}` : ''}`) as Promise<Job[]>;
  },
  get: (id: string) => api.get(`/jobs/${id}`) as Promise<Job>,
  submit: (req: JobRequest) => api.post('/jobs', req) as Promise<Job>,
  cancel: (id: string) => api.post(`/jobs/${id}/cancel`, {}) as Promise<void>,
  pause: (id: string) => api.post(`/jobs/${id}/pause`, {}) as Promise<void>,
  resume: (id: string) => api.post(`/jobs/${id}/resume`, {}) as Promise<void>,
  getLogs: (id: string) => api.get(`/jobs/${id}/logs`) as Promise<JobLog[]>,
};

export function wsJobProgress(
  jobID: string,
  onMessage: (msg: JobWSMessage) => void,
  onClose?: () => void,
  onError?: () => void
): WebSocket {
  const proto = location.protocol === 'https:' ? 'wss' : 'ws';
  const token = typeof localStorage !== 'undefined' ? localStorage.getItem('meshium_session_token') : null;
  const tokenParam = token ? `?token=${encodeURIComponent(token)}` : '';
  const url = `${proto}://${location.host}/ws/jobs/${jobID}/progress${tokenParam}`;
  const ws = new WebSocket(url);

  ws.onmessage = (event) => {
    const msg = JSON.parse(event.data) as JobWSMessage;
    // Convert ETA from nanoseconds to seconds if present
    if (msg.progress && msg.progress.eta) {
      msg.progress.eta = Math.floor(msg.progress.eta / 1e9);
    }
    onMessage(msg);
  };

  ws.onclose = () => onClose?.();
  ws.onerror = () => onError?.();

  return ws;
}
