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
    if (status === 'changed') return 'bg-error/15 text-error ring-1 ring-error';
    if (status === 'verified' || verified) return 'bg-success/15 text-success ring-1 ring-success';
    return 'bg-warning/15 text-warning ring-1 ring-warning';
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
    <div class="rounded-2xl border border-border bg-surface p-6 shadow-sm">
      <div class="flex flex-col gap-4 lg:flex-row lg:items-end lg:justify-between">
        <div>
          <div class="inline-flex items-center gap-2 rounded-full bg-accent-subtle px-3 py-1 text-xs font-medium text-accent">
            <Fingerprint size={14} /> Trust On First Use
          </div>
          <h1 class="mt-3 text-2xl font-bold tracking-tight text-fg">Known Hosts</h1>
          <p class="mt-2 max-w-3xl text-sm text-fg-subtle">
            Review host fingerprints, verify known hosts, and remove entries that are no longer trusted.
          </p>
        </div>

        <a href="/ssh" class="inline-flex items-center justify-center rounded-lg border border-border-strong bg-surface px-4 py-2 text-sm font-medium text-fg-muted transition hover:bg-surface-muted">
          Back to Dashboard
        </a>
      </div>

      <div class="mt-6 grid gap-4 md:grid-cols-3">
        <div class="rounded-xl border border-border bg-surface-muted p-4">
          <p class="text-xs font-semibold uppercase tracking-wide text-fg-subtle">Verified</p>
          <p class="mt-2 text-2xl font-bold text-fg">{stats.verified}</p>
        </div>
        <div class="rounded-xl border border-border bg-surface-muted p-4">
          <p class="text-xs font-semibold uppercase tracking-wide text-fg-subtle">Unknown</p>
          <p class="mt-2 text-2xl font-bold text-fg">{stats.unknown}</p>
        </div>
        <div class="rounded-xl border border-border bg-surface-muted p-4">
          <p class="text-xs font-semibold uppercase tracking-wide text-fg-subtle">Changed</p>
          <p class="mt-2 text-2xl font-bold text-fg">{stats.changed}</p>
        </div>
      </div>
    </div>

    <div class="flex flex-col gap-3 rounded-2xl border border-border bg-surface p-4 shadow-sm md:flex-row md:items-center">
      <div class="relative flex-1">
        <Search size={18} class="absolute left-3 top-1/2 -translate-y-1/2 text-fg-subtle" />
        <input
          type="text"
          bind:value={searchQuery}
          placeholder="Search hosts, fingerprints, algorithms..."
          class="w-full rounded-lg border border-border-strong bg-surface py-2 pl-10 pr-4 text-fg shadow-sm outline-none transition focus:border-accent focus:ring-2 focus:ring-accent/20"
        />
      </div>

      <select
        bind:value={statusFilter}
        class="rounded-lg border border-border-strong bg-surface px-3 py-2 text-sm text-fg-muted shadow-sm outline-none transition focus:border-accent focus:ring-2 focus:ring-accent/20"
      >
        <option value="all">All statuses</option>
        <option value="verified">Verified</option>
        <option value="unknown">Unknown</option>
        <option value="changed">Changed</option>
      </select>
    </div>

    {#if loading}
      <div class="rounded-2xl border border-dashed border-border-strong bg-surface px-4 py-8 text-center text-fg-subtle">Loading known hosts...</div>
    {:else if error}
      <div class="rounded-2xl border border-error bg-error/10 px-4 py-3 text-sm text-error" role="alert">{error}</div>
    {:else if filteredEntries.length === 0}
      <div class="rounded-2xl border border-dashed border-border-strong bg-surface px-4 py-8 text-center text-fg-subtle">
        No known hosts match the current filters.
      </div>
    {:else}
      <div class="overflow-x-auto rounded-2xl border border-border bg-surface shadow-sm">
        <table class="min-w-full divide-y divide-border">
          <thead class="bg-surface-muted">
            <tr>
              <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-fg-subtle">Host</th>
              <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-fg-subtle">Fingerprint</th>
              <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-fg-subtle">Algorithm</th>
              <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-fg-subtle">Status</th>
              <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-fg-subtle">Verified</th>
              <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-fg-subtle">Created / Updated</th>
              <th class="px-4 py-3 text-right text-xs font-semibold uppercase tracking-wide text-fg-subtle">Actions</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-border bg-surface">
            {#each filteredEntries as entry}
              <tr class="hover:bg-surface-muted">
                <td class="px-4 py-4 align-top">
                  <div class="font-medium text-fg">{entry.host}</div>
                  <div class="mt-1 text-sm text-fg-subtle">Port {entry.port}</div>
                  {#if entry.serverId}
                    <div class="mt-1 text-xs text-fg-subtle">Server #{entry.serverId}</div>
                  {/if}
                </td>
                <td class="px-4 py-4 align-top text-sm text-fg-muted">
                  <div class="font-mono text-xs text-fg-muted">SHA256</div>
                  <div class="font-mono text-xs text-fg">{shortFingerprint(entry.fingerprintSha256)}</div>
                  <div class="mt-3 font-mono text-xs text-fg-muted">MD5</div>
                  <div class="font-mono text-xs text-fg">{shortFingerprint(entry.fingerprintMd5)}</div>
                </td>
                <td class="px-4 py-4 align-top text-sm text-fg-muted">
                  <div class="font-medium text-fg">{entry.algorithm || '—'}</div>
                  <div class="mt-1 text-xs text-fg-subtle">{entry.bits ? `${entry.bits} bits` : 'Unknown bit size'}</div>
                </td>
                <td class="px-4 py-4 align-top">
                  <span class={`inline-flex rounded-full px-2.5 py-1 text-xs font-medium ${statusBadge(entry.status, entry.verified)}`}>
                    {entry.status || 'unknown'}
                  </span>
                </td>
                <td class="px-4 py-4 align-top text-sm text-fg-muted">
                  {#if entry.verified}
                    <span class="inline-flex items-center gap-1 rounded-full bg-success/10 px-2.5 py-1 text-xs font-medium text-success">
                      <ShieldCheck size={14} /> Verified
                    </span>
                  {:else}
                    <span class="inline-flex items-center gap-1 rounded-full bg-warning/10 px-2.5 py-1 text-xs font-medium text-warning">
                      <HelpCircle size={14} /> Not verified
                    </span>
                  {/if}
                </td>
                <td class="px-4 py-4 align-top text-sm text-fg-subtle">
                  <div>Created: {formatDate(entry.createdAt)}</div>
                  <div class="mt-1">Updated: {formatDate(entry.updatedAt)}</div>
                </td>
                <td class="px-4 py-4 align-top text-right">
                  <button
                    type="button"
                    on:click={() => removeKnownHost(entry)}
                    disabled={removing === `${entry.host}:${entry.port}`}
                    class="inline-flex items-center gap-2 rounded-lg border border-error bg-surface px-3 py-2 text-sm font-medium text-error transition hover:bg-error/10 disabled:cursor-not-allowed disabled:opacity-50"
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

    <div class="rounded-2xl border border-border bg-surface p-5 text-sm text-fg-subtle shadow-sm">
      <div class="flex items-start gap-3">
        <AlertTriangle size={18} class="mt-0.5 text-warning" />
        <p>
          Verified hosts are trusted entries stored by the SSH backend. Unknown or changed hosts should be reviewed before use.
        </p>
      </div>
    </div>
  </div>
</div>
