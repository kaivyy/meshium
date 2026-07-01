<script lang="ts">
  import { onMount } from 'svelte';
  import { Download, Play, Pause, RefreshCw, Search, Server as ServerIcon, FileText, TerminalSquare, Wrench } from 'lucide-svelte';
  import { api } from '$lib/api/client';
  import { discoveryApi, type SystemService } from '$lib/api/discovery';
  import { buildLogStreamPath, logsApi, type LogFileInfo, type LogResponse } from '$lib/api/logs';
  import { wsConnectGeneric, wsURL } from '$lib/api/websocket';
  import { Badge, Card, EmptyState, PageHeader, Spinner } from '$lib/components/ui';
  import { toast } from '$lib/stores/toast';
  import { type Server } from '$lib/stores/servers';

  type ActiveTab = 'file' | 'system' | 'service' | 'search';
  type LogLevel = 'error' | 'warning' | 'info' | 'debug';
  type StreamStatus = 'idle' | 'connecting' | 'live';

  interface DisplayLine {
    number: number;
    text: string;
    level: LogLevel;
  }

  interface LogStreamMessage {
    type: 'line' | 'error';
    line?: string;
    message?: string;
  }

  let servers = $state<Server[]>([]);
  let loadingServers = $state(true);
  let loadingMetadata = $state(false);
  let loadingLogs = $state(false);

  let selectedServerId = $state('');
  let activeTab = $state<ActiveTab>('file');
  let logFiles = $state<LogFileInfo[]>([]);
  let services = $state<SystemService[]>([]);

  let selectedFilePath = $state('');
  let customFilePath = $state('');
  let selectedServiceName = $state('');
  let selectedLineCount = $state('100');
  let filterText = $state('');

  let searchFilePath = $state('');
  let searchPattern = $state('');

  let logLines = $state<string[]>([]);
  let currentLogTotal = $state(0);
  let currentLogTruncated = $state(false);

  let streamingEnabled = $state(false);
  let streamStatus = $state<StreamStatus>('idle');
  let streamUrl = $state('');

  let viewerEl = $state<HTMLDivElement | null>(null);
  let streamSocket: WebSocket | null = null;
  let streamToken = 0;
  let manualStreamStop = false;
  let metadataToken = 0;
  let loadToken = 0;

  const currentServerId = $derived.by(() => {
    if (!selectedServerId) return null;
    const id = Number(selectedServerId);
    return Number.isFinite(id) && id > 0 ? id : null;
  });

  const currentServer = $derived.by(() => {
    if (!currentServerId) return null;
    return servers.find((server) => server.id === currentServerId) ?? null;
  });

  const lineLimit = $derived.by(() => {
    const value = Number(selectedLineCount);
    return Number.isFinite(value) && value > 0 ? value : 100;
  });

  const resolvedFilePath = $derived.by(() => {
    const custom = customFilePath.trim();
    if (custom) return custom;
    return selectedFilePath.trim();
  });

  const resolvedSearchFilePath = $derived.by(() => {
    const preferred = searchFilePath.trim();
    if (preferred) return preferred;
    return resolvedFilePath;
  });

  const filteredLines = $derived.by(() => {
    const q = filterText.trim().toLowerCase();
    if (!q) return logLines;
    return logLines.filter((line) => line.toLowerCase().includes(q));
  });

  const displayLines = $derived.by(() => {
    return filteredLines.map((text, index) => ({
      number: index + 1,
      text,
      level: detectLevel(text)
    }));
  });

  const hasLiveSource = $derived.by(() => {
    if (!currentServerId) return false;
    if (activeTab === 'file') return resolvedFilePath.length > 0;
    if (activeTab === 'service') return selectedServiceName.length > 0;
    if (activeTab === 'system') return true;
    return false;
  });

  const currentSourceLabel = $derived.by(() => {
    switch (activeTab) {
      case 'file':
        return resolvedFilePath ? `File: ${resolvedFilePath}` : 'File logs';
      case 'system':
        return 'System journal';
      case 'service':
        return selectedServiceName ? `Service: ${selectedServiceName}` : 'Service logs';
      case 'search':
        return resolvedSearchFilePath ? `Search in ${resolvedSearchFilePath}` : 'Search results';
    }
  });

  const currentActionLabel = $derived.by(() => {
    if (activeTab === 'search') return 'Search';
    return 'Refresh';
  });

  onMount(async () => {
    await loadServers();
  });

  function detectLevel(text: string): LogLevel {
    const lower = text.toLowerCase();
    if (/(error|fatal|critical|panic|severe)/i.test(text)) return 'error';
    if (/(warn|warning|deprecated)/i.test(text)) return 'warning';
    if (/(debug|trace)/i.test(text)) return 'debug';
    if (/(info|notice|started|starting|success|ok)/i.test(text)) return 'info';
    return 'info';
  }

  function scrollToBottom() {
    if (!viewerEl) return;
    requestAnimationFrame(() => {
      if (viewerEl) {
        viewerEl.scrollTop = viewerEl.scrollHeight;
      }
    });
  }

  function applyResponse(response: LogResponse) {
    logLines = response.lines;
    currentLogTotal = response.total;
    currentLogTruncated = response.truncated;
    scrollToBottom();
  }

  function stopStreaming() {
    manualStreamStop = true;
    streamToken += 1;
    streamUrl = '';
    streamStatus = 'idle';
    if (streamSocket) {
      streamSocket.close();
      streamSocket = null;
    }
  }

  function buildCurrentStreamPath(): string | null {
    if (!currentServerId) return null;

    if (activeTab === 'file') {
      const file = resolvedFilePath;
      if (!file) return null;
      return buildLogStreamPath(currentServerId, { file, lines: lineLimit });
    }

    if (activeTab === 'system') {
      return buildLogStreamPath(currentServerId, { system: true, lines: lineLimit });
    }

    if (activeTab === 'service') {
      if (!selectedServiceName.trim()) return null;
      return buildLogStreamPath(currentServerId, { service: selectedServiceName, lines: lineLimit });
    }

    return null;
  }

  function startStreaming() {
    const path = buildCurrentStreamPath();
    if (!path) return;

    stopStreaming();
    manualStreamStop = false;
    streamStatus = 'connecting';
    streamUrl = wsURL(path);

    const token = ++streamToken;
    const socket = wsConnectGeneric<LogStreamMessage>(
      path,
      (msg) => {
        if (token !== streamToken || streamSocket !== socket) return;
        if (msg.type === 'line' && msg.line) {
          logLines = [...logLines, msg.line];
          currentLogTotal = logLines.length;
          scrollToBottom();
          return;
        }
        if (msg.type === 'error' && msg.message) {
          toast.error(msg.message);
          stopStreaming();
        }
      },
      () => {
        if (token !== streamToken || streamSocket !== socket) return;
        if (!manualStreamStop) {
          streamingEnabled = false;
          streamStatus = 'idle';
          streamSocket = null;
          toast.error('Live log stream disconnected');
        }
      },
      () => {
        if (token !== streamToken || streamSocket !== socket) return;
        if (manualStreamStop) {
          manualStreamStop = false;
          return;
        }
        streamStatus = 'idle';
        streamSocket = null;
      },
      () => {
        if (token !== streamToken || streamSocket !== socket) return;
        streamStatus = 'live';
      }
    );

    streamSocket = socket;
  }

  async function loadServers() {
    loadingServers = true;
    try {
      const data = await api.get('/servers') as Server[];
      servers = data;

      if (data.length > 0) {
        selectedServerId = String(data[0].id);
        await loadMetadata(data[0].id);
        if (activeTab !== 'search') {
          await refreshLogs();
        }
      }
    } catch (err) {
      toast.error('Failed to load servers');
    } finally {
      loadingServers = false;
    }
  }

  async function loadMetadata(serverID: number) {
    const token = ++metadataToken;
    loadingMetadata = true;
    try {
      const [files, snapshot] = await Promise.all([
        logsApi.listLogFiles(serverID, '/var/log'),
        discoveryApi.getSnapshot(serverID).catch(() => null)
      ]);

      if (token !== metadataToken) return;

      logFiles = files;
      services = snapshot?.services ?? [];

      if (files.length > 0) {
        if (!selectedFilePath || !files.some((file) => file.path === selectedFilePath)) {
          selectedFilePath = files[0].path;
        }
      } else if (!selectedFilePath) {
        selectedFilePath = '/var/log/syslog';
      }

      if (!selectedServiceName || !services.some((service) => service.name === selectedServiceName)) {
        selectedServiceName = services[0]?.name ?? '';
      }

      if (!searchFilePath) {
        searchFilePath = selectedFilePath || '/var/log/syslog';
      }
    } catch {
      toast.error('Failed to load log metadata');
    } finally {
      if (token === metadataToken) {
        loadingMetadata = false;
      }
    }
  }

  async function refreshLogs() {
    if (!currentServerId || activeTab === 'search') return;

    const token = ++loadToken;
    loadingLogs = true;
    stopStreaming();

    try {
      let response: LogResponse;
      if (activeTab === 'file') {
        if (!resolvedFilePath) {
          toast.error('Select a log file first');
          return;
        }
        response = await logsApi.readLog(currentServerId, resolvedFilePath, lineLimit, filterText.trim());
      } else if (activeTab === 'system') {
        response = await logsApi.getSystemLogs(currentServerId, lineLimit);
      } else {
        if (!selectedServiceName.trim()) {
          toast.error('Select a service first');
          return;
        }
        response = await logsApi.getServiceLogs(currentServerId, selectedServiceName, lineLimit);
      }

      if (token !== loadToken) return;
      applyResponse(response);
    } catch {
      if (token === loadToken) {
        logLines = [];
        currentLogTotal = 0;
        currentLogTruncated = false;
        toast.error('Failed to load logs');
      }
    } finally {
      if (token === loadToken) {
        loadingLogs = false;
        if (streamingEnabled && hasLiveSource) {
          startStreaming();
        }
      }
    }
  }

  async function runSearch() {
    if (!currentServerId) return;
    const pattern = searchPattern.trim();
    if (!pattern) {
      toast.error('Enter a search pattern');
      return;
    }

    const token = ++loadToken;
    loadingLogs = true;
    stopStreaming();

    try {
      const response = await logsApi.searchLogs(currentServerId, {
        file: resolvedSearchFilePath || resolvedFilePath || '/var/log/syslog',
        pattern,
        lines: lineLimit
      });

      if (token !== loadToken) return;
      activeTab = 'search';
      applyResponse(response);
    } catch {
      if (token === loadToken) {
        logLines = [];
        currentLogTotal = 0;
        currentLogTruncated = false;
        toast.error('Failed to search logs');
      }
    } finally {
      if (token === loadToken) {
        loadingLogs = false;
      }
    }
  }

  async function refreshCurrentView() {
    if (activeTab === 'search') {
      await runSearch();
      return;
    }
    await refreshLogs();
  }

  async function changeServer(event: Event) {
    const value = (event.currentTarget as HTMLSelectElement).value;
    selectedServerId = value;
    const serverID = Number(value);
    if (!Number.isFinite(serverID) || serverID <= 0) return;

    customFilePath = '';
    searchFilePath = '';
    await loadMetadata(serverID);
    if (activeTab !== 'search') {
      await refreshLogs();
    }
  }

  async function changeTab(tab: ActiveTab) {
    if (tab === activeTab) return;
    activeTab = tab;

    if (tab === 'search') {
      stopStreaming();
      if (!searchFilePath) {
        searchFilePath = resolvedFilePath || '/var/log/syslog';
      }
      return;
    }

    await refreshLogs();
  }

  async function changeFile(event: Event) {
    const value = (event.currentTarget as HTMLSelectElement).value;
    selectedFilePath = value;
    customFilePath = '';
    if (activeTab === 'file') {
      await refreshLogs();
    }
  }

  async function changeService(event: Event) {
    const value = (event.currentTarget as HTMLSelectElement).value;
    selectedServiceName = value;
    if (activeTab === 'service') {
      await refreshLogs();
    }
  }

  async function changeLineCount(event: Event) {
    selectedLineCount = (event.currentTarget as HTMLSelectElement).value;
    if (activeTab !== 'search') {
      await refreshLogs();
    }
  }

  function toggleStreaming() {
    if (activeTab === 'search' || !hasLiveSource) return;
    streamingEnabled = !streamingEnabled;

    if (!streamingEnabled) {
      stopStreaming();
      return;
    }

    refreshLogs();
  }

  async function copyVisibleLogs() {
    const text = displayLines.map((line) => line.text).join('\n');
    if (!text) {
      toast.error('Nothing to copy');
      return;
    }

    try {
      await navigator.clipboard.writeText(text);
      toast.success('Copied logs to clipboard');
    } catch {
      toast.error('Failed to copy logs');
    }
  }

  function downloadVisibleLogs() {
    const text = displayLines.map((line) => line.text).join('\n');
    if (!text) {
      toast.error('Nothing to download');
      return;
    }

    const serverName = currentServer?.name ?? 'server';
    const tabLabel = activeTab === 'search' ? 'search' : activeTab;
    const filename = `${serverName}-${tabLabel}-${new Date().toISOString().replace(/[:.]/g, '-')}.log`;
    const blob = new Blob([text], { type: 'text/plain;charset=utf-8' });
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement('a');
    anchor.href = url;
    anchor.download = filename;
    anchor.click();
    URL.revokeObjectURL(url);
  }

  function ensureSearchFile() {
    if (!searchFilePath) {
      searchFilePath = resolvedFilePath || selectedFilePath || '/var/log/syslog';
    }
  }
</script>

<svelte:head>
  <title>Logs - Meshium</title>
</svelte:head>

<div class="mx-auto max-w-7xl p-4 sm:p-6">
  <PageHeader title="Logs" subtitle="View and stream remote log files over SSH.">
    {#snippet actions()}
      <button
        type="button"
        onclick={refreshCurrentView}
        disabled={loadingLogs || loadingMetadata || !currentServerId || (activeTab === 'search' && !searchPattern.trim())}
        class="inline-flex items-center gap-2 rounded-lg border border-slate-300 bg-white px-4 py-2 text-sm font-medium text-slate-700 transition hover:bg-slate-50 disabled:opacity-60"
      >
        {#if loadingLogs}<Spinner size="sm" label="Refreshing logs" />{:else if activeTab === 'search'}<Search size={16} />{:else}<RefreshCw size={16} />{/if}
        {currentActionLabel}
      </button>
    {/snippet}
  </PageHeader>

  {#if loadingServers}
    <div class="mt-6 flex items-center gap-3 rounded-2xl border border-slate-200 bg-white p-6 text-sm text-slate-600">
      <Spinner size="sm" label="Loading servers" />
      Loading servers...
    </div>
  {:else if servers.length === 0}
    <div class="mt-6">
      <EmptyState
        title="No servers available"
        description="Add a server first, then return here to view logs."
        icon={serverEmptyIcon}
      />
    </div>
  {:else}
    <div class="mt-6 grid gap-4 xl:grid-cols-[20rem_minmax(0,1fr)]">
      <Card padding="lg">
        <div class="space-y-5">
          <div>
            <label class="mb-2 block text-xs font-semibold uppercase tracking-wide text-slate-500">Server</label>
            <select
              value={selectedServerId}
              onchange={changeServer}
              class="w-full rounded-xl border border-slate-300 bg-white px-3 py-2 text-sm text-slate-900 outline-none transition focus:border-blue-500"
            >
              {#each servers as server (server.id)}
                <option value={String(server.id)}>{server.name} — {server.host}:{server.port || 22}</option>
              {/each}
            </select>
          </div>

          <div>
            <label class="mb-2 block text-xs font-semibold uppercase tracking-wide text-slate-500">View</label>
            <div class="grid grid-cols-2 gap-2">
              <button type="button" onclick={() => changeTab('file')} class={`rounded-lg border px-3 py-2 text-sm font-medium transition ${activeTab === 'file' ? 'border-blue-500 bg-blue-50 text-blue-700' : 'border-slate-300 bg-white text-slate-700 hover:bg-slate-50'}`}>
                <FileText size={16} class="mr-2 inline-block" />Files
              </button>
              <button type="button" onclick={() => changeTab('system')} class={`rounded-lg border px-3 py-2 text-sm font-medium transition ${activeTab === 'system' ? 'border-blue-500 bg-blue-50 text-blue-700' : 'border-slate-300 bg-white text-slate-700 hover:bg-slate-50'}`}>
                <TerminalSquare size={16} class="mr-2 inline-block" />System
              </button>
              <button type="button" onclick={() => changeTab('service')} class={`rounded-lg border px-3 py-2 text-sm font-medium transition ${activeTab === 'service' ? 'border-blue-500 bg-blue-50 text-blue-700' : 'border-slate-300 bg-white text-slate-700 hover:bg-slate-50'}`}>
                <Wrench size={16} class="mr-2 inline-block" />Service
              </button>
              <button type="button" onclick={() => changeTab('search')} class={`rounded-lg border px-3 py-2 text-sm font-medium transition ${activeTab === 'search' ? 'border-blue-500 bg-blue-50 text-blue-700' : 'border-slate-300 bg-white text-slate-700 hover:bg-slate-50'}`}>
                <Search size={16} class="mr-2 inline-block" />Search
              </button>
            </div>
          </div>

          {#if activeTab === 'file'}
            <div class="space-y-3">
              <div>
                <label class="mb-2 block text-xs font-semibold uppercase tracking-wide text-slate-500">Log file list</label>
                <select
                  value={selectedFilePath}
                  onchange={changeFile}
                  class="w-full rounded-xl border border-slate-300 bg-white px-3 py-2 text-sm text-slate-900 outline-none transition focus:border-blue-500"
                >
                  {#if logFiles.length === 0}
                    <option value="">No log files found</option>
                  {:else}
                    {#each logFiles as file (file.path)}
                      <option value={file.path}>{file.name} — {file.path}</option>
                    {/each}
                  {/if}
                </select>
              </div>

              <div>
                <label class="mb-2 block text-xs font-semibold uppercase tracking-wide text-slate-500">Custom file path</label>
                <input
                  value={customFilePath}
                  oninput={(event) => {
                    customFilePath = (event.currentTarget as HTMLInputElement).value;
                  }}
                  onblur={ensureSearchFile}
                  placeholder="/var/log/syslog"
                  class="w-full rounded-xl border border-slate-300 bg-white px-3 py-2 text-sm text-slate-900 outline-none transition focus:border-blue-500"
                />
              </div>
            </div>
          {:else if activeTab === 'service'}
            <div>
              <label class="mb-2 block text-xs font-semibold uppercase tracking-wide text-slate-500">Service</label>
              <select
                value={selectedServiceName}
                onchange={changeService}
                class="w-full rounded-xl border border-slate-300 bg-white px-3 py-2 text-sm text-slate-900 outline-none transition focus:border-blue-500"
              >
                {#if services.length === 0}
                  <option value="">No services found</option>
                {:else}
                  {#each services as service (service.name)}
                    <option value={service.name}>{service.name}</option>
                  {/each}
                {/if}
              </select>
            </div>
          {:else if activeTab === 'search'}
            <div class="space-y-3">
              <div>
                <label class="mb-2 block text-xs font-semibold uppercase tracking-wide text-slate-500">Search file</label>
                <input
                  value={searchFilePath}
                  oninput={(event) => {
                    searchFilePath = (event.currentTarget as HTMLInputElement).value;
                  }}
                  placeholder={resolvedFilePath || '/var/log/syslog'}
                  class="w-full rounded-xl border border-slate-300 bg-white px-3 py-2 text-sm text-slate-900 outline-none transition focus:border-blue-500"
                />
              </div>

              <div>
                <label class="mb-2 block text-xs font-semibold uppercase tracking-wide text-slate-500">Pattern</label>
                <input
                  value={searchPattern}
                  oninput={(event) => {
                    searchPattern = (event.currentTarget as HTMLInputElement).value;
                  }}
                  placeholder="error|warning|timeout"
                  class="w-full rounded-xl border border-slate-300 bg-white px-3 py-2 text-sm text-slate-900 outline-none transition focus:border-blue-500"
                />
              </div>
            </div>
          {/if}

          <div class="space-y-3 border-t border-slate-200 pt-5">
            <div>
              <label class="mb-2 block text-xs font-semibold uppercase tracking-wide text-slate-500">Lines</label>
              <select
                value={selectedLineCount}
                onchange={changeLineCount}
                class="w-full rounded-xl border border-slate-300 bg-white px-3 py-2 text-sm text-slate-900 outline-none transition focus:border-blue-500"
              >
                <option value="50">50</option>
                <option value="100">100</option>
                <option value="250">250</option>
                <option value="500">500</option>
                <option value="1000">1000</option>
              </select>
            </div>

            <div>
              <label class="mb-2 block text-xs font-semibold uppercase tracking-wide text-slate-500">Filter</label>
              <input
                value={filterText}
                oninput={(event) => {
                  filterText = (event.currentTarget as HTMLInputElement).value;
                }}
                placeholder="Filter visible lines"
                class="w-full rounded-xl border border-slate-300 bg-white px-3 py-2 text-sm text-slate-900 outline-none transition focus:border-blue-500"
              />
            </div>

            <div class="grid grid-cols-2 gap-2">
              <button
                type="button"
                onclick={toggleStreaming}
                disabled={activeTab === 'search' || !hasLiveSource}
                class={`inline-flex items-center justify-center gap-2 rounded-xl border px-3 py-2 text-sm font-medium transition disabled:cursor-not-allowed disabled:opacity-50 ${streamingEnabled ? 'border-red-200 bg-red-50 text-red-700 hover:bg-red-100' : 'border-emerald-200 bg-emerald-50 text-emerald-700 hover:bg-emerald-100'}`}
                title={streamUrl ? 'Live stream connected over WebSocket' : 'Live streaming toggles between a static view and a live SSH tail'}
              >
                {#if streamingEnabled}<Pause size={16} />Stop{:else}<Play size={16} />Live{/if}
              </button>

              <button
                type="button"
                onclick={refreshCurrentView}
                disabled={loadingLogs || loadingMetadata || !currentServerId}
                class="inline-flex items-center justify-center gap-2 rounded-xl border border-slate-300 bg-white px-3 py-2 text-sm font-medium text-slate-700 transition hover:bg-slate-50 disabled:opacity-60"
              >
                {#if loadingLogs}<Spinner size="sm" label="Refreshing logs" />{:else if activeTab === 'search'}<Search size={16} />{:else}<RefreshCw size={16} />{/if}
                {currentActionLabel}
              </button>
            </div>

            <div class="grid grid-cols-2 gap-2">
              <button
                type="button"
                onclick={copyVisibleLogs}
                class="inline-flex items-center justify-center gap-2 rounded-xl border border-slate-300 bg-white px-3 py-2 text-sm font-medium text-slate-700 transition hover:bg-slate-50"
              >
                Copy
              </button>
              <button
                type="button"
                onclick={downloadVisibleLogs}
                class="inline-flex items-center justify-center gap-2 rounded-xl border border-slate-300 bg-white px-3 py-2 text-sm font-medium text-slate-700 transition hover:bg-slate-50"
              >
                <Download size={16} />Download
              </button>
            </div>
          </div>
        </div>
      </Card>

      <Card padding="lg">
        <div class="flex flex-col gap-4">
          <div class="flex flex-wrap items-center justify-between gap-3">
            <div class="min-w-0">
              <h2 class="truncate text-lg font-semibold text-slate-900">{currentSourceLabel}</h2>
              <p class="text-sm text-slate-500">
                {#if currentServer}
                  {currentServer.name} · {currentServer.host}:{currentServer.port || 22}
                {:else}
                  Select a server to begin
                {/if}
              </p>
            </div>

            <div class="flex flex-wrap items-center gap-2">
              <Badge variant={streamingEnabled ? 'success' : 'neutral'}>{streamStatus === 'live' ? 'Live' : streamingEnabled ? 'Connecting' : 'Static'}</Badge>
              <Badge variant={currentLogTruncated ? 'warning' : 'info'}>{currentLogTotal} lines</Badge>
              {#if currentLogTruncated}<Badge variant="warning">Truncated</Badge>{/if}
              {#if loadingMetadata}<Badge variant="info">Loading metadata</Badge>{/if}
            </div>
          </div>

          <div class="rounded-2xl border border-slate-200 bg-slate-950" style="min-height: 36rem;">
            <div class="border-b border-slate-800 px-4 py-3 text-xs text-slate-400">
              {#if activeTab === 'search'}
                Search results for <span class="font-mono text-slate-200">{searchPattern || 'pattern'}</span>
              {:else if activeTab === 'file'}
                Viewing <span class="font-mono text-slate-200">{resolvedFilePath || 'a log file'}</span>
              {:else if activeTab === 'system'}
                Viewing <span class="font-mono text-slate-200">journalctl -f</span>
              {:else}
                Viewing <span class="font-mono text-slate-200">journalctl -u {selectedServiceName || 'service'}</span>
              {/if}
            </div>

            <div bind:this={viewerEl} class="max-h-[70vh] overflow-auto font-mono text-sm leading-6 text-slate-100">
              {#if loadingLogs && displayLines.length === 0}
                <div class="flex min-h-[20rem] items-center justify-center gap-3 p-8 text-slate-400">
                  <Spinner size="lg" label="Loading logs" />
                  <span>Loading logs...</span>
                </div>
              {:else if displayLines.length === 0}
                <div class="flex min-h-[20rem] items-center justify-center p-8">
                  <EmptyState
                    title="No log lines"
                    description={activeTab === 'search' ? 'Run a search to populate results.' : 'Choose a source and refresh to load log lines.'}
                    icon={emptyIcon}
                  />
                </div>
              {:else}
                <div class="divide-y divide-slate-800">
                  {#each displayLines as entry (entry.number)}
                    <div class={`grid grid-cols-[4rem_5.5rem_1fr] gap-3 px-4 py-2 ${entry.level === 'error' ? 'bg-red-950/35' : entry.level === 'warning' ? 'bg-amber-950/25' : entry.level === 'debug' ? 'bg-slate-950/50' : 'bg-emerald-950/20'}`}>
                      <div class="text-right text-xs text-slate-500">{entry.number}</div>
                      <div>
                        <span class={`rounded-full px-2 py-0.5 text-[11px] font-semibold uppercase tracking-wide ${entry.level === 'error' ? 'bg-red-500/20 text-red-200' : entry.level === 'warning' ? 'bg-amber-500/20 text-amber-200' : entry.level === 'debug' ? 'bg-slate-700 text-slate-200' : 'bg-emerald-500/20 text-emerald-200'}`}>
                          {entry.level}
                        </span>
                      </div>
                      <div class="whitespace-pre-wrap break-all text-slate-100">{entry.text}</div>
                    </div>
                  {/each}
                </div>
              {/if}
            </div>
          </div>
        </div>
      </Card>
    </div>
  {/if}
</div>

{#snippet serverEmptyIcon()}
  <ServerIcon size={20} />
{/snippet}

{#snippet emptyIcon()}
  <TerminalSquare size={20} />
{/snippet}
