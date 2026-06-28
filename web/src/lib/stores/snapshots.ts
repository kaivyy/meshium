import { writable } from 'svelte/store';
import { discoveryApi, type ServerSnapshot } from '$lib/api/discovery';

/**
 * Shared snapshot cache.
 *
 * Every page (discovery, monitoring, docker, files, alerts, dashboard) needs
 * server snapshots.  Without a shared cache each page independently fetches
 * the same endpoints — and when a server hasn't been discovered yet the 404
 * is re-fetched on every page load, flooding the console.
 *
 * The cache stores three states per server ID:
 *   - ServerSnapshot  → snapshot exists
 *   - null            → snapshot fetched but does not exist (404)
 *   - undefined       → not yet fetched (key absent from map)
 */

type CachedSnapshot = ServerSnapshot | null;

const cache = new Map<number, CachedSnapshot>();
const inFlight = new Map<number, Promise<CachedSnapshot>>();

export const snapshotsStore = writable<Record<number, CachedSnapshot>>({});

function flush(): void {
  snapshotsStore.set(Object.fromEntries(cache));
}

/**
 * Load a snapshot for a server.  Returns the cached value if available
 * (including null for "not found"), and only makes a network request when
 * the cache has no entry for the server.
 */
export async function loadSnapshot(serverId: number): Promise<void> {
  // Already cached (including null = not found) — don't retry.
  if (cache.has(serverId)) return;

  // Already in flight — don't duplicate the request.
  if (inFlight.has(serverId)) {
    await inFlight.get(serverId);
    return;
  }

  const promise = (async (): Promise<CachedSnapshot> => {
    try {
      const snap = await discoveryApi.getSnapshot(serverId);
      cache.set(serverId, snap);
      return snap;
    } catch {
      // 404 or other error — cache as "not found" so we don't retry.
      cache.set(serverId, null);
      return null;
    } finally {
      inFlight.delete(serverId);
      flush();
    }
  })();

  inFlight.set(serverId, promise);
  await promise;
}

/**
 * Load snapshots for multiple servers in parallel.
 */
export async function loadSnapshots(serverIds: number[]): Promise<void> {
  await Promise.all(serverIds.map((id) => loadSnapshot(id)));
}

/**
 * Get a cached snapshot.  Returns undefined if not yet fetched.
 */
export function getSnapshot(serverId: number): CachedSnapshot | undefined {
  return cache.get(serverId);
}

/**
 * Check if a snapshot exists for a server.
 * Returns true only if a non-null snapshot is cached.
 */
export function hasSnapshot(serverId: number): boolean {
  const snap = cache.get(serverId);
  return snap != null && snap.capturedAt !== undefined;
}

/**
 * Invalidate the cache for a specific server (e.g. after discovery).
 * The next call to loadSnapshot will re-fetch.
 */
export function invalidateSnapshot(serverId: number): void {
  cache.delete(serverId);
  flush();
  loadSnapshot(serverId);
}

/**
 * Invalidate all cached snapshots (e.g. after a full refresh).
 */
export function invalidateAll(): void {
  cache.clear();
  flush();
}
