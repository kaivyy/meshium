<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import {
    AlertCircle, AlertTriangle, RefreshCw, Server as ServerIcon,
    HardDrive, Container, Database, Clock, CheckCircle2, XCircle
  } from 'lucide-svelte';
  import { api } from '$lib/api/client';
  import { discoveryApi, type ServerSnapshot, type DiskPartition } from '$lib/api/discovery';
  import { type Server } from '$lib/stores/servers';
  import { Badge, Card, EmptyState, PageHeader, Skeleton, Spinner } from '$lib/components/ui';
  import { formatRelativeTime } from '$lib/utils/format';
  import { toast } from '$lib/stores/toast';

  type AlertSeverity = 'critical' | 'warning' | 'info';

  interface Alert {
    id: string;
    severity: AlertSeverity;
    category: string;
    title: string;
    message: string;
    serverId?: number;
    serverName?: string;
    timestamp: string;
  }

  let servers = $state([] as Server[]);
  let snapshots = $state({} as Record<number, ServerSnapshot | null>);
  let jobs = $state([] as Array<{ id: string; type: string; status: string; createdAt: string; error?: string }>);
  let migrations = $state([] as Array<{ id: number; status: string; createdAt: string; error?: string }>);
  let loading = $state(true);
  let filterSeverity = $state('all');

  const allAlerts = $derived.by(() => {
    const alerts: Alert[] = [];

    // Disk usage alerts from snapshots
    servers.forEach(s => {
      const snap = snapshots[s.id];
      if (!snap) return;

      // Check disk partitions
      if (snap.diskUsage) {
        snap.diskUsage.forEach((p: DiskPartition) => {
          if (p.usePercent > 90) {
            alerts.push({
              id: `disk-${s.id}-${p.mountPoint}`,
              severity: 'critical',
              category: 'Disk',
              title: `Disk critically full on ${s.name}`,
              message: `Partition ${p.mountPoint} (${p.filesystem}) is at ${Math.round(p.usePercent)}% — ${p.availGb.toFixed(1)} GB remaining`,
              serverId: s.id,
              serverName: s.name,
              timestamp: snap.capturedAt,
            });
          } else if (p.usePercent > 75) {
            alerts.push({
              id: `disk-${s.id}-${p.mountPoint}`,
              severity: 'warning',
              category: 'Disk',
              title: `Disk space low on ${s.name}`,
              message: `Partition ${p.mountPoint} (${p.filesystem}) is at ${Math.round(p.usePercent)}% — ${p.availGb.toFixed(1)} GB remaining`,
              serverId: s.id,
              serverName: s.name,
              timestamp: snap.capturedAt,
            });
          }
        });
      }

      // Check RAM usage
      if (snap.hardware?.ramTotalMb && snap.hardware?.ramUsedMb) {
        const ramPct = (snap.hardware.ramUsedMb / snap.hardware.ramTotalMb) * 100;
        if (ramPct > 90) {
          alerts.push({
            id: `ram-${s.id}`,
            severity: 'critical',
            category: 'Memory',
            title: `High memory usage on ${s.name}`,
            message: `RAM is at ${Math.round(ramPct)}% (${snap.hardware.ramUsedMb} MB / ${snap.hardware.ramTotalMb} MB)`,
            serverId: s.id,
            serverName: s.name,
            timestamp: snap.capturedAt,
          });
        } else if (ramPct > 75) {
          alerts.push({
            id: `ram-${s.id}`,
            severity: 'warning',
            category: 'Memory',
            title: `Elevated memory usage on ${s.name}`,
            message: `RAM is at ${Math.round(ramPct)}% (${snap.hardware.ramUsedMb} MB / ${snap.hardware.ramTotalMb} MB)`,
            serverId: s.id,
            serverName: s.name,
            timestamp: snap.capturedAt,
          });
        }
      }

      // Check stopped Docker containers
      if (snap.docker?.containers) {
        const stopped = snap.docker.containers.filter(c => c.state === 'exited' || c.state === 'dead');
        stopped.forEach(c => {
          alerts.push({
            id: `container-${s.id}-${c.name}`,
            severity: 'warning',
            category: 'Docker',
            title: `Container stopped on ${s.name}`,
            message: `Container "${c.name}" (image: ${c.image}) is in ${c.state} state`,
            serverId: s.id,
            serverName: s.name,
            timestamp: snap.capturedAt,
          });
        });
      }

      // Check collection errors
      if (snap.collectionErrors && snap.collectionErrors.length > 0) {
        snap.collectionErrors.forEach(e => {
          alerts.push({
            id: `collector-${s.id}-${e.collector}`,
            severity: 'warning',
            category: 'Discovery',
            title: `Collection error on ${s.name}`,
            message: `Collector "${e.collector}" failed: ${e.error}`,
            serverId: s.id,
            serverName: s.name,
            timestamp: snap.capturedAt,
          });
        });
      }

      // Check SSL cert expiry
      if (snap.nginx?.sslCerts) {
        snap.nginx.sslCerts.forEach(cert => {
          if (cert.daysRemaining <= 0) {
            alerts.push({
              id: `ssl-${s.id}-${cert.domain}`,
              severity: 'critical',
              category: 'SSL',
              title: `SSL certificate expired on ${s.name}`,
              message: `Certificate for ${cert.domain} expired ${Math.abs(cert.daysRemaining)} days ago`,
              serverId: s.id,
              serverName: s.name,
              timestamp: snap.capturedAt,
            });
          } else if (cert.daysRemaining <= 30) {
            alerts.push({
              id: `ssl-${s.id}-${cert.domain}`,
              severity: 'warning',
              category: 'SSL',
              title: `SSL certificate expiring soon on ${s.name}`,
              message: `Certificate for ${cert.domain} expires in ${cert.daysRemaining} days`,
              serverId: s.id,
              serverName: s.name,
              timestamp: snap.capturedAt,
            });
          }
        });
      }
    });

    // Failed jobs
    jobs.forEach(j => {
      if (j.status === 'failed') {
        alerts.push({
          id: `job-${j.id}`,
          severity: 'critical',
          category: 'Jobs',
          title: `Job failed: ${j.type}`,
          message: j.error || `Job ${j.id} (${j.type}) failed`,
          timestamp: j.createdAt,
        });
      }
    });

    // Failed migrations
    migrations.forEach(m => {
      if (m.status === 'failed' || m.status === 'rollback_failed') {
        alerts.push({
          id: `migration-${m.id}`,
          severity: 'critical',
          category: 'Migrations',
          title: `Migration ${m.id} ${m.status === 'rollback_failed' ? 'rollback failed' : 'failed'}`,
          message: m.error || `Migration ${m.id} is in ${m.status} state`,
          timestamp: m.createdAt,
        });
      }
    });

    // Sort by severity (critical first) then by timestamp (newest first)
    return alerts.sort((a, b) => {
      if (a.severity === 'critical' && b.severity !== 'critical') return -1;
      if (a.severity !== 'critical' && b.severity === 'critical') return 1;
      return new Date(b.timestamp).getTime() - new Date(a.timestamp).getTime();
    });
  });

  const filteredAlerts = $derived.by(() => {
    if (filterSeverity === 'all') return allAlerts;
    return allAlerts.filter(a => a.severity === filterSeverity);
  });

  const alertStats = $derived.by(() => {
    let critical = 0, warning = 0, info = 0;
    allAlerts.forEach(a => {
      if (a.severity === 'critical') critical++;
      else if (a.severity === 'warning') warning++;
      else info++;
    });
    return { critical, warning, info, total: allAlerts.length };
  });

  onMount(async () => {
    await loadAll();
  });

  async function loadAll() {
    loading = true;
    try {
      const [serverData, jobData, migData] = await Promise.all([
        api.get('/servers') as Promise<Server[]>,
        api.get('/jobs?limit=50') as Promise<{ jobs: Array<{ id: string; type: string; status: string; createdAt: string; error?: string }> }>,
        api.get('/migrations') as Promise<Array<{ id: number; status: string; createdAt: string; error?: string }>>,
      ]);
      servers = serverData;
      jobs = jobData?.jobs || [];
      migrations = migData || [];
      await Promise.all(serverData.map(s => loadSnapshot(s.id)));
    } catch {
      toast.error('Failed to load alerts');
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

  function severityVariant(s: AlertSeverity): 'error' | 'warning' | 'neutral' {
    if (s === 'critical') return 'error';
    if (s === 'warning') return 'warning';
    return 'neutral';
  }

  function severityIcon(s: AlertSeverity) {
    if (s === 'critical') return AlertCircle;
    if (s === 'warning') return AlertTriangle;
    return CheckCircle2;
  }

  function categoryIcon(cat: string) {
    const map: Record<string, typeof HardDrive> = {
      'Disk': HardDrive,
      'Memory': HardDrive,
      'Docker': Container,
      'SSL': AlertCircle,
      'Discovery': ServerIcon,
      'Jobs': Clock,
      'Migrations': ServerIcon,
    };
    return map[cat] || AlertCircle;
  }
</script>

<svelte:head><title>Alerts - Meshium</title></svelte:head>

<div class="p-4 sm:p-6 max-w-7xl mx-auto">
  <PageHeader title="Alerts" subtitle="Infrastructure alerts derived from server snapshots, jobs, and migrations.">
    {#snippet actions()}
      <button type="button" onclick={loadAll} disabled={loading} class="inline-flex items-center gap-2 rounded-lg border border-slate-300 bg-white px-4 py-2 text-sm font-medium text-slate-700 hover:bg-slate-50 disabled:opacity-60">
        {#if loading}<Spinner size="sm" label="Refreshing" />{:else}<RefreshCw size={16} />{/if}
        Refresh
      </button>
    {/snippet}
  </PageHeader>

  <!-- Stats -->
  <div class="mb-6 grid gap-4 sm:grid-cols-3">
    <Card padding="lg">
      <div class="flex items-center gap-3">
        <div class="flex h-10 w-10 items-center justify-center rounded-lg bg-red-50 text-red-600"><AlertCircle size={20} /></div>
        <div>
          <p class="text-2xl font-bold text-slate-900">{alertStats.critical}</p>
          <p class="text-xs text-slate-500">Critical</p>
        </div>
      </div>
    </Card>
    <Card padding="lg">
      <div class="flex items-center gap-3">
        <div class="flex h-10 w-10 items-center justify-center rounded-lg bg-yellow-50 text-yellow-600"><AlertTriangle size={20} /></div>
        <div>
          <p class="text-2xl font-bold text-slate-900">{alertStats.warning}</p>
          <p class="text-xs text-slate-500">Warnings</p>
        </div>
      </div>
    </Card>
    <Card padding="lg">
      <div class="flex items-center gap-3">
        <div class="flex h-10 w-10 items-center justify-center rounded-lg bg-green-50 text-green-600"><CheckCircle2 size={20} /></div>
        <div>
          <p class="text-2xl font-bold text-slate-900">{alertStats.total === 0 ? 'All Clear' : alertStats.total}</p>
          <p class="text-xs text-slate-500">{alertStats.total === 0 ? 'No alerts' : 'Total Alerts'}</p>
        </div>
      </div>
    </Card>
  </div>

  <!-- Filter -->
  <div class="mb-4 flex gap-2">
    {#each ['all', 'critical', 'warning'] as sev}
      <button type="button" onclick={() => filterSeverity = sev} class={`rounded-lg border px-4 py-2 text-sm font-medium transition ${filterSeverity === sev ? 'border-slate-300 bg-white text-slate-900 shadow-sm' : 'border-transparent text-slate-500 hover:bg-slate-50'}`}>
        {sev === 'all' ? 'All' : sev === 'critical' ? 'Critical' : 'Warnings'}
      </button>
    {/each}
  </div>

  <!-- Alert list -->
  {#if loading && servers.length === 0}
    <div class="space-y-3">
      {#each Array(5) as _}
        <Card><div class="flex items-center gap-4"><Skeleton width="40px" /><Skeleton width="60%" /><Skeleton width="100px" /></div></Card>
      {/each}
    </div>
  {:else if filteredAlerts.length === 0}
    <EmptyState title="No alerts" description={alertStats.total === 0 ? "All systems healthy. No alerts detected." : "No alerts match your filter."} icon={emptyIcon} />
  {:else}
    <div class="space-y-3">
      {#each filteredAlerts as alert (alert.id)}
        {@const Icon = severityIcon(alert.severity)}
        <Card padding="lg" hoverable>
          <div class="flex items-start gap-4">
            <div class={`flex h-10 w-10 shrink-0 items-center justify-center rounded-lg ${alert.severity === 'critical' ? 'bg-red-50 text-red-600' : 'bg-yellow-50 text-yellow-600'}`}>
              <Icon size={20} />
            </div>
            <div class="min-w-0 flex-1">
              <div class="flex items-center gap-2">
                <h3 class="text-sm font-semibold text-slate-900">{alert.title}</h3>
                <Badge variant={severityVariant(alert.severity)} size="sm">{alert.severity}</Badge>
                <Badge variant="neutral" size="sm">{alert.category}</Badge>
              </div>
              <p class="mt-1 text-sm text-slate-600">{alert.message}</p>
              <div class="mt-2 flex items-center gap-3 text-xs text-slate-400">
                {#if alert.serverName}
                  <button type="button" onclick={() => goto(`/servers/${alert.serverId}`)} class="hover:text-blue-600 hover:underline">{alert.serverName} →</button>
                {/if}
                <span>{formatRelativeTime(alert.timestamp)}</span>
              </div>
            </div>
          </div>
        </Card>
      {/each}
    </div>
  {/if}
</div>

{#snippet emptyIcon()}<CheckCircle2 size={22} />{/snippet}
