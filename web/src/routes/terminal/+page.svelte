<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import {
    Terminal as TerminalIcon, Server as ServerIcon, RefreshCw,
    Wifi, WifiOff, ChevronRight, CircleDot, CheckCircle2, XCircle,
    Loader2, ArrowRight, Trash2, ArrowUp, ArrowDown, Copy
  } from 'lucide-svelte';
  import { api } from '$lib/api/client';
  import { type Server } from '$lib/stores/servers';
  import { Badge, Card, EmptyState, PageHeader, Skeleton, Spinner } from '$lib/components/ui';
  import { toast } from '$lib/stores/toast';

  // --- Types ---
  interface TerminalLine {
    id: number;
    type: 'input' | 'output' | 'stderr' | 'error' | 'info' | 'system';
    content: string;
    timestamp: Date;
  }

  // --- State ---
  let servers = $state([] as Server[]);
  let loading = $state(true);
  let selectedServerId = $state<number | null>(null);
  let wsConnection: WebSocket | null = null;
  let connectionStatus = $state<'idle' | 'connecting' | 'connected' | 'failed'>('idle');
  let hostname = $state('');

  // Terminal state
  let lines = $state<TerminalLine[]>([]);
  let input = $state('');
  let commandHistory = $state<string[]>([]);
  let historyIndex = $state(-1);
  let lineIdCounter = 0;
  let terminalContainer: HTMLElement | null = null;
  let inputElement: HTMLInputElement | null = null;

  // Quick commands
  const quickCommands = [
    { label: 'System Info', cmd: 'uname -a' },
    { label: 'Disk Usage', cmd: 'df -h' },
    { label: 'Memory', cmd: 'free -h' },
    { label: 'CPU Info', cmd: 'lscpu | head -20' },
    { label: 'Top Processes', cmd: 'ps aux --sort=-%cpu | head -10' },
    { label: 'Network', cmd: 'ss -tlnp' },
    { label: 'Docker', cmd: 'docker ps -a' },
    { label: 'Uptime', cmd: 'uptime' },
    { label: 'Whoami', cmd: 'whoami && id' },
    { label: 'OS Release', cmd: 'cat /etc/os-release' },
  ];

  const selectedServer = $derived.by(() => servers.find(s => s.id === selectedServerId) || null);

  // --- Lifecycle ---
  onMount(async () => {
    await loadServers();
  });

  onDestroy(() => {
    closeConnection();
  });

  // --- Server list ---
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

  // --- Terminal connection ---
  function connectTerminal() {
    if (!selectedServerId) return;
    closeConnection();
    lines = [];
    connectionStatus = 'connecting';

    const proto = location.protocol === 'https:' ? 'wss' : 'ws';
    const token = typeof localStorage !== 'undefined' ? localStorage.getItem('meshium_session_token') : null;
    const tokenParam = token ? `?token=${encodeURIComponent(token)}` : '';
    const url = `${proto}://${location.host}/ws/terminal/${selectedServerId}${tokenParam}`;

    try {
      wsConnection = new WebSocket(url);
    } catch {
      connectionStatus = 'failed';
      toast.error('Failed to open WebSocket connection');
      return;
    }

    wsConnection.onopen = () => {
      addLine('system', `Connecting to ${selectedServer?.name || 'server'}...`);
    };

    wsConnection.onmessage = (event) => {
      try {
        const msg = JSON.parse(event.data);
        if (msg.type === 'connected') {
          connectionStatus = 'connected';
          hostname = msg.hostname || '';
          addLine('info', `Connected to ${hostname || selectedServer?.host || 'server'}`);
          addLine('info', `Type commands below. Use ↑/↓ for history. Type 'exit' or 'clear' for special actions.`);
          setTimeout(() => inputElement?.focus(), 100);
        } else if (msg.type === 'output') {
          if (msg.stdout) addLine('output', msg.stdout);
          if (msg.stderr) addLine('stderr', msg.stderr);
          if (msg.exitCode !== 0 && msg.exitCode !== undefined) {
            addLine('info', `[exit code: ${msg.exitCode}]`);
          }
        } else if (msg.type === 'error') {
          addLine('error', msg.message || 'Unknown error');
        }
      } catch {
        // ignore parse errors
      }
    };

    wsConnection.onclose = () => {
      if (connectionStatus === 'connecting') {
        connectionStatus = 'failed';
      } else if (connectionStatus === 'connected') {
        addLine('system', 'Connection closed.');
        connectionStatus = 'idle';
      }
      wsConnection = null;
    };

    wsConnection.onerror = () => {
      connectionStatus = 'failed';
      addLine('error', 'WebSocket connection error');
    };
  }

  function closeConnection() {
    if (wsConnection) {
      wsConnection.close();
      wsConnection = null;
    }
    connectionStatus = 'idle';
    hostname = '';
  }

  // --- Command execution ---
  function executeCommand() {
    const cmd = input.trim();
    if (!cmd) return;
    if (!wsConnection || wsConnection.readyState !== WebSocket.OPEN) {
      toast.error('Not connected to server');
      return;
    }

    // Special commands
    if (cmd.toLowerCase() === 'clear' || cmd.toLowerCase() === 'cls') {
      lines = [];
      input = '';
      historyIndex = -1;
      return;
    }
    if (cmd.toLowerCase() === 'exit' || cmd.toLowerCase() === 'quit') {
      closeConnection();
      input = '';
      return;
    }

    // Add to history
    commandHistory = [...commandHistory, cmd];
    historyIndex = -1;

    // Display the command
    addLine('input', cmd);
    input = '';

    // Send to server
    wsConnection.send(JSON.stringify({ type: 'command', command: cmd }));
  }

  function runQuickCommand(cmd: string) {
    if (!wsConnection || wsConnection.readyState !== WebSocket.OPEN) {
      toast.error('Connect to a server first');
      return;
    }
    addLine('input', cmd);
    commandHistory = [...commandHistory, cmd];
    historyIndex = -1;
    wsConnection.send(JSON.stringify({ type: 'command', command: cmd }));
  }

  // --- History navigation ---
  function handleKeyDown(event: KeyboardEvent) {
    if (event.key === 'Enter') {
      event.preventDefault();
      executeCommand();
    } else if (event.key === 'ArrowUp') {
      event.preventDefault();
      if (commandHistory.length === 0) return;
      if (historyIndex === -1) {
        historyIndex = commandHistory.length - 1;
      } else if (historyIndex > 0) {
        historyIndex--;
      }
      input = commandHistory[historyIndex];
    } else if (event.key === 'ArrowDown') {
      event.preventDefault();
      if (historyIndex === -1) return;
      if (historyIndex < commandHistory.length - 1) {
        historyIndex++;
        input = commandHistory[historyIndex];
      } else {
        historyIndex = -1;
        input = '';
      }
    } else if (event.key === 'Tab') {
      event.preventDefault();
      // Simple tab completion for common commands
      const partial = input.trim();
      if (partial) {
        const matches = quickCommands.filter(q => q.cmd.startsWith(partial));
        if (matches.length === 1) {
          input = matches[0].cmd;
        }
      }
    } else if (event.ctrlKey && event.key === 'l') {
      event.preventDefault();
      lines = [];
    }
  }

  // --- Terminal helpers ---
  function addLine(type: TerminalLine['type'], content: string) {
    lines = [...lines, { id: ++lineIdCounter, type, content, timestamp: new Date() }];
    setTimeout(() => scrollToBottom(), 0);
  }

  function scrollToBottom() {
    if (terminalContainer) {
      terminalContainer.scrollTop = terminalContainer.scrollHeight;
    }
  }

  function clearTerminal() {
    lines = [];
  }

  function copyLastOutput() {
    const outputLines = lines.filter(l => l.type === 'output' || l.type === 'stderr');
    if (outputLines.length === 0) return;
    const lastOutput = outputLines[outputLines.length - 1];
    navigator.clipboard.writeText(lastOutput.content).then(() => {
      toast.success('Copied to clipboard');
    });
  }

  function connectionStatusBadge() {
    switch (connectionStatus) {
      case 'idle': return { label: 'Idle', variant: 'neutral' as const };
      case 'connecting': return { label: 'Connecting...', variant: 'warning' as const };
      case 'connected': return { label: 'Connected', variant: 'success' as const };
      case 'failed': return { label: 'Failed', variant: 'error' as const };
    }
  }

  function formatTime(date: Date): string {
    return date.toLocaleTimeString('en-US', { hour12: false });
  }

  function lineColor(type: TerminalLine['type']): string {
    switch (type) {
      case 'input': return 'text-green-400';
      case 'output': return 'text-slate-200';
      case 'stderr': return 'text-yellow-400';
      case 'error': return 'text-red-400';
      case 'info': return 'text-blue-400';
      case 'system': return 'text-slate-500';
      default: return 'text-slate-200';
    }
  }

  function linePrefix(type: TerminalLine['type']): string {
    switch (type) {
      case 'input': return '$ ';
      case 'output': return '';
      case 'stderr': return '';
      case 'error': return '✗ ';
      case 'info': return 'ℹ ';
      case 'system': return '→ ';
      default: return '';
    }
  }
</script>

<svelte:head><title>Terminal - Meshium</title></svelte:head>

<div class="p-4 sm:p-6 max-w-7xl mx-auto">
  <PageHeader title="Terminal" subtitle="Interactive SSH terminal — run commands on your servers in real-time.">
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
        <EmptyState title="No servers" description="Add a server to use the terminal." icon={emptyIcon} />
      {:else}
        <div class="space-y-2">
          {#each servers as server (server.id)}
            <button
              type="button"
              onclick={() => {
                selectedServerId = server.id;
                closeConnection();
                lines = [];
              }}
              class={`w-full rounded-xl border p-3 text-left transition ${selectedServerId === server.id ? 'border-blue-500 bg-blue-50 shadow-sm' : 'border-slate-200 bg-white hover:border-slate-300 hover:bg-slate-50'}`}
            >
              <div class="flex items-center gap-3">
                <div class={`flex h-8 w-8 shrink-0 items-center justify-center rounded-lg ${selectedServerId === server.id ? 'bg-blue-100 text-blue-600' : 'bg-slate-100 text-slate-500'}`}>
                  <ServerIcon size={16} />
                </div>
                <div class="min-w-0 flex-1">
                  <p class="truncate text-sm font-medium text-slate-900">{server.name}</p>
                  <p class="truncate text-xs text-slate-500">{server.username}@{server.host}:{server.port}</p>
                </div>
                {#if selectedServerId === server.id && connectionStatus === 'connected'}
                  <span class="inline-block h-2 w-2 rounded-full bg-green-500"></span>
                {/if}
                {#if selectedServerId === server.id}
                  <ChevronRight size={16} class="text-blue-500" />
                {/if}
              </div>
            </button>
          {/each}
        </div>
      {/if}

      <!-- Quick commands -->
      {#if connectionStatus === 'connected'}
        <div class="mt-6">
          <h3 class="mb-2 text-xs font-semibold uppercase tracking-wider text-slate-500">Quick Commands</h3>
          <div class="flex flex-wrap gap-1.5">
            {#each quickCommands as qc}
              <button
                type="button"
                onclick={() => runQuickCommand(qc.cmd)}
                class="rounded-md border border-slate-200 bg-white px-2.5 py-1 text-xs font-medium text-slate-600 transition hover:border-blue-300 hover:bg-blue-50 hover:text-blue-700"
                title={qc.cmd}
              >
                {qc.label}
              </button>
            {/each}
          </div>
        </div>
      {/if}
    </div>

    <!-- Terminal -->
    <div class="lg:col-span-2">
      {#if !selectedServer}
        <Card padding="lg">
          <div class="flex flex-col items-center justify-center py-16 text-center">
            <TerminalIcon size={32} class="text-slate-300" />
            <p class="mt-4 text-sm text-slate-500">Select a server to start an interactive terminal session.</p>
          </div>
        </Card>
      {:else}
        <Card padding="none">
          <!-- Terminal header -->
          <div class="flex items-center justify-between border-b border-slate-700 bg-slate-800 px-4 py-2.5">
            <div class="flex items-center gap-2">
              <!-- Traffic light dots -->
              <div class="flex items-center gap-1.5">
                <span class="h-3 w-3 rounded-full bg-red-500"></span>
                <span class="h-3 w-3 rounded-full bg-yellow-500"></span>
                <span class="h-3 w-3 rounded-full bg-green-500"></span>
              </div>
              <span class="ml-2 font-mono text-xs text-slate-400">
                {selectedServer.username}@{selectedServer.host}
                {#if hostname}: {hostname}{/if}
              </span>
            </div>
            <div class="flex items-center gap-2">
              <Badge variant={connectionStatusBadge().variant} size="sm">{connectionStatusBadge().label}</Badge>
              {#if connectionStatus === 'connected'}
                <button
                  type="button"
                  onclick={clearTerminal}
                  title="Clear terminal"
                  class="rounded p-1 text-slate-400 hover:bg-slate-700 hover:text-slate-200"
                >
                  <Trash2 size={14} />
                </button>
                <button
                  type="button"
                  onclick={copyLastOutput}
                  title="Copy last output"
                  class="rounded p-1 text-slate-400 hover:bg-slate-700 hover:text-slate-200"
                >
                  <Copy size={14} />
                </button>
              {/if}
            </div>
          </div>

          <!-- Terminal body -->
          {#if connectionStatus !== 'connected'}
            <div class="flex min-h-[450px] flex-col items-center justify-center bg-slate-900 p-4">
              {#if connectionStatus === 'connecting'}
                <div class="flex items-center gap-3 text-slate-400">
                  <Loader2 size={20} class="animate-spin" />
                  <span class="text-sm">Connecting to {selectedServer.name}...</span>
                </div>
              {:else if connectionStatus === 'failed'}
                <div class="text-center">
                  <WifiOff size={28} class="mx-auto text-red-500" />
                  <p class="mt-3 text-sm text-red-400">Connection failed</p>
                  <p class="mt-1 text-xs text-slate-500">Check that the server is online and SSH credentials are correct.</p>
                </div>
              {:else}
                <div class="text-center">
                  <TerminalIcon size={28} class="mx-auto text-slate-600" />
                  <p class="mt-3 text-sm text-slate-500">Ready to connect</p>
                  <p class="mt-1 text-xs text-slate-600">Click "Connect" to start an interactive SSH session.</p>
                </div>
              {/if}
            </div>
          {:else}
            <!-- Terminal output -->
            <div
              bind:this={terminalContainer}
              class="h-[450px] overflow-y-auto bg-slate-900 p-4 font-mono text-sm leading-relaxed"
            >
              {#each lines as line (line.id)}
                <div class="whitespace-pre-wrap break-all {lineColor(line.type)}">
                  {#if line.type === 'input'}
                    <span class="text-green-400">{formatTime(line.timestamp)} </span>
                    <span class="text-cyan-400">{selectedServer.username}@{hostname || selectedServer.host}</span>
                    <span class="text-slate-500">:</span>
                    <span class="text-blue-400">~</span>
                    <span class="text-slate-500">$ </span>
                    <span class="text-slate-100">{line.content}</span>
                  {:else}
                    <span class="text-slate-600">{linePrefix(line.type)}</span>{line.content}
                  {/if}
                </div>
              {/each}
            </div>

            <!-- Input bar -->
            <div class="flex items-center gap-2 border-t border-slate-700 bg-slate-800 px-4 py-2.5">
              <span class="font-mono text-sm text-cyan-400">$</span>
              <input
                bind:this={inputElement}
                bind:value={input}
                onkeydown={handleKeyDown}
                type="text"
                autocomplete="off"
                autocorrect="off"
                autocapitalize="off"
                spellcheck="false"
                placeholder="Type a command and press Enter..."
                class="flex-1 bg-transparent font-mono text-sm text-slate-100 placeholder-slate-600 outline-none"
              />
              <button
                type="button"
                onclick={executeCommand}
                disabled={!input.trim()}
                class="rounded bg-blue-600 px-3 py-1 text-xs font-medium text-white transition hover:bg-blue-700 disabled:opacity-40"
              >
                Run
              </button>
            </div>
          {/if}

          <!-- Action bar -->
          <div class="flex items-center justify-between border-t border-slate-700 bg-slate-800 px-4 py-2.5">
            <div class="flex items-center gap-2">
              {#if connectionStatus === 'connected'}
                <button
                  type="button"
                  onclick={closeConnection}
                  class="inline-flex items-center gap-1.5 rounded-lg border border-red-700 bg-red-900/50 px-3 py-1.5 text-xs font-medium text-red-300 transition hover:bg-red-900"
                >
                  <WifiOff size={12} />
                  Disconnect
                </button>
              {:else}
                <button
                  type="button"
                  onclick={connectTerminal}
                  disabled={connectionStatus === 'connecting'}
                  class="inline-flex items-center gap-1.5 rounded-lg bg-blue-600 px-3 py-1.5 text-xs font-medium text-white transition hover:bg-blue-700 disabled:opacity-60"
                >
                  {#if connectionStatus === 'connecting'}
                    <Loader2 size={12} class="animate-spin" />
                    Connecting...
                  {:else}
                    <Wifi size={12} />
                    Connect
                  {/if}
                </button>
              {/if}
            </div>
            <div class="text-xs text-slate-500">
              {#if connectionStatus === 'connected'}
                {commandHistory.length} commands · ↑/↓ history · Ctrl+L clear
              {:else}
                SSH terminal for {selectedServer.name}
              {/if}
            </div>
          </div>
        </Card>
      {/if}
    </div>
  </div>
</div>

{#snippet emptyIcon()}<TerminalIcon size={22} />{/snippet}
