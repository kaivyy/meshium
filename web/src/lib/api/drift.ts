import { api } from '$lib/api/client';

export type DriftCategory = 'packages' | 'services' | 'docker' | 'users' | 'network' | 'disk' | 'system';
export type DriftChangeType = 'added' | 'removed' | 'modified';
export type DriftSeverity = 'info' | 'warning' | 'critical';

export interface DriftChange {
  category: DriftCategory;
  type: DriftChangeType;
  name: string;
  oldValue?: string;
  newValue?: string;
  severity: DriftSeverity;
}

export interface DriftSummary {
  snapshotA?: string;
  snapshotB?: string;
  comparedAt?: string;
  totalChanges?: number;
  packagesAdded: number;
  packagesRemoved: number;
  servicesChanged: number;
  dockerChanged: number;
  usersChanged: number;
  networkChanged: number;
  diskChanged: number;
}

export interface DriftReport {
  serverId: number;
  serverName: string;
  snapshotA: string;
  snapshotB: string;
  changes: DriftChange[];
  totalChanges: number;
  summary: DriftSummary;
}

export interface DriftCompareRequest {
  snapshotA: string;
  snapshotB: string;
}

export const driftApi = {
  getLatest: (serverID: number) =>
    api.get(`/servers/${serverID}/drift`) as Promise<DriftReport>,
  getHistory: (serverID: number, limit = 10) =>
    api.get(`/servers/${serverID}/drift/history?limit=${limit}`) as Promise<DriftSummary[]>,
  checkDrift: (serverID: number) =>
    api.post(`/servers/${serverID}/drift/check`, {}) as Promise<DriftReport>,
  compareSnapshots: (serverID: number, snapshotA: string, snapshotB: string) =>
    api.post(`/servers/${serverID}/drift/compare`, { snapshotA, snapshotB }) as Promise<DriftReport>,
  compareServers: (sourceID: number, targetID: number) =>
    api.get(`/drift/compare/${sourceID}/${targetID}`) as Promise<DriftReport>
};
