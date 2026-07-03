<script lang="ts">
  import { onMount } from 'svelte';
  import { History, Search, ServerCog } from 'lucide-svelte';
  import { api } from '$lib/api/client';

  interface ServerSummary {
    id: number;
    name: string;
    host: string;
    port: number;
    username: string;
  }

  interface ConnectionHistoryEntry {
    id: number;
    serverId: number;
    hostname: string;
    ip: string;
    username: string;
    authMethod: string;
    keyFingerprint: string;
    agentUsed: boolean;
    bastionId: number;
    success: boolean;
    durationMs: number;
    latencyMs: number;
    exitStatus: number;
    failureReason: string;
    cipher: string;
    kex: string;
    compression: string;
    remoteBanner: string;
    timestamp: string;
    serverName?: string;
  }

  let servers: ServerSummary[] = [];
  let history: ConnectionHistoryEntry[] = [];
  let loading = true;
  let error = '';
  let serverFilter = 'all';
  let outcomeFilter: 'all' | 'success' | 'failure' = 'all';
  let startDate = '';
  let endDate = '';
  let searchQuery = '';

  onMount(loadHistory);

  async function loadHistory() {
    loading = true;
    error = '';

    try {
      servers = (await api.get('/servers')) as Array<ServerSummary>;
      const results = await Promise.all(
        servers.map(async (server) => {
          try {
            const entries = (await api.get(`/servers/${server.id}/connection-history?limit=50`)) as Array<ConnectionHistoryEntry>;
            return entries.map((entry) => ({ ...entry, serverName: server.name }));
          } catch {
            return [] as Array<ConnectionHistoryEntry>;
          }
        })
      );

      history = results.flat().sort((a, b) => new Date(b.timestamp).getTime() - new Date(a.timestamp).getTime());
    } catch (e) {
      error = e instanceof Error ? e.message : 'Failed to load connection history';
    } finally {
      loading = false;
    }
  }

  function formatDate(value: string) {
    if (!value) return '—';
    const date = new Date(value);
    return Number.isNaN(date.getTime()) ? value : date.toLocaleString();
  }

  function formatDuration(ms: number) {
    if (!ms) return '—';
    if (ms < 1000) return `${ms} ms`;
    return `${(ms / 1000).toFixed(2)} s`;
  }

  function formatLatency(ms: number) {
    if (!ms) return '—';
    return `${ms} ms`;
  }

  function matchesDateRange(value: string) {
    const timestamp = new Date(value);
    if (Number.isNaN(timestamp.getTime())) return true;

    if (startDate) {
      const start = new Date(`${startDate}T00:00:00`);
      if (timestamp < start) return false;
    }

    if (endDate) {
      const end = new Date(`${endDate}T23:59:59.999`);
      if (timestamp > end) return false;
    }

    return true;
  }

  function successBadge(success: boolean) {
    return success ? 'bg-emerald-100 text-emerald-700 ring-1 ring-emerald-200' : 'bg-rose-100 text-rose-700 ring-1 ring-rose-200';
  }

  $: filteredHistory = history.filter((entry) => {
    if (serverFilter !== 'all' && String(entry.serverId) !== serverFilter) {
      return false;
    }

    if (outcomeFilter === 'success' && !entry.success) return false;
    if (outcomeFilter === 'failure' && entry.success) return false;
    if (!matchesDateRange(entry.timestamp)) return false;

    const query = searchQuery.trim().toLowerCase();
    if (!query) return true;

    return [
      entry.serverName,
      entry.hostname,
      entry.ip,
      entry.username,
      entry.authMethod,
      entry.cipher,
      entry.kex,
      entry.failureReason,
      entry.remoteBanner
    ]
      .join(' ')
      .toLowerCase()
      .includes(query);
  });

  $: stats = {
    total: filteredHistory.length,
    success: filteredHistory.filter((entry) => entry.success).length,
    failure: filteredHistory.filter((entry) => !entry.success).length,
    avgLatency:
      filteredHistory.length > 0
        ? Math.round(filteredHistory.reduce((sum, entry) => sum + (entry.latencyMs || entry.durationMs || 0), 0) / filteredHistory.length)
        : 0
  };
</script>

<svelte:head>
  <title>Connection History</title>
</svelte:head>

<div class="p-6">
  <div class="mx-auto flex max-w-7xl flex-col gap-6">
    <div class="rounded-2xl border border-slate-200 bg-white p-6 shadow-sm">
      <div class="flex flex-col gap-4 lg:flex-row lg:items-end lg:justify-between">
        <div>
          <div class="inline-flex items-center gap-2 rounded-full bg-blue-50 px-3 py-1 text-xs font-medium text-blue-700">
            <History size={14} /> Connection Audit
          </div>
          <h1 class="mt-3 text-2xl font-bold tracking-tight text-slate-900">Connection History</h1>
          <p class="mt-2 max-w-3xl text-sm text-slate-500">
            Review SSH connection attempts, authentication methods, cipher suites, KEX algorithms, and failure reasons.
          </p>
        </div>

        <a href="/ssh" class="inline-flex items-center justify-center rounded-lg border border-slate-300 bg-white px-4 py-2 text-sm font-medium text-slate-700 transition hover:bg-slate-50">
          Back to Dashboard
        </a>
      </div>

      <div class="mt-6 grid gap-4 md:grid-cols-4">
        <div class="rounded-xl border border-slate-200 bg-slate-50 p-4">
          <p class="text-xs font-semibold uppercase tracking-wide text-slate-500">Entries</p>
          <p class="mt-2 text-2xl font-bold text-slate-900">{stats.total}</p>
        </div>
        <div class="rounded-xl border border-slate-200 bg-slate-50 p-4">
          <p class="text-xs font-semibold uppercase tracking-wide text-slate-500">Success</p>
          <p class="mt-2 text-2xl font-bold text-slate-900">{stats.success}</p>
        </div>
        <div class="rounded-xl border border-slate-200 bg-slate-50 p-4">
          <p class="text-xs font-semibold uppercase tracking-wide text-slate-500">Failures</p>
          <p class="mt-2 text-2xl font-bold text-slate-900">{stats.failure}</p>
        </div>
        <div class="rounded-xl border border-slate-200 bg-slate-50 p-4">
          <p class="text-xs font-semibold uppercase tracking-wide text-slate-500">Avg Latency</p>
          <p class="mt-2 text-2xl font-bold text-slate-900">{stats.avgLatency ? `${stats.avgLatency} ms` : '—'}</p>
        </div>
      </div>
    </div>

    <div class="rounded-2xl border border-slate-200 bg-white p-4 shadow-sm">
      <div class="grid gap-3 lg:grid-cols-[1.2fr_0.8fr_0.8fr_0.8fr]">
        <div class="relative">
          <Search size={18} class="absolute left-3 top-1/2 -translate-y-1/2 text-slate-400" />
          <input
            type="text"
            bind:value={searchQuery}
            placeholder="Search server, host, auth method, cipher, failure reason..."
            class="w-full rounded-lg border border-slate-300 bg-white py-2 pl-10 pr-4 text-slate-900 shadow-sm outline-none transition focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20"
          />
        </div>

        <select bind:value={serverFilter} class="rounded-lg border border-slate-300 bg-white px-3 py-2 text-sm text-slate-700 shadow-sm outline-none transition focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20">
          <option value="all">All servers</option>
          {#each servers as server}
            <option value={String(server.id)}>{server.name} · {server.host}</option>
          {/each}
        </select>

        <select bind:value={outcomeFilter} class="rounded-lg border border-slate-300 bg-white px-3 py-2 text-sm text-slate-700 shadow-sm outline-none transition focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20">
          <option value="all">All outcomes</option>
          <option value="success">Success</option>
          <option value="failure">Failure</option>
        </select>

        <div class="flex gap-3">
          <input type="date" bind:value={startDate} class="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-sm text-slate-700 shadow-sm outline-none transition focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20" />
          <input type="date" bind:value={endDate} class="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-sm text-slate-700 shadow-sm outline-none transition focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20" />
        </div>
      </div>
    </div>

    {#if loading}
      <div class="rounded-2xl border border-dashed border-slate-300 bg-white px-4 py-8 text-center text-slate-500">Loading connection history...</div>
    {:else if error}
      <div class="rounded-2xl border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700" role="alert">{error}</div>
    {:else if filteredHistory.length === 0}
      <div class="rounded-2xl border border-dashed border-slate-300 bg-white px-4 py-8 text-center text-slate-500">
        No history entries match the current filters.
      </div>
    {:else}
      <div class="overflow-x-auto rounded-2xl border border-slate-200 bg-white shadow-sm">
        <table class="min-w-full divide-y divide-slate-200">
          <thead class="bg-slate-50">
            <tr>
              <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500">Server</th>
              <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500">Auth</th>
              <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500">Cipher / KEX</th>
              <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500">Latency</th>
              <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500">Duration</th>
              <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500">Result</th>
              <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500">Timestamp</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-slate-100 bg-white">
            {#each filteredHistory as entry}
              <tr class="hover:bg-slate-50">
                <td class="px-4 py-4 align-top">
                  <div class="font-medium text-slate-900">{entry.serverName || `Server #${entry.serverId}`}</div>
                  <div class="mt-1 text-xs text-slate-500">{entry.hostname || entry.ip || '—'} · {entry.username}</div>
                </td>
                <td class="px-4 py-4 align-top text-sm text-slate-700">
                  <div class="font-medium text-slate-900">{entry.authMethod || '—'}</div>
                  <div class="mt-1 text-xs text-slate-500">{entry.agentUsed ? 'Agent used' : 'No agent'}{entry.keyFingerprint ? ` · ${entry.keyFingerprint.slice(0, 16)}…` : ''}</div>
                </td>
                <td class="px-4 py-4 align-top text-sm text-slate-700">
                  <div class="font-medium text-slate-900">{entry.cipher || '—'}</div>
                  <div class="mt-1 text-xs text-slate-500">{entry.kex || '—'}</div>
                  {#if entry.compression}
                    <div class="mt-1 text-xs text-slate-400">Compression: {entry.compression}</div>
                  {/if}
                </td>
                <td class="px-4 py-4 align-top text-sm text-slate-700">{formatLatency(entry.latencyMs)}</td>
                <td class="px-4 py-4 align-top text-sm text-slate-700">{formatDuration(entry.durationMs)}</td>
                <td class="px-4 py-4 align-top">
                  <div class={`inline-flex rounded-full px-2.5 py-1 text-xs font-medium ${successBadge(entry.success)}`}>
                    {entry.success ? 'Success' : 'Failure'}
                  </div>
                  {#if !entry.success && entry.failureReason}
                    <p class="mt-2 max-w-sm text-xs text-rose-600">{entry.failureReason}</p>
                  {/if}
                  {#if entry.exitStatus}
                    <p class="mt-1 text-xs text-slate-400">Exit status: {entry.exitStatus}</p>
                  {/if}
                </td>
                <td class="px-4 py-4 align-top text-sm text-slate-500">{formatDate(entry.timestamp)}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}

    <div class="rounded-2xl border border-slate-200 bg-white p-5 shadow-sm">
      <div class="flex items-start gap-3">
        <ServerCog size={18} class="mt-0.5 text-slate-500" />
        <p class="text-sm text-slate-500">
          The history table aggregates recent entries from every server so you can correlate failures, latency spikes, and cipher changes in one place.
        </p>
      </div>
    </div>
  </div>
</div>
