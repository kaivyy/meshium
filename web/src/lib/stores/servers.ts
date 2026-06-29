import { writable } from 'svelte/store';
import {
  serverApi,
  type ServerCreateRequest,
  type ServerListFilters,
  type ServerResponse,
  type ServerUpdateRequest,
  type ServerInfo as ApiServerInfo
} from '$lib/api/client';

export interface Server extends ServerResponse {}

export interface ServerInfo extends ApiServerInfo {}

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
      server.authMethod,
      server.credentialStatus,
      server.keyType ?? '',
      server.environment,
      server.region,
      server.tags.join(' '),
      server.bastionId ? String(server.bastionId) : ''
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

export async function fetchServers(filters?: ServerListFilters) {
  updateState((state) => ({ ...state, loading: true, error: null }));

  try {
    const servers = await serverApi.list(filters);
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

export async function createServer(data: ServerCreateRequest | Record<string, unknown>) {
  const server = await serverApi.create(data as ServerCreateRequest);
  await fetchServers();
  return server;
}

export async function updateServer(id: number, data: ServerUpdateRequest | Record<string, unknown>) {
  await serverApi.update(id, data as ServerUpdateRequest);
  await fetchServers();
}

export async function deleteServer(id: number) {
  await serverApi.delete(id);
  await fetchServers();
}

export async function toggleFavorite(id: number) {
  await serverApi.toggleFavorite(id);
  await fetchServers();
}
