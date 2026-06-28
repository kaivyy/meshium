<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import {
    Terminal as TerminalIcon, Server as ServerIcon, RefreshCw,
    Wifi, WifiOff, ChevronRight, CircleDot, CheckCircle2, XCircle,
    Loader2, ArrowRight
  } from 'lucide-svelte';
  import { api } from '$lib/api/client';
  import { type Server } from '$lib/stores/servers';
  import { Badge, Card, EmptyState, PageHeader, Skeleton, Spinner } from '$lib/components/ui';
  import { toast } from '$lib/stores/toast';

  interface WSMessage {
    step: string;
    status: string;
    value?: string;
    error?: string;
    latencyMs?: number;
  }

  interface LogEntry {
    timestamp: string;
    step: string;
    status: string;
    value?: string;
    error?: string;
    latencyMs?: number;
  }

  let servers = $state([] as Server[]);
  let loading = $state(true);
  let selectedServerId = $state<number | null>(null);
  let testRunning = $state(false);
  let logEntries = $state<LogEntry[]>([]);
  let wsConnection: WebSocket | null = null;
  let connectionStatus = $state<'idle' | 'connecting' | 'connected' | 'failed'>('idle');

  const selectedServer = $derived.by(() => servers.find(s => s.id === selectedServerId) || null);

  onMount(async () => {
    await loadServers();
  });

  onDestroy(() => {
    closeConnection();
  });

  async function loadServers() {
    loading = true;
    try {
      servers = await api.get('/servers') as Server[];
    } catch {
      toast.error('Failed to load servers');
    } finally {
      loading = false;
    }
  }

  function closeConnection() {
    if (wsConnection) {
      wsConnection.close();
      wsConnection = null;
    }
    testRunning = false;
  }

  function runConnectionTest() {
    if (!selectedServerId) return;
    closeConnection();
    logEntries = [];
    testRunning = true;
    connectionStatus = 'connecting';

    const proto = location.protocol === 'https:' ? 'wss' : 'ws';
    const token = typeof localStorage !== 'undefined' ? localStorage.getItem('meshium_session_token') : null;
    const tokenParam = token ? `?token=${encodeURIComponent(token)}` : '';
    const url = `${proto}://${location.host}/ws/connect/${selectedServerId}${tokenParam}`;

    try {
      wsConnection = new WebSocket(url);
    } catch {
      connectionStatus = 'failed';
      testRunning = false;
      toast.error('Failed to open WebSocket connection');
      return;
    }

    wsConnection.onmessage = (event) => {
      try {
        const msg = JSON.parse(event.data) as WSMessage;
        const entry: LogEntry = {
          timestamp: new Date().toISOString(),
          ...msg,
        };
        logEntries = [...logEntries, entry];

        if (msg.status === 'complete') {
          connectionStatus = 'connected';
          testRunning = false;
        } else if (msg.status === 'error') {
          connectionStatus = 'failed';
          testRunning = false;
        }
      } catch {
        // ignore parse errors
      }
    };

    wsConnection.onclose = () => {
      if (testRunning) {
        connectionStatus = 'failed';
        testRunning = false;
      }
      wsConnection = null;
    };

    wsConnection.onerror = () => {
      connectionStatus = 'failed';
      testRunning = false;
    };
  }

  function statusIcon(status: string) {
    if (status === 'complete' || status === 'ok' || status === 'success') return CheckCircle2;
    if (status === 'error' || status === 'failed') return XCircle;
    if (status === 'running' || status === 'pending') return Loader2;
    return CircleDot;
  }

  function statusColor(status: string): string {
    if (status === 'complete' || status === 'ok' || status === 'success') return 'text-green-600';
    if (status === 'error' || status === 'failed') return 'text-red-600';
    if (status === 'running' || status === 'pending') return 'text-blue-600';
    return 'text-slate-500';
  }

  function connectionStatusBadge() {
    switch (connectionStatus) {
      case 'idle': return { label: 'Idle', variant: 'neutral' as const };
      case 'connecting': return { label: 'Connecting...', variant: 'warning' as const };
      case 'connected': return { label: 'Connected', variant: 'success' as const };
      case 'failed': return { label: 'Failed', variant: 'error' as const };
    }
  }
</script>

<svelte:head><title>Terminal - Meshium</title></svelte:head>

<div class="p-4 sm:p-6 max-w-7xl mx-auto">
  <PageHeader title="Terminal" subtitle="Test SSH connections and run diagnostics on your servers.">
    {#snippet actions()}
      <button type="button" onclick={loadServers} disabled={loading} class="inline-flex items-center gap-2 rounded-lg border border-slate-300 bg-white px-4 py-2 text-sm font-medium text-slate-700 hover:bg-slate-50 disabled:opacity-60">
        {#if loading}<Spinner size="sm" label="Refreshing" />{:else}<RefreshCw size={16} />{/if}
        Refresh
      </button>
    {/snippet}
  </PageHeader>

  <div class="grid gap-6 lg:grid-cols-3">
    <!-- Server list -->
    <div class="lg:col-span-1">
      <h2 class="mb-3 text-sm font-semibold text-slate-700">Select Server</h2>
      {#if loading}
        <div class="space-y-2">
          {#each Array(4) as _}
            <Card><Skeleton width="100%" height="3rem" /></Card>
          {/each}
        </div>
      {:else if servers.length === 0}
        <EmptyState title="No servers" description="Add a server to test connections." icon={emptyIcon} />
      {:else}
        <div class="space-y-2">
          {#each servers as server (server.id)}
            <button
              type="button"
              onclick={() => { selectedServerId = server.id; closeConnection(); logEntries = []; connectionStatus = 'idle'; }}
              class={`w-full rounded-xl border p-3 text-left transition ${selectedServerId === server.id ? 'border-blue-500 bg-blue-50 shadow-sm' : 'border-slate-200 bg-white hover:border-slate-300 hover:bg-slate-50'}`}
            >
              <div class="flex items-center gap-3">
                <div class={`flex h-8 w-8 shrink-0 items-center justify-center rounded-lg ${selectedServerId === server.id ? 'bg-blue-100 text-blue-600' : 'bg-slate-100 text-slate-500'}`}>
                  <ServerIcon size={16} />
                </div>
                <div class="min-w-0 flex-1">
                  <p class="truncate text-sm font-medium text-slate-900">{server.name}</p>
                  <p class="truncate text-xs text-slate-500">{server.host}:{server.port}</p>
                </div>
                {#if selectedServerId === server.id}
                  <ChevronRight size={16} class="text-blue-500" />
                {/if}
              </div>
            </button>
          {/each}
        </div>
      {/if}
    </div>

    <!-- Terminal output -->
    <div class="lg:col-span-2">
      {#if !selectedServer}
        <Card padding="lg">
          <div class="flex flex-col items-center justify-center py-16 text-center">
            <TerminalIcon size={32} class="text-slate-300" />
            <p class="mt-4 text-sm text-slate-500">Select a server to start a connection test.</p>
          </div>
        </Card>
      {:else}
        <Card padding="lg">
          <!-- Header -->
          <div class="mb-4 flex items-center justify-between border-b border-slate-100 pb-4">
            <div>
              <div class="flex items-center gap-2">
                <h3 class="text-sm font-semibold text-slate-900">{selectedServer.name}</h3>
                <Badge variant={connectionStatusBadge().variant} size="sm">{connectionStatusBadge().label}</Badge>
              </div>
              <p class="mt-0.5 text-xs text-slate-500">{selectedServer.username}@{selectedServer.host}:{selectedServer.port}</p>
            </div>
            <button
              type="button"
              onclick={runConnectionTest}
              disabled={testRunning}
              class="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-60"
            >
              {#if testRunning}
                <Loader2 size={16} class="animate-spin" />
                Testing...
              {:else}
                <Wifi size={16} />
                Test Connection
              {/if}
            </button>
          </div>

          <!-- Terminal output -->
          <div class="min-h-[400px] rounded-lg bg-slate-900 p-4 font-mono text-sm">
            {#if logEntries.length === 0}
              <div class="flex h-full min-h-[350px] items-center justify-center text-slate-500">
                <div class="text-center">
                  <TerminalIcon size={24} class="mx-auto text-slate-600" />
                  <p class="mt-2 text-xs">Connection test output will appear here.</p>
                  <p class="mt-1 text-xs text-slate-600">Click "Test Connection" to start.</p>
                </div>
              </div>
            {:else}
              <div class="space-y-1">
                {#each logEntries as entry (entry.timestamp + entry.step)}
                  {@const Icon = statusIcon(entry.status)}
                  <div class="flex items-start gap-2">
                    <span class={statusColor(entry.status)}>
                      <Icon size={14} class={`mt-0.5 ${entry.status === 'running' ? 'animate-spin' : ''}`} />
                    </span>
                    <div class="min-w-0 flex-1">
                      <span class="text-slate-300">[{entry.step}]</span>
                      <span class="ml-2 text-slate-100">{entry.value || entry.status}</span>
                      {#if entry.latencyMs !== undefined}
                        <span class="ml-2 text-slate-500">({entry.latencyMs}ms)</span>
                      {/if}
                      {#if entry.error}
                        <div class="mt-0.5 text-red-400">{entry.error}</div>
                      {/if}
                    </div>
                  </div>
                {/each}
                {#if testRunning}
                  <div class="flex items-center gap-2 text-blue-400">
                    <Loader2 size={14} class="animate-spin" />
                    <span>Waiting for response...</span>
                  </div>
                {/if}
              </div>
            {/if}
          </div>

          <!-- Server info -->
          {#if connectionStatus === 'connected'}
            <div class="mt-4 rounded-lg bg-green-50 p-4 ring-1 ring-green-200">
              <div class="flex items-center gap-2">
                <CheckCircle2 size={16} class="text-green-600" />
                <p class="text-sm font-medium text-green-800">Connection successful</p>
              </div>
              <p class="mt-1 text-xs text-green-700">
                SSH connection to {selectedServer.name} is working. You can now run migrations and discovery scans on this server.
              </p>
              <div class="mt-3 flex gap-2">
                <a href={`/servers/${selectedServer.id}`} class="inline-flex items-center gap-1 text-xs font-medium text-green-700 hover:underline">
                  View Server Details <ArrowRight size={12} />
                </a>
                <a href={`/discovery`} class="inline-flex items-center gap-1 text-xs font-medium text-green-700 hover:underline">
                  Run Discovery <ArrowRight size={12} />
                </a>
              </div>
            </div>
          {:else if connectionStatus === 'failed'}
            <div class="mt-4 rounded-lg bg-red-50 p-4 ring-1 ring-red-200">
              <div class="flex items-center gap-2">
                <WifiOff size={16} class="text-red-600" />
                <p class="text-sm font-medium text-red-800">Connection failed</p>
              </div>
              <p class="mt-1 text-xs text-red-700">
                Check that the server is online, SSH credentials are correct, and port {selectedServer.port} is accessible.
              </p>
              <a href={`/servers/${selectedServer.id}/edit`} class="mt-2 inline-flex items-center gap-1 text-xs font-medium text-red-700 hover:underline">
                Edit Server Settings <ArrowRight size={12} />
              </a>
            </div>
          {/if}
        </Card>
      {/if}
    </div>
  </div>
</div>

{#snippet emptyIcon()}<TerminalIcon size={22} />{/snippet}
