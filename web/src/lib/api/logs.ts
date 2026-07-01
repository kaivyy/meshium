import { api } from './client';

export interface LogResponse {
  file: string;
  lines: string[];
  total: number;
  truncated: boolean;
}

export interface LogFileInfo {
  name: string;
  path: string;
  size: number;
  modified: string;
}

export interface LogSearchRequest {
  file: string;
  pattern: string;
  lines: number;
}

export interface LogStreamQuery {
  file?: string;
  service?: string;
  system?: boolean;
  lines?: number;
  filter?: string;
}

export function buildLogStreamPath(serverId: number, query: LogStreamQuery = {}): string {
  const params = new URLSearchParams();
  if (query.file) params.set('file', query.file);
  if (query.service) params.set('service', query.service);
  if (query.system) params.set('system', 'true');
  if (typeof query.lines === 'number') params.set('lines', String(query.lines));
  if (query.filter) params.set('filter', query.filter);
  const suffix = params.toString();
  return suffix ? `/ws/logs/${serverId}?${suffix}` : `/ws/logs/${serverId}`;
}

export const logsApi = {
  readLog(serverId: number, file: string, lines = 100, filter = '') {
    const params = new URLSearchParams({ file, lines: String(lines) });
    if (filter) params.set('filter', filter);
    return api.get<LogResponse>(`/servers/${serverId}/logs?${params.toString()}`);
  },

  listLogFiles(serverId: number, dir = '/var/log') {
    const params = new URLSearchParams({ dir });
    return api.get<LogFileInfo[]>(`/servers/${serverId}/logs/files?${params.toString()}`);
  },

  getSystemLogs(serverId: number, lines = 100) {
    const params = new URLSearchParams({ lines: String(lines) });
    return api.get<LogResponse>(`/servers/${serverId}/logs/system?${params.toString()}`);
  },

  getServiceLogs(serverId: number, serviceName: string, lines = 100) {
    const params = new URLSearchParams({ lines: String(lines) });
    return api.get<LogResponse>(`/servers/${serverId}/logs/service/${encodeURIComponent(serviceName)}?${params.toString()}`);
  },

  searchLogs(serverId: number, body: LogSearchRequest) {
    return api.post<LogResponse>(`/servers/${serverId}/logs/search`, body);
  }
};
