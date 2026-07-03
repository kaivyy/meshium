<script lang="ts">
  import { onMount } from 'svelte';
  import { Trash2, ShieldCheck, HelpCircle, AlertTriangle, Search, Fingerprint } from 'lucide-svelte';
  import { api } from '$lib/api/client';

  interface KnownHostEntry {
    host: string;
    port: number;
    hostKey: string;
    fingerprintSha256: string;
    fingerprintMd5: string;
    algorithm: string;
    bits: number;
    status: 'verified' | 'unknown' | 'changed' | string;
    verified: boolean;
    serverId: number;
    createdAt: string;
    updatedAt: string;
  }

  let entries: KnownHostEntry[] = [];
  let loading = true;
  let error = '';
  let removing = '';
  let statusFilter: 'all' | 'verified' | 'unknown' | 'changed' = 'all';
  let searchQuery = '';

  onMount(loadKnownHosts);

  async function loadKnownHosts() {
    loading = true;
    error = '';

    try {
      entries = (await api.get('/known-hosts')) as Array<KnownHostEntry>;
    } catch (e) {
      error = e instanceof Error ? e.message : 'Failed to load known hosts';
    } finally {
      loading = false;
    }
  }

  async function removeKnownHost(entry: KnownHostEntry) {
    if (!confirm(`Remove known host ${entry.host}:${entry.port}?`)) {
      return;
    }

    removing = `${entry.host}:${entry.port}`;
    error = '';

    try {
      await api.delete(`/known-hosts/${encodeURIComponent(entry.host)}/${entry.port}`);
      entries = entries.filter((item) => !(item.host === entry.host && item.port === entry.port));
    } catch (e) {
      error = e instanceof Error ? e.message : 'Failed to remove known host';
    } finally {
      removing = '';
    }
  }

  function formatDate(value: string) {
    if (!value) return '—';
    const date = new Date(value);
    return Number.isNaN(date.getTime()) ? value : date.toLocaleString();
  }

  function shortFingerprint(value: string) {
    if (!value) return '—';
    return value.length > 42 ? `${value.slice(0, 20)}…${value.slice(-16)}` : value;
  }

  function statusBadge(status: string, verified: boolean) {
    if (status === 'changed') return 'bg-rose-100 text-rose-700 ring-1 ring-rose-200';
    if (status === 'verified' || verified) return 'bg-emerald-100 text-emerald-700 ring-1 ring-emerald-200';
    return 'bg-amber-100 text-amber-700 ring-1 ring-amber-200';
  }

  $: filteredEntries = entries.filter((entry) => {
    if (statusFilter !== 'all' && entry.status !== statusFilter && !(statusFilter === 'verified' && entry.verified)) {
      return false;
    }

    const query = searchQuery.trim().toLowerCase();
    if (!query) return true;

    return [entry.host, String(entry.port), entry.fingerprintSha256, entry.fingerprintMd5, entry.algorithm, entry.status]
      .join(' ')
      .toLowerCase()
      .includes(query);
  });

  $: stats = {
    verified: entries.filter((entry) => entry.verified || entry.status === 'verified').length,
    unknown: entries.filter((entry) => entry.status === 'unknown').length,
    changed: entries.filter((entry) => entry.status === 'changed').length
  };
</script>

<svelte:head>
  <title>Known Hosts</title>
</svelte:head>

<div class="p-6">
  <div class="mx-auto flex max-w-7xl flex-col gap-6">
    <div class="rounded-2xl border border-slate-200 bg-white p-6 shadow-sm">
      <div class="flex flex-col gap-4 lg:flex-row lg:items-end lg:justify-between">
        <div>
          <div class="inline-flex items-center gap-2 rounded-full bg-blue-50 px-3 py-1 text-xs font-medium text-blue-700">
            <Fingerprint size={14} /> Trust On First Use
          </div>
          <h1 class="mt-3 text-2xl font-bold tracking-tight text-slate-900">Known Hosts</h1>
          <p class="mt-2 max-w-3xl text-sm text-slate-500">
            Review host fingerprints, verify known hosts, and remove entries that are no longer trusted.
          </p>
        </div>

        <a href="/ssh" class="inline-flex items-center justify-center rounded-lg border border-slate-300 bg-white px-4 py-2 text-sm font-medium text-slate-700 transition hover:bg-slate-50">
          Back to Dashboard
        </a>
      </div>

      <div class="mt-6 grid gap-4 md:grid-cols-3">
        <div class="rounded-xl border border-slate-200 bg-slate-50 p-4">
          <p class="text-xs font-semibold uppercase tracking-wide text-slate-500">Verified</p>
          <p class="mt-2 text-2xl font-bold text-slate-900">{stats.verified}</p>
        </div>
        <div class="rounded-xl border border-slate-200 bg-slate-50 p-4">
          <p class="text-xs font-semibold uppercase tracking-wide text-slate-500">Unknown</p>
          <p class="mt-2 text-2xl font-bold text-slate-900">{stats.unknown}</p>
        </div>
        <div class="rounded-xl border border-slate-200 bg-slate-50 p-4">
          <p class="text-xs font-semibold uppercase tracking-wide text-slate-500">Changed</p>
          <p class="mt-2 text-2xl font-bold text-slate-900">{stats.changed}</p>
        </div>
      </div>
    </div>

    <div class="flex flex-col gap-3 rounded-2xl border border-slate-200 bg-white p-4 shadow-sm md:flex-row md:items-center">
      <div class="relative flex-1">
        <Search size={18} class="absolute left-3 top-1/2 -translate-y-1/2 text-slate-400" />
        <input
          type="text"
          bind:value={searchQuery}
          placeholder="Search hosts, fingerprints, algorithms..."
          class="w-full rounded-lg border border-slate-300 bg-white py-2 pl-10 pr-4 text-slate-900 shadow-sm outline-none transition focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20"
        />
      </div>

      <select
        bind:value={statusFilter}
        class="rounded-lg border border-slate-300 bg-white px-3 py-2 text-sm text-slate-700 shadow-sm outline-none transition focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20"
      >
        <option value="all">All statuses</option>
        <option value="verified">Verified</option>
        <option value="unknown">Unknown</option>
        <option value="changed">Changed</option>
      </select>
    </div>

    {#if loading}
      <div class="rounded-2xl border border-dashed border-slate-300 bg-white px-4 py-8 text-center text-slate-500">Loading known hosts...</div>
    {:else if error}
      <div class="rounded-2xl border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700" role="alert">{error}</div>
    {:else if filteredEntries.length === 0}
      <div class="rounded-2xl border border-dashed border-slate-300 bg-white px-4 py-8 text-center text-slate-500">
        No known hosts match the current filters.
      </div>
    {:else}
      <div class="overflow-x-auto rounded-2xl border border-slate-200 bg-white shadow-sm">
        <table class="min-w-full divide-y divide-slate-200">
          <thead class="bg-slate-50">
            <tr>
              <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500">Host</th>
              <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500">Fingerprint</th>
              <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500">Algorithm</th>
              <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500">Status</th>
              <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500">Verified</th>
              <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500">Created / Updated</th>
              <th class="px-4 py-3 text-right text-xs font-semibold uppercase tracking-wide text-slate-500">Actions</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-slate-100 bg-white">
            {#each filteredEntries as entry}
              <tr class="hover:bg-slate-50">
                <td class="px-4 py-4 align-top">
                  <div class="font-medium text-slate-900">{entry.host}</div>
                  <div class="mt-1 text-sm text-slate-500">Port {entry.port}</div>
                  {#if entry.serverId}
                    <div class="mt-1 text-xs text-slate-400">Server #{entry.serverId}</div>
                  {/if}
                </td>
                <td class="px-4 py-4 align-top text-sm text-slate-700">
                  <div class="font-mono text-xs text-slate-600">SHA256</div>
                  <div class="font-mono text-xs text-slate-900">{shortFingerprint(entry.fingerprintSha256)}</div>
                  <div class="mt-3 font-mono text-xs text-slate-600">MD5</div>
                  <div class="font-mono text-xs text-slate-900">{shortFingerprint(entry.fingerprintMd5)}</div>
                </td>
                <td class="px-4 py-4 align-top text-sm text-slate-700">
                  <div class="font-medium text-slate-900">{entry.algorithm || '—'}</div>
                  <div class="mt-1 text-xs text-slate-500">{entry.bits ? `${entry.bits} bits` : 'Unknown bit size'}</div>
                </td>
                <td class="px-4 py-4 align-top">
                  <span class={`inline-flex rounded-full px-2.5 py-1 text-xs font-medium ${statusBadge(entry.status, entry.verified)}`}>
                    {entry.status || 'unknown'}
                  </span>
                </td>
                <td class="px-4 py-4 align-top text-sm text-slate-700">
                  {#if entry.verified}
                    <span class="inline-flex items-center gap-1 rounded-full bg-emerald-50 px-2.5 py-1 text-xs font-medium text-emerald-700">
                      <ShieldCheck size={14} /> Verified
                    </span>
                  {:else}
                    <span class="inline-flex items-center gap-1 rounded-full bg-amber-50 px-2.5 py-1 text-xs font-medium text-amber-700">
                      <HelpCircle size={14} /> Not verified
                    </span>
                  {/if}
                </td>
                <td class="px-4 py-4 align-top text-sm text-slate-500">
                  <div>Created: {formatDate(entry.createdAt)}</div>
                  <div class="mt-1">Updated: {formatDate(entry.updatedAt)}</div>
                </td>
                <td class="px-4 py-4 align-top text-right">
                  <button
                    type="button"
                    on:click={() => removeKnownHost(entry)}
                    disabled={removing === `${entry.host}:${entry.port}`}
                    class="inline-flex items-center gap-2 rounded-lg border border-red-200 bg-white px-3 py-2 text-sm font-medium text-red-600 transition hover:bg-red-50 disabled:cursor-not-allowed disabled:opacity-50"
                  >
                    <Trash2 size={16} />
                    {removing === `${entry.host}:${entry.port}` ? 'Removing...' : 'Remove'}
                  </button>
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}

    <div class="rounded-2xl border border-slate-200 bg-white p-5 text-sm text-slate-500 shadow-sm">
      <div class="flex items-start gap-3">
        <AlertTriangle size={18} class="mt-0.5 text-amber-500" />
        <p>
          Verified hosts are trusted entries stored by the SSH backend. Unknown or changed hosts should be reviewed before use.
        </p>
      </div>
    </div>
  </div>
</div>
