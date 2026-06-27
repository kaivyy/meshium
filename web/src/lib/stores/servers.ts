import { writable } from 'svelte/store';
import { api } from '$lib/api/client';

export interface Server {
  id: number;
  name: string;
  description: string;
  host: string;
  port: number;
  username: string;
  tags: string[];
  environment: string;
  region: string;
  icon: string;
  color: string;
  favorite: boolean;
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

export const serverStore = writable<{
  servers: Server[];
  loading: boolean;
  error: string | null;
}>({
  servers: [],
  loading: false,
  error: null
});

export async function fetchServers(filter?: {
  environment?: string;
  region?: string;
  tag?: string;
  q?: string;
}) {
  serverStore.update((s) => ({ ...s, loading: true, error: null }));

  const params = new URLSearchParams();
  if (filter?.environment) params.set('environment', filter.environment);
  if (filter?.region) params.set('region', filter.region);
  if (filter?.tag) params.set('tag', filter.tag);
  if (filter?.q) params.set('q', filter.q);

  const query = params.toString() ? `?${params}` : '';
  try {
    const servers = await api.get<Server[]>(`/servers${query}`);
    serverStore.set({ servers, loading: false, error: null });
  } catch (e) {
    serverStore.set({ servers: [], loading: false, error: (e as Error).message });
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
