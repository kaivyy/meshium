<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { goto } from '$app/navigation';
  import { Link, Server as ServerIcon, ArrowRight, Clock, CheckCircle2, XCircle, Loader2, AlertCircle } from 'lucide-svelte';
  import { PageHeader, Card, Badge, Skeleton } from '$lib/components/ui';
  import { fetchServers, serverStore } from '$lib/stores/servers';
  import { api } from '$lib/api/client';
  import { jobsApi, type Job } from '$lib/api/jobs';
  import { type ServerSnapshot } from '$lib/api/discovery';
  import { snapshotsStore, loadSnapshots } from '$lib/stores/snapshots';
  import { formatRelativeTime, formatLabel } from '$lib/utils/format';

  let totalServers = $state(0);
  let onlineServers = $state(0);
  let activeJobs = $state(0);
  let failedJobs = $state(0);
  let activeMigrations = $state(0);
  let recentJobs = $state([] as Job[]);
  let recentMigrations = $state([] as Array<{ id: number; status: string; createdAt: string; sourceId: number; targetId: number }>);
  let loadingActivity = $state(true);
  let refreshTimer: ReturnType<typeof setInterval> | null = null;

  onMount(async () => {
    fetchServers();
    await loadActivity();
    // Auto-refresh activity feed every 30s
    refreshTimer = setInterval(() => loadActivity(), 30000);
  });

  onDestroy(() => {
    if (refreshTimer) clearInterval(refreshTimer);
  });

  async function loadActivity() {
    loadingActivity = true;
    try {
      const [jobData, migData, serverData] = await Promise.all([
        jobsApi.list({ limit: 10 }),
        api.get('/migrations') as Promise<Array<{ id: number; status: string; createdAt: string; sourceId: number; targetId: number }>>,
        api.get('/servers') as Promise<Array<{ id: number; name: string; host: string; port: number }>>,
      ]);

      recentJobs = (jobData || []).slice(0, 10);
      recentMigrations = (migData || []).slice(0, 10);

      activeJobs = recentJobs.filter(j => j.status === 'running' || j.status === 'queued').length;
      failedJobs = recentJobs.filter(j => j.status === 'failed').length;
      activeMigrations = (migData || []).filter(m => m.status === 'running' || m.status === 'planned').length;

      totalServers = serverData?.length || 0;

      // Check which servers have snapshots (proxy for "online")
      await loadSnapshots((serverData || []).map((s) => s.id));
      const snaps: Record<number, ServerSnapshot | null> = $snapshotsStore;

      onlineServers = Object.values(snaps).filter(s => s != null).length;
    } catch {
      // ignore errors
    } finally {
      loadingActivity = false;
    }
  }

  const stats = [
    { label: 'Total Servers', value: () => totalServers, color: 'text-info', bg: 'bg-info/10' },
    { label: 'Online', value: () => onlineServers, color: 'text-success', bg: 'bg-success/10' },
    { label: 'Active Migrations', value: () => activeMigrations, color: 'text-accent', bg: 'bg-accent/10' },
    { label: 'Running Jobs', value: () => activeJobs, color: 'text-info', bg: 'bg-info/10' },
    { label: 'Failed Jobs', value: () => failedJobs, color: 'text-error', bg: 'bg-error/10' },
  ];

  const quickLinks = [
    { href: '/servers', label: 'Servers', desc: 'Manage your infrastructure', color: 'hover:border-accent hover:bg-accent-subtle' },
    { href: '/discovery', label: 'Discovery', desc: 'Scan & inventory servers', color: 'hover:border-accent hover:bg-accent-subtle' },
    { href: '/migrations', label: 'Migrations', desc: 'Migrate servers & services', color: 'hover:border-success hover:bg-success/10' },
    { href: '/jobs', label: 'Jobs', desc: 'Background tasks & operations', color: 'hover:border-warning hover:bg-warning/10' },
    { href: '/docker', label: 'Docker', desc: 'Containers & images', color: 'hover:border-info hover:bg-info/10' },
    { href: '/monitoring', label: 'Monitoring', desc: 'Real-time metrics', color: 'hover:border-accent hover:bg-accent-subtle' },
  ];

  function jobStatusVariant(status: string): 'success' | 'warning' | 'error' | 'neutral' {
    if (status === 'done') return 'success';
    if (status === 'running' || status === 'queued') return 'warning';
    if (status === 'failed' || status === 'cancelled') return 'error';
    return 'neutral';
  }

  function migStatusVariant(status: string): 'success' | 'warning' | 'error' | 'neutral' {
    if (status === 'completed') return 'success';
    if (status === 'running' || status === 'planned') return 'warning';
    if (status === 'failed' || status === 'rollback_failed') return 'error';
    return 'neutral';
  }

  function jobStatusIcon(status: string) {
    if (status === 'done') return CheckCircle2;
    if (status === 'failed' || status === 'cancelled') return XCircle;
    if (status === 'running' || status === 'queued') return Loader2;
    return Clock;
  }
</script>

<svelte:head><title>Dashboard - Meshium</title></svelte:head>

<div class="p-4 sm:p-6">
  <div class="max-w-7xl mx-auto">
    <PageHeader title="Dashboard" subtitle="Overview of your infrastructure and active operations." />

    <!-- Stats cards -->
    <div class="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-5 mb-6">
      {#each stats as stat}
        <Card padding="md">
          <div class="flex items-center gap-3">
            <div class={`flex h-10 w-10 items-center justify-center rounded-lg ${stat.bg}`}>
              <span class={`text-lg font-bold ${stat.color}`}>{stat.value()}</span>
            </div>
            <div>
              <p class="text-xs font-medium text-fg-subtle">{stat.label}</p>
              <p class="text-xl font-bold text-fg">{stat.value()}</p>
            </div>
          </div>
        </Card>
      {/each}
    </div>

    <!-- Quick links -->
    <h2 class="mb-3 text-sm font-semibold text-fg-muted">Quick Access</h2>
    <div class="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3 mb-6">
      {#each quickLinks as link}
        <a href={link.href} class={`block rounded-xl border border-border bg-surface p-4 shadow-sm transition ${link.color}`}>
          <div class="flex items-center justify-between">
            <div>
              <h3 class="text-sm font-semibold text-fg">{link.label}</h3>
              <p class="mt-1 text-xs text-fg-subtle">{link.desc}</p>
            </div>
            <Link size={16} class="text-fg-subtle" />
          </div>
        </a>
      {/each}
    </div>

    <!-- Recent Activity -->
    <h2 class="mb-3 text-sm font-semibold text-fg-muted">Recent Activity</h2>
    <div class="grid gap-4 lg:grid-cols-2">
      <!-- Recent Jobs -->
      <Card padding="lg">
        <div class="mb-4 flex items-center justify-between">
          <h3 class="text-sm font-semibold text-fg">Recent Jobs</h3>
          <a href="/jobs" class="inline-flex items-center gap-1 text-xs font-medium text-accent hover:underline">
            View all <ArrowRight size={12} />
          </a>
        </div>
        {#if loadingActivity}
          <div class="space-y-3">
            {#each Array(4) as _}
              <div class="flex items-center gap-3"><Skeleton width="32px" /><Skeleton width="60%" /><Skeleton width="80px" /></div>
            {/each}
          </div>
        {:else if recentJobs.length === 0}
          <div class="flex flex-col items-center justify-center py-8 text-center">
            <Clock size={24} class="text-fg-subtle" />
            <p class="mt-2 text-sm text-fg-subtle">No jobs yet.</p>
          </div>
        {:else}
          <div class="space-y-2">
            {#each recentJobs.slice(0, 8) as job (job.id)}
              {@const Icon = jobStatusIcon(job.status)}
              <div class="flex items-center gap-3 rounded-lg p-2 hover:bg-surface-muted">
                <span class={job.status === 'done' ? 'text-success' : job.status === 'failed' ? 'text-error' : job.status === 'running' ? 'text-info' : 'text-fg-subtle'}>
                  <Icon size={16} class={job.status === 'running' ? 'animate-spin' : ''} />
                </span>
                <div class="min-w-0 flex-1">
                  <div class="flex items-center gap-2">
                    <span class="text-sm font-medium text-fg">{formatLabel(job.type)}</span>
                    <Badge variant={jobStatusVariant(job.status)} size="sm">{formatLabel(job.status)}</Badge>
                  </div>
                  <p class="text-xs text-fg-subtle">{formatRelativeTime(job.createdAt)}</p>
                </div>
                <button type="button" onclick={() => goto(`/jobs/${job.id}`)} class="text-xs text-accent hover:underline">View</button>
              </div>
            {/each}
          </div>
        {/if}
      </Card>

      <!-- Recent Migrations -->
      <Card padding="lg">
        <div class="mb-4 flex items-center justify-between">
          <h3 class="text-sm font-semibold text-fg">Recent Migrations</h3>
          <a href="/migrations" class="inline-flex items-center gap-1 text-xs font-medium text-accent hover:underline">
            View all <ArrowRight size={12} />
          </a>
        </div>
        {#if loadingActivity}
          <div class="space-y-3">
            {#each Array(4) as _}
              <div class="flex items-center gap-3"><Skeleton width="32px" /><Skeleton width="60%" /><Skeleton width="80px" /></div>
            {/each}
          </div>
        {:else if recentMigrations.length === 0}
          <div class="flex flex-col items-center justify-center py-8 text-center">
            <ServerIcon size={24} class="text-fg-subtle" />
            <p class="mt-2 text-sm text-fg-subtle">No migrations yet.</p>
            <a href="/migrations/new" class="mt-2 text-xs font-medium text-accent hover:underline">Start a migration →</a>
          </div>
        {:else}
          <div class="space-y-2">
            {#each recentMigrations.slice(0, 8) as mig (mig.id)}
              <div class="flex items-center gap-3 rounded-lg p-2 hover:bg-surface-muted">
                <span class={mig.status === 'completed' ? 'text-success' : mig.status === 'failed' || mig.status === 'rollback_failed' ? 'text-error' : 'text-info'}>
                  {#if mig.status === 'completed'}
                    <CheckCircle2 size={16} />
                  {:else if mig.status === 'failed' || mig.status === 'rollback_failed'}
                    <XCircle size={16} />
                  {:else}
                    <Loader2 size={16} class="animate-spin" />
                  {/if}
                </span>
                <div class="min-w-0 flex-1">
                  <div class="flex items-center gap-2">
                    <span class="text-sm font-medium text-fg">Migration #{mig.id}</span>
                    <Badge variant={migStatusVariant(mig.status)} size="sm">{formatLabel(mig.status)}</Badge>
                  </div>
                  <p class="text-xs text-fg-subtle">{formatRelativeTime(mig.createdAt)}</p>
                </div>
                <button type="button" onclick={() => goto(`/migrations/${mig.id}`)} class="text-xs text-accent hover:underline">View</button>
              </div>
            {/each}
          </div>
        {/if}
      </Card>
    </div>
  </div>
</div>
