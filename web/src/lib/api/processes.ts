import { api } from './client';

export interface ProcessInfo {
  pid: number;
  ppid: number;
  user: string;
  cpu: number;
  memory: number;
  vsz: number;
  rss: number;
  stat: string;
  start: string;
  time: string;
  command: string;
}

export type ProcessSort = 'cpu' | 'mem' | 'pid';
export type ProcessSignal = 'TERM' | 'KILL' | 'HUP' | 'INT' | 'QUIT' | 'USR1' | 'USR2' | 'STOP' | 'CONT';

export interface KillProcessRequest {
  signal?: ProcessSignal;
}

export const processesApi = {
  list: (serverId: number, sort: ProcessSort = 'cpu', limit = 50) =>
    api.get<ProcessInfo[]>(`/servers/${serverId}/processes?sort=${sort}&limit=${limit}`),

  get: (serverId: number, pid: number) =>
    api.get<ProcessInfo>(`/servers/${serverId}/processes/${pid}`),

  top: (serverId: number, limit = 10, sortBy: ProcessSort = 'cpu') =>
    api.get<ProcessInfo[]>(`/servers/${serverId}/processes/top?limit=${limit}&sortBy=${sortBy}`),

  kill: (serverId: number, pid: number, signal: ProcessSignal = 'TERM') =>
    api.post<{ status: string }>(`/servers/${serverId}/processes/${pid}/kill`, { signal })
};
