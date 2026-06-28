<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import {
    Activity, RefreshCw, Cpu, HardDrive, MemoryStick, AlertCircle,
    Server as ServerIcon, Clock
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

  interface ServerHealth {
    server: Server;
    snapshot: ServerSnapshot | null;
    cpuUsage: number | null;
    ramUsage: number | null;
    diskUsage: number | null;
    diskWarning: boolean;
    diskCritical: boolean;
  }

  const serverHealth = $derived.by(() => {
    return servers.map(s => {
      const snap = snapshots[s.id];
      let cpuUsage: number | null = null;
      let ramUsage: number | null = null;
      let diskUsage: number | null = null;
      let diskWarning = false;
      let diskCritical = false;

      if (snap) {
        if (snap.hardware?.ramTotalMb && snap.hardware?.ramUsedMb) {
          ramUsage = (snap.hardware.ramUsedMb / snap.hardware.ramTotalMb) * 100;
        }
        if (snap.hardware?.diskTotalGb && snap.hardware?.diskUsedGb) {
          diskUsage = (snap.hardware.diskUsedGb / snap.hardware.diskTotalGb) * 100;
          diskWarning = diskUsage > 75;
          diskCritical = diskUsage > 90;
        }
        // Check individual partitions for warnings
        if (snap.diskUsage) {
          snap.diskUsage.forEach((p) => {
            if (p.usePercent > 90) diskCritical = true;
            else if (p.usePercent > 75) diskWarning = true;
          });
        }
      }

      return { server: s, snapshot: snap, cpuUsage, ramUsage, diskUsage, diskWarning, diskCritical };
    });
  });

  const fleetStats = $derived.by(() => {
    let totalServers = servers.length;
    let scannedServers = 0;
    let totalCores = 0;
    let totalRamMB = 0;
    let totalDiskGB = 0;
    let warnings = 0;
    let critical = 0;

    serverHealth.forEach(h => {
      if (h.snapshot) {
        scannedServers++;
        totalCores += h.snapshot.hardware?.cpuCores || 0;
        totalRamMB += h.snapshot.hardware?.ramTotalMb || 0;
        totalDiskGB += h.snapshot.hardware?.diskTotalGb || 0;
        if (h.diskCritical) critical++;
        else if (h.diskWarning) warnings++;
      }
    });

    return { totalServers, scannedServers, totalCores, totalRamMB, totalDiskGB, warnings, critical };
  });

  onMount(async () => {
    await loadServers();
  });

  async function loadServers() {
    loading = true;
    try {
      const data = await api.get('/servers') as Server[];
      servers = data;
      await Promise.all(data.map(s => loadSnapshot(s.id)));
    } catch {
      toast.error('Failed to load servers');
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

  function ramVariant(usage: number | null): 'default' | 'success' | 'warning' | 'error' {
    if (usage === null) return 'default';
    if (usage > 90) return 'error';
    if (usage > 75) return 'warning';
    return 'success';
  }

  function diskVariant(warning: boolean, critical: boolean): 'default' | 'success' | 'warning' | 'error' {
    if (critical) return 'error';
    if (warning) return 'warning';
    return 'success';
  }

  function formatGB(gb: number): string {
    if (gb >= 1000) return `${(gb / 1000).toFixed(1)} TB`;
    return `${Math.round(gb)} GB`;
  }

  function formatRAM(mb: number): string {
    if (mb >= 1024) return `${(mb / 1024).toFixed(1)} GB`;
    return `${Math.round(mb)} MB`;
  }

  function getSnapshotAge(snap: ServerSnapshot | null | undefined): string {
    if (!snap?.capturedAt) return 'Never';
    return formatRelativeTime(snap.capturedAt);
  }

  function hasSnapshot(snap: ServerSnapshot | null | undefined): boolean {
    return snap != null && snap.capturedAt !== undefined;
  }
</script>

<svelte:head><title>Monitoring - Meshium</title></svelte:head>

<div class="p-4 sm:p-6 max-w-7xl mx-auto">
  <PageHeader title="Monitoring" subtitle="Resource utilization and health across your fleet.">
    {#snippet actions()}
      <button type="button" onclick={loadServers} disabled={loading} class="inline-flex items-center gap-2 rounded-lg border border-slate-300 bg-white px-4 py-2 text-sm font-medium text-slate-700 hover:bg-slate-50 disabled:opacity-60">
        {#if loading}<Spinner size="sm" label="Refreshing" />{:else}<RefreshCw size={16} />{/if}
        Refresh
      </button>
    {/snippet}
  </PageHeader>

  <!-- Fleet stats -->
  <div class="mb-6 grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
    <Card padding="lg">
      <div class="flex items-center gap-3">
        <div class="flex h-10 w-10 items-center justify-center rounded-lg bg-blue-50 text-blue-600"><ServerIcon size={20} /></div>
        <div>
          <p class="text-2xl font-bold text-slate-900">{fleetStats.scannedServers}/{fleetStats.totalServers}</p>
          <p class="text-xs text-slate-500">Servers Scanned</p>
        </div>
      </div>
    </Card>
    <Card padding="lg">
      <div class="flex items-center gap-3">
        <div class="flex h-10 w-10 items-center justify-center rounded-lg bg-purple-50 text-purple-600"><Cpu size={20} /></div>
        <div>
          <p class="text-2xl font-bold text-slate-900">{fleetStats.totalCores}</p>
          <p class="text-xs text-slate-500">Total CPU Cores</p>
        </div>
      </div>
    </Card>
    <Card padding="lg">
      <div class="flex items-center gap-3">
        <div class="flex h-10 w-10 items-center justify-center rounded-lg bg-green-50 text-green-600"><MemoryStick size={20} /></div>
        <div>
          <p class="text-2xl font-bold text-slate-900">{formatRAM(fleetStats.totalRamMB)}</p>
          <p class="text-xs text-slate-500">Total RAM</p>
        </div>
      </div>
    </Card>
    <Card padding="lg">
      <div class="flex items-center gap-3">
        <div class="flex h-10 w-10 items-center justify-center rounded-lg bg-yellow-50 text-yellow-600"><AlertCircle size={20} /></div>
        <div>
          <p class="text-2xl font-bold text-slate-900">{fleetStats.warnings + fleetStats.critical}</p>
          <p class="text-xs text-slate-500">Alerts ({fleetStats.critical} critical)</p>
        </div>
      </div>
    </Card>
  </div>

  <!-- Server health cards -->
  {#if loading && servers.length === 0}
    <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
      {#each Array(6) as _}
        <Card><div class="space-y-3"><Skeleton width="60%" /><Skeleton width="40%" /><Skeleton height="3rem" rounded /></div></Card>
      {/each}
    </div>
  {:else if serverHealth.length === 0}
    <EmptyState title="No servers yet" description="Add servers to monitor their resource utilization." icon={emptyIcon} />
  {:else}
    <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
      {#each serverHealth as h (h.server.id)}
        <Card padding="lg" hoverable>
          <!-- Header -->
          <div class="mb-3 flex items-start justify-between gap-2">
            <div class="min-w-0 flex-1">
              <div class="flex items-center gap-2">
                <h3 class="truncate text-sm font-semibold text-slate-900">{h.server.name}</h3>
                {#if h.diskCritical}<Badge variant="error" size="sm">Critical</Badge>{:else if h.diskWarning}<Badge variant="warning" size="sm">Warning</Badge>{/if}
              </div>
              <p class="mt-0.5 truncate text-xs text-slate-500">{h.server.host}:{h.server.port}</p>
            </div>
            <a href={`/servers/${h.server.id}`} class="text-xs text-blue-600 hover:underline">View →</a>
          </div>

          {#if !hasSnapshot(h.snapshot)}
            <div class="rounded-lg border border-dashed border-slate-200 bg-slate-50 p-4 text-center">
              <Activity size={20} class="mx-auto text-slate-300" />
              <p class="mt-2 text-sm text-slate-500">Not scanned yet</p>
            </div>
          {:else}
            <div class="space-y-3">
              <!-- CPU -->
              <div>
                <div class="mb-1 flex items-center justify-between text-xs">
                  <span class="flex items-center gap-1 text-slate-500"><Cpu size={12} />CPU</span>
                  <span class="font-medium text-slate-700">{h.snapshot!.hardware?.cpuCores || 0} cores</span>
                </div>
                <p class="text-xs text-slate-400 truncate">{h.snapshot!.hardware?.cpuModel || 'Unknown'}</p>
              </div>

              <!-- RAM -->
              <div>
                <div class="mb-1 flex items-center justify-between text-xs">
                  <span class="flex items-center gap-1 text-slate-500"><MemoryStick size={12} />RAM</span>
                  <span class="font-medium text-slate-700">{h.ramUsage !== null ? `${Math.round(h.ramUsage)}%` : '—'}</span>
                </div>
                <ProgressBar value={h.ramUsage ?? 0} variant={ramVariant(h.ramUsage)} />
                <p class="mt-1 text-xs text-slate-400">
                  {h.snapshot!.hardware?.ramUsedMb ? formatRAM(h.snapshot!.hardware.ramUsedMb) : '—'} / {h.snapshot!.hardware?.ramTotalMb ? formatRAM(h.snapshot!.hardware.ramTotalMb) : '—'}
                </p>
              </div>

              <!-- Disk -->
              <div>
                <div class="mb-1 flex items-center justify-between text-xs">
                  <span class="flex items-center gap-1 text-slate-500"><HardDrive size={12} />Disk</span>
                  <span class="font-medium text-slate-700">{h.diskUsage !== null ? `${Math.round(h.diskUsage)}%` : '—'}</span>
                </div>
                <ProgressBar value={h.diskUsage ?? 0} variant={diskVariant(h.diskWarning, h.diskCritical)} />
                <p class="mt-1 text-xs text-slate-400">
                  {h.snapshot!.hardware?.diskUsedGb ? formatGB(h.snapshot!.hardware.diskUsedGb) : '—'} / {h.snapshot!.hardware?.diskTotalGb ? formatGB(h.snapshot!.hardware.diskTotalGb) : '—'}
                </p>
              </div>

              <!-- Partitions with warnings -->
              {#if h.snapshot!.diskUsage && h.snapshot!.diskUsage.filter(p => p.usePercent > 75).length > 0}
                <div class="space-y-1.5 border-t border-slate-100 pt-2">
                  {#each h.snapshot!.diskUsage.filter(p => p.usePercent > 75) as part}
                    <div class="flex items-center justify-between text-xs">
                      <span class="truncate text-slate-600">{part.mountPoint}</span>
                      <Badge variant={part.usePercent > 90 ? 'error' : 'warning'} size="sm">{Math.round(part.usePercent)}%</Badge>
                    </div>
                  {/each}
                </div>
              {/if}

              <!-- Last scan -->
              <div class="flex items-center gap-1 text-xs text-slate-400">
                <Clock size={12} />
                <span>Scanned {getSnapshotAge(h.snapshot)}</span>
              </div>
            </div>
          {/if}
        </Card>
      {/each}
    </div>
  {/if}
</div>

{#snippet emptyIcon()}<Activity size={22} />{/snippet}
