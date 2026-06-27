import { writable } from 'svelte/store';
import { api } from '$lib/api/client';

export interface Server {
  id: number;
  name: string;
  description: string;
  host: string;
  port: number;
  username: string;
  authMethod?: 'password' | 'key';
  tags: string[];
  environment: string;
  region: string;
  icon: string;
  color: string;
  favorite: boolean;
  bastionId?: number;
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
    const servers = await api.get<Server[]>('/servers');
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
