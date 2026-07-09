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
    event.preventDefault();
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
      case 'completed': return 'bg-success/15 text-success';
      case 'failed': return 'bg-error/15 text-error';
      case 'running': return 'bg-info/15 text-info';
      case 'planned': return 'bg-surface-muted text-fg-muted';
      case 'rolled_back': return 'bg-warning/15 text-warning';
      default: return 'bg-surface-muted text-fg-muted';
    }
  }

  function statusDot(status: string): string {
    switch (status) {
      case 'completed': return 'bg-success';
      case 'failed': return 'bg-error';
      case 'running': return 'bg-info animate-pulse';
      case 'planned': return 'bg-fg-subtle';
      case 'rolled_back': return 'bg-warning';
      default: return 'bg-fg-subtle';
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
        <a href="/migrations/new" class="flex items-center gap-1 px-4 py-2 bg-accent text-accent-fg rounded-lg hover:bg-accent-hover text-sm font-medium transition-colors">
          <Plus size={16} /> New Migration
        </a>
      {:else if hasServers}
        <a href="/servers/new" class="flex items-center gap-1 px-4 py-2 bg-accent text-accent-fg rounded-lg hover:bg-accent-hover text-sm font-medium transition-colors">
          <Plus size={16} /> Add Target Server
        </a>
      {:else}
        <a href="/servers/new" class="flex items-center gap-1 px-4 py-2 bg-accent text-accent-fg rounded-lg hover:bg-accent-hover text-sm font-medium transition-colors">
          <Plus size={16} /> Add Server
        </a>
      {/if}
    {/snippet}
  </PageHeader>

  {#if loading}
    <div class="space-y-3">
      {#each Array(3) as _, index}
        <div class="flex items-center justify-between gap-4 rounded-lg border border-border bg-surface p-4">
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
        <h2 class="text-sm font-semibold text-fg-muted">Migration Readiness</h2>
        <span class="text-xs text-fg-subtle">{readinessCount}/{readinessSteps.length} ready</span>
      </div>
      <div class="w-full h-1.5 bg-surface-muted rounded-full overflow-hidden mb-4">
        <div class="h-full bg-accent rounded-full transition-all duration-500" style="width: {readinessPercent}%"></div>
      </div>

      <div class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
        <!-- Servers Connected Card -->
        <div class="rounded-lg border bg-surface p-4 {hasMultipleServers ? 'border-border' : 'border-dashed border-border-strong'}">
          <div class="flex items-center gap-2 mb-3">
            <div class="w-8 h-8 rounded-lg flex items-center justify-center {hasMultipleServers ? 'bg-success/10 text-success' : 'bg-surface-muted text-fg-subtle'}">
              {#if hasMultipleServers}
                <CheckCircle2 size={18} />
              {:else}
                <ServerIcon size={18} />
              {/if}
            </div>
            <div>
              <h3 class="text-sm font-medium text-fg">Servers Connected</h3>
              <p class="text-xs text-fg-subtle">
                {#if hasMultipleServers}
                  {servers.length} servers ready to migrate between
                {:else if hasServers}
                  Add at least one more server
                {:else}
                  Connect servers to migrate between
                {/if}
              </p>
            </div>
          </div>
          {#if hasServers}
            <div class="space-y-1.5 text-xs">
              {#each servers.slice(0, 4) as s}
                <div class="flex items-center justify-between gap-2">
                  <span class="font-medium text-fg truncate">{s.name}</span>
                  {#if serverInfos[s.id]}
                    <span class="shrink-0 font-medium {serverInfos[s.id].sshStatus === 'connected' || serverInfos[s.id].sshStatus === 'ok' ? 'text-success' : 'text-warning'}">
                      {serverInfos[s.id].sshStatus || '—'}
                    </span>
                  {:else}
                    <span class="shrink-0 font-mono text-fg-subtle">{s.host}</span>
                  {/if}
                </div>
              {/each}
              {#if servers.length > 4}
                <p class="text-fg-subtle">+{servers.length - 4} more</p>
              {/if}
              <a href="/servers/new" class="inline-flex items-center gap-1 text-xs text-accent hover:text-accent font-medium mt-1">
                <Plus size={14} /> Add another server
              </a>
            </div>
          {:else}
            <div class="text-center py-2">
              <p class="text-xs text-fg-subtle mb-2">No servers connected</p>
              <a href="/servers/new" class="inline-flex items-center gap-1 text-xs text-accent hover:text-accent font-medium">
                <Plus size={14} /> Add a server
              </a>
            </div>
          {/if}
        </div>

        <!-- Discovery Card -->
        <div class="rounded-lg border bg-surface p-4 {hasServers && Object.keys(serverInfos).length > 0 ? 'border-border' : 'border-dashed border-border-strong'}">
          <div class="flex items-center gap-2 mb-3">
            <div class="w-8 h-8 rounded-lg flex items-center justify-center {hasServers && Object.keys(serverInfos).length > 0 ? 'bg-success/10 text-success' : 'bg-surface-muted text-fg-subtle'}">
              {#if hasServers && Object.keys(serverInfos).length > 0}
                <CheckCircle2 size={18} />
              {:else}
                <Search size={18} />
              {/if}
            </div>
            <div>
              <h3 class="text-sm font-medium text-fg">Discovery</h3>
              <p class="text-xs text-fg-subtle">Scan servers for services & containers</p>
            </div>
          </div>
          {#if hasServers && Object.keys(serverInfos).length > 0}
            <div class="space-y-1.5 text-xs">
              <div class="flex items-center justify-between">
                <span class="text-fg-subtle">Servers scanned</span>
                <span class="font-medium text-fg">{Object.keys(serverInfos).length} of {servers.length}</span>
              </div>
              <div class="flex items-center justify-between">
                <span class="text-fg-subtle">Status</span>
                <span class="font-medium {Object.keys(serverInfos).length >= servers.length ? 'text-success' : 'text-warning'}">
                  {Object.keys(serverInfos).length >= servers.length ? 'Complete' : 'Partial'}
                </span>
              </div>
              <a href="/discovery" class="inline-flex items-center gap-1 text-xs text-accent hover:text-accent font-medium mt-1">
                <Search size={14} /> View Discovery
              </a>
            </div>
          {:else if hasServers}
            <div class="text-center py-2">
              <p class="text-xs text-fg-subtle mb-2">Discovery not yet run</p>
              <a href="/discovery" class="inline-flex items-center gap-1 text-xs text-accent hover:text-accent font-medium">
                <Search size={14} /> Run Discovery
              </a>
            </div>
          {:else}
            <div class="text-center py-2">
              <p class="text-xs text-fg-subtle">Connect a server first</p>
            </div>
          {/if}
        </div>

        <!-- Ready to Migrate Card -->
        <div class="rounded-lg border bg-surface p-4 {hasMultipleServers ? 'border-border' : 'border-dashed border-border-strong'}">
          <div class="flex items-center gap-2 mb-3">
            <div class="w-8 h-8 rounded-lg flex items-center justify-center {hasMultipleServers ? 'bg-accent/10 text-accent' : 'bg-surface-muted text-fg-subtle'}">
              <ArrowRightLeft size={18} />
            </div>
            <div>
              <h3 class="text-sm font-medium text-fg">Ready to Migrate</h3>
              <p class="text-xs text-fg-subtle">Pick source & target in the wizard</p>
            </div>
          </div>
          {#if hasMultipleServers}
            <div class="space-y-2 text-xs">
              <p class="text-fg-muted">Choose any server as the source and any other as the target — you're not limited to two.</p>
              <a href="/migrations/new" class="inline-flex items-center gap-1 text-xs text-accent hover:text-accent font-medium">
                <Plus size={14} /> Start a migration
              </a>
            </div>
          {:else}
            <div class="text-center py-2">
              <p class="text-xs text-fg-subtle">Connect at least 2 servers to start</p>
            </div>
          {/if}
        </div>
      </div>
    </div>

    <!-- ═══ Guided Flow Section ═══ -->
    <div class="mb-6">
      <h2 class="text-sm font-semibold text-fg-muted mb-3">Guided Flow</h2>
      <div class="flex flex-wrap gap-2 items-center">
        {#each readinessSteps as step, i}
          <div class="flex items-center gap-2">
            <div class="flex items-center gap-2 px-3 py-2 rounded-lg border {step.done ? 'bg-success/10 border-success text-success' : 'bg-surface border-border text-fg-muted'}">
              {#if step.done}
                <CheckCircle2 size={16} class="text-success" />
              {:else}
                <Circle size={16} class="text-fg-subtle" />
              {/if}
              <span class="text-xs font-medium">{i + 1}. {step.label}</span>
            </div>
            {#if i < readinessSteps.length - 1}
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="text-fg-subtle hidden sm:block">
                <path d="m5 12h14" /><path d="m12 5 7 7-7 7" />
              </svg>
            {/if}
          </div>
        {/each}
      </div>
    </div>

    <!-- ═══ Migration History Section ═══ -->
    <div>
      <h2 class="text-sm font-semibold text-fg-muted mb-3">Migration History</h2>
      {#if hasMigrations}
        <div class="space-y-2">
          {#each migrations as m}
            <a
              href="/migrations/{m.id}/pipeline"
              class="block p-4 bg-surface rounded-lg border border-border hover:border-border-strong transition-colors"
            >
              <div class="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
                <div class="flex items-center gap-3">
                  <div class="w-2 h-2 rounded-full {statusDot(m.status)}"></div>
                  <div class="flex items-center gap-2">
                    <span class="text-sm font-medium text-fg">{serverName(m.sourceId ?? m.sourceServerId ?? 0)}</span>
                    <ArrowRight size={14} class="text-fg-subtle" />
                    <span class="text-sm font-medium text-fg">{serverName(m.targetId ?? m.targetServerId ?? 0)}</span>
                  </div>
                </div>
                <div class="flex items-center gap-3">
                  <span class="px-2 py-1 rounded-full text-xs font-medium {statusBadge(m.status)}">
                    {formatLabel(m.status)}
                  </span>
                  <span class="text-xs text-fg-subtle">{m.createdAt}</span>
                  <button
                    type="button"
                    aria-label="Delete migration"
                    onclick={(e) => deleteMigration(m.id, e)}
                    class="text-fg-subtle hover:text-error"
                  >
                    <Trash2 size={16} />
                  </button>
                </div>
              </div>
              <div class="flex flex-wrap gap-1 mt-2">
                {#each m.categories as cat}
                  <span class="px-2 py-0.5 bg-surface-muted text-fg-muted text-xs rounded">{cat}</span>
                {/each}
              </div>
            </a>
          {/each}
        </div>
      {:else}
        <div class="rounded-lg border border-border bg-surface p-6 text-center">
          <div class="inline-flex items-center justify-center rounded-full bg-surface-muted p-3 text-fg-subtle mb-3">
            <ArrowRightLeft size={24} />
          </div>
          <p class="text-sm text-fg-muted mb-1">No migrations yet.</p>
          <p class="text-xs text-fg-subtle">Once you create a migration, it will appear here with status, health, progress, and rollback availability.</p>
          {#if hasMultipleServers}
            <a href="/migrations/new" class="mt-4 inline-flex items-center justify-center gap-2 rounded-lg bg-accent px-4 py-2 text-sm font-medium text-accent-fg transition hover:bg-accent-hover">
              <Plus size={18} /> Create Migration
            </a>
          {/if}
        </div>
      {/if}
    </div>
  {/if}
</div>
