<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { api } from '$lib/api/client';
  import {
    assistantApi,
    type AssistantRole,
    type ChatMessage,
    type ChatResponse,
    type CommandHelp,
    type LogAnalysis
  } from '$lib/api/assistant';
  import type { Server } from '$lib/stores/servers';
  import { Badge, EmptyState, Modal, PageHeader, Skeleton, Spinner, Card } from '$lib/components/ui';
  import { toast } from '$lib/stores/toast';
  import {
    Activity,
    Bot,
    Check,
    ClipboardCopy,
    Copy,
    Database,
    FileText,
    HelpCircle,
    RefreshCw,
    Send,
    Sparkles,
    Server as ServerIcon,
    ShieldAlert,
    Trash2,
    Wand2
  } from 'lucide-svelte';

  type RenderBlock =
    | { type: 'paragraph'; text: string }
    | { type: 'list'; items: string[] }
    | { type: 'code'; language: string; code: string };

  type InlineSegment =
    | { type: 'text'; text: string }
    | { type: 'bold'; text: string }
    | { type: 'code'; text: string };

  interface ConversationMessage extends ChatMessage {
    id: string;
    suggestions?: string[];
    commands?: string[];
    context?: string;
    error?: boolean;
  }

  const STORAGE_KEY = 'meshium-assistant-conversation-v1';
  const MAX_HISTORY = 16;
  const starterPrompts = [
    'Summarize this server for me',
    'What should I check before migrating this server?',
    'What packages are installed on this server?',
    'Why is nginx not running?',
    'Help me understand this log entry'
  ];

  let servers = $state<Server[]>([]);
  let loadingServers = $state(true);
  let selectedServerId = $state<number | null>(null);
  let messages = $state<ConversationMessage[]>([]);
  let input = $state('');
  let sending = $state(false);
  let hydrated = $state(false);
  let suggestions = $state<string[]>([]);
  let suggestionsLoading = $state(false);
  let suggestionsError = $state<string | null>(null);
  let messagesContainer: HTMLDivElement | null = null;
  let inputRef: HTMLTextAreaElement | null = null;

  let logModalOpen = $state(false);
  let logInput = $state('');
  let logAnalysis = $state<LogAnalysis | null>(null);
  let logAnalysisLoading = $state(false);
  let logAnalysisError = $state<string | null>(null);

  let commandHelpOpen = $state(false);
  let commandHelp = $state<CommandHelp | null>(null);
  let commandHelpLoading = $state(false);
  let commandHelpError = $state<string | null>(null);

  const selectedServer = $derived(
    servers.find((server) => server.id === selectedServerId) ?? null
  );

  const conversationSeed = $derived.by(() =>
    messages
      .map(({ role, content, timestamp }) => ({ role, content, timestamp }))
      .slice(-MAX_HISTORY)
  );

  onMount(async () => {
    await Promise.all([loadServers(), restoreConversation()]);
    hydrated = true;
    await loadSuggestions(selectedServerId ?? undefined);
    await scrollToBottom(false);
  });

  $effect(() => {
    if (!hydrated || typeof localStorage === 'undefined') return;
    const payload = {
      selectedServerId,
      messages: messages.map(({ id, ...rest }) => rest).slice(-64)
    };
    localStorage.setItem(STORAGE_KEY, JSON.stringify(payload));
  });

  async function loadServers() {
    loadingServers = true;
    try {
      servers = await api.get('/servers') as Server[];
      if (selectedServerId !== null && !servers.some((server) => server.id === selectedServerId)) {
        selectedServerId = null;
      }
    } catch (error) {
      toast.error(error instanceof Error ? error.message : 'Failed to load servers');
    } finally {
      loadingServers = false;
    }
  }

  async function restoreConversation() {
    if (typeof localStorage === 'undefined') return;

    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return;

    try {
      const parsed = JSON.parse(raw) as {
        selectedServerId?: number | null;
        messages?: Array<Omit<ConversationMessage, 'id'>>;
      };

      selectedServerId = typeof parsed.selectedServerId === 'number' ? parsed.selectedServerId : null;
      messages = (parsed.messages ?? []).map((message) => ({
        id: crypto.randomUUID(),
        role: message.role,
        content: message.content,
        timestamp: message.timestamp ?? new Date().toISOString(),
        suggestions: message.suggestions,
        commands: message.commands,
        context: message.context,
        error: message.error
      }));
    } catch {
      localStorage.removeItem(STORAGE_KEY);
    }
  }

  async function loadSuggestions(serverId?: number) {
    suggestionsLoading = true;
    suggestionsError = null;

    try {
      const result = await assistantApi.suggestions(serverId);
      suggestions = result.suggestions ?? [];
    } catch (error) {
      suggestionsError = error instanceof Error ? error.message : 'Failed to load proactive suggestions';
      suggestions = [];
    } finally {
      suggestionsLoading = false;
    }
  }

  function setSelectedServer(event: Event) {
    const value = (event.currentTarget as HTMLSelectElement).value;
    const parsed = value ? Number(value) : null;
    selectedServerId = Number.isNaN(parsed) ? null : parsed;
    void loadSuggestions(selectedServerId ?? undefined);
  }

  function createMessage(role: AssistantRole, content: string, extras: Partial<ConversationMessage> = {}) {
    return {
      id: crypto.randomUUID(),
      role,
      content,
      timestamp: new Date().toISOString(),
      ...extras
    } satisfies ConversationMessage;
  }

  async function sendMessage(customMessage?: string) {
    const text = (customMessage ?? input).trim();
    if (!text || sending) return;

    const userMessage = createMessage('user', text);
    messages = [...messages, userMessage];
    input = '';
    sending = true;
    await scrollToBottom();

    try {
      const response = await assistantApi.chat({
        message: text,
        serverId: selectedServerId ?? undefined,
        history: conversationSeed
      });

      appendAssistantResponse(response);
      if (response.suggestions?.length) {
        suggestions = response.suggestions;
      }
    } catch (error) {
      const message = error instanceof Error ? error.message : 'Failed to get assistant response';
      messages = [
        ...messages,
        createMessage('assistant', `I couldn't respond right now: ${message}`, { error: true })
      ];
      toast.error(message);
    } finally {
      sending = false;
      await scrollToBottom();
      inputRef?.focus();
    }
  }

  function appendAssistantResponse(response: ChatResponse) {
    messages = [
      ...messages,
      createMessage('assistant', response.message, {
        suggestions: response.suggestions,
        commands: response.commands,
        context: response.context
      })
    ];
  }

  function usePrompt(prompt: string) {
    input = prompt;
    inputRef?.focus();
  }

  function clearConversation() {
    messages = [];
    suggestions = [];
    suggestionsError = null;
    toast.success('Conversation cleared');
  }

  async function refreshSuggestions() {
    await loadSuggestions(selectedServerId ?? undefined);
  }

  async function analyzeLogs() {
    const content = logInput.trim();
    if (!content || logAnalysisLoading) return;

    logAnalysisLoading = true;
    logAnalysis = null;
    logAnalysisError = null;

    try {
      logAnalysis = await assistantApi.analyzeLog({
        serverId: selectedServerId ?? undefined,
        logContent: content
      });

      messages = [
        ...messages,
        createMessage(
          'assistant',
          `Log analysis: ${logAnalysis.severity.toUpperCase()} severity.\n\n${logAnalysis.explanation}`,
          {
            suggestions: logAnalysis.suggestions
          }
        )
      ];
      if (logAnalysis.suggestions?.length) {
        suggestions = logAnalysis.suggestions;
      }
      await scrollToBottom();
    } catch (error) {
      logAnalysisError = error instanceof Error ? error.message : 'Failed to analyze logs';
      toast.error(logAnalysisError);
    } finally {
      logAnalysisLoading = false;
    }
  }

  async function openCommandHelp(command: string) {
    const trimmed = command.trim();
    if (!trimmed || commandHelpLoading) return;

    commandHelpOpen = true;
    commandHelpLoading = true;
    commandHelpError = null;
    commandHelp = null;

    try {
      commandHelp = await assistantApi.commandHelp(trimmed);
    } catch (error) {
      commandHelpError = error instanceof Error ? error.message : 'Failed to load command help';
      toast.error(commandHelpError);
    } finally {
      commandHelpLoading = false;
    }
  }

  function closeCommandHelp() {
    commandHelpOpen = false;
    commandHelp = null;
    commandHelpError = null;
  }

  function openLogModal() {
    logModalOpen = true;
  }

  function closeLogModal() {
    logModalOpen = false;
  }

  async function copyText(text: string) {
    try {
      await navigator.clipboard.writeText(text);
      toast.success('Copied to clipboard');
    } catch {
      toast.error('Copy failed');
    }
  }

  async function scrollToBottom(smooth = true) {
    await tick();
    messagesContainer?.scrollTo({
      top: messagesContainer.scrollHeight,
      behavior: smooth ? 'smooth' : 'auto'
    });
  }

  function formatTime(timestamp?: string) {
    if (!timestamp) return '';
    return new Date(timestamp).toLocaleTimeString([], {
      hour: 'numeric',
      minute: '2-digit'
    });
  }

  function parseBlocks(text: string): RenderBlock[] {
    const lines = text.replace(/\r\n/g, '\n').split('\n');
    const blocks: RenderBlock[] = [];
    let index = 0;

    while (index < lines.length) {
      const line = lines[index];
      const trimmed = line.trim();

      if (!trimmed) {
        index++;
        continue;
      }

      if (trimmed.startsWith('```')) {
        const language = trimmed.slice(3).trim();
        index++;
        const code: string[] = [];
        while (index < lines.length && !lines[index].trim().startsWith('```')) {
          code.push(lines[index]);
          index++;
        }
        if (index < lines.length) index++;
        blocks.push({ type: 'code', language, code: code.join('\n') });
        continue;
      }

      if (/^([-*]|\d+\.)\s+/.test(trimmed)) {
        const items: string[] = [];
        while (index < lines.length && /^([-*]|\d+\.)\s+/.test(lines[index].trim())) {
          items.push(lines[index].trim().replace(/^([-*]|\d+\.)\s+/, ''));
          index++;
        }
        blocks.push({ type: 'list', items });
        continue;
      }

      const paragraph: string[] = [];
      while (
        index < lines.length &&
        lines[index].trim() &&
        !lines[index].trim().startsWith('```') &&
        !/^([-*]|\d+\.)\s+/.test(lines[index].trim())
      ) {
        paragraph.push(lines[index].trim());
        index++;
      }
      blocks.push({ type: 'paragraph', text: paragraph.join(' ') });
    }

    return blocks;
  }

  function parseInline(text: string): InlineSegment[] {
    const segments: InlineSegment[] = [];
    const regex = /(\*\*[^*]+\*\*|`[^`]+`)/g;
    let lastIndex = 0;
    let match: RegExpExecArray | null;

    while ((match = regex.exec(text)) !== null) {
      if (match.index > lastIndex) {
        segments.push({ type: 'text', text: text.slice(lastIndex, match.index) });
      }

      const token = match[0];
      if (token.startsWith('**')) {
        segments.push({ type: 'bold', text: token.slice(2, -2) });
      } else {
        segments.push({ type: 'code', text: token.slice(1, -1) });
      }
      lastIndex = match.index + token.length;
    }

    if (lastIndex < text.length) {
      segments.push({ type: 'text', text: text.slice(lastIndex) });
    }

    return segments;
  }

  function severityBadgeClass(
    severity: LogAnalysis['severity']
  ): 'success' | 'warning' | 'error' | 'info' | 'neutral' {
    return severity === 'critical'
      ? 'error'
      : severity === 'high'
        ? 'warning'
        : severity === 'medium'
          ? 'info'
          : 'neutral';
  }
</script>

<svelte:head>
  <title>Assistant - Meshium</title>
</svelte:head>

<div class="min-h-[calc(100vh-4rem)] bg-bg px-4 py-6 text-fg sm:px-6">
  <div class="mx-auto flex max-w-7xl flex-col gap-6">
    <div class="rounded-3xl border border-border bg-surface px-5 py-5 shadow-2xl shadow-cyan-950/20 backdrop-blur-sm [&_h1]:text-fg [&_p]:text-fg-subtle">
      <PageHeader
        title="Assistant"
        subtitle="Rule-based help for servers, migrations, logs, and shell commands."
      >
        {#snippet actions()}
          <button
            type="button"
            onclick={refreshSuggestions}
            class="inline-flex items-center gap-2 rounded-xl border border-border-strong bg-surface-muted px-4 py-2 text-sm font-medium text-fg-muted transition hover:border-accent hover:text-fg"
          >
            {#if suggestionsLoading}
              <Spinner size="sm" label="Refreshing" />
            {:else}
              <Sparkles size={16} />
            {/if}
            Refresh suggestions
          </button>
          <button
            type="button"
            onclick={clearConversation}
            class="inline-flex items-center gap-2 rounded-xl border border-border-strong bg-surface-muted px-4 py-2 text-sm font-medium text-fg-muted transition hover:border-error hover:text-fg"
          >
            <Trash2 size={16} />
            Clear chat
          </button>
        {/snippet}
      </PageHeader>

      <div class="grid gap-4 lg:grid-cols-[1.6fr_0.9fr]">
        <div class="space-y-4">
          <div class="rounded-2xl border border-border bg-bg/60 p-4 shadow-lg shadow-slate-950/40">
            <div class="flex flex-col gap-4 sm:flex-row sm:items-end sm:justify-between">
              <div class="min-w-0 flex-1">
                <label for="assistant-server-select" class="mb-2 block text-xs font-semibold uppercase tracking-[0.2em] text-fg-subtle">Server context</label>
                {#if loadingServers}
                  <div class="space-y-2">
                    <Skeleton width="100%" height="2.75rem" />
                    <Skeleton width="60%" height="0.85rem" />
                  </div>
                {:else}
                  <select
                    id="assistant-server-select"
                    value={selectedServerId ?? ''}
                    onchange={setSelectedServer}
                    class="w-full rounded-xl border border-border-strong bg-surface/90 px-4 py-3 text-sm text-fg outline-none transition focus:border-accent focus:ring-2 focus:ring-accent/20"
                  >
                    <option value="">General / all servers</option>
                    {#each servers as server}
                      <option value={server.id}>{server.name} · {server.host}</option>
                    {/each}
                  </select>
                  {#if selectedServer}
                    <div class="mt-3 flex flex-wrap items-center gap-2 text-sm text-fg-muted">
                      <span class="inline-flex items-center gap-1 rounded-full border border-border-strong bg-surface px-3 py-1">
                        <ServerIcon size={14} />
                        {selectedServer.name}
                      </span>
                      {#if selectedServer.environment}
                        <Badge variant="info" size="sm">{selectedServer.environment}</Badge>
                      {/if}
                      {#if selectedServer.region}
                        <Badge variant="neutral" size="sm">{selectedServer.region}</Badge>
                      {/if}
                      <span class="text-fg-subtle">{selectedServer.host}:{selectedServer.port}</span>
                    </div>
                  {:else}
                    <p class="mt-3 text-sm text-fg-subtle">No server selected. Ask a general question or choose a server for tailored advice.</p>
                  {/if}
                {/if}
              </div>

              <div class="flex flex-wrap gap-2 sm:flex-col sm:items-end">
                <button
                  type="button"
                  onclick={openLogModal}
                  class="inline-flex items-center gap-2 rounded-xl border border-border-strong bg-surface px-4 py-2 text-sm font-medium text-fg-muted transition hover:border-warning hover:text-white"
                >
                  <FileText size={16} />
                  Analyze logs
                </button>
                <button
                  type="button"
                  onclick={() => sendMessage('Check this server for package updates and recommend the safest next steps.')}
                  class="inline-flex items-center gap-2 rounded-xl border border-border-strong bg-surface px-4 py-2 text-sm font-medium text-fg-muted transition hover:border-success hover:text-white"
                >
                  <Database size={16} />
                  Check updates
                </button>
                <button
                  type="button"
                  onclick={() => sendMessage('Suggest a practical migration plan for this server.')}
                  class="inline-flex items-center gap-2 rounded-xl border border-border-strong bg-surface px-4 py-2 text-sm font-medium text-fg-muted transition hover:border-accent hover:text-white"
                >
                  <Wand2 size={16} />
                  Suggest migration
                </button>
              </div>
            </div>
          </div>

          <div class="rounded-2xl border border-border bg-bg/60 p-4 shadow-lg shadow-slate-950/40">
            <div class="mb-2 text-xs font-semibold uppercase tracking-[0.2em] text-fg-subtle">Suggested prompts</div>
            <div class="flex flex-wrap gap-2">
              {#each starterPrompts as prompt}
                <button
                  type="button"
                  onclick={() => usePrompt(prompt)}
                  class="rounded-full border border-border-strong bg-surface/80 px-3 py-2 text-sm text-fg-muted transition hover:border-accent hover:text-white"
                >
                  {prompt}
                </button>
              {/each}
            </div>
          </div>

          <div class="rounded-2xl border border-border bg-bg/60 shadow-lg shadow-slate-950/40">
            <div class="border-b border-border px-4 py-3">
              <div class="flex items-center justify-between gap-3">
                <div>
                  <h2 class="text-sm font-semibold text-fg">Conversation</h2>
                  <p class="text-xs text-fg-subtle">Rule-based replies with server context, command suggestions, and log explanations.</p>
                </div>
                {#if sending}
                  <div class="flex items-center gap-2 text-xs text-fg-subtle"><Activity size={14} class="animate-pulse" />Thinking</div>
                {/if}
              </div>
            </div>

            <div bind:this={messagesContainer} class="max-h-[58vh] space-y-4 overflow-y-auto px-4 py-4 sm:px-5">
              {#if messages.length === 0}
                <div class="rounded-2xl border border-dashed border-border bg-surface/40 p-6 text-center">
                  <div class="mx-auto inline-flex items-center justify-center rounded-full bg-accent/10 p-3 text-accent">
                    <Bot size={22} />
                  </div>
                  <h3 class="mt-4 text-base font-semibold text-fg">Ask anything about your servers</h3>
                  <p class="mt-2 text-sm text-fg-subtle">
                    Try asking about migrations, package lists, logs, services, or shell commands.
                  </p>
                </div>
              {/if}

              {#each messages as message (message.id)}
                <div class={`flex gap-3 ${message.role === 'user' ? 'justify-end' : 'justify-start'}`}>
                  {#if message.role !== 'user'}
                    <div class="mt-1 flex h-9 w-9 shrink-0 items-center justify-center rounded-full bg-accent/15 text-accent ring-1 ring-accent/20">
                      <Bot size={16} />
                    </div>
                  {/if}

                  <div class={`max-w-[min(100%,44rem)] rounded-2xl border px-4 py-3 shadow-sm ${
                    message.role === 'user'
                      ? 'border-accent/20 bg-accent text-accent-fg'
                      : message.error
                        ? 'border-error/20 bg-error/10 text-error'
                        : 'border-border bg-surface-muted text-fg'
                  }`}>
                    <div class="mb-2 flex items-center justify-between gap-3">
                      <div class="flex items-center gap-2 text-xs font-medium uppercase tracking-[0.16em] text-fg-subtle">
                        <span>{message.role === 'user' ? 'You' : 'Assistant'}</span>
                        {#if message.context}
                          <span class="rounded-full border border-border-strong bg-bg px-2 py-0.5 normal-case tracking-normal text-fg-muted">
                            Context
                          </span>
                        {/if}
                      </div>
                      <span class="text-[11px] text-fg-subtle">{formatTime(message.timestamp)}</span>
                    </div>

                    <div class="space-y-3 text-sm leading-6 text-fg">
                      {#each parseBlocks(message.content) as block}
                        {#if block.type === 'paragraph'}
                          <p>
                            {#each parseInline(block.text) as segment}
                              {#if segment.type === 'text'}
                                {segment.text}
                              {:else if segment.type === 'bold'}
                                <strong class="font-semibold text-white">{segment.text}</strong>
                              {:else}
                                <code class="rounded bg-bg/80 px-1.5 py-0.5 text-[0.85em] text-accent">{segment.text}</code>
                              {/if}
                            {/each}
                          </p>
                        {:else if block.type === 'list'}
                          <ul class="space-y-1 pl-5">
                            {#each block.items as item}
                              <li class="list-disc">
                                {#each parseInline(item) as segment}
                                  {#if segment.type === 'text'}
                                    {segment.text}
                                  {:else if segment.type === 'bold'}
                                    <strong class="font-semibold text-white">{segment.text}</strong>
                                  {:else}
                                    <code class="rounded bg-bg/80 px-1.5 py-0.5 text-[0.85em] text-accent">{segment.text}</code>
                                  {/if}
                                {/each}
                              </li>
                            {/each}
                          </ul>
                        {:else}
                          <div class="rounded-xl border border-border bg-bg/80">
                            <div class="flex items-center justify-between border-b border-border px-3 py-2 text-xs text-fg-subtle">
                              <span>{block.language || 'code'}</span>
                              <button
                                type="button"
                                onclick={() => copyText(block.code)}
                                class="inline-flex items-center gap-1 rounded-lg border border-border-strong bg-surface px-2 py-1 text-fg-muted transition hover:border-accent"
                              >
                                <Copy size={12} />
                                Copy
                              </button>
                            </div>
                            <pre class="overflow-x-auto p-3 text-xs leading-6 text-fg"><code>{block.code}</code></pre>
                          </div>
                        {/if}
                      {/each}
                    </div>

                    {#if message.commands?.length}
                      <div class="mt-4 space-y-3">
                        <div class="flex items-center gap-2 text-xs font-semibold uppercase tracking-[0.16em] text-fg-subtle">
                          <ShieldAlert size={14} />
                          Suggested commands
                        </div>
                        {#each message.commands as command}
                          <div class="overflow-hidden rounded-xl border border-border bg-bg/90">
                            <div class="flex items-center justify-between gap-2 border-b border-border px-3 py-2 text-xs text-fg-subtle">
                              <span class="truncate">{command}</span>
                              <div class="flex items-center gap-2">
                                <button
                                  type="button"
                                  onclick={() => openCommandHelp(command)}
                                  class="inline-flex items-center gap-1 rounded-lg border border-border-strong bg-surface px-2 py-1 text-fg-muted transition hover:border-accent"
                                >
                                  <HelpCircle size={12} />
                                  Help
                                </button>
                                <button
                                  type="button"
                                  onclick={() => copyText(command)}
                                  class="inline-flex items-center gap-1 rounded-lg border border-border-strong bg-surface px-2 py-1 text-fg-muted transition hover:border-accent"
                                >
                                  <ClipboardCopy size={12} />
                                  Copy
                                </button>
                              </div>
                            </div>
                            <pre class="overflow-x-auto p-3 text-xs leading-6 text-accent"><code>{command}</code></pre>
                          </div>
                        {/each}
                      </div>
                    {/if}

                    {#if message.suggestions?.length}
                      <div class="mt-4 space-y-2">
                        <div class="flex items-center gap-2 text-xs font-semibold uppercase tracking-[0.16em] text-fg-subtle">
                          <Sparkles size={14} />
                          Next steps
                        </div>
                        <div class="flex flex-wrap gap-2">
                          {#each message.suggestions as suggestion}
                            <button
                              type="button"
                              onclick={() => usePrompt(suggestion)}
                              class="rounded-full border border-border-strong bg-surface px-3 py-1.5 text-xs text-fg-muted transition hover:border-accent hover:text-white"
                            >
                              {suggestion}
                            </button>
                          {/each}
                        </div>
                      </div>
                    {/if}
                  </div>

                  {#if message.role === 'user'}
                    <div class="mt-1 flex h-9 w-9 shrink-0 items-center justify-center rounded-full bg-surface-muted text-fg ring-1 ring-border-strong">
                      <ServerIcon size={16} />
                    </div>
                  {/if}
                </div>
              {/each}

              {#if sending}
                <div class="flex gap-3">
                  <div class="mt-1 flex h-9 w-9 items-center justify-center rounded-full bg-accent/15 text-accent ring-1 ring-accent/20">
                    <Bot size={16} />
                  </div>
                  <div class="rounded-2xl border border-border bg-surface/80 px-4 py-3 text-sm text-fg-muted">
                    <Spinner size="sm" label="Thinking" />
                  </div>
                </div>
              {/if}
            </div>

            <div class="border-t border-border px-4 py-4 sm:px-5">
              <div class="mb-3 flex flex-wrap gap-2">
                {#each starterPrompts as prompt}
                  <button
                    type="button"
                    onclick={() => sendMessage(prompt)}
                    class="rounded-full border border-border-strong bg-surface/80 px-3 py-1.5 text-xs text-fg-muted transition hover:border-accent hover:text-white"
                  >
                    {prompt}
                  </button>
                {/each}
              </div>

              <div class="flex flex-col gap-3 sm:flex-row sm:items-end">
                <div class="flex-1">
                  <label for="assistant-message-input" class="mb-2 block text-xs font-semibold uppercase tracking-[0.2em] text-fg-subtle">Your question</label>
                  <textarea
                    id="assistant-message-input"
                    bind:this={inputRef}
                    bind:value={input}
                    rows="3"
                    placeholder="Ask about a server, a migration plan, a log snippet, or a shell command..."
                    class="w-full rounded-2xl border border-border-strong bg-surface/90 px-4 py-3 text-sm text-fg outline-none transition placeholder:text-fg-subtle focus:border-accent focus:ring-2 focus:ring-accent/20"
                    onkeydown={(event) => {
                      if (event.key === 'Enter' && !event.shiftKey) {
                        event.preventDefault();
                        void sendMessage();
                      }
                    }}
                  ></textarea>
                </div>

                <div class="flex gap-2">
                  <button
                    type="button"
                    onclick={() => void sendMessage()}
                    disabled={sending || !input.trim()}
                    class="inline-flex items-center justify-center gap-2 rounded-2xl bg-accent px-5 py-3 text-sm font-medium text-accent-fg transition hover:bg-accent-hover disabled:cursor-not-allowed disabled:opacity-60"
                  >
                    {#if sending}
                      <Spinner size="sm" label="Sending" />
                    {:else}
                      <Send size={16} />
                    {/if}
                    Send
                  </button>
                </div>
              </div>
            </div>
          </div>
        </div>

        <aside class="space-y-4">
          <div class="rounded-2xl border border-border bg-bg/60 p-4 shadow-lg shadow-slate-950/40">
            <div class="mb-4 flex items-center justify-between gap-3">
              <div>
                <h2 class="text-sm font-semibold text-fg">Proactive suggestions</h2>
                <p class="text-xs text-fg-subtle">Tailored to the selected server when possible.</p>
              </div>
              {#if suggestionsLoading}
                <Spinner size="sm" label="Loading suggestions" />
              {/if}
            </div>

            {#if suggestionsError}
              <div class="rounded-xl border border-warning/20 bg-warning/10 p-3 text-sm text-warning">
                {suggestionsError}
              </div>
            {:else if suggestions.length === 0}
              <div class="rounded-xl border border-dashed border-border bg-surface/40 p-4 text-sm text-fg-subtle">
                No proactive suggestions yet. Ask a question or select a server to get tailored advice.
              </div>
            {:else}
              <div class="space-y-2">
                {#each suggestions as suggestion}
                  <button
                    type="button"
                    onclick={() => sendMessage(suggestion)}
                    class="w-full rounded-xl border border-border bg-surface/70 px-3 py-3 text-left text-sm text-fg-muted transition hover:border-accent hover:bg-surface"
                  >
                    {suggestion}
                  </button>
                {/each}
              </div>
            {/if}
          </div>

          <div class="rounded-2xl border border-border bg-bg/60 p-4 shadow-lg shadow-slate-950/40">
            <div class="mb-3 flex items-center gap-2">
              <Sparkles size={16} class="text-accent" />
              <h2 class="text-sm font-semibold text-fg">What this assistant can do</h2>
            </div>
            <ul class="space-y-3 text-sm text-fg-subtle">
              <li class="flex gap-2">
                <Check size={16} class="mt-0.5 shrink-0 text-success" />
                Explain logs and error messages in plain language.
              </li>
              <li class="flex gap-2">
                <Check size={16} class="mt-0.5 shrink-0 text-success" />
                Suggest migration steps based on discovery data.
              </li>
              <li class="flex gap-2">
                <Check size={16} class="mt-0.5 shrink-0 text-success" />
                Recommend useful shell commands with copy buttons.
              </li>
              <li class="flex gap-2">
                <Check size={16} class="mt-0.5 shrink-0 text-success" />
                Surface server-specific suggestions without calling an external LLM.
              </li>
            </ul>
          </div>
        </aside>
      </div>
    </div>
  </div>
</div>

<Modal open={logModalOpen} title="Analyze logs" size="lg" onClose={closeLogModal}>
  <div class="space-y-4">
    <p class="text-sm text-fg-muted">
      Paste a log snippet or error trace. The assistant will explain the likely cause and suggest next steps.
    </p>

    <textarea
      bind:value={logInput}
      rows="10"
      placeholder="Paste logs here..."
      class="w-full rounded-2xl border border-border bg-surface px-4 py-3 text-sm text-fg outline-none focus:border-accent focus:ring-2 focus:ring-accent/20"
    ></textarea>

    <div class="flex flex-wrap items-center gap-3">
      <button
        type="button"
        onclick={() => void analyzeLogs()}
        disabled={logAnalysisLoading || !logInput.trim()}
        class="inline-flex items-center gap-2 rounded-xl bg-fg px-4 py-2 text-sm font-medium text-bg transition hover:bg-fg-muted disabled:cursor-not-allowed disabled:opacity-60"
      >
        {#if logAnalysisLoading}
          <Spinner size="sm" label="Analyzing" />
        {:else}
          <ShieldAlert size={16} />
        {/if}
        Analyze
      </button>
      {#if selectedServer}
        <span class="text-sm text-fg-subtle">Using {selectedServer.name} as context.</span>
      {/if}
    </div>

    {#if logAnalysisError}
      <div class="rounded-xl border border-error/20 bg-error/10 p-3 text-sm text-error">{logAnalysisError}</div>
    {/if}

    {#if logAnalysis}
      <div class="space-y-3 rounded-2xl border border-border bg-surface-muted p-4">
        <div class="flex items-center justify-between gap-3">
          <Badge variant={severityBadgeClass(logAnalysis.severity)} size="sm">
            {logAnalysis.severity}
          </Badge>
        </div>
        <p class="text-sm leading-6 text-fg-muted">{logAnalysis.explanation}</p>
        {#if logAnalysis.suggestions.length}
          <div>
            <p class="mb-2 text-xs font-semibold uppercase tracking-[0.2em] text-fg-subtle">Suggestions</p>
            <ul class="space-y-2 text-sm text-fg-muted">
              {#each logAnalysis.suggestions as suggestion}
                <li class="rounded-lg bg-surface px-3 py-2 shadow-sm">{suggestion}</li>
              {/each}
            </ul>
          </div>
        {/if}
      </div>
    {/if}
  </div>
</Modal>

<Modal open={commandHelpOpen} title="Command help" size="lg" onClose={closeCommandHelp}>
  <div class="space-y-4">
    {#if commandHelpLoading}
      <div class="flex items-center gap-2 text-sm text-fg-muted">
        <Spinner size="sm" label="Loading command help" />
        Looking up command details...
      </div>
    {:else if commandHelpError}
      <div class="rounded-xl border border-error/20 bg-error/10 p-3 text-sm text-error">{commandHelpError}</div>
    {:else if commandHelp}
      <div>
        <div class="mb-2 flex items-center gap-2">
          <Badge variant="info" size="sm">{commandHelp.command}</Badge>
        </div>
        <p class="text-sm leading-6 text-fg-muted">{commandHelp.description}</p>
      </div>

      {#if commandHelp.warning}
        <div class="rounded-xl border border-warning/20 bg-warning/10 p-3 text-sm text-warning">
          {commandHelp.warning}
        </div>
      {/if}

      {#if commandHelp.examples.length}
        <div>
          <p class="mb-2 text-xs font-semibold uppercase tracking-[0.2em] text-fg-subtle">Examples</p>
          <div class="space-y-2">
            {#each commandHelp.examples as example}
              <div class="rounded-xl border border-border bg-surface-muted p-3 font-mono text-xs leading-6 text-fg-muted">
                {example}
              </div>
            {/each}
          </div>
        </div>
      {/if}
    {:else}
      <div class="text-sm text-fg-subtle">Select a suggested command to see more details.</div>
    {/if}
  </div>
</Modal>
