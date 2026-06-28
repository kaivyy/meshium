<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import {
    FolderTree, HardDrive, RefreshCw, Server as ServerIcon,
    Search, Filter, ChevronDown, ChevronUp, AlertCircle, Folder
  } from 'lucide-svelte';
  import { api } from '$lib/api/client';
  import { discoveryApi, type ServerSnapshot, type DiskPartition } from '$lib/api/discovery';
  import { type Server } from '$lib/stores/servers';
  import { Badge, Card, EmptyState, PageHeader, Skeleton, Spinner, ProgressBar } from '$lib/components/ui';
  import { formatRelativeTime } from '$lib/utils/format';
  import { toast } from '$lib/stores/toast';

  let servers = $state([] as Server[]);
  let snapshots = $state({} as Record<number, ServerSnapshot | null>);
  let loading = $state(true);
  let searchQuery = $state('');
  let showFilters = $state(false);
  let filterServer = $state('all');
  let filterUsage = $state('all');

  interface AggregatedPartition {
    partition: DiskPartition;
    serverId: number;
    serverName: string;
    capturedAt: string;
  }

  const allPartitions = $derived.by(() => {
    const result: AggregatedPartition[] = [];
    servers.forEach(s => {
      const snap = snapshots[s.id];
      if (snap?.diskUsage) {
        snap.diskUsage.forEach(p => {
          result.push({ partition: p, serverId: s.id, serverName: s.name, capturedAt: snap.capturedAt });
        });
      }
    });
    return result;
  });

  const serverOptions = $derived.by(() => {
    const svrs = new Set();
    allPartitions.forEach(p => svrs.add(p.serverName));
    return ['all', ...Array.from(svrs).sort()];
  });

  const filteredPartitions = $derived.by(() => {
    let result = allPartitions;
    if (searchQuery) {
      const q = searchQuery.toLowerCase();
      result = result.filter(p =>
        p.partition.mountPoint.toLowerCase().includes(q) ||
        p.partition.filesystem.toLowerCase().includes(q) ||
        p.serverName.toLowerCase().includes(q)
      );
    }
    if (filterServer !== 'all') {
      result = result.filter(p => p.serverName === filterServer);
    }
    if (filterUsage !== 'all') {
      if (filterUsage === 'critical') result = result.filter(p => p.partition.usePercent > 90);
      else if (filterUsage === 'warning') result = result.filter(p => p.partition.usePercent > 75 && p.partition.usePercent <= 90);
      else if (filterUsage === 'healthy') result = result.filter(p => p.partition.usePercent <= 75);
    }
    return result.sort((a, b) => b.partition.usePercent - a.partition.usePercent);
  });

  const diskStats = $derived.by(() => {
    let total = allPartitions.length;
    let critical = 0, warning = 0, healthy = 0;
    let totalSizeGb = 0, totalUsedGb = 0;
    allPartitions.forEach(p => {
      if (p.partition.usePercent > 90) critical++;
      else if (p.partition.usePercent > 75) warning++;
      else healthy++;
      totalSizeGb += p.partition.sizeGb;
      totalUsedGb += p.partition.usedGb;
    });
    return { total, critical, warning, healthy, totalSizeGb, totalUsedGb };
  });

  onMount(async () => {
    await loadAll();
  });

  async function loadAll() {
    loading = true;
    try {
      const data = await api.get('/servers') as Server[];
      servers = data;
      await Promise.all(data.map(s => loadSnapshot(s.id)));
    } catch {
      toast.error('Failed to load disk data');
    } finally {
      loading = false;
    }
  }

  async function loadSnapshot(serverId: number) {
    try {
      const snap = await discoveryApi.getSnapshot(serverId);
      snapshots = { ...snapshots, [serverId]: snap };
    } catch { /* Snapshot may not exist yet */ }
  }

  function usageVariant(pct: number): 'default' | 'success' | 'warning' | 'error' {
    if (pct > 90) return 'error';
    if (pct > 75) return 'warning';
    return 'success';
  }

  function formatGB(gb: number): string {
    if (gb >= 1000) return `${(gb / 1000).toFixed(1)} TB`;
    return `${gb.toFixed(1)} GB`;
  }
</script>

<svelte:head><title>Files - Meshium</title></svelte:head>

<div class="p-4 sm:p-6 max-w-7xl mx-auto">
  <PageHeader title="Files" subtitle="Disk usage and filesystem overview across all servers.">
    {#snippet actions()}
      <button type="button" onclick={loadAll} disabled={loading} class="inline-flex items-center gap-2 rounded-lg border border-slate-300 bg-white px-4 py-2 text-sm font-medium text-slate-700 hover:bg-slate-50 disabled:opacity-60">
        {#if loading}<Spinner size="sm" label="Refreshing" />{:else}<RefreshCw size={16} />{/if}
        Refresh
      </button>
    {/snippet}
  </PageHeader>

  <!-- Stats -->
  <div class="mb-6 grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
    <Card padding="lg">
      <div class="flex items-center gap-3">
        <div class="flex h-10 w-10 items-center justify-center rounded-lg bg-blue-50 text-blue-600"><HardDrive size={20} /></div>
        <div>
          <p class="text-2xl font-bold text-slate-900">{diskStats.total}</p>
          <p class="text-xs text-slate-500">Partitions</p>
        </div>
      </div>
    </Card>
    <Card padding="lg">
      <div class="flex items-center gap-3">
        <div class="flex h-10 w-10 items-center justify-center rounded-lg bg-green-50 text-green-600"><Folder size={20} /></div>
        <div>
          <p class="text-2xl font-bold text-slate-900">{formatGB(diskStats.totalUsedGb)} / {formatGB(diskStats.totalSizeGb)}</p>
          <p class="text-xs text-slate-500">Total Used / Size</p>
        </div>
      </div>
    </Card>
    <Card padding="lg">
      <div class="flex items-center gap-3">
        <div class="flex h-10 w-10 items-center justify-center rounded-lg bg-yellow-50 text-yellow-600"><AlertCircle size={20} /></div>
        <div>
          <p class="text-2xl font-bold text-slate-900">{diskStats.warning}</p>
          <p class="text-xs text-slate-500">Warnings (>75%)</p>
        </div>
      </div>
    </Card>
    <Card padding="lg">
      <div class="flex items-center gap-3">
        <div class="flex h-10 w-10 items-center justify-center rounded-lg bg-red-50 text-red-600"><AlertCircle size={20} /></div>
        <div>
          <p class="text-2xl font-bold text-slate-900">{diskStats.critical}</p>
          <p class="text-xs text-slate-500">Critical (>90%)</p>
        </div>
      </div>
    </Card>
  </div>

  <!-- Search and Filters -->
  <div class="mb-6">
    <Card>
    <div class="flex flex-col gap-4">
      <div class="flex items-center gap-3">
        <div class="relative flex-1">
          <Search size={16} class="absolute left-3 top-1/2 -translate-y-1/2 text-slate-400" />
          <input type="text" bind:value={searchQuery} placeholder="Search mount points, filesystems, servers..." class="w-full rounded-lg border border-slate-300 bg-white py-2 pl-10 pr-4 text-sm outline-none focus:border-blue-500" />
        </div>
        <button type="button" onclick={() => showFilters = !showFilters} class="inline-flex items-center gap-2 rounded-lg border border-slate-300 bg-white px-4 py-2 text-sm font-medium text-slate-700 hover:bg-slate-50">
          <Filter size={16} />Filters{#if showFilters}<ChevronUp size={16} />{:else}<ChevronDown size={16} />{/if}
        </button>
      </div>
      {#if showFilters}
        <div class="grid gap-4 sm:grid-cols-2">
          <label class="block">
            <span class="mb-1 block text-xs font-semibold uppercase tracking-wide text-slate-500">Server</span>
            <select bind:value={filterServer} class="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-sm outline-none focus:border-blue-500">
              {#each serverOptions as srv}<option value={srv}>{srv === 'all' ? 'All servers' : srv}</option>{/each}
            </select>
          </label>
          <label class="block">
            <span class="mb-1 block text-xs font-semibold uppercase tracking-wide text-slate-500">Usage Level</span>
            <select bind:value={filterUsage} class="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-sm outline-none focus:border-blue-500">
              <option value="all">All levels</option>
              <option value="critical">Critical (>90%)</option>
              <option value="warning">Warning (75-90%)</option>
              <option value="healthy">Healthy (under 75%)</option>
            </select>
          </label>
        </div>
      {/if}
    </div>
    </Card>
  </div>

  <!-- Partitions -->
  {#if loading && servers.length === 0}
    <div class="space-y-3">
      {#each Array(5) as _}
        <Card><div class="space-y-3"><Skeleton width="40%" /><Skeleton height="2rem" rounded /></div></Card>
      {/each}
    </div>
  {:else if filteredPartitions.length === 0}
    <EmptyState title="No disk data" description={allPartitions.length === 0 ? "No disk partition data available. Run discovery scans to collect disk info." : "No partitions match your filters."} icon={emptyIcon} />
  {:else}
    <div class="space-y-3">
      {#each filteredPartitions as item (item.serverId + item.partition.mountPoint)}
        <Card padding="lg" hoverable>
          <div class="flex items-start justify-between gap-4">
            <div class="min-w-0 flex-1">
              <div class="flex items-center gap-2">
                <FolderTree size={16} class="text-slate-400" />
                <span class="font-mono text-sm font-medium text-slate-900">{item.partition.mountPoint}</span>
                {#if item.partition.usePercent > 90}
                  <Badge variant="error" size="sm">Critical</Badge>
                {:else if item.partition.usePercent > 75}
                  <Badge variant="warning" size="sm">Warning</Badge>
                {/if}
              </div>
              <p class="mt-1 font-mono text-xs text-slate-500">{item.partition.filesystem}</p>
              <div class="mt-2 flex items-center gap-3 text-xs text-slate-400">
                <button type="button" onclick={() => goto(`/servers/${item.serverId}`)} class="hover:text-blue-600 hover:underline">{item.serverName}</button>
                <span>·</span>
                <span>Scanned {formatRelativeTime(item.capturedAt)}</span>
              </div>
            </div>
            <div class="shrink-0 text-right">
              <p class="text-sm font-medium text-slate-700">{formatGB(item.partition.usedGb)} / {formatGB(item.partition.sizeGb)}</p>
              <p class="text-xs text-slate-400">{formatGB(item.partition.availGb)} free</p>
            </div>
          </div>
          <div class="mt-3">
            <ProgressBar value={item.partition.usePercent} variant={usageVariant(item.partition.usePercent)} />
          </div>
        </Card>
      {/each}
    </div>
  {/if}
</div>

{#snippet emptyIcon()}<FolderTree size={22} />{/snippet}
