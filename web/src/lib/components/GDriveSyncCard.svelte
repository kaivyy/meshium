<script lang="ts">
  import { onMount } from 'svelte';
  import { CloudUpload, Plus, RefreshCw, Trash2, X } from 'lucide-svelte';
  import { api } from '$lib/api/client';

  interface GsyncPair {
    id: string;
    name: string;
    localPath: string;
    remotePath: string;
    enabled: boolean;
    intervalHours: number;
  }
  interface RunResult {
    startedAt: string;
    finishedAt: string;
    ok: boolean;
    summary: string;
  }
  interface GsyncStatus {
    configured: boolean;
    binaryOk: boolean;
    pairs: GsyncPair[];
    lastRuns: Record<string, RunResult>;
  }

  let status = $state<GsyncStatus | null>(null);
  let tokenInput = $state('');
  let savingToken = $state(false);
  let tokenError = $state('');
  let connecting = $state(false);

  let showPairForm = $state(false);
  let pairName = $state('');
  let pairLocal = $state('');
  let pairRemote = $state('');
  let pairInterval = $state(24);
  let savingPair = $state(false);
  let pairError = $state('');

  let runningId = $state('');
  let runError = $state('');

  async function load() {
    try {
      status = await api.get<GsyncStatus>('/gsync/status');
    } catch {
      /* card stays in "unavailable" state */
    }
  }
  onMount(load);

  async function connect() {
    savingToken = true;
    tokenError = '';
    try {
      await api.post('/gsync/config', { token: tokenInput });
      tokenInput = '';
      await load();
    } catch (e) {
      tokenError = e instanceof Error ? e.message : 'Failed to save the token.';
    } finally {
      savingToken = false;
    }
  }

  async function disconnect() {
    if (!confirm('Disconnect Google Drive? Stored sync pairs are kept but disabled until you reconnect.')) return;
    connecting = true;
    try {
      await api.delete('/gsync/config');
      await load();
    } finally {
      connecting = false;
    }
  }

  async function savePair(existing?: GsyncPair) {
    savingPair = true;
    pairError = '';
    try {
      const saved = await api.post<GsyncPair>('/gsync/pairs', {
        id: existing?.id ?? '',
        name: pairName || existing?.name || 'sync',
        localPath: pairLocal,
        remotePath: pairRemote,
        enabled: true,
        intervalHours: Number(pairInterval) || 24
      });
      status?.pairs.push(saved);
      showPairForm = false;
      pairName = pairLocal = pairRemote = '';
      pairInterval = 24;
      await load();
    } catch (e) {
      pairError = e instanceof Error ? e.message : 'Failed to save the sync pair.';
    } finally {
      savingPair = false;
    }
  }

  async function removePair(id: string) {
    if (!confirm('Delete this sync pair?')) return;
    await api.delete(`/gsync/pairs/${id}`);
    await load();
  }

  async function runNow(p: GsyncPair) {
    runningId = p.id;
    runError = '';
    try {
      const res = await api.post<{ ok: boolean; error?: string }>(`/gsync/run/${p.id}`);
      if (!res.ok && res.error) runError = `${p.name}: ${res.error}`;
      await load();
    } catch (e) {
      runError = e instanceof Error ? e.message : 'Sync failed.';
    } finally {
      runningId = '';
    }
  }

  function fmtTime(iso: string): string {
    return new Date(iso).toLocaleString();
  }
</script>

<section class="rounded-2xl border border-border bg-surface p-4 sm:p-6 shadow-sm">
  <div class="mb-4 flex items-start justify-between gap-3">
    <div>
      <h2 class="text-lg font-semibold text-fg">Google Drive Sync</h2>
      <p class="mt-1 text-sm text-fg-subtle">
        Mirror local folders to Google Drive (rclone). One-way, like rsync.
      </p>
    </div>
    <CloudUpload size={20} class="shrink-0 text-fg-subtle" />
  </div>

  {#if !status}
    <div class="rounded-lg border border-dashed border-border-strong bg-surface-muted px-4 py-6 text-center text-sm text-fg-subtle">
      Unable to load sync status.
    </div>
  {:else if !status.binaryOk}
    <div class="rounded-xl border border-warning bg-warning/10 px-4 py-3 text-sm text-fg">
      The <code class="font-mono">rclone</code> binary is not installed on this host.
      Install it with <code class="font-mono">apt install rclone</code>, then restart Meshium.
    </div>
  {:else if !status.configured}
    <div class="space-y-3">
      <p class="text-sm text-fg-muted">
        On any computer with rclone installed, run:
        <code class="mt-1 block rounded-lg bg-surface-muted p-2 font-mono text-xs text-fg">rclone authorize "drive"</code>
        then paste the resulting JSON here. The token is stored encrypted and never displayed again.
      </p>
      <textarea
        class="w-full rounded-lg border border-border bg-surface-muted p-3 font-mono text-xs text-fg"
        rows="4"
        placeholder={'{"access_token":"...","refresh_token":"..."}'}
        bind:value={tokenInput}
      ></textarea>
      {#if tokenError}<p class="text-sm text-error">{tokenError}</p>{/if}
      <button
        type="button"
        on:click={connect}
        disabled={savingToken || !tokenInput.trim()}
        class="inline-flex items-center gap-2 rounded-lg border border-border-strong bg-surface px-4 py-2 text-sm font-medium text-fg transition hover:bg-surface-muted disabled:cursor-not-allowed disabled:opacity-50"
      >
        {savingToken ? 'Saving...' : 'Connect Google Drive'}
      </button>
    </div>
  {:else}
    <div class="mb-4 flex flex-wrap items-center justify-between gap-2">
      <span class="inline-flex items-center gap-2 text-sm text-fg-muted">
        <span class="size-2 rounded-full bg-success"></span> Connected
      </span>
      <div class="flex gap-2">
        <button
          type="button"
          on:click={() => (showPairForm = !showPairForm)}
          class="inline-flex items-center gap-1 rounded-lg border border-border-strong bg-surface px-3 py-1.5 text-sm font-medium text-fg transition hover:bg-surface-muted"
        >
          {#if showPairForm}<X size={14} /> Cancel{:else}<Plus size={14} /> Add folder{/if}
        </button>
        <button
          type="button"
          on:click={disconnect}
          disabled={connecting}
          class="inline-flex items-center gap-1 rounded-lg border border-error bg-surface px-3 py-1.5 text-sm text-error transition hover:bg-error/15 disabled:opacity-50"
        >
          Disconnect
        </button>
      </div>
    </div>

    {#if showPairForm}
      <form
        class="mb-4 grid gap-3 rounded-xl border border-border bg-surface-muted p-4 sm:grid-cols-2"
        on:submit|preventDefault={() => savePair()}
      >
        <label class="text-sm text-fg-muted sm:col-span-2">
          Name
          <input class="mt-1 w-full rounded-lg border border-border bg-surface p-2 text-sm text-fg" bind:value={pairName} placeholder="meshium-db" />
        </label>
        <label class="text-sm text-fg-muted">
          Local folder (absolute path)
          <input class="mt-1 w-full rounded-lg border border-border bg-surface p-2 text-sm text-fg" bind:value={pairLocal} placeholder="/root/.meshium" required />
        </label>
        <label class="text-sm text-fg-muted">
          Drive folder
          <input class="mt-1 w-full rounded-lg border border-border bg-surface p-2 text-sm text-fg" bind:value={pairRemote} placeholder="backup/meshium" required />
        </label>
        <label class="text-sm text-fg-muted">
          Sync every (hours)
          <input type="number" min="1" class="mt-1 w-full rounded-lg border border-border bg-surface p-2 text-sm text-fg" bind:value={pairInterval} />
        </label>
        <div class="flex items-end">
          <button
            type="submit"
            disabled={savingPair}
            class="inline-flex items-center justify-center rounded-lg bg-accent px-4 py-2 text-sm font-medium text-accent-fg transition hover:opacity-90 disabled:opacity-50"
          >
            {savingPair ? 'Saving...' : 'Save'}
          </button>
        </div>
        {#if pairError}<p class="text-sm text-error sm:col-span-2">{pairError}</p>{/if}
      </form>
    {/if}

    {#if status.pairs.length === 0}
      <p class="rounded-lg border border-dashed border-border-strong bg-surface-muted px-4 py-6 text-center text-sm text-fg-subtle">
        No folders are being synced yet. Add one to start.
      </p>
    {:else}
      <ul class="space-y-2">
        {#each status.pairs as p (p.id)}
          {@const last = status.lastRuns[p.id]}
          <li class="rounded-xl border border-border bg-surface-muted p-3">
            <div class="flex flex-wrap items-center justify-between gap-2">
              <div class="min-w-0">
                <p class="truncate text-sm font-medium text-fg">{p.name}</p>
                <p class="truncate font-mono text-xs text-fg-subtle">{p.localPath} → Drive:{p.remotePath}</p>
              </div>
              <div class="flex shrink-0 items-center gap-2">
                {#if !p.enabled}<span class="text-xs text-fg-subtle">paused</span>{/if}
                <button
                  type="button"
                  title="Sync now"
                  on:click={() => runNow(p)}
                  disabled={runningId === p.id}
                  class="inline-flex items-center gap-1 rounded-lg border border-border-strong bg-surface px-3 py-1.5 text-xs font-medium text-fg transition hover:bg-surface-muted disabled:opacity-50"
                >
                  {#if runningId === p.id}<RefreshCw size={12} class="animate-spin" /> Syncing{:else}<CloudUpload size={12} /> Sync now{/if}
                </button>
                <button
                  type="button"
                  aria-label="Delete sync pair"
                  on:click={() => removePair(p.id)}
                  class="rounded-lg border border-transparent p-1.5 text-fg-subtle transition hover:text-error"
                >
                  <Trash2 size={14} />
                </button>
              </div>
            </div>
            {#if last}
              <p class="mt-2 text-xs {last.ok ? 'text-fg-subtle' : 'text-error'}">
                Last run {fmtTime(last.startedAt)} — {last.ok ? 'OK' : 'FAILED'}
              </p>
              {#if !last.ok}
                <pre class="mt-1 max-h-24 overflow-y-auto whitespace-pre-wrap break-all rounded-lg bg-surface p-2 font-mono text-[11px] text-error">{last.summary}</pre>
              {/if}
            {/if}
          </li>
        {/each}
      </ul>
    {/if}

    {#if runError}
      <div class="mt-3 rounded-xl border border-error bg-error/15 px-4 py-3 text-sm text-error" role="alert">{runError}</div>
    {/if}
  {/if}
</section>
