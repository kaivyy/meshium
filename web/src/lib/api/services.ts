import { api } from './client';

export interface ServiceInfo {
  name: string;
  description: string;
  loadState: string;
  activeState: string;
  subState: string;
  type: string;
  enabled: boolean;
}

export interface ServiceStatus extends ServiceInfo {}

function servicePath(name: string): string {
  return encodeURIComponent(name);
}

export const servicesApi = {
  listServices: (serverId: number) =>
    api.get<ServiceInfo[]>(`/servers/${serverId}/services`),
  getServiceStatus: (serverId: number, name: string) =>
    api.get<ServiceStatus>(`/servers/${serverId}/services/${servicePath(name)}`),
  startService: (serverId: number, name: string) =>
    api.post<ServiceStatus>(`/servers/${serverId}/services/${servicePath(name)}/start`, {}),
  stopService: (serverId: number, name: string) =>
    api.post<ServiceStatus>(`/servers/${serverId}/services/${servicePath(name)}/stop`, {}),
  restartService: (serverId: number, name: string) =>
    api.post<ServiceStatus>(`/servers/${serverId}/services/${servicePath(name)}/restart`, {}),
  enableService: (serverId: number, name: string) =>
    api.post<ServiceStatus>(`/servers/${serverId}/services/${servicePath(name)}/enable`, {}),
  disableService: (serverId: number, name: string) =>
    api.post<ServiceStatus>(`/servers/${serverId}/services/${servicePath(name)}/disable`, {}),
};
