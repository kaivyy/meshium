<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { api } from '$lib/api/client';
  import { wsConnectGeneric, wsURL } from '$lib/api/websocket';
  import type { ServerMetrics } from '$lib/api/metrics';

  // ── Props ──
  export let sourceId: number | null = null;
  export let targetId: number | null = null;
  export let pipelineRunning = false;

  // ── State ──
  let sourceMetrics: ServerMetrics | null = null;
  let targetMetrics: ServerMetrics | null = null;
  let sourceWs: WebSocket | null = null;
  let targetWs: WebSocket | null = null;
  let loading = true;
  let sourceConnected = false;
  let targetConnected = false;

  // ── Lifecycle ──
  onMount(async () => {
    await fetchInitialMetrics();
  });

  onDestroy(() => {
    disconnectWebSockets();
  });

  // Only hold live monitoring sockets open while the pipeline is running.
  // Otherwise both source and target SSH streams stay open needlessly.
  $: if (pipelineRunning) {
    connectWebSockets();
  } else {
    disconnectWebSockets();
  }

  // ── Data Fetching ──
  async function fetchInitialMetrics() {
    loading = true;
    const promises: Promise<void>[] = [];
    if (sourceId) {
      promises.push(
        api.get(`/servers/${sourceId}/metrics`)
          .then((m) => { sourceMetrics = m as ServerMetrics; })
          .catch(() => {})
      );
    }
    if (targetId) {
      promises.push(
        api.get(`/servers/${targetId}/metrics`)
          .then((m) => { targetMetrics = m as ServerMetrics; })
          .catch(() => {})
      );
    }
    await Promise.all(promises);
    loading = false;
  }

  function connectWebSockets() {
    if (sourceId && !sourceWs) {
      sourceWs = wsConnectGeneric<ServerMetrics>(
        `/ws/monitoring/${sourceId}?interval=5`,
        (msg) => { sourceMetrics = msg; sourceConnected = true; },
        () => { sourceConnected = false; },
        () => { sourceConnected = false; },
        () => { sourceConnected = true; }
      );
    }
    if (targetId && !targetWs) {
      targetWs = wsConnectGeneric<ServerMetrics>(
        `/ws/monitoring/${targetId}?interval=5`,
        (msg) => { targetMetrics = msg; targetConnected = true; },
        () => { targetConnected = false; },
        () => { targetConnected = false; },
        () => { targetConnected = true; }
      );
    }
  }

  function disconnectWebSockets() {
    sourceWs?.close();
    sourceWs = null;
    sourceConnected = false;
    targetWs?.close();
    targetWs = null;
    targetConnected = false;
  }

  // ── Helpers ──
  function formatBytes(bytes: number): string {
    if (!bytes || bytes === 0) return '0 B';
    const k = 1024;
    const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + ' ' + sizes[i];
  }

  function formatRate(bytesPerSec: number): string {
    if (!bytesPerSec || bytesPerSec === 0) return '0 B/s';
    return formatBytes(bytesPerSec) + '/s';
  }

  function cpuColor(pct: number): string {
    if (pct > 85) return 'text-red-400';
    if (pct > 65) return 'text-yellow-400';
    return 'text-green-400';
  }

  function cpuBarColor(pct: number): string {
    if (pct > 85) return 'bg-red-500';
    if (pct > 65) return 'bg-yellow-500';
    return 'bg-green-500';
  }

  function ramColor(pct: number): string {
    if (pct > 90) return 'text-red-400';
    if (pct > 75) return 'text-yellow-400';
    return 'text-green-400';
  }

  function ramBarColor(pct: number): string {
    if (pct > 90) return 'bg-red-500';
    if (pct > 75) return 'bg-yellow-500';
    return 'bg-green-500';
  }

  function diskColor(pct: number): string {
    if (pct > 90) return 'text-red-400';
    if (pct > 80) return 'text-yellow-400';
    return 'text-green-400';
  }

  function diskBarColor(pct: number): string {
    if (pct > 90) return 'bg-red-500';
    if (pct > 80) return 'bg-yellow-500';
    return 'bg-green-500';
  }

  function netColor(bytesPerSec: number): string {
    if (bytesPerSec > 50 * 1024 * 1024) return 'text-yellow-400';
    return 'text-green-400';
  }

  function uptimeString(secs: number): string {
    if (!secs) return '—';
    const days = Math.floor(secs / 86400);
    const hours = Math.floor((secs % 86400) / 3600);
    if (days > 0) return `${days}d ${hours}h`;
    const mins = Math.floor((secs % 3600) / 60);
    return `${hours}h ${mins}m`;
  }
</script>

<div class="w-72 shrink-0 border-l border-gray-800 bg-gray-950 flex flex-col overflow-hidden">
  <!-- Header -->
  <div class="px-4 py-3 border-b border-gray-800 shrink-0">
    <div class="flex items-center gap-2">
      <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="text-blue-400">
        <path d="M22 12h-4l-3 9L9 3l-3 9H2"/>
      </svg>
      <h2 class="text-sm font-semibold text-white">Live Monitor</h2>
      <span class="ml-auto text-xs {pipelineRunning ? 'text-green-400' : 'text-gray-500'}">{pipelineRunning ? 'Live' : 'Idle'}</span>
    </div>
  </div>

  {#if loading}
    <div class="flex-1 flex items-center justify-center text-gray-500 text-xs">
      <svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="animate-spin mr-2"><path d="M21 12a9 9 0 1 1-6.219-8.56"/></svg>
      Loading...
    </div>
  {:else}
    <div class="flex-1 overflow-y-auto">
      <!-- ═══ SOURCE VPS A ═══ -->
      <div class="px-4 py-3 border-b border-gray-800">
        <div class="flex items-center gap-2 mb-3">
          <div class="w-2 h-2 rounded-full {sourceConnected ? 'bg-green-500' : 'bg-red-500'}"></div>
          <h3 class="text-xs font-bold text-gray-300 uppercase tracking-wide">Source VPS A</h3>
          <span class="ml-auto text-xs text-gray-500">#{sourceId ?? '—'}</span>
        </div>

        {#if sourceMetrics}
          <!-- CPU -->
          <div class="mb-2.5">
            <div class="flex items-center justify-between mb-1">
              <span class="text-xs text-gray-400">CPU</span>
              <span class="text-xs font-mono {cpuColor(sourceMetrics.cpu.usage)}">{sourceMetrics.cpu.usage.toFixed(1)}%</span>
            </div>
            <div class="h-1.5 bg-gray-800 rounded-full overflow-hidden">
              <div class="h-full rounded-full transition-all duration-500 {cpuBarColor(sourceMetrics.cpu.usage)}" style="width: {sourceMetrics.cpu.usage}%"></div>
            </div>
            <div class="text-xs text-gray-500 mt-0.5">{sourceMetrics.cpu.cores} cores</div>
          </div>

          <!-- RAM -->
          <div class="mb-2.5">
            <div class="flex items-center justify-between mb-1">
              <span class="text-xs text-gray-400">RAM</span>
              <span class="text-xs font-mono {ramColor(sourceMetrics.memory.usagePercent)}">{sourceMetrics.memory.usagePercent.toFixed(0)}%</span>
            </div>
            <div class="h-1.5 bg-gray-800 rounded-full overflow-hidden">
              <div class="h-full rounded-full transition-all duration-500 {ramBarColor(sourceMetrics.memory.usagePercent)}" style="width: {sourceMetrics.memory.usagePercent}%"></div>
            </div>
            <div class="text-xs text-gray-500 mt-0.5">{formatBytes(sourceMetrics.memory.used)} / {formatBytes(sourceMetrics.memory.total)}</div>
          </div>

          <!-- Disk -->
          {#if sourceMetrics.disk && sourceMetrics.disk.length > 0}
            <div class="mb-2.5">
              <div class="flex items-center justify-between mb-1">
                <span class="text-xs text-gray-400">Disk</span>
                <span class="text-xs font-mono {diskColor(sourceMetrics.disk[0].usagePercent)}">{sourceMetrics.disk[0].usagePercent.toFixed(0)}%</span>
              </div>
              <div class="h-1.5 bg-gray-800 rounded-full overflow-hidden">
                <div class="h-full rounded-full transition-all duration-500 {diskBarColor(sourceMetrics.disk[0].usagePercent)}" style="width: {sourceMetrics.disk[0].usagePercent}%"></div>
              </div>
              <div class="text-xs text-gray-500 mt-0.5">{formatBytes(sourceMetrics.disk[0].used)} / {formatBytes(sourceMetrics.disk[0].total)}</div>
            </div>
          {/if}

          <!-- Network -->
          {#if sourceMetrics.network && sourceMetrics.network.length > 0}
            <div class="mb-2.5">
              <div class="flex items-center justify-between mb-1">
                <span class="text-xs text-gray-400">Network</span>
                <span class="text-xs font-mono {netColor(sourceMetrics.network[0].rxBytes + sourceMetrics.network[0].txBytes)}">
                  {formatRate(sourceMetrics.network[0].rxBytes + sourceMetrics.network[0].txBytes)}
                </span>
              </div>
              <div class="flex items-center gap-3 text-xs text-gray-500">
                <span>&darr; {formatRate(sourceMetrics.network[0].rxBytes)}</span>
                <span>&uarr; {formatRate(sourceMetrics.network[0].txBytes)}</span>
              </div>
            </div>
          {/if}

          <!-- Load & Uptime -->
          <div class="grid grid-cols-2 gap-2 mb-2.5">
            <div class="bg-gray-900 rounded px-2 py-1.5">
              <div class="text-xs text-gray-500">Load 1m</div>
              <div class="text-xs font-mono text-gray-300">{sourceMetrics.load.load1.toFixed(2)}</div>
            </div>
            <div class="bg-gray-900 rounded px-2 py-1.5">
              <div class="text-xs text-gray-500">Uptime</div>
              <div class="text-xs font-mono text-gray-300">{uptimeString(sourceMetrics.uptime)}</div>
            </div>
          </div>

          <!-- Docker / Containers -->
          {#if sourceMetrics.processCount > 0}
            <div class="bg-gray-900 rounded px-2 py-1.5 mb-2">
              <div class="flex items-center justify-between">
                <span class="text-xs text-gray-500">Processes</span>
                <span class="text-xs font-mono text-gray-300">{sourceMetrics.processCount}</span>
              </div>
            </div>
          {/if}
        {:else}
          <div class="text-xs text-gray-600 py-4 text-center">
            No data available
          </div>
        {/if}
      </div>

      <!-- ═══ TARGET VPS B ═══ -->
      <div class="px-4 py-3 border-b border-gray-800">
        <div class="flex items-center gap-2 mb-3">
          <div class="w-2 h-2 rounded-full {targetConnected ? 'bg-green-500' : 'bg-red-500'}"></div>
          <h3 class="text-xs font-bold text-gray-300 uppercase tracking-wide">Target VPS B</h3>
          <span class="ml-auto text-xs text-gray-500">#{targetId ?? '—'}</span>
        </div>

        {#if targetMetrics}
          <!-- CPU -->
          <div class="mb-2.5">
            <div class="flex items-center justify-between mb-1">
              <span class="text-xs text-gray-400">CPU</span>
              <span class="text-xs font-mono {cpuColor(targetMetrics.cpu.usage)}">{targetMetrics.cpu.usage.toFixed(1)}%</span>
            </div>
            <div class="h-1.5 bg-gray-800 rounded-full overflow-hidden">
              <div class="h-full rounded-full transition-all duration-500 {cpuBarColor(targetMetrics.cpu.usage)}" style="width: {targetMetrics.cpu.usage}%"></div>
            </div>
            <div class="text-xs text-gray-500 mt-0.5">{targetMetrics.cpu.cores} cores</div>
          </div>

          <!-- RAM -->
          <div class="mb-2.5">
            <div class="flex items-center justify-between mb-1">
              <span class="text-xs text-gray-400">RAM</span>
              <span class="text-xs font-mono {ramColor(targetMetrics.memory.usagePercent)}">{targetMetrics.memory.usagePercent.toFixed(0)}%</span>
            </div>
            <div class="h-1.5 bg-gray-800 rounded-full overflow-hidden">
              <div class="h-full rounded-full transition-all duration-500 {ramBarColor(targetMetrics.memory.usagePercent)}" style="width: {targetMetrics.memory.usagePercent}%"></div>
            </div>
            <div class="text-xs text-gray-500 mt-0.5">{formatBytes(targetMetrics.memory.used)} / {formatBytes(targetMetrics.memory.total)}</div>
          </div>

          <!-- Disk -->
          {#if targetMetrics.disk && targetMetrics.disk.length > 0}
            <div class="mb-2.5">
              <div class="flex items-center justify-between mb-1">
                <span class="text-xs text-gray-400">Disk</span>
                <span class="text-xs font-mono {diskColor(targetMetrics.disk[0].usagePercent)}">{targetMetrics.disk[0].usagePercent.toFixed(0)}%</span>
              </div>
              <div class="h-1.5 bg-gray-800 rounded-full overflow-hidden">
                <div class="h-full rounded-full transition-all duration-500 {diskBarColor(targetMetrics.disk[0].usagePercent)}" style="width: {targetMetrics.disk[0].usagePercent}%"></div>
              </div>
              <div class="text-xs text-gray-500 mt-0.5">{formatBytes(targetMetrics.disk[0].used)} / {formatBytes(targetMetrics.disk[0].total)}</div>
            </div>
          {/if}

          <!-- Network -->
          {#if targetMetrics.network && targetMetrics.network.length > 0}
            <div class="mb-2.5">
              <div class="flex items-center justify-between mb-1">
                <span class="text-xs text-gray-400">Network</span>
                <span class="text-xs font-mono {netColor(targetMetrics.network[0].rxBytes + targetMetrics.network[0].txBytes)}">
                  {formatRate(targetMetrics.network[0].rxBytes + targetMetrics.network[0].txBytes)}
                </span>
              </div>
              <div class="flex items-center gap-3 text-xs text-gray-500">
                <span>&darr; {formatRate(targetMetrics.network[0].rxBytes)}</span>
                <span>&uarr; {formatRate(targetMetrics.network[0].txBytes)}</span>
              </div>
            </div>
          {/if}

          <!-- Load & Uptime -->
          <div class="grid grid-cols-2 gap-2 mb-2.5">
            <div class="bg-gray-900 rounded px-2 py-1.5">
              <div class="text-xs text-gray-500">Load 1m</div>
              <div class="text-xs font-mono text-gray-300">{targetMetrics.load.load1.toFixed(2)}</div>
            </div>
            <div class="bg-gray-900 rounded px-2 py-1.5">
              <div class="text-xs text-gray-500">Uptime</div>
              <div class="text-xs font-mono text-gray-300">{uptimeString(targetMetrics.uptime)}</div>
            </div>
          </div>

          <!-- Processes -->
          {#if targetMetrics.processCount > 0}
            <div class="bg-gray-900 rounded px-2 py-1.5 mb-2">
              <div class="flex items-center justify-between">
                <span class="text-xs text-gray-500">Processes</span>
                <span class="text-xs font-mono text-gray-300">{targetMetrics.processCount}</span>
              </div>
            </div>
          {/if}
        {:else}
          <div class="text-xs text-gray-600 py-4 text-center">
            No data available
          </div>
        {/if}
      </div>

      <!-- ═══ Comparison Summary ═══ -->
      {#if sourceMetrics && targetMetrics}
        <div class="px-4 py-3">
          <h3 class="text-xs font-bold text-gray-400 uppercase tracking-wide mb-2">A vs B</h3>
          <div class="space-y-1.5">
            <div class="flex items-center justify-between text-xs">
              <span class="text-gray-500">CPU</span>
              <div class="flex items-center gap-1.5 font-mono">
                <span class="{cpuColor(sourceMetrics.cpu.usage)}">{sourceMetrics.cpu.usage.toFixed(0)}%</span>
                <span class="text-gray-600">vs</span>
                <span class="{cpuColor(targetMetrics.cpu.usage)}">{targetMetrics.cpu.usage.toFixed(0)}%</span>
              </div>
            </div>
            <div class="flex items-center justify-between text-xs">
              <span class="text-gray-500">RAM</span>
              <div class="flex items-center gap-1.5 font-mono">
                <span class="{ramColor(sourceMetrics.memory.usagePercent)}">{sourceMetrics.memory.usagePercent.toFixed(0)}%</span>
                <span class="text-gray-600">vs</span>
                <span class="{ramColor(targetMetrics.memory.usagePercent)}">{targetMetrics.memory.usagePercent.toFixed(0)}%</span>
              </div>
            </div>
            {#if sourceMetrics.disk && sourceMetrics.disk.length > 0 && targetMetrics.disk && targetMetrics.disk.length > 0}
              <div class="flex items-center justify-between text-xs">
                <span class="text-gray-500">Disk</span>
                <div class="flex items-center gap-1.5 font-mono">
                  <span class="{diskColor(sourceMetrics.disk[0].usagePercent)}">{sourceMetrics.disk[0].usagePercent.toFixed(0)}%</span>
                  <span class="text-gray-600">vs</span>
                  <span class="{diskColor(targetMetrics.disk[0].usagePercent)}">{targetMetrics.disk[0].usagePercent.toFixed(0)}%</span>
                </div>
              </div>
            {/if}
          </div>
        </div>
      {/if}
    </div>
  {/if}
</div>
