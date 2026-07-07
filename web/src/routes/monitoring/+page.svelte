<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import {
    Activity,
    Clock,
    Cpu,
    HardDrive,
    Pause,
    Play,
    RefreshCw,
    Server as ServerIcon,
    Wifi,
    WifiOff,
  } from 'lucide-svelte';
  import { api } from '$lib/api/client';
  import { type ProcessInfo, type ServerMetrics } from '$lib/api/metrics';
  import { wsConnectGeneric, wsURL } from '$lib/api/websocket';
  import { Badge, Card, EmptyState, PageHeader, Skeleton, Spinner } from '$lib/components/ui';
  import { formatRelativeTime } from '$lib/utils/format';
  import { toast } from '$lib/stores/toast';
  import { invalidateAll, loadSnapshots, snapshotsStore } from '$lib/stores/snapshots';
  import { type Server } from '$lib/stores/servers';

  type StreamStatus = 'idle' | 'connecting' | 'connected' | 'error';
  type BadgeVariant = 'success' | 'warning' | 'error' | 'neutral' | 'info';

  interface NetworkRate {
    rxPerSec: number;
    txPerSec: number;
  }

  let servers = $state([] as Server[]);
  let selectedServerId = $state<number | null>(null);
  let loadingServers = $state(true);
  let loadingMetrics = $state(false);
  let topProcessesLoading = $state(false);
  let streamingEnabled = $state(true);
  let selectedInterval = $state(5);
  let streamStatus = $state<StreamStatus>('idle');
  let streamEndpoint = $state('');
  let lastUpdated = $state<Date | null>(null);
  let metrics = $state<ServerMetrics | null>(null);
  let topProcesses = $state<ProcessInfo[]>([]);
  let history = $state<ServerMetrics[]>([]);
  let wsConnection: WebSocket | null = null;
  let wsGeneration = 0;

  const historyWindow = $derived.by(() => history.slice(-30));
  const selectedServer = $derived.by(() => servers.find((server) => server.id === selectedServerId) ?? null);
  const selectedSnapshot = $derived.by(() => {
    if (!selectedServerId) {
      return null;
    }
    return $snapshotsStore[selectedServerId] ?? null;
  });
  const networkRates = $derived.by(() => computeNetworkRates(historyWindow));
  const networkPeak = $derived.by(() => {
    let peak = 0;
    for (const rate of Object.values(networkRates)) {
      peak = Math.max(peak, rate.rxPerSec, rate.txPerSec);
    }
    return peak;
  });

  function ratePercent(bytesPerSec: number | undefined): number {
    if (!bytesPerSec || networkPeak <= 0) {
      return 0;
    }
    return Math.max(2, Math.min(100, (bytesPerSec / networkPeak) * 100));
  }
  const cpuSparkline = $derived.by(() => buildSparkline(historyWindow.map((sample) => sample.cpu.usage)));
  const memorySparkline = $derived.by(() => buildSparkline(historyWindow.map((sample) => sample.memory.usagePercent)));
  const streamBadgeVariant = $derived.by((): BadgeVariant => {
    if (streamStatus === 'connected') return 'success';
    if (streamStatus === 'connecting') return 'warning';
    if (streamStatus === 'error') return 'error';
    return 'neutral';
  });
  const streamLabel = $derived.by(() => {
    if (streamStatus === 'connected') return 'Live';
    if (streamStatus === 'connecting') return 'Connecting';
    if (streamStatus === 'error') return 'Disconnected';
    return 'Paused';
  });

  onMount(async () => {
    await loadServers();
  });

  onDestroy(() => {
    stopStreaming(true);
  });

  async function loadServers() {
    loadingServers = true;
    try {
      const data = (await api.get('/servers')) as Server[];
      servers = data;
      await invalidateAll();
      await loadSnapshots(data.map((server) => server.id));

      if (data.length > 0) {
        const stillValid = selectedServerId !== null && data.some((server) => server.id === selectedServerId);
        const nextServerId = stillValid && selectedServerId !== null ? selectedServerId : data[0].id;
        selectedServerId = nextServerId;
        await loadMetricsBundle(nextServerId);
        if (streamingEnabled) {
          openStreaming();
        }
      }
    } catch (error) {
      console.error(error);
      toast.error('Failed to load servers');
    } finally {
      loadingServers = false;
    }
  }

  async function loadMetricsBundle(serverId: number | null = selectedServerId) {
    if (!serverId) {
      return;
    }

    loadingMetrics = true;
    try {
      const [currentMetrics, processes, historyData] = await Promise.all([
        api.get(`/servers/${serverId}/metrics`) as Promise<ServerMetrics>,
        api.get(`/servers/${serverId}/metrics/top?limit=10`) as Promise<ProcessInfo[]>,
        api.get(`/servers/${serverId}/metrics/history?points=30`) as Promise<ServerMetrics[]>,
      ]);

      metrics = currentMetrics;
      topProcesses = processes;
      history = historyData.length > 0 ? historyData.slice(-30) : [currentMetrics];
      lastUpdated = new Date(currentMetrics.timestamp * 1000);
      streamStatus = streamingEnabled ? 'connecting' : 'idle';
    } catch (error) {
      console.error(error);
      toast.error('Failed to load monitoring data');
    } finally {
      loadingMetrics = false;
    }
  }

  async function refreshTopProcesses() {
    if (!selectedServerId || topProcessesLoading) {
      return;
    }

    topProcessesLoading = true;
    try {
      topProcesses = (await api.get(`/servers/${selectedServerId}/metrics/top?limit=10`)) as ProcessInfo[];
    } catch (error) {
      console.error(error);
    } finally {
      topProcessesLoading = false;
    }
  }

  function handleServerChange(event: Event) {
    const target = event.currentTarget as HTMLSelectElement;
    const value = Number(target.value);
    if (!Number.isFinite(value)) {
      return;
    }

    selectedServerId = value;
    history = [];
    stopStreaming(false);

    void (async () => {
      await loadMetricsBundle(value);
      if (streamingEnabled) {
        openStreaming();
      }
    })();
  }

  function handleIntervalChange(event: Event) {
    const target = event.currentTarget as HTMLSelectElement;
    const value = Number(target.value);
    if (!Number.isFinite(value) || value <= 0) {
      return;
    }

    selectedInterval = value;
    if (streamingEnabled) {
      openStreaming();
    }
  }

  function toggleStreaming() {
    if (!selectedServerId) {
      return;
    }

    streamingEnabled = !streamingEnabled;
    if (streamingEnabled) {
      openStreaming();
      return;
    }

    stopStreaming(true);
  }

  function refreshDashboard() {
    if (!selectedServerId) {
      return;
    }

    void loadMetricsBundle(selectedServerId);
  }

  function stopStreaming(resetToggle = false) {
    wsGeneration++;

    if (wsConnection) {
      wsConnection.close();
      wsConnection = null;
    }

    streamEndpoint = '';
    streamStatus = 'idle';
    if (resetToggle) {
      streamingEnabled = false;
    }
  }

  function openStreaming() {
    if (!selectedServerId) {
      return;
    }

    stopStreaming(false);

    const path = `/ws/monitoring/${selectedServerId}?interval=${selectedInterval}`;
    streamEndpoint = displayMonitoringEndpoint(path);
    streamStatus = 'connecting';

    const generation = ++wsGeneration;
    wsConnection = wsConnectGeneric(
      path,
      (message) => {
        if (generation !== wsGeneration) {
          return;
        }
        handleSocketMessage(message as unknown);
      },
      () => {
        if (generation !== wsGeneration) {
          return;
        }
        if (streamingEnabled) {
          streamStatus = 'error';
        }
      },
      () => {
        if (generation !== wsGeneration) {
          return;
        }
        streamStatus = streamingEnabled ? 'error' : 'idle';
        wsConnection = null;
      },
    );
  }

  function handleSocketMessage(message: unknown) {
    if (isMonitoringErrorMessage(message)) {
      toast.error(message.message);
      streamStatus = 'error';
      return;
    }

    if (!isServerMetrics(message)) {
      return;
    }

    metrics = message;
    lastUpdated = new Date(message.timestamp * 1000);
    history = [...history, message].slice(-60);
    streamStatus = 'connected';
    void refreshTopProcesses();
  }

  function isMonitoringErrorMessage(message: unknown): message is { type: 'error'; message: string } {
    return (
      typeof message === 'object' &&
      message !== null &&
      (message as { type?: unknown }).type === 'error' &&
      typeof (message as { message?: unknown }).message === 'string'
    );
  }

  function isServerMetrics(message: unknown): message is ServerMetrics {
    if (typeof message !== 'object' || message === null) {
      return false;
    }

    const sample = message as Record<string, unknown>;
    return (
      typeof sample.timestamp === 'number' &&
      typeof sample.cpu === 'object' &&
      sample.cpu !== null &&
      typeof sample.memory === 'object' &&
      sample.memory !== null &&
      Array.isArray(sample.disk) &&
      Array.isArray(sample.network)
    );
  }

  function displayMonitoringEndpoint(path: string) {
    const url = new URL(wsURL(path));
    url.searchParams.delete('token');
    return `${url.pathname}${url.search}`;
  }

  function gaugeVariant(value: number, warning = 75, critical = 90): BadgeVariant {
    if (value >= critical) return 'error';
    if (value >= warning) return 'warning';
    return 'success';
  }

  function loadVariant(load: number, cores: number): BadgeVariant {
    if (!cores || cores <= 0) return load >= 5 ? 'error' : load >= 2 ? 'warning' : 'success';
    if (load >= cores * 1.5) return 'error';
    if (load >= cores) return 'warning';
    return 'success';
  }

  function temperatureVariant(value: number): BadgeVariant {
    if (value >= 80) return 'error';
    if (value >= 65) return 'warning';
    return 'success';
  }

  function formatBytes(bytes: number): string {
    if (!Number.isFinite(bytes) || bytes <= 0) {
      return '0 B';
    }

    const units = ['B', 'KB', 'MB', 'GB', 'TB'];
    let value = bytes;
    let unit = 0;
    while (value >= 1024 && unit < units.length - 1) {
      value /= 1024;
      unit += 1;
    }
    return `${value.toFixed(value >= 100 ? 0 : value >= 10 ? 1 : 2)} ${units[unit]}`;
  }

  function formatRate(bytesPerSecond: number): string {
    return `${formatBytes(bytesPerSecond)}/s`;
  }

  function formatPercentage(value: number): string {
    return `${Math.max(0, Math.min(100, value)).toFixed(1)}%`;
  }

  function formatTemperature(value: number | undefined): string {
    if (typeof value !== 'number' || !Number.isFinite(value) || value <= 0) {
      return '—';
    }
    return `${value.toFixed(1)}°C`;
  }

  function formatUptime(seconds: number): string {
    if (!Number.isFinite(seconds) || seconds <= 0) {
      return '—';
    }

    const days = Math.floor(seconds / 86400);
    const hours = Math.floor((seconds % 86400) / 3600);
    const minutes = Math.floor((seconds % 3600) / 60);
    const parts: string[] = [];

    if (days > 0) parts.push(`${days}d`);
    if (hours > 0 || parts.length > 0) parts.push(`${hours}h`);
    parts.push(`${minutes}m`);
    return parts.join(' ');
  }

  function formatLoad(load: number): string {
    if (!Number.isFinite(load)) {
      return '—';
    }
    return load.toFixed(2);
  }

  function formatNumber(value: number): string {
    if (!Number.isFinite(value)) {
      return '—';
    }
    return new Intl.NumberFormat().format(value);
  }

  function buildSparkline(values: number[], width = 360, height = 120): string {
    if (values.length < 2) {
      return '';
    }

    const validValues = values.filter((value) => Number.isFinite(value));
    if (validValues.length < 2) {
      return '';
    }

    const min = Math.min(...validValues);
    const max = Math.max(...validValues);
    const spread = max - min || 1;
    const points = validValues.map((value, index) => {
      const x = (index / (validValues.length - 1)) * width;
      const normalized = (value - min) / spread;
      const y = height - normalized * (height - 8) - 4;
      return `${x.toFixed(1)},${y.toFixed(1)}`;
    });

    return `M ${points.join(' L ')}`;
  }

  function computeNetworkRates(samples: ServerMetrics[]): Record<string, NetworkRate> {
    const rates: Record<string, NetworkRate> = {};
    if (samples.length < 2) {
      return rates;
    }

    const current = samples[samples.length - 1];
    const previous = samples[samples.length - 2];
    const elapsed = Math.max((current.timestamp - previous.timestamp) || 1, 1);

    const currentNetwork = Array.isArray(current.network) ? current.network : [];
    const previousNetwork = Array.isArray(previous.network) ? previous.network : [];

    for (const iface of currentNetwork) {
      const before = previousNetwork.find((entry) => entry.interface === iface.interface);
      if (!before) {
        continue;
      }

      rates[iface.interface] = {
        rxPerSec: Math.max(0, iface.rxBytes - before.rxBytes) / elapsed,
        txPerSec: Math.max(0, iface.txBytes - before.txBytes) / elapsed,
      };
    }

    return rates;
  }

  function processCountLabel(count: number): string {
    if (!Number.isFinite(count) || count <= 0) {
      return '—';
    }
    return formatNumber(count);
  }

  function hasNetworkData() {
    return Boolean(metrics && Array.isArray(metrics.network) && metrics.network.length > 0);
  }
</script>

{#snippet streamHeaderAction()}
  {#if selectedServer}
    <Badge variant={streamBadgeVariant}>
      <span class="inline-flex items-center gap-1.5">
        <span class={`h-2 w-2 rounded-full ${streamStatus === 'connected' ? 'bg-success animate-pulse' : streamStatus === 'connecting' ? 'bg-warning animate-pulse' : streamStatus === 'error' ? 'bg-error' : 'bg-fg-subtle'}`}></span>
        {streamLabel}
      </span>
    </Badge>
  {/if}
{/snippet}

{#snippet emptyServersIcon()}
  <ServerIcon size={20} />
{/snippet}

<svelte:head>
  <title>Monitoring</title>
  <meta
    name="description"
    content="Live server monitoring with CPU, memory, disk, network, and process insights."
  />
</svelte:head>

<div class="mx-auto max-w-7xl px-4 py-6 sm:px-6 lg:px-8">
  <PageHeader
    title="Monitoring"
    subtitle="Live server metrics, WebSocket streaming, and historical snapshots in one place."
    actions={streamHeaderAction}
  />

  {#if loadingServers && servers.length === 0}
    <div class="space-y-4">
      <Skeleton class="h-20 w-full" />
      <div class="grid gap-4 lg:grid-cols-3">
        <Skeleton class="h-40 w-full" />
        <Skeleton class="h-40 w-full" />
        <Skeleton class="h-40 w-full" />
      </div>
    </div>
  {:else if servers.length === 0}
    <EmptyState
      title="No servers available"
      description="Add a server to start collecting live metrics."
      icon={emptyServersIcon}
    />
  {:else}
    <div class="mb-4 grid gap-4 xl:grid-cols-[minmax(0,1.8fr)_minmax(0,1fr)]">
      <Card>
        <div class="flex flex-col gap-4 lg:flex-row lg:items-end lg:justify-between">
          <div class="space-y-2">
            <div class="flex items-center gap-2 text-sm font-medium text-fg-muted">
              <ServerIcon size={16} />
              Server
            </div>
            <div class="flex flex-col gap-2 sm:flex-row sm:items-center">
              <select
                class="w-full min-w-0 rounded-lg border border-border bg-surface px-3 py-2 text-sm text-fg shadow-sm outline-none transition focus:border-accent sm:w-auto sm:min-w-[260px]"
                value={selectedServerId?.toString() ?? ''}
                onchange={handleServerChange}
              >
                <option value="" disabled selected={selectedServerId === null}>Select a server</option>
                {#each servers as server}
                  <option value={server.id}>{server.name} · {server.host}</option>
                {/each}
              </select>

              {#if selectedServer}
                <div class="flex flex-wrap items-center gap-2">
                  <Badge variant="neutral">{selectedServer.username}@{selectedServer.host}:{selectedServer.port}</Badge>
                  {#if selectedServer.environment}
                    <Badge variant="info">{selectedServer.environment}</Badge>
                  {/if}
                  {#if selectedServer.region}
                    <Badge variant="neutral">{selectedServer.region}</Badge>
                  {/if}
                </div>
              {/if}
            </div>
          </div>

          <div class="grid grid-cols-2 gap-2 sm:flex sm:flex-wrap sm:items-center">
            <button
              type="button"
              class="inline-flex items-center justify-center gap-2 rounded-lg border border-border bg-surface px-3 py-2 text-sm font-medium text-fg-muted transition hover:border-border-strong hover:bg-surface-muted"
              onclick={toggleStreaming}
              disabled={!selectedServerId}
            >
              {#if streamingEnabled}
                <Pause size={16} />
                Stop live
              {:else}
                <Play size={16} />
                Start live
              {/if}
            </button>

            <button
              type="button"
              class="inline-flex items-center justify-center gap-2 rounded-lg border border-border bg-surface px-3 py-2 text-sm font-medium text-fg-muted transition hover:border-border-strong hover:bg-surface-muted"
              onclick={refreshDashboard}
              disabled={!selectedServerId || loadingMetrics || topProcessesLoading}
            >
              {#if loadingMetrics || topProcessesLoading}
                <Spinner size="sm" label="Refreshing monitoring data" />
              {:else}
                <RefreshCw size={16} />
              {/if}
              Refresh
            </button>

            <select
              class="rounded-lg border border-border bg-surface px-3 py-2 text-sm text-fg-muted shadow-sm outline-none transition focus:border-accent"
              value={selectedInterval.toString()}
              onchange={handleIntervalChange}
              disabled={!selectedServerId}
            >
              <option value="1">1s</option>
              <option value="5">5s</option>
              <option value="10">10s</option>
              <option value="30">30s</option>
            </select>
          </div>
        </div>

        <div class="mt-4 flex flex-wrap items-center gap-3 text-xs text-fg-subtle">
          <Badge variant={streamBadgeVariant}>{streamLabel}</Badge>
          {#if streamEndpoint}
            <span class="font-mono">{streamEndpoint}</span>
          {/if}
          {#if lastUpdated}
            <span>Last updated {formatRelativeTime(lastUpdated.toISOString())}</span>
          {/if}
        </div>
      </Card>

      <Card>
        <div class="flex h-full flex-col justify-between gap-4">
          <div>
            <h2 class="text-sm font-semibold text-fg">Live history</h2>
            <p class="mt-1 text-sm text-fg-subtle">CPU and memory samples from the current session.</p>
          </div>

          {#if historyWindow.length > 1}
            <div class="rounded-xl border border-border bg-surface-muted p-3">
              <svg viewBox="0 0 360 120" class="h-28 w-full" preserveAspectRatio="none" aria-hidden="true">
                <path d={cpuSparkline} fill="none" stroke="currentColor" stroke-width="3" class="text-accent" />
                <path d={memorySparkline} fill="none" stroke="currentColor" stroke-width="3" class="text-success" />
                <line x1="0" y1="116" x2="360" y2="116" stroke="currentColor" stroke-width="1" class="text-border" />
              </svg>
              <div class="mt-2 flex items-center justify-between text-xs text-fg-subtle">
                <span><span class="inline-block h-2 w-2 rounded-full bg-accent"></span> CPU</span>
                <span><span class="inline-block h-2 w-2 rounded-full bg-success"></span> Memory</span>
                <span>{historyWindow.length} samples</span>
              </div>
            </div>
          {:else}
            <div class="rounded-xl border border-dashed border-border bg-surface-muted p-4 text-sm text-fg-subtle">
              Start live streaming to build a session history.
            </div>
          {/if}
        </div>
      </Card>
    </div>

    {#if loadingMetrics && !metrics}
      <div class="mb-6 grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <Skeleton class="h-40 w-full" />
        <Skeleton class="h-40 w-full" />
        <Skeleton class="h-40 w-full" />
        <Skeleton class="h-40 w-full" />
      </div>
    {:else if metrics}
      <div class="mb-6 grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <Card>
          <div class="flex items-start justify-between gap-4">
            <div>
              <p class="text-sm font-medium text-fg-subtle">CPU usage</p>
              <p class="mt-2 text-3xl font-bold text-fg">{metrics.cpu.usage.toFixed(1)}%</p>
              <p class="mt-1 text-sm text-fg-subtle">{metrics.cpu.cores} cores · {metrics.cpu.model || 'Unknown model'}</p>
            </div>
            <Badge variant={gaugeVariant(metrics.cpu.usage)}>{formatPercentage(metrics.cpu.usage)}</Badge>
          </div>

          <div class="mt-4 flex items-center justify-center">
            <svg viewBox="0 0 120 120" class="h-32 w-32">
              <defs>
                <linearGradient id="cpu-gauge" x1="0%" x2="100%" y1="0%" y2="100%">
                  <stop offset="0%" stop-color="currentColor" class={metrics.cpu.usage >= 90 ? 'text-error' : metrics.cpu.usage >= 75 ? 'text-warning' : 'text-success'} />
                  <stop offset="100%" stop-color="currentColor" class={metrics.cpu.usage >= 90 ? 'text-error' : metrics.cpu.usage >= 75 ? 'text-warning' : 'text-success'} />
                </linearGradient>
              </defs>
              <circle cx="60" cy="60" r="46" fill="none" stroke="currentColor" stroke-width="12" class="text-surface-muted" />
              <circle
                cx="60"
                cy="60"
                r="46"
                fill="none"
                stroke="url(#cpu-gauge)"
                stroke-width="12"
                stroke-linecap="round"
                transform="rotate(-90 60 60)"
                style={`stroke-dasharray: ${2 * Math.PI * 46 * (metrics.cpu.usage / 100)} ${2 * Math.PI * 46};`}
              />
              <text x="60" y="57" text-anchor="middle" class="fill-fg text-lg font-bold">{metrics.cpu.usage.toFixed(0)}%</text>
              <text x="60" y="74" text-anchor="middle" class="fill-fg-subtle text-[10px]">utilization</text>
            </svg>
          </div>
        </Card>

        <Card>
          <div class="flex items-start justify-between gap-4">
            <div>
              <p class="text-sm font-medium text-fg-subtle">Memory</p>
              <p class="mt-2 text-3xl font-bold text-fg">{metrics.memory.usagePercent.toFixed(1)}%</p>
              <p class="mt-1 text-sm text-fg-subtle">
                {formatBytes(metrics.memory.used * 1024 * 1024)} used of {formatBytes(metrics.memory.total * 1024 * 1024)}
              </p>
            </div>
            <Badge variant={gaugeVariant(metrics.memory.usagePercent)}>{formatPercentage(metrics.memory.usagePercent)}</Badge>
          </div>

          <div class="mt-4 h-3 overflow-hidden rounded-full bg-surface-muted">
            <div
              class={`h-full rounded-full ${metrics.memory.usagePercent >= 90 ? 'bg-error' : metrics.memory.usagePercent >= 75 ? 'bg-warning' : 'bg-success'}`}
              style={`width: ${Math.min(100, metrics.memory.usagePercent)}%;`}
            ></div>
          </div>

          <dl class="mt-4 grid grid-cols-2 gap-3 text-sm">
            <div>
              <dt class="text-fg-subtle">Free</dt>
              <dd class="font-medium text-fg">{formatBytes(metrics.memory.free * 1024 * 1024)}</dd>
            </div>
            <div>
              <dt class="text-fg-subtle">Available</dt>
              <dd class="font-medium text-fg">{formatBytes(metrics.memory.available * 1024 * 1024)}</dd>
            </div>
            <div>
              <dt class="text-fg-subtle">Swap</dt>
              <dd class="font-medium text-fg">{formatBytes(metrics.memory.swapUsed * 1024 * 1024)} / {formatBytes(metrics.memory.swapTotal * 1024 * 1024)}</dd>
            </div>
            <div>
              <dt class="text-fg-subtle">Cached</dt>
              <dd class="font-medium text-fg">{formatBytes(metrics.memory.cached * 1024 * 1024)}</dd>
            </div>
          </dl>
        </Card>

        <Card>
          <div class="flex items-start justify-between gap-4">
            <div>
              <p class="text-sm font-medium text-fg-subtle">System</p>
              <p class="mt-2 text-3xl font-bold text-fg">{formatUptime(metrics.uptime)}</p>
              <p class="mt-1 text-sm text-fg-subtle">{processCountLabel(metrics.processCount)} processes</p>
            </div>
            <div class="flex flex-col items-end gap-2">
              <Badge variant={temperatureVariant(metrics.temperature ?? 0)}>{formatTemperature(metrics.temperature)}</Badge>
              <Badge variant={loadVariant(metrics.load.load1, metrics.cpu.cores)}>{formatLoad(metrics.load.load1)} load</Badge>
            </div>
          </div>

          <div class="mt-4 grid grid-cols-3 gap-2 text-center text-sm">
            <div class="rounded-lg bg-surface-muted px-3 py-2">
              <p class="text-xs text-fg-subtle">1m</p>
              <p class="font-semibold text-fg">{formatLoad(metrics.load.load1)}</p>
            </div>
            <div class="rounded-lg bg-surface-muted px-3 py-2">
              <p class="text-xs text-fg-subtle">5m</p>
              <p class="font-semibold text-fg">{formatLoad(metrics.load.load5)}</p>
            </div>
            <div class="rounded-lg bg-surface-muted px-3 py-2">
              <p class="text-xs text-fg-subtle">15m</p>
              <p class="font-semibold text-fg">{formatLoad(metrics.load.load15)}</p>
            </div>
          </div>
        </Card>

        <Card>
          <div class="flex items-start justify-between gap-4">
            <div>
              <p class="text-sm font-medium text-fg-subtle">Disk</p>
              <p class="mt-2 text-3xl font-bold text-fg">{metrics.disk.length}</p>
              <p class="mt-1 text-sm text-fg-subtle">Mounted filesystems</p>
            </div>
            <HardDrive class="text-fg-subtle" size={24} />
          </div>

          <div class="mt-4 space-y-3">
            {#each metrics.disk.slice(0, 3) as partition}
              <div>
                <div class="mb-1 flex items-center justify-between text-xs text-fg-subtle">
                  <span class="truncate">{partition.mount}</span>
                  <span>{formatPercentage(partition.usagePercent)}</span>
                </div>
                <div class="h-2 overflow-hidden rounded-full bg-surface-muted">
                  <div
                    class={`h-full rounded-full ${partition.usagePercent >= 90 ? 'bg-error' : partition.usagePercent >= 75 ? 'bg-warning' : 'bg-success'}`}
                    style={`width: ${Math.min(100, partition.usagePercent)}%;`}
                  ></div>
                </div>
              </div>
            {/each}
          </div>
        </Card>
      </div>
    {/if}

    <div class="grid gap-6 xl:grid-cols-[minmax(0,1.4fr)_minmax(0,1fr)]">
      <div class="space-y-6">
        <Card>
          <div class="flex items-center justify-between gap-4">
            <div>
              <h2 class="text-base font-semibold text-fg">Network interfaces</h2>
              <p class="mt-1 text-sm text-fg-subtle">Live RX/TX counters and estimated transfer rates.</p>
            </div>
            <div class="flex items-center gap-2 text-sm text-fg-subtle">
              {#if hasNetworkData()}
                <Wifi size={16} class="text-success" />
              {:else}
                <WifiOff size={16} class="text-fg-subtle" />
              {/if}
            </div>
          </div>

          {#if metrics && Array.isArray(metrics.network) && metrics.network.length > 0}
            <div class="mt-4 grid gap-3 md:grid-cols-2">
              {#each metrics.network as iface}
                <div class="rounded-xl border border-border bg-surface-muted p-4">
                  <div class="flex items-start justify-between gap-3">
                    <div class="min-w-0">
                      <p class="truncate font-medium text-fg">{iface.interface}</p>
                      <p class="mt-1 text-xs text-fg-subtle">{formatBytes(iface.rxBytes)} RX · {formatBytes(iface.txBytes)} TX</p>
                    </div>
                    <span class="shrink-0">
                      <Badge variant="info">
                        {networkRates[iface.interface] ? `${formatRate(networkRates[iface.interface].rxPerSec)} · ${formatRate(networkRates[iface.interface].txPerSec)}` : 'warming up'}
                      </Badge>
                    </span>
                  </div>

                  <div class="mt-3 space-y-2 text-sm">
                    <div>
                      <div class="mb-1 flex items-center justify-between text-xs text-fg-subtle">
                        <span>RX</span>
                        <span>{networkRates[iface.interface] ? formatRate(networkRates[iface.interface].rxPerSec) : '—'}</span>
                      </div>
                      <div class="h-2 overflow-hidden rounded-full bg-surface-muted">
                        <div class="h-2 rounded-full bg-accent transition-all duration-500" style={`width: ${ratePercent(networkRates[iface.interface]?.rxPerSec)}%;`}></div>
                      </div>
                    </div>
                    <div>
                      <div class="mb-1 flex items-center justify-between text-xs text-fg-subtle">
                        <span>TX</span>
                        <span>{networkRates[iface.interface] ? formatRate(networkRates[iface.interface].txPerSec) : '—'}</span>
                      </div>
                      <div class="h-2 overflow-hidden rounded-full bg-surface-muted">
                        <div class="h-2 rounded-full bg-success transition-all duration-500" style={`width: ${ratePercent(networkRates[iface.interface]?.txPerSec)}%;`}></div>
                      </div>
                    </div>
                  </div>
                </div>
              {/each}
            </div>
          {:else}
            <div class="mt-4 rounded-xl border border-dashed border-border bg-surface-muted p-4 text-sm text-fg-subtle">
              No network interface data available.
            </div>
          {/if}
        </Card>

        <Card>
          <div class="flex items-center justify-between gap-4">
            <div>
              <h2 class="text-base font-semibold text-fg">Historical snapshot</h2>
              <p class="mt-1 text-sm text-fg-subtle">Latest discovery scan, if available, shown as a baseline.</p>
            </div>
            <Badge variant={selectedSnapshot ? 'info' : 'neutral'}>
              {selectedSnapshot ? formatRelativeTime(selectedSnapshot.capturedAt) : 'No snapshot'}
            </Badge>
          </div>

          {#if selectedSnapshot}
            <div class="mt-4 grid gap-4 lg:grid-cols-2">
              <div class="rounded-xl border border-border bg-surface-muted p-4">
                <div class="flex items-center gap-2 text-sm font-medium text-fg-muted">
                  <Cpu size={16} />
                  Hardware
                </div>
                <dl class="mt-3 grid grid-cols-2 gap-3 text-sm">
                  <div>
                    <dt class="text-fg-subtle">CPU</dt>
                    <dd class="font-medium text-fg">{selectedSnapshot.hardware.cpuModel}</dd>
                  </div>
                  <div>
                    <dt class="text-fg-subtle">Cores</dt>
                    <dd class="font-medium text-fg">{selectedSnapshot.hardware.cpuCores}</dd>
                  </div>
                  <div>
                    <dt class="text-fg-subtle">RAM</dt>
                    <dd class="font-medium text-fg">{selectedSnapshot.hardware.ramUsedMb} / {selectedSnapshot.hardware.ramTotalMb} MB</dd>
                  </div>
                  <div>
                    <dt class="text-fg-subtle">Disk</dt>
                    <dd class="font-medium text-fg">{selectedSnapshot.hardware.diskUsedGb.toFixed(1)} / {selectedSnapshot.hardware.diskTotalGb.toFixed(1)} GB</dd>
                  </div>
                </dl>
              </div>

              <div class="rounded-xl border border-border bg-surface-muted p-4">
                <div class="flex items-center gap-2 text-sm font-medium text-fg-muted">
                  <Clock size={16} />
                  Snapshot details
                </div>
                <dl class="mt-3 grid grid-cols-2 gap-3 text-sm">
                  <div>
                    <dt class="text-fg-subtle">Captured</dt>
                    <dd class="font-medium text-fg">{formatRelativeTime(selectedSnapshot.capturedAt)}</dd>
                  </div>
                  <div>
                    <dt class="text-fg-subtle">Hostname</dt>
                    <dd class="font-medium text-fg">{selectedSnapshot.os.hostname}</dd>
                  </div>
                  <div>
                    <dt class="text-fg-subtle">Kernel</dt>
                    <dd class="font-medium text-fg">{selectedSnapshot.os.kernel}</dd>
                  </div>
                  <div>
                    <dt class="text-fg-subtle">Uptime</dt>
                    <dd class="font-medium text-fg">{formatUptime(selectedSnapshot.os.uptimeSeconds)}</dd>
                  </div>
                </dl>
              </div>
            </div>

            {#if selectedSnapshot.diskUsage.length > 0}
              <div class="mt-4 space-y-3">
                {#each selectedSnapshot.diskUsage.slice(0, 4) as partition}
                  <div>
                    <div class="mb-1 flex items-center justify-between text-xs text-fg-subtle">
                      <span>{partition.mountPoint}</span>
                      <span>{partition.usePercent.toFixed(0)}%</span>
                    </div>
                    <div class="h-2 overflow-hidden rounded-full bg-surface-muted">
                      <div
                        class={`h-full rounded-full ${partition.usePercent >= 90 ? 'bg-error' : partition.usePercent >= 75 ? 'bg-warning' : 'bg-success'}`}
                        style={`width: ${Math.min(100, partition.usePercent)}%;`}
                      ></div>
                    </div>
                  </div>
                {/each}
              </div>
            {/if}
          {:else}
            <div class="mt-4 rounded-xl border border-dashed border-border bg-surface-muted p-4 text-sm text-fg-subtle">
              Discovery snapshots will appear here after a scan completes.
            </div>
          {/if}
        </Card>
      </div>

      <div class="space-y-6">
        <Card>
          <div class="flex items-center justify-between gap-4">
            <div>
              <h2 class="text-base font-semibold text-fg">Top processes</h2>
              <p class="mt-1 text-sm text-fg-subtle">Sorted by CPU usage.</p>
            </div>
            {#if topProcessesLoading}
              <Spinner size="sm" label="Loading top processes" />
            {/if}
          </div>

          {#if topProcesses.length > 0}
            <div class="mt-4 hidden overflow-x-auto rounded-xl border border-border md:block">
              <table class="min-w-full divide-y divide-border text-sm">
                <thead class="bg-surface-muted text-left text-xs uppercase tracking-wide text-fg-subtle">
                  <tr>
                    <th class="px-3 py-2">PID</th>
                    <th class="px-3 py-2">User</th>
                    <th class="px-3 py-2 text-right">CPU</th>
                    <th class="px-3 py-2 text-right">MEM</th>
                    <th class="px-3 py-2">Command</th>
                  </tr>
                </thead>
                <tbody class="divide-y divide-border bg-surface">
                  {#each topProcesses as process}
                    <tr>
                      <td class="px-3 py-2 font-mono text-xs text-fg-muted">{process.pid}</td>
                      <td class="px-3 py-2 text-fg-muted">{process.user}</td>
                      <td class="px-3 py-2 text-right font-medium {gaugeVariant(process.cpu, 50, 80) === 'error' ? 'text-error' : gaugeVariant(process.cpu, 50, 80) === 'warning' ? 'text-warning' : 'text-success'}">
                        {process.cpu.toFixed(1)}%
                      </td>
                      <td class="px-3 py-2 text-right font-medium {gaugeVariant(process.memory, 50, 80) === 'error' ? 'text-error' : gaugeVariant(process.memory, 50, 80) === 'warning' ? 'text-warning' : 'text-success'}">
                        {process.memory.toFixed(1)}%
                      </td>
                      <td class="px-3 py-2 text-fg-muted">
                        <span class="block max-w-[240px] truncate font-mono text-xs" title={process.command}>{process.command}</span>
                      </td>
                    </tr>
                  {/each}
                </tbody>
              </table>
            </div>

            <!-- Mobile: stacked cards -->
            <div class="mt-4 space-y-3 md:hidden">
              {#each topProcesses as process}
                <div class="rounded-xl border border-border bg-surface p-4">
                  <div class="flex items-center justify-between gap-2">
                    <span class="min-w-0 break-words font-medium text-fg">{process.user}</span>
                    <span class="font-mono text-xs text-fg-subtle">PID {process.pid}</span>
                  </div>
                  <dl class="mt-3 grid grid-cols-2 gap-x-4 gap-y-2 text-sm">
                    <div>
                      <dt class="text-xs uppercase tracking-wide text-fg-subtle">CPU</dt>
                      <dd class="font-medium {gaugeVariant(process.cpu, 50, 80) === 'error' ? 'text-error' : gaugeVariant(process.cpu, 50, 80) === 'warning' ? 'text-warning' : 'text-success'}">
                        {process.cpu.toFixed(1)}%
                      </dd>
                    </div>
                    <div>
                      <dt class="text-xs uppercase tracking-wide text-fg-subtle">MEM</dt>
                      <dd class="font-medium {gaugeVariant(process.memory, 50, 80) === 'error' ? 'text-error' : gaugeVariant(process.memory, 50, 80) === 'warning' ? 'text-warning' : 'text-success'}">
                        {process.memory.toFixed(1)}%
                      </dd>
                    </div>
                    <div class="col-span-2">
                      <dt class="text-xs uppercase tracking-wide text-fg-subtle">Command</dt>
                      <dd class="break-all font-mono text-xs text-fg-muted" title={process.command}>{process.command}</dd>
                    </div>
                  </dl>
                </div>
              {/each}
            </div>
          {:else}
            <div class="mt-4 rounded-xl border border-dashed border-border bg-surface-muted p-4 text-sm text-fg-subtle">
              Top process information will appear once metrics are collected.
            </div>
          {/if}
        </Card>

        <Card>
          <div class="flex items-center gap-2 text-sm font-medium text-fg-muted">
            <Activity size={16} />
            Current session
          </div>
          <dl class="mt-4 grid grid-cols-2 gap-4 text-sm">
            <div>
              <dt class="text-fg-subtle">Samples</dt>
              <dd class="mt-1 text-lg font-semibold text-fg">{historyWindow.length}</dd>
            </div>
            <div>
              <dt class="text-fg-subtle">Process count</dt>
              <dd class="mt-1 text-lg font-semibold text-fg">{metrics ? processCountLabel(metrics.processCount) : '—'}</dd>
            </div>
            <div>
              <dt class="text-fg-subtle">Temp</dt>
              <dd class="mt-1 text-lg font-semibold text-fg">{metrics ? formatTemperature(metrics.temperature) : '—'}</dd>
            </div>
            <div>
              <dt class="text-fg-subtle">Load 1m</dt>
              <dd class="mt-1 text-lg font-semibold text-fg">{metrics ? formatLoad(metrics.load.load1) : '—'}</dd>
            </div>
          </dl>
        </Card>
      </div>
    </div>
  {/if}
</div>

{#if !selectedServerId && servers.length > 0}
  <div class="fixed bottom-4 right-4 rounded-full bg-surface px-4 py-2 text-xs text-fg shadow-lg">
    Select a server to begin monitoring
  </div>
{/if}
