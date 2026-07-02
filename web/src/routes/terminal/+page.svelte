<script lang="ts">
  import { onMount, onDestroy, tick } from 'svelte';
  import { browser } from '$app/environment';
  import {
    Terminal as TerminalIcon, Server as ServerIcon, RefreshCw,
    Wifi, WifiOff, ChevronRight, Loader2, Maximize2, Copy, Trash2,
    ExternalLink
  } from 'lucide-svelte';
  import { api } from '$lib/api/client';
  import { type Server } from '$lib/stores/servers';
  import { Badge, Card, EmptyState, PageHeader, Skeleton, Spinner } from '$lib/components/ui';
  import { toast } from '$lib/stores/toast';

  // xterm CSS — must be imported for the terminal to render
  import '@xterm/xterm/css/xterm.css';

  // Lazy-load xterm JS only in browser (it needs DOM)
  let TerminalCtor: any = null;
  let FitAddonCtor: any = null;
  let WebLinksAddonCtor: any = null;

  // --- Types ---
  type WSMessage =
    | { type: 'connected'; hostname: string; os: string }
    | { type: 'output'; data: string }
    | { type: 'error'; message: string }
    | { type: 'closed'; data: string };

  // --- State ---
  let servers = $state([] as Server[]);
  let loading = $state(true);
  let selectedServerId = $state<number | null>(null);
  let wsConnection: WebSocket | null = null;
  let connectionStatus = $state<'idle' | 'connecting' | 'connected' | 'failed'>('idle');
  let hostname = $state('');

  // Terminal emulator
  let terminalContainer = $state<HTMLDivElement | null>(null);
  let term: any = null;
  let fitAddon: any = null;
  let resizeObserver: ResizeObserver | null = null;

  const selectedServer = $derived.by(() => servers.find(s => s.id === selectedServerId) || null);

  // --- Lifecycle ---
  onMount(async () => {
    // Pre-load xterm modules in the browser
    if (browser) {
      try {
        const [xtermMod, fitMod, linksMod] = await Promise.all([
          import('@xterm/xterm'),
          import('@xterm/addon-fit'),
          import('@xterm/addon-web-links')
        ]);
        TerminalCtor = xtermMod.Terminal;
        FitAddonCtor = fitMod.FitAddon;
        WebLinksAddonCtor = linksMod.WebLinksAddon;
      } catch (err) {
        console.error('Failed to load xterm modules:', err);
      }
    }
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
  async function connectTerminal() {
    if (!selectedServerId) return;
    if (!TerminalCtor || !FitAddonCtor || !WebLinksAddonCtor) {
      toast.error('Terminal modules not loaded yet. Please try again.');
      return;
    }
    closeConnection();
    connectionStatus = 'connecting';

    // Create xterm instance
    term = new TerminalCtor({
      cols: 80,
      rows: 24,
      cursorBlink: true,
      fontSize: 14,
      fontFamily: "'JetBrains Mono', 'Fira Code', 'Cascadia Code', 'Source Code Pro', Menlo, Monaco, 'Courier New', monospace",
      theme: {
        background: '#0f172a',
        foreground: '#e2e8f0',
        cursor: '#38bdf8',
        cursorAccent: '#0f172a',
        selectionBackground: '#334155',
        black: '#1e293b',
        red: '#ef4444',
        green: '#22c55e',
        yellow: '#eab308',
        blue: '#3b82f6',
        magenta: '#a855f7',
        cyan: '#06b6d4',
        white: '#e2e8f0',
        brightBlack: '#475569',
        brightRed: '#f87171',
        brightGreen: '#4ade80',
        brightYellow: '#facc15',
        brightBlue: '#60a5fa',
        brightMagenta: '#c084fc',
        brightCyan: '#22d3ee',
        brightWhite: '#f8fafc'
      },
      allowProposedApi: true,
      scrollback: 5000,
      convertEol: false
    });

    fitAddon = new FitAddonCtor();
    term.loadAddon(fitAddon);
    term.loadAddon(new WebLinksAddonCtor());

    // Wait for the container to be in the DOM
    await tick();
    if (terminalContainer) {
      term.open(terminalContainer);
      try {
        fitAddon.fit();
      } catch { /* ignore fit errors */ }
    }

    // Handle user input → send to WebSocket
    term.onData((data: string) => {
      if (wsConnection && wsConnection.readyState === WebSocket.OPEN) {
        wsConnection.send(JSON.stringify({ type: 'input', data }));
      }
    });

    // Handle resize → send new size to backend
    term.onResize(({ cols, rows }: { cols: number; rows: number }) => {
      if (wsConnection && wsConnection.readyState === WebSocket.OPEN) {
        wsConnection.send(JSON.stringify({ type: 'resize', cols, rows }));
      }
    });

    // Observe container resize
    if (terminalContainer) {
      resizeObserver = new ResizeObserver(() => {
        if (fitAddon && term) {
          try {
            fitAddon.fit();
          } catch { /* ignore */ }
        }
      });
      resizeObserver.observe(terminalContainer);
    }

    // Connect WebSocket
    const proto = location.protocol === 'https:' ? 'wss' : 'ws';
    const token = typeof localStorage !== 'undefined' ? localStorage.getItem('meshium_session_token') : null;
    const tokenParam = token ? `&token=${encodeURIComponent(token)}` : '';
    const cols = term.cols || 80;
    const rows = term.rows || 24;
    const url = `${proto}://${location.host}/ws/terminal/${selectedServerId}?cols=${cols}&rows=${rows}${tokenParam}`;

    try {
      wsConnection = new WebSocket(url);
    } catch {
      connectionStatus = 'failed';
      toast.error('Failed to open WebSocket connection');
      return;
    }

    wsConnection.onopen = () => {
      // Connection established, waiting for "connected" message from server
    };

    wsConnection.onmessage = (event) => {
      try {
        const msg = JSON.parse(event.data) as WSMessage;
        if (msg.type === 'connected') {
          connectionStatus = 'connected';
          hostname = msg.hostname || '';
          if (term) {
            term.focus();
          }
        } else if (msg.type === 'output') {
          if (term) {
            term.write(msg.data);
          }
        } else if (msg.type === 'error') {
          if (term) {
            term.write(`\r\n\x1b[31m${msg.message}\x1b[0m\r\n`);
          }
        } else if (msg.type === 'closed') {
          if (term) {
            term.write(`\r\n\x1b[90m${msg.data}\x1b[0m\r\n`);
          }
          connectionStatus = 'idle';
        }
      } catch {
        // ignore parse errors
      }
    };

    wsConnection.onclose = () => {
      if (connectionStatus === 'connecting') {
        connectionStatus = 'failed';
      } else if (connectionStatus === 'connected') {
        if (term) {
          term.write('\r\n\x1b[90m— Connection closed —\x1b[0m\r\n');
        }
        connectionStatus = 'idle';
      }
      wsConnection = null;
    };

    wsConnection.onerror = () => {
      connectionStatus = 'failed';
      if (term) {
        term.write('\r\n\x1b[31m✗ WebSocket connection error\x1b[0m\r\n');
      }
    };
  }

  function closeConnection() {
    if (wsConnection) {
      wsConnection.close();
      wsConnection = null;
    }
    if (resizeObserver) {
      resizeObserver.disconnect();
      resizeObserver = null;
    }
    if (term) {
      term.dispose();
      term = null;
    }
    if (fitAddon) {
      fitAddon = null;
    }
    connectionStatus = 'idle';
    hostname = '';
  }

  function clearTerminal() {
    if (term) {
      term.clear();
    }
  }

  function copyAll() {
    if (term) {
      const buffer = term.buffer.active;
      const lines: string[] = [];
      for (let i = 0; i < buffer.length; i++) {
        const line = buffer.getLine(i);
        if (line) {
          lines.push(line.translateToString(true));
        }
      }
      const text = lines.join('\n').trimEnd();
      if (text) {
        navigator.clipboard.writeText(text).then(() => {
          toast.success('Terminal content copied');
        });
      }
    }
  }

  function focusTerminal() {
    if (term) {
      term.focus();
    }
  }

  function fitTerminal() {
    if (fitAddon && term) {
      try {
        fitAddon.fit();
      } catch { /* ignore */ }
    }
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
  <PageHeader title="Terminal" subtitle="Real-time interactive SSH terminal — full PTY support with colors, interactive commands, and live streaming.">
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
                  <span class="inline-block h-2 w-2 rounded-full bg-green-500 animate-pulse"></span>
                {/if}
                {#if selectedServerId === server.id}
                  <ChevronRight size={16} class="text-blue-500" />
                {/if}
              </div>
            </button>
          {/each}
        </div>
      {/if}

      <!-- Info panel when connected -->
      {#if connectionStatus === 'connected'}
        <div class="mt-6 rounded-xl border border-slate-200 bg-slate-50 p-4">
          <h3 class="mb-3 text-xs font-semibold uppercase tracking-wider text-slate-500">Session Info</h3>
          <dl class="space-y-2 text-sm">
            <div class="flex justify-between">
              <dt class="text-slate-500">Host</dt>
              <dd class="font-mono text-slate-700">{selectedServer?.host}</dd>
            </div>
            <div class="flex justify-between">
              <dt class="text-slate-500">User</dt>
              <dd class="font-mono text-slate-700">{selectedServer?.username}</dd>
            </div>
            {#if hostname}
              <div class="flex justify-between">
                <dt class="text-slate-500">Hostname</dt>
                <dd class="font-mono text-slate-700">{hostname}</dd>
              </div>
            {/if}
          </dl>
          <div class="mt-4 space-y-2">
            <button
              type="button"
              onclick={clearTerminal}
              class="flex w-full items-center gap-2 rounded-lg border border-slate-200 bg-white px-3 py-2 text-xs font-medium text-slate-600 transition hover:bg-slate-50"
            >
              <Trash2 size={14} /> Clear Terminal
            </button>
            <button
              type="button"
              onclick={copyAll}
              class="flex w-full items-center gap-2 rounded-lg border border-slate-200 bg-white px-3 py-2 text-xs font-medium text-slate-600 transition hover:bg-slate-50"
            >
              <Copy size={14} /> Copy All Output
            </button>
            <button
              type="button"
              onclick={fitTerminal}
              class="flex w-full items-center gap-2 rounded-lg border border-slate-200 bg-white px-3 py-2 text-xs font-medium text-slate-600 transition hover:bg-slate-50"
            >
              <Maximize2 size={14} /> Fit to Container
            </button>
          </div>
        </div>

        <div class="mt-4 rounded-xl border border-blue-100 bg-blue-50 p-3">
          <p class="text-xs text-blue-700">
            <strong>Real PTY Terminal</strong> — supports interactive commands (top, vim, htop), ANSI colors, Ctrl+C, and live streaming.
          </p>
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
        <div class="overflow-hidden rounded-xl border border-slate-700 shadow-lg">
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
                  onclick={focusTerminal}
                  title="Focus terminal"
                  class="rounded p-1 text-slate-400 hover:bg-slate-700 hover:text-slate-200"
                >
                  <ExternalLink size={14} />
                </button>
              {/if}
            </div>
          </div>

          <!-- Terminal body -->
          {#if connectionStatus !== 'connected'}
            <div class="flex min-h-[500px] flex-col items-center justify-center bg-slate-900 p-4">
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
                  <p class="mt-1 text-xs text-slate-600">Click "Connect" to start a real interactive SSH terminal session.</p>
                  <p class="mt-2 text-xs text-slate-600">Full PTY support — interactive commands, colors, streaming output.</p>
                </div>
              {/if}
            </div>
          {:else}
            <!-- xterm.js terminal -->
            <div
              bind:this={terminalContainer}
              class="h-[500px] bg-slate-900 p-2"
            ></div>
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
                Real PTY terminal · Interactive commands supported · Ctrl+C works
              {:else}
                SSH terminal for {selectedServer.name}
              {/if}
            </div>
          </div>
        </div>
      {/if}
    </div>
  </div>
</div>

{#snippet emptyIcon()}<TerminalIcon size={22} />{/snippet}
