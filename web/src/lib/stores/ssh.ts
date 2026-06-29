import { writable } from 'svelte/store';
import {
  serverApi,
  sshApi,
  type AuthDashboard,
  type AuthPriorityEntry,
  type ConnectionHistoryEntry,
  type ConnectionProfile,
  type CredentialHealthScore,
  type HostKeyChange,
  type KnownHostEntry,
  type SSHAgentStatus
} from '$lib/api/client';

export const knownHosts = writable<KnownHostEntry[]>([]);
export const connectionProfiles = writable<ConnectionProfile[]>([]);
export const authPriority = writable<AuthPriorityEntry[]>([]);
export const dashboard = writable<AuthDashboard | null>(null);
export const connectionHistory = writable<ConnectionHistoryEntry[]>([]);
export const credentialHealth = writable<CredentialHealthScore | null>(null);
export const sshAgentStatus = writable<SSHAgentStatus | null>(null);
export const hostKeyChanges = writable<HostKeyChange[]>([]);
export const loading = writable(false);
export const error = writable<string | null>(null);

let pendingRequests = 0;

function setLoadingState(active: boolean) {
  pendingRequests += active ? 1 : -1;
  if (pendingRequests < 0) {
    pendingRequests = 0;
  }
  loading.set(pendingRequests > 0);
}

async function withLoading<T>(action: () => Promise<T>): Promise<T> {
  setLoadingState(true);
  error.set(null);
  try {
    return await action();
  } catch (e) {
    error.set(e instanceof Error ? e.message : 'Request failed');
    throw e;
  } finally {
    setLoadingState(false);
  }
}

export async function loadKnownHosts() {
  const data = await withLoading(() => sshApi.listKnownHosts());
  knownHosts.set(data);
  return data;
}

export async function refreshKnownHosts() {
  return loadKnownHosts();
}

export async function removeKnownHost(host: string, port: number) {
  await withLoading(() => sshApi.removeKnownHost(host, port));
  await loadKnownHosts();
}

export async function loadConnectionProfiles() {
  const data = await withLoading(() => sshApi.listConnectionProfiles());
  connectionProfiles.set(data);
  return data;
}

export async function createConnectionProfile(profile: ConnectionProfile) {
  const created = await withLoading(() => sshApi.createConnectionProfile(profile));
  await loadConnectionProfiles();
  return created;
}

export async function updateConnectionProfile(id: number, profile: ConnectionProfile) {
  await withLoading(() => sshApi.updateConnectionProfile(id, profile));
  await loadConnectionProfiles();
}

export async function deleteConnectionProfile(id: number) {
  await withLoading(() => sshApi.deleteConnectionProfile(id));
  await loadConnectionProfiles();
}

export async function loadAuthPriority() {
  const data = await withLoading(() => sshApi.getAuthPriority());
  authPriority.set(data);
  return data;
}

export async function updateAuthPriority(entries: AuthPriorityEntry[]) {
  await withLoading(() => sshApi.setAuthPriority(entries));
  await loadAuthPriority();
}

export async function loadDashboard() {
  const data = await withLoading(() => sshApi.getDashboard());
  dashboard.set(data);
  return data;
}

export async function loadConnectionHistory(serverId: number, limit = 100) {
  const data = await withLoading(() => serverApi.getConnectionHistory(serverId, limit));
  connectionHistory.set(data);
  return data;
}

export async function loadCredentialHealth(serverId: number) {
  const data = await withLoading(() => serverApi.getCredentialHealth(serverId));
  credentialHealth.set(data);
  return data;
}

export async function loadSSHAgentStatus() {
  const data = await withLoading(() => sshApi.getSSHAgentStatus());
  sshAgentStatus.set(data);
  return data;
}

export async function loadHostKeyChanges(limit = 100) {
  const data = await withLoading(() => sshApi.listHostKeyChanges(limit));
  hostKeyChanges.set(data);
  return data;
}

export function resetSSHStore() {
  knownHosts.set([]);
  connectionProfiles.set([]);
  authPriority.set([]);
  dashboard.set(null);
  connectionHistory.set([]);
  credentialHealth.set(null);
  sshAgentStatus.set(null);
  hostKeyChanges.set([]);
  error.set(null);
  loading.set(false);
  pendingRequests = 0;
}
