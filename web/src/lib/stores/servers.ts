import { writable } from 'svelte/store';
import { api } from '$lib/api/client';

export interface Server {
  id: number;
  name: string;
  description: string;
  host: string;
  port: number;
  username: string;
  authMethod?: 'password' | 'key' | 'agent' | 'keyboard-interactive';
  tags: string[];
  environment: string;
  region: string;
  icon: string;
  color: string;
  favorite: boolean;
  bastionId?: number;
  credentialStatus?: 'unknown' | 'valid' | 'invalid' | 'expired' | 'locked';
  fingerprint?: string;
  keyType?: string;
  lastSuccess?: string;
  lastFailure?: string;
  failureCount?: number;
  successCount?: number;
  createdAt: string;
  updatedAt: string;
}

export interface ServerInfo {
  sshStatus: string;
  latencyMs: number;
  hostname: string;
  os: string;
  kernel: string;
  architecture: string;
  cpuModel: string;
  cpuCores: number;
  ramTotalMb: number;
  diskTotalGb: number;
  virtualization: string;
  provider: string;
  publicIp: string;
  privateIp: string;
  timezone: string;
}

export interface ServerStoreState {
  servers: Server[];
  filteredServers: Server[];
  searchQuery: string;
  filterFavorites: boolean;
  loading: boolean;
  error: string | null;
}

function normalize(value: string) {
  return value.trim().toLowerCase();
}

function applyFilters(state: ServerStoreState): ServerStoreState {
  const query = normalize(state.searchQuery);

  const filteredServers = state.servers.filter((server) => {
    if (state.filterFavorites && !server.favorite) {
      return false;
    }

    if (!query) {
      return true;
    }

    const searchable = [
      server.name,
      server.description,
      server.host,
      server.username,
      server.environment,
      server.region,
      server.tags.join(' ')
    ]
      .join(' ')
      .toLowerCase();

    return searchable.includes(query);
  });

  return {
    ...state,
    filteredServers
  };
}

function updateState(update: (state: ServerStoreState) => ServerStoreState) {
  serverStore.update((state) => applyFilters(update(state)));
}

export const serverStore = writable<ServerStoreState>({
  servers: [],
  filteredServers: [],
  searchQuery: '',
  filterFavorites: false,
  loading: false,
  error: null
});

export function setSearchQuery(searchQuery: string) {
  updateState((state) => ({ ...state, searchQuery }));
}

export function setFilterFavorites(filterFavorites: boolean) {
  updateState((state) => ({ ...state, filterFavorites }));
}

export async function fetchServers() {
  updateState((state) => ({ ...state, loading: true, error: null }));

  try {
    const response = await api.get<Server[] | null>('/servers');
    const servers = Array.isArray(response) ? response : [];
    updateState((state) => ({
      ...state,
      servers,
      loading: false,
      error: null
    }));
  } catch (e) {
    updateState((state) => ({
      ...state,
      loading: false,
      error: e instanceof Error ? e.message : 'Failed to fetch servers'
    }));
  }
}

export async function createServer(
  data: Partial<Server> & {
    password?: string;
    sshKey?: string;
    passphrase?: string;
  }
) {
  const server = await api.post<Server>('/servers', data);
  await fetchServers();
  return server;
}

export async function updateServer(
  id: number,
  data: Partial<Server> & {
    password?: string;
    sshKey?: string;
    passphrase?: string;
  }
) {
  await api.put<Server>(`/servers/${id}`, data);
  await fetchServers();
}

export async function deleteServer(id: number) {
  await api.delete(`/servers/${id}`);
  await fetchServers();
}

export async function toggleFavorite(id: number) {
  await api.patch(`/servers/${id}/favorite`);
  await fetchServers();
}

// --- SSH Key Management API ---

export interface KeyInstallResult {
  success: boolean;
  message: string;
  fingerprint: string;
  keyType: string;
  alreadyInstalled: boolean;
}

export interface KeyVerifyResult {
  success: boolean;
  message: string;
  fingerprint: string;
  installed: boolean;
}

export interface KeyRotationResult {
  success: boolean;
  message: string;
  oldFingerprint: string;
  newFingerprint: string;
  keyType: string;
}

export interface FingerprintResult {
  sha256: string;
  md5: string;
  keyType: string;
}

export interface AuthTestResult {
  success: boolean;
  authMethod: string;
  latencyMs: number;
  message: string;
}

export interface ConnectionHistoryEntry {
  id: number;
  serverId: number;
  success: boolean;
  durationMs: number;
  reason: string;
  remoteIp: string;
  fingerprint: string;
  authMethod: string;
  createdAt: string;
}

export interface ConnectionMetrics {
  totalAttempts: number;
  successCount: number;
  failureCount: number;
  successRate: number;
  failureRate: number;
  lastSuccess: string;
  lastFailure: string;
}

export interface SSHAgentStatus {
  available: boolean;
  socket: string;
  identities: number;
  fingerprints: string[];
}

export async function installKey(serverId: number, keyType: string = 'ed25519', password?: string): Promise<KeyInstallResult> {
  return api.post(`/servers/${serverId}/install-key`, { keyType, password }) as Promise<KeyInstallResult>;
}

export async function verifyKey(serverId: number): Promise<KeyVerifyResult> {
  return api.post(`/servers/${serverId}/verify-key`) as Promise<KeyVerifyResult>;
}

export async function rotateKey(serverId: number, keyType: string = 'ed25519'): Promise<KeyRotationResult> {
  return api.post(`/servers/${serverId}/rotate-key`, { keyType }) as Promise<KeyRotationResult>;
}

export async function getFingerprint(serverId: number): Promise<FingerprintResult> {
  return api.get(`/servers/${serverId}/fingerprint`) as Promise<FingerprintResult>;
}

export async function testAuth(serverId: number): Promise<AuthTestResult> {
  return api.post(`/servers/${serverId}/test-auth`) as Promise<AuthTestResult>;
}

export async function getConnectionHistory(serverId: number, limit: number = 100): Promise<ConnectionHistoryEntry[]> {
  return api.get(`/servers/${serverId}/history?limit=${limit}`) as Promise<ConnectionHistoryEntry[]>;
}

export async function getConnectionMetrics(serverId: number): Promise<ConnectionMetrics> {
  return api.get(`/servers/${serverId}/metrics`) as Promise<ConnectionMetrics>;
}

export async function getSSHAgentStatus(): Promise<SSHAgentStatus> {
  return api.get('/ssh-agent/status') as Promise<SSHAgentStatus>;
}

export async function removePassword(serverId: number): Promise<void> {
  await api.post(`/servers/${serverId}/remove-password`);
}

export async function clearSSHKey(serverId: number): Promise<void> {
  await api.post(`/servers/${serverId}/clear-key`);
}
