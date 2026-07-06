<script lang="ts">
  import { onMount } from 'svelte';
  import { migrationApi, type MigrationPlan } from '$lib/api/migrations';
  import { api } from '$lib/api/client';
  import { discoveryApi, type ServerConnectionInfo } from '$lib/api/discovery';
  import type { Server } from '$lib/stores/servers';
  import { Plus, ArrowRight, ArrowRightLeft, Trash2, Server as ServerIcon, CheckCircle2, Circle, AlertTriangle, Shield, Search, Activity } from 'lucide-svelte';
  import { PageHeader, Skeleton } from '$lib/components/ui';
  import { toast } from '$lib/stores/toast';
  import { formatLabel } from '$lib/utils/format';

  let migrations: MigrationPlan[] = [];
  let servers: Server[] = [];
  let serverInfos: Record<number, ServerConnectionInfo> = {};
  let loading = true;
  let loadingInfo = false;

  onMount(async () => {
    try {
      const [migs, svrs] = await Promise.all([
        migrationApi.list(),
        api.get('/servers') as Promise<Server[]>,
      ]);
      migrations = migs;
      servers = Array.isArray(svrs) ? svrs : [];
      // Fetch server info for all servers in background
      fetchServerInfos();
    } catch {
      // handle error
    } finally {
      loading = false;
    }
  });

  async function fetchServerInfos() {
    loadingInfo = true;
    const promises = servers.map(async (s) => {
      try {
        const info = await discoveryApi.getInfo(s.id);
        serverInfos = { ...serverInfos, [s.id]: info };
      } catch { /* ignore */ }
    });
    await Promise.all(promises);
    loadingInfo = false;
  }

  function serverName(id: number): string {
    const server = servers.find((s) => s.id === id);
    return server ? server.name : `Server #${id}`;
  }

  async function deleteMigration(id: number, event: MouseEvent) {
    event.stopPropagation();
    if (!confirm('Delete this migration?')) return;
    try {
      await migrationApi.delete(id);
      migrations = migrations.filter((m) => m.id !== id);
      toast.success('Migration deleted');
    } catch {
      toast.error('Failed to delete migration');
    }
  }

  function statusBadge(status: string): string {
    switch (status) {
      case 'completed': return 'bg-green-100 text-green-700';
      case 'failed': return 'bg-red-100 text-red-700';
      case 'running': return 'bg-blue-100 text-blue-700';
      case 'planned': return 'bg-slate-100 text-slate-700';
      case 'rolled_back': return 'bg-yellow-100 text-yellow-700';
      default: return 'bg-slate-100 text-slate-700';
    }
  }

  function statusDot(status: string): string {
    switch (status) {
      case 'completed': return 'bg-green-500';
      case 'failed': return 'bg-red-500';
      case 'running': return 'bg-blue-500 animate-pulse';
      case 'planned': return 'bg-slate-400';
      case 'rolled_back': return 'bg-yellow-500';
      default: return 'bg-slate-400';
    }
  }

  // ── Readiness helpers ──
  $: hasServers = servers.length > 0;
  $: hasMultipleServers = servers.length >= 2;
  $: hasMigrations = migrations.length > 0;

  $: readinessSteps = [
    { label: 'Connect source server', done: hasServers, href: '/servers/new' },
    { label: 'Connect target server', done: hasMultipleServers, href: '/servers/new' },
    { label: 'Run discovery', done: hasServers && Object.keys(serverInfos).length > 0, href: '/discovery' },
    { label: 'Review compatibility', done: hasMultipleServers, href: '/discovery' },
    { label: 'Create migration plan', done: hasMigrations, href: '/migrations/new' },
    { label: 'Start pipeline wizard', done: false, href: '/migrations' },
  ];

  $: readinessCount = readinessSteps.filter((s) => s.done).length;
  $: readinessPercent = (readinessCount / readinessSteps.length) * 100;
</script>

<div class="p-4 sm:p-6 max-w-5xl mx-auto">
  <PageHeader title="Migrations" subtitle="Prepare, run, and monitor zero-downtime server migrations.">
    {#snippet actions()}
      {#if hasMultipleServers}
        <a href="/migrations/new" class="flex items-center gap-1 px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 text-sm font-medium transition-colors">
          <Plus size={16} /> New Migration
        </a>
      {:else if hasServers}
        <a href="/servers/new" class="flex items-center gap-1 px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 text-sm font-medium transition-colors">
          <Plus size={16} /> Add Target Server
        </a>
      {:else}
        <a href="/servers/new" class="flex items-center gap-1 px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 text-sm font-medium transition-colors">
          <Plus size={16} /> Add Server
        </a>
      {/if}
    {/snippet}
  </PageHeader>

  {#if loading}
    <div class="space-y-3">
      {#each Array(3) as _, index}
        <div class="flex items-center justify-between gap-4 rounded-lg border border-slate-200 bg-white p-4">
          <Skeleton width="200px" />
          <div class="flex items-center gap-3">
            <Skeleton width="80px" height="20px" rounded />
            <Skeleton width="100px" />
          </div>
        </div>
      {/each}
    </div>
  {:else}
    <!-- ═══ Migration Readiness Section ═══ -->
    <div class="mb-6">
      <div class="flex items-center justify-between mb-3">
        <h2 class="text-sm font-semibold text-slate-700">Migration Readiness</h2>
        <span class="text-xs text-slate-500">{readinessCount}/{readinessSteps.length} ready</span>
      </div>
      <div class="w-full h-1.5 bg-slate-200 rounded-full overflow-hidden mb-4">
        <div class="h-full bg-blue-500 rounded-full transition-all duration-500" style="width: {readinessPercent}%"></div>
      </div>

      <div class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
        <!-- Source Server Card -->
        <div class="rounded-lg border bg-white p-4 {hasServers ? 'border-slate-200' : 'border-dashed border-slate-300'}">
          <div class="flex items-center gap-2 mb-3">
            <div class="w-8 h-8 rounded-lg flex items-center justify-center {hasServers ? 'bg-green-50 text-green-600' : 'bg-slate-100 text-slate-400'}">
              {#if hasServers}
                <CheckCircle2 size={18} />
              {:else}
                <ServerIcon size={18} />
              {/if}
            </div>
            <div>
              <h3 class="text-sm font-medium text-slate-900">Source Server</h3>
              <p class="text-xs text-slate-500">Primary VPS to migrate from</p>
            </div>
          </div>
          {#if hasServers}
            {#each servers.slice(0, 1) as s}
              <div class="space-y-1.5 text-xs">
                <div class="flex items-center justify-between">
                  <span class="text-slate-500">Name</span>
                  <span class="font-medium text-slate-900">{s.name}</span>
                </div>
                <div class="flex items-center justify-between">
                  <span class="text-slate-500">Host</span>
                  <span class="font-mono text-slate-700">{s.host}:{s.port}</span>
                </div>
                {#if serverInfos[s.id]}
                  <div class="flex items-center justify-between">
                    <span class="text-slate-500">OS</span>
                    <span class="text-slate-700">{serverInfos[s.id].os || '—'}</span>
                  </div>
                  <div class="flex items-center justify-between">
                    <span class="text-slate-500">SSH</span>
                    <span class="font-medium {serverInfos[s.id].sshStatus === 'connected' || serverInfos[s.id].sshStatus === 'ok' ? 'text-green-600' : 'text-yellow-600'}">
                      {serverInfos[s.id].sshStatus || '—'}
                    </span>
                  </div>
                {/if}
              </div>
            {/each}
          {:else}
            <div class="text-center py-2">
              <p class="text-xs text-slate-500 mb-2">No source server connected</p>
              <a href="/servers/new" class="inline-flex items-center gap-1 text-xs text-blue-600 hover:text-blue-700 font-medium">
                <Plus size={14} /> Add Source Server
              </a>
            </div>
          {/if}
        </div>

        <!-- Target Server Card -->
        <div class="rounded-lg border bg-white p-4 {hasMultipleServers ? 'border-slate-200' : 'border-dashed border-slate-300'}">
          <div class="flex items-center gap-2 mb-3">
            <div class="w-8 h-8 rounded-lg flex items-center justify-center {hasMultipleServers ? 'bg-green-50 text-green-600' : 'bg-slate-100 text-slate-400'}">
              {#if hasMultipleServers}
                <CheckCircle2 size={18} />
              {:else}
                <ServerIcon size={18} />
              {/if}
            </div>
            <div>
              <h3 class="text-sm font-medium text-slate-900">Target Server</h3>
              <p class="text-xs text-slate-500">Destination VPS to migrate to</p>
            </div>
          </div>
          {#if hasMultipleServers}
            {#each servers.slice(1, 2) as s}
              <div class="space-y-1.5 text-xs">
                <div class="flex items-center justify-between">
                  <span class="text-slate-500">Name</span>
                  <span class="font-medium text-slate-900">{s.name}</span>
                </div>
                <div class="flex items-center justify-between">
                  <span class="text-slate-500">Host</span>
                  <span class="font-mono text-slate-700">{s.host}:{s.port}</span>
                </div>
                {#if serverInfos[s.id]}
                  <div class="flex items-center justify-between">
                    <span class="text-slate-500">OS</span>
                    <span class="text-slate-700">{serverInfos[s.id].os || '—'}</span>
                  </div>
                  <div class="flex items-center justify-between">
                    <span class="text-slate-500">SSH</span>
                    <span class="font-medium {serverInfos[s.id].sshStatus === 'connected' || serverInfos[s.id].sshStatus === 'ok' ? 'text-green-600' : 'text-yellow-600'}">
                      {serverInfos[s.id].sshStatus || '—'}
                    </span>
                  </div>
                {/if}
              </div>
            {/each}
          {:else if hasServers}
            <div class="text-center py-2">
              <p class="text-xs text-slate-500 mb-2">No target server connected</p>
              <a href="/servers/new" class="inline-flex items-center gap-1 text-xs text-blue-600 hover:text-blue-700 font-medium">
                <Plus size={14} /> Add Target Server
              </a>
            </div>
          {:else}
            <div class="text-center py-2">
              <p class="text-xs text-slate-500 mb-2">No target server connected</p>
              <a href="/servers/new" class="inline-flex items-center gap-1 text-xs text-blue-600 hover:text-blue-700 font-medium">
                <Plus size={14} /> Add Target Server
              </a>
            </div>
          {/if}
        </div>

        <!-- Discovery Card -->
        <div class="rounded-lg border bg-white p-4 {hasServers && Object.keys(serverInfos).length > 0 ? 'border-slate-200' : 'border-dashed border-slate-300'}">
          <div class="flex items-center gap-2 mb-3">
            <div class="w-8 h-8 rounded-lg flex items-center justify-center {hasServers && Object.keys(serverInfos).length > 0 ? 'bg-green-50 text-green-600' : 'bg-slate-100 text-slate-400'}">
              {#if hasServers && Object.keys(serverInfos).length > 0}
                <CheckCircle2 size={18} />
              {:else}
                <Search size={18} />
              {/if}
            </div>
            <div>
              <h3 class="text-sm font-medium text-slate-900">Discovery</h3>
              <p class="text-xs text-slate-500">Scan servers for services & containers</p>
            </div>
          </div>
          {#if hasServers && Object.keys(serverInfos).length > 0}
            <div class="space-y-1.5 text-xs">
              <div class="flex items-center justify-between">
                <span class="text-slate-500">Servers scanned</span>
                <span class="font-medium text-slate-900">{Object.keys(serverInfos).length}</span>
              </div>
              <div class="flex items-center justify-between">
                <span class="text-slate-500">Status</span>
                <span class="font-medium text-green-600">Complete</span>
              </div>
              <a href="/discovery" class="inline-flex items-center gap-1 text-xs text-blue-600 hover:text-blue-700 font-medium mt-1">
                <Search size={14} /> View Discovery
              </a>
            </div>
          {:else if hasServers}
            <div class="text-center py-2">
              <p class="text-xs text-slate-500 mb-2">Discovery not yet run</p>
              <a href="/discovery" class="inline-flex items-center gap-1 text-xs text-blue-600 hover:text-blue-700 font-medium">
                <Search size={14} /> Run Discovery
              </a>
            </div>
          {:else}
            <div class="text-center py-2">
              <p class="text-xs text-slate-500">Connect a server first</p>
            </div>
          {/if}
        </div>
      </div>
    </div>

    <!-- ═══ Guided Flow Section ═══ -->
    <div class="mb-6">
      <h2 class="text-sm font-semibold text-slate-700 mb-3">Guided Flow</h2>
      <div class="flex flex-wrap gap-2 items-center">
        {#each readinessSteps as step, i}
          <div class="flex items-center gap-2">
            <div class="flex items-center gap-2 px-3 py-2 rounded-lg border {step.done ? 'bg-green-50 border-green-200 text-green-700' : 'bg-white border-slate-200 text-slate-600'}">
              {#if step.done}
                <CheckCircle2 size={16} class="text-green-500" />
              {:else}
                <Circle size={16} class="text-slate-400" />
              {/if}
              <span class="text-xs font-medium">{i + 1}. {step.label}</span>
            </div>
            {#if i < readinessSteps.length - 1}
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="text-slate-300 hidden sm:block">
                <path d="m5 12h14" /><path d="m12 5 7 7-7 7" />
              </svg>
            {/if}
          </div>
        {/each}
      </div>
    </div>

    <!-- ═══ Migration History Section ═══ -->
    <div>
      <h2 class="text-sm font-semibold text-slate-700 mb-3">Migration History</h2>
      {#if hasMigrations}
        <div class="space-y-2">
          {#each migrations as m}
            <a
              href="/migrations/{m.id}/pipeline"
              class="block p-4 bg-white rounded-lg border border-slate-200 hover:border-slate-300 transition-colors"
            >
              <div class="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
                <div class="flex items-center gap-3">
                  <div class="w-2 h-2 rounded-full {statusDot(m.status)}"></div>
                  <div class="flex items-center gap-2">
                    <span class="text-sm font-medium text-slate-900">{serverName(m.sourceServerId)}</span>
                    <ArrowRight size={14} class="text-slate-400" />
                    <span class="text-sm font-medium text-slate-900">{serverName(m.targetServerId)}</span>
                  </div>
                </div>
                <div class="flex items-center gap-3">
                  <span class="px-2 py-1 rounded-full text-xs font-medium {statusBadge(m.status)}">
                    {formatLabel(m.status)}
                  </span>
                  <span class="text-xs text-slate-500">{m.createdAt}</span>
                  <button
                    on:click={(e) => deleteMigration(m.id, e)}
                    class="text-slate-400 hover:text-red-500"
                  >
                    <Trash2 size={16} />
                  </button>
                </div>
              </div>
              <div class="flex flex-wrap gap-1 mt-2">
                {#each m.categories as cat}
                  <span class="px-2 py-0.5 bg-slate-50 text-slate-600 text-xs rounded">{cat}</span>
                {/each}
              </div>
            </a>
          {/each}
        </div>
      {:else}
        <div class="rounded-lg border border-slate-200 bg-white p-6 text-center">
          <div class="inline-flex items-center justify-center rounded-full bg-slate-100 p-3 text-slate-400 mb-3">
            <ArrowRightLeft size={24} />
          </div>
          <p class="text-sm text-slate-600 mb-1">No migrations yet.</p>
          <p class="text-xs text-slate-500">Once you create a migration, it will appear here with status, health, progress, and rollback availability.</p>
          {#if hasMultipleServers}
            <a href="/migrations/new" class="mt-4 inline-flex items-center justify-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white transition hover:bg-blue-700">
              <Plus size={18} /> Create Migration
            </a>
          {/if}
        </div>
      {/if}
    </div>
  {/if}
</div>
