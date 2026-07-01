import { api } from '$lib/api/client';

export interface PackageUpdate {
  name: string;
  currentVersion: string;
  availableVersion: string;
  architecture: string;
  repository: string;
  type: 'security' | 'bugfix' | 'enhancement' | 'normal' | string;
  security: boolean;
  size: string;
}

export interface UpdateStatus {
  serverId: number;
  hostname: string;
  packageManager: string;
  lastChecked: string;
  totalUpdates: number;
  securityUpdates: number;
  packages: PackageUpdate[];
}

export interface PackageInfo {
  name: string;
  version: string;
  description: string;
  size: string;
  installed: boolean;
  packageManager: string;
}

export interface InstallRequest {
  securityOnly: boolean;
}

export interface InstallResult {
  serverId: number;
  packageManager: string;
  securityOnly?: boolean;
  updated?: number;
  packageName?: string;
  removed?: boolean;
  lastChecked: string;
}

export const updatesApi = {
  getStatus: (serverId: number) => api.get<UpdateStatus>(`/servers/${serverId}/updates`),
  installUpdates: (serverId: number, body: InstallRequest) =>
    api.post<InstallResult>(`/servers/${serverId}/updates/install`, body),
  listInstalledPackages: (serverId: number, filter = '') => {
    const params = new URLSearchParams();
    if (filter.trim()) params.set('filter', filter.trim());
    const query = params.toString();
    return api.get<PackageInfo[]>(`/servers/${serverId}/updates/packages${query ? `?${query}` : ''}`);
  },
  getPackageInfo: (serverId: number, packageName: string) =>
    api.get<PackageInfo>(`/servers/${serverId}/updates/packages/${encodeURIComponent(packageName)}`),
  installPackage: (serverId: number, packageName: string) =>
    api.post<InstallResult>(`/servers/${serverId}/updates/packages/${encodeURIComponent(packageName)}`),
  removePackage: (serverId: number, packageName: string) =>
    api.delete<InstallResult>(`/servers/${serverId}/updates/packages/${encodeURIComponent(packageName)}`)
};
