<script lang="ts">
  import { onMount, onDestroy, tick } from 'svelte';
  import { browser } from '$app/environment';
  import {
    Terminal as TerminalIcon, Server as ServerIcon, RefreshCw,
    Wifi, WifiOff, ChevronRight, Loader2, Maximize2, Copy, Trash2,
    ExternalLink, ArrowUp, ArrowDown, ArrowLeft, ArrowRight,
    ChevronUp, ChevronDown, Keyboard, X
  } from 'lucide-svelte';
  import { api } from '$lib/api/client';
  import { wsConnectGeneric } from '$lib/api/websocket';
  import { type Server } from '$lib/stores/servers';
  import { Badge, Card, EmptyState, PageHeader, Spinner } from '$lib/components/ui';
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

  // Special key state
  let ctrlActive = $state(false);
  let altActive = $state(false);
  let showSpecialKeys = $state(false);
  let isMobile = $state(false);

  const selectedServer = $derived.by(() => servers.find(s => s.id === selectedServerId) || null);

  // --- Lifecycle ---
  onMount(async () => {
    // Detect mobile
    if (browser) {
      const checkMobile = () => {
        isMobile = window.innerWidth < 768 || /Android|iPhone|iPad|iPod/i.test(navigator.userAgent);
        if (isMobile) showSpecialKeys = true;
      };
      checkMobile();
      window.addEventListener('resize', checkMobile);

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

    const cols = term.cols || 80;
    const rows = term.rows || 24;
    const path = `/ws/terminal/${selectedServerId}?cols=${cols}&rows=${rows}`;

    try {
      const socket = wsConnectGeneric<WSMessage>(
        path,
        (msg) => {
          if (wsConnection !== socket) return;
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
        },
        () => {
          if (wsConnection !== socket) return;
          connectionStatus = 'failed';
          if (term) {
            term.write('\r\n\x1b[31m✗ WebSocket connection error\x1b[0m\r\n');
          }
        },
        () => {
          if (wsConnection !== socket) return;
          if (connectionStatus === 'connecting') {
            connectionStatus = 'failed';
          } else if (connectionStatus === 'connected') {
            if (term) {
              term.write('\r\n\x1b[90m— Connection closed —\x1b[0m\r\n');
            }
            connectionStatus = 'idle';
          }
          wsConnection = null;
        }
      );
      wsConnection = socket;
    } catch {
      connectionStatus = 'failed';
      toast.error('Failed to open WebSocket connection');
      return;
    }
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

  // --- Special key sending ---
  function sendKey(data: string) {
    if (wsConnection && wsConnection.readyState === WebSocket.OPEN) {
      wsConnection.send(JSON.stringify({ type: 'input', data }));
    }
    if (term) {
      term.focus();
    }
  }

  function sendSpecialKey(key: string) {
    // Escape sequences for special keys
    const keys: Record<string, string> = {
      'esc': '\x1b',
      'tab': '\t',
      'arrowup': '\x1b[A',
      'arrowdown': '\x1b[B',
      'arrowright': '\x1b[C',
      'arrowleft': '\x1b[D',
      'home': '\x1b[H',
      'end': '\x1b[F',
      'pageup': '\x1b[5~',
      'pagedown': '\x1b[6~',
      'insert': '\x1b[2~',
      'delete': '\x1b[3~',
      'backspace': '\x7f',
      'enter': '\r',
      'space': ' ',
      'pipe': '|',
      'tilde': '~',
      'backtick': '`',
      'dollar': '$',
      'ampersand': '&',
      'semicolon': ';',
      'asterisk': '*',
      'question': '?',
      'exclaim': '!',
      'hash': '#',
      'at': '@',
      'slash': '/',
      'backslash': '\\',
      'doublequote': '"',
      'singlequote': "'",
      'lessthan': '<',
      'greaterthan': '>',
      'equals': '=',
      'plus': '+',
      'minus': '-',
      'underscore': '_',
      'caret': '^',
      'percent': '%',
      'openbracket': '[',
      'closebracket': ']',
      'openbrace': '{',
      'closebrace': '}',
      'openparen': '(',
      'closeparen': ')',
    };
    const data = keys[key] ?? '';
    if (data) {
      sendKey(data);
    }
    // Reset modifiers after sending
    ctrlActive = false;
    altActive = false;
  }

  function sendCtrlCombo(char: string) {
    // Ctrl+letter → control character (e.g., Ctrl+C = \x03)
    const code = char.toUpperCase().charCodeAt(0) - 64;
    if (code >= 0 && code <= 31) {
      sendKey(String.fromCharCode(code));
    }
    ctrlActive = false;
    term?.focus();
  }

  function toggleCtrl() {
    ctrlActive = !ctrlActive;
    if (!ctrlActive) altActive = false;
  }

  function toggleAlt() {
    altActive = !altActive;
    if (!altActive) ctrlActive = false;
  }

  function connectionStatusBadge() {
    switch (connectionStatus) {
      case 'idle': return { label: 'Idle', variant: 'neutral' as const };
      case 'connecting': return { label: 'Connecting...', variant: 'warning' as const };
      case 'connected': return { label: 'Connected', variant: 'success' as const };
      case 'failed': return { label: 'Failed', variant: 'error' as const };
    }
  }

  // Ctrl+key shortcuts
  const ctrlKeys = [
    { label: 'C', char: 'c', desc: 'Interrupt' },
    { label: 'D', char: 'd', desc: 'EOF' },
    { label: 'Z', char: 'z', desc: 'Suspend' },
    { label: 'L', char: 'l', desc: 'Clear' },
    { label: 'R', char: 'r', desc: 'Reverse search' },
    { label: 'W', char: 'w', desc: 'Delete word' },
    { label: 'U', char: 'u', desc: 'Delete line' },
    { label: 'A', char: 'a', desc: 'Line start' },
    { label: 'E', char: 'e', desc: 'Line end' },
  ];

  // Special characters
  const specialChars = [
    { label: '|', key: 'pipe' },
    { label: '~', key: 'tilde' },
    { label: '`', key: 'backtick' },
    { label: '$', key: 'dollar' },
    { label: '&', key: 'ampersand' },
    { label: ';', key: 'semicolon' },
    { label: '*', key: 'asterisk' },
    { label: '?', key: 'question' },
    { label: '!', key: 'exclaim' },
    { label: '#', key: 'hash' },
    { label: '@', key: 'at' },
    { label: '/', key: 'slash' },
    { label: '\\', key: 'backslash' },
    { label: '"', key: 'doublequote' },
    { label: "'", key: 'singlequote' },
    { label: '<', key: 'lessthan' },
    { label: '>', key: 'greaterthan' },
    { label: '=', key: 'equals' },
    { label: '+', key: 'plus' },
    { label: '-', key: 'minus' },
    { label: '_', key: 'underscore' },
    { label: '^', key: 'caret' },
    { label: '%', key: 'percent' },
    { label: '[', key: 'openbracket' },
    { label: ']', key: 'closebracket' },
    { label: '{', key: 'openbrace' },
    { label: '}', key: 'closebrace' },
    { label: '(', key: 'openparen' },
    { label: ')', key: 'closeparen' },
  ];
</script>

<svelte:head><title>Terminal - Meshium</title></svelte:head>

<div class="p-4 sm:p-6 max-w-7xl mx-auto">
  <PageHeader title="Terminal" subtitle="Real-time interactive SSH terminal — full PTY support with colors, interactive commands, and live streaming.">
    {#snippet actions()}
      <button
        type="button"
        onclick={loadServers}
        disabled={loading}
        class="inline-flex items-center gap-2 rounded-lg border border-border-strong bg-surface px-4 py-2 text-sm font-medium text-fg-muted hover:bg-surface-muted focus-visible:ring-2 focus-visible:ring-accent focus-visible:ring-offset-2 disabled:opacity-60"
        aria-label="Refresh server list"
      >
        {#if loading}<Spinner size="sm" label="Refreshing" />{:else}<RefreshCw size={16} />{/if}
        Refresh
      </button>
      <button
        type="button"
        onclick={() => showSpecialKeys = !showSpecialKeys}
        class={`inline-flex items-center gap-2 rounded-lg border px-4 py-2 text-sm font-medium transition focus-visible:ring-2 focus-visible:ring-accent focus-visible:ring-offset-2 ${showSpecialKeys ? 'border-accent bg-accent-subtle text-accent' : 'border-border-strong bg-surface text-fg-muted hover:bg-surface-muted'}`}
        aria-label="Toggle special keys"
        aria-pressed={showSpecialKeys}
      >
        <Keyboard size={16} />
        Keys
      </button>
    {/snippet}
  </PageHeader>

  <div class="grid gap-6 lg:grid-cols-3">
    <!-- Server list -->
    <div class="lg:col-span-1">
      <h2 class="mb-3 text-sm font-semibold text-fg-muted">Select Server</h2>
      {#if loading}
        <div class="flex items-center justify-center gap-3 py-8 text-fg-subtle">
          <Spinner size="md" label="Loading servers" />
          <span class="text-sm">Loading servers...</span>
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
              class={`w-full rounded-xl border p-3 text-left transition focus-visible:ring-2 focus-visible:ring-accent focus-visible:ring-offset-2 ${selectedServerId === server.id ? 'border-accent bg-accent-subtle shadow-sm' : 'border-border bg-surface hover:border-border-strong hover:bg-surface-muted'}`}
              aria-pressed={selectedServerId === server.id}
            >
              <div class="flex items-center gap-3">
                <div class={`flex h-8 w-8 shrink-0 items-center justify-center rounded-lg ${selectedServerId === server.id ? 'bg-accent-subtle text-accent' : 'bg-surface-muted text-fg-subtle'}`} aria-hidden="true">
                  <ServerIcon size={16} />
                </div>
                <div class="min-w-0 flex-1">
                  <p class="truncate text-sm font-medium text-fg">{server.name}</p>
                  <p class="truncate text-xs text-fg-subtle">{server.username}@{server.host}:{server.port}</p>
                </div>
                {#if selectedServerId === server.id && connectionStatus === 'connected'}
                  <span class="inline-block h-2 w-2 rounded-full bg-success animate-pulse" aria-label="Connected"></span>
                {/if}
                {#if selectedServerId === server.id}
                  <ChevronRight size={16} class="text-accent" aria-hidden="true" />
                {/if}
              </div>
            </button>
          {/each}
        </div>
      {/if}

      <!-- Info panel when connected -->
      {#if connectionStatus === 'connected'}
        <div class="mt-6 rounded-xl border border-border bg-surface-muted p-4">
          <h3 class="mb-3 text-xs font-semibold uppercase tracking-wider text-fg-subtle">Session Info</h3>
          <dl class="space-y-2 text-sm">
            <div class="flex justify-between">
              <dt class="text-fg-subtle">Host</dt>
              <dd class="font-mono text-fg-muted">{selectedServer?.host}</dd>
            </div>
            <div class="flex justify-between">
              <dt class="text-fg-subtle">User</dt>
              <dd class="font-mono text-fg-muted">{selectedServer?.username}</dd>
            </div>
            {#if hostname}
              <div class="flex justify-between">
                <dt class="text-fg-subtle">Hostname</dt>
                <dd class="font-mono text-fg-muted">{hostname}</dd>
              </div>
            {/if}
          </dl>
          <div class="mt-4 space-y-2">
            <button
              type="button"
              onclick={clearTerminal}
              class="flex w-full items-center gap-2 rounded-lg border border-border bg-surface px-3 py-2 text-xs font-medium text-fg-muted transition hover:bg-surface-muted focus-visible:ring-2 focus-visible:ring-accent focus-visible:ring-offset-2"
            >
              <Trash2 size={14} /> Clear Terminal
            </button>
            <button
              type="button"
              onclick={copyAll}
              class="flex w-full items-center gap-2 rounded-lg border border-border bg-surface px-3 py-2 text-xs font-medium text-fg-muted transition hover:bg-surface-muted focus-visible:ring-2 focus-visible:ring-accent focus-visible:ring-offset-2"
            >
              <Copy size={14} /> Copy All Output
            </button>
            <button
              type="button"
              onclick={fitTerminal}
              class="flex w-full items-center gap-2 rounded-lg border border-border bg-surface px-3 py-2 text-xs font-medium text-fg-muted transition hover:bg-surface-muted focus-visible:ring-2 focus-visible:ring-accent focus-visible:ring-offset-2"
            >
              <Maximize2 size={14} /> Fit to Container
            </button>
          </div>
        </div>

        <div class="mt-4 rounded-xl border border-accent/20 bg-accent/10 p-3">
          <p class="text-xs text-accent">
            <strong>Real PTY Terminal</strong> — supports interactive commands (top, vim, htop), ANSI colors, Ctrl+C, and live streaming.
          </p>
        </div>
      {/if}
    </div>

    <!-- Terminal -->
    <div class="min-w-0 lg:col-span-2">
      {#if !selectedServer}
        <Card padding="lg">
          <div class="flex flex-col items-center justify-center py-16 text-center">
            <TerminalIcon size={32} class="text-fg-subtle" aria-hidden="true" />
            <p class="mt-4 text-sm text-fg-subtle">Select a server to start an interactive terminal session.</p>
          </div>
        </Card>
      {:else}
        <div class="overflow-hidden rounded-xl border border-border shadow-lg">
          <!-- Terminal header -->
          <div class="flex items-center justify-between border-b border-border bg-surface px-4 py-2.5">
            <div class="flex items-center gap-2">
              <!-- Traffic light dots -->
              <div class="flex items-center gap-1.5" aria-hidden="true">
                <span class="h-3 w-3 rounded-full bg-error"></span>
                <span class="h-3 w-3 rounded-full bg-warning"></span>
                <span class="h-3 w-3 rounded-full bg-success"></span>
              </div>
              <span class="ml-2 font-mono text-xs text-fg-subtle">
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
                  aria-label="Focus terminal"
                  class="rounded p-1 text-fg-subtle hover:bg-surface-muted hover:text-fg focus-visible:ring-2 focus-visible:ring-accent"
                >
                  <ExternalLink size={14} />
                </button>
              {/if}
            </div>
          </div>

          <!-- Terminal body — container ALWAYS in DOM so term.open() has a valid element.
               Overlay (connecting/failed/idle) is absolute-positioned on top. -->
          <div class="relative">
            <!-- xterm.js terminal container — always rendered -->
            <div
              bind:this={terminalContainer}
              class="h-[400px] sm:h-[500px] overflow-hidden bg-surface-muted p-2"
            ></div>

            <!-- Overlay: shown when not yet connected -->
            {#if connectionStatus !== 'connected'}
              <div class="absolute inset-0 flex flex-col items-center justify-center bg-surface-muted p-4">
                {#if connectionStatus === 'connecting'}
                  <div class="flex items-center gap-3 text-fg-subtle">
                    <Loader2 size={20} class="animate-spin" />
                    <span class="text-sm">Connecting to {selectedServer.name}...</span>
                  </div>
                {:else if connectionStatus === 'failed'}
                  <div class="text-center">
                    <WifiOff size={28} class="mx-auto text-error" aria-hidden="true" />
                    <p class="mt-3 text-sm text-error">Connection failed</p>
                    <p class="mt-1 text-xs text-fg-subtle">Check that the server is online and SSH credentials are correct.</p>
                  </div>
                {:else}
                  <div class="text-center">
                    <TerminalIcon size={28} class="mx-auto text-fg-subtle" aria-hidden="true" />
                    <p class="mt-3 text-sm text-fg-subtle">Ready to connect</p>
                    <p class="mt-1 text-xs text-fg-subtle">Click "Connect" to start a real interactive SSH terminal session.</p>
                    <p class="mt-2 text-xs text-fg-subtle">Full PTY support — interactive commands, colors, streaming output.</p>
                  </div>
                {/if}
              </div>
            {/if}
          </div>

          <!-- Special Keys Bar (always visible when connected, toggleable) -->
          {#if showSpecialKeys && connectionStatus === 'connected'}
            <div class="border-t border-border bg-surface p-2">
              <!-- Modifier keys row -->
              <div class="mb-2 flex flex-wrap items-center gap-1.5">
                <button
                  type="button"
                  onclick={toggleCtrl}
                  class={`rounded px-3 py-1.5 text-xs font-bold transition focus-visible:ring-2 focus-visible:ring-accent ${ctrlActive ? 'bg-accent text-accent-fg' : 'bg-surface-muted text-fg-muted hover:bg-border'}`}
                  aria-pressed={ctrlActive}
                  title="Ctrl modifier — tap then a key"
                >
                  CTRL
                </button>
                <button
                  type="button"
                  onclick={toggleAlt}
                  class={`rounded px-3 py-1.5 text-xs font-bold transition focus-visible:ring-2 focus-visible:ring-accent ${altActive ? 'bg-accent text-accent-fg' : 'bg-surface-muted text-fg-muted hover:bg-border'}`}
                  aria-pressed={altActive}
                  title="Alt modifier — tap then a key"
                >
                  ALT
                </button>

                <div class="mx-1 h-6 w-px bg-border-strong"></div>

                <!-- ESC -->
                <button
                  type="button"
                  onclick={() => sendSpecialKey('esc')}
                  class="rounded bg-surface-muted px-3 py-1.5 text-xs font-semibold text-fg-muted transition hover:bg-border focus-visible:ring-2 focus-visible:ring-accent"
                  aria-label="Escape key"
                >
                  ESC
                </button>
                <!-- Tab -->
                <button
                  type="button"
                  onclick={() => sendSpecialKey('tab')}
                  class="rounded bg-surface-muted px-3 py-1.5 text-xs font-semibold text-fg-muted transition hover:bg-border focus-visible:ring-2 focus-visible:ring-accent"
                  aria-label="Tab key"
                >
                  TAB
                </button>
                <!-- Enter -->
                <button
                  type="button"
                  onclick={() => sendSpecialKey('enter')}
                  class="rounded bg-surface-muted px-3 py-1.5 text-xs font-semibold text-fg-muted transition hover:bg-border focus-visible:ring-2 focus-visible:ring-accent"
                  aria-label="Enter key"
                >
                  ⏎
                </button>
                <!-- Backspace -->
                <button
                  type="button"
                  onclick={() => sendSpecialKey('backspace')}
                  class="rounded bg-surface-muted px-3 py-1.5 text-xs font-semibold text-fg-muted transition hover:bg-border focus-visible:ring-2 focus-visible:ring-accent"
                  aria-label="Backspace key"
                >
                  ⌫
                </button>

                <div class="mx-1 h-6 w-px bg-border-strong"></div>

                <!-- Arrow keys -->
                <button
                  type="button"
                  onclick={() => sendSpecialKey('arrowup')}
                  class="rounded bg-surface-muted px-2.5 py-1.5 text-fg-muted transition hover:bg-border focus-visible:ring-2 focus-visible:ring-accent"
                  aria-label="Arrow up"
                >
                  <ChevronUp size={14} />
                </button>
                <button
                  type="button"
                  onclick={() => sendSpecialKey('arrowdown')}
                  class="rounded bg-surface-muted px-2.5 py-1.5 text-fg-muted transition hover:bg-border focus-visible:ring-2 focus-visible:ring-accent"
                  aria-label="Arrow down"
                >
                  <ChevronDown size={14} />
                </button>
                <button
                  type="button"
                  onclick={() => sendSpecialKey('arrowleft')}
                  class="rounded bg-surface-muted px-2.5 py-1.5 text-fg-muted transition hover:bg-border focus-visible:ring-2 focus-visible:ring-accent"
                  aria-label="Arrow left"
                >
                  <ArrowLeft size={14} />
                </button>
                <button
                  type="button"
                  onclick={() => sendSpecialKey('arrowright')}
                  class="rounded bg-surface-muted px-2.5 py-1.5 text-fg-muted transition hover:bg-border focus-visible:ring-2 focus-visible:ring-accent"
                  aria-label="Arrow right"
                >
                  <ArrowRight size={14} />
                </button>

                <div class="mx-1 h-6 w-px bg-border-strong"></div>

                <!-- Home / End / Page Up / Page Down -->
                <button
                  type="button"
                  onclick={() => sendSpecialKey('home')}
                  class="rounded bg-surface-muted px-2.5 py-1.5 text-xs font-semibold text-fg-muted transition hover:bg-border focus-visible:ring-2 focus-visible:ring-accent"
                  aria-label="Home key"
                >
                  Home
                </button>
                <button
                  type="button"
                  onclick={() => sendSpecialKey('end')}
                  class="rounded bg-surface-muted px-2.5 py-1.5 text-xs font-semibold text-fg-muted transition hover:bg-border focus-visible:ring-2 focus-visible:ring-accent"
                  aria-label="End key"
                >
                  End
                </button>
                <button
                  type="button"
                  onclick={() => sendSpecialKey('pageup')}
                  class="rounded bg-surface-muted px-2.5 py-1.5 text-xs font-semibold text-fg-muted transition hover:bg-border focus-visible:ring-2 focus-visible:ring-accent"
                  aria-label="Page up key"
                >
                  PgUp
                </button>
                <button
                  type="button"
                  onclick={() => sendSpecialKey('pagedown')}
                  class="rounded bg-surface-muted px-2.5 py-1.5 text-xs font-semibold text-fg-muted transition hover:bg-border focus-visible:ring-2 focus-visible:ring-accent"
                  aria-label="Page down key"
                >
                  PgDn
                </button>
              </div>

              <!-- Ctrl+key shortcuts (visible when ctrl is active or always shown) -->
              {#if ctrlActive}
                <div class="mb-2 flex flex-wrap items-center gap-1.5 rounded-lg bg-bg/50 p-2">
                  <span class="mr-1 text-xs font-medium text-fg-subtle">Ctrl +</span>
                  {#each ctrlKeys as ck}
                    <button
                      type="button"
                      onclick={() => sendCtrlCombo(ck.char)}
                      class="rounded bg-surface-muted px-2.5 py-1.5 text-xs font-mono font-semibold text-fg-muted transition hover:bg-accent hover:text-accent-fg focus-visible:ring-2 focus-visible:ring-accent"
                      title={`Ctrl+${ck.label} — ${ck.desc}`}
                      aria-label={`Ctrl+${ck.label} — ${ck.desc}`}
                    >
                      {ck.label}
                    </button>
                  {/each}
                </div>
              {/if}

              <!-- Special characters row -->
              <div class="flex flex-wrap items-center gap-1">
                {#each specialChars as sc}
                  <button
                    type="button"
                    onclick={() => sendSpecialKey(sc.key)}
                    class="rounded bg-surface-muted px-2 py-1.5 text-xs font-mono text-fg-muted transition hover:bg-border focus-visible:ring-2 focus-visible:ring-accent"
                    aria-label={`Send ${sc.label}`}
                  >
                    {sc.label}
                  </button>
                {/each}
              </div>
            </div>
          {/if}

          <!-- Action bar -->
          <div class="flex items-center justify-between border-t border-border bg-surface px-4 py-2.5">
            <div class="flex items-center gap-2">
              {#if connectionStatus === 'connected'}
                <button
                  type="button"
                  onclick={closeConnection}
                  class="inline-flex items-center gap-1.5 rounded-lg border border-error/40 bg-error/15 px-3 py-1.5 text-xs font-medium text-error transition hover:bg-error/25 focus-visible:ring-2 focus-visible:ring-error"
                >
                  <WifiOff size={12} />
                  Disconnect
                </button>
              {:else}
                <button
                  type="button"
                  onclick={connectTerminal}
                  disabled={connectionStatus === 'connecting'}
                  class="inline-flex items-center gap-1.5 rounded-lg bg-accent px-3 py-1.5 text-xs font-medium text-accent-fg transition hover:bg-accent-hover focus-visible:ring-2 focus-visible:ring-accent disabled:opacity-60"
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
            <div class="text-xs text-fg-subtle">
              {#if connectionStatus === 'connected'}
                Real PTY · Interactive commands · Ctrl+C works
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
