<script lang="ts">
  interface LogEntry {
    timestamp: string;
    level: 'info' | 'warn' | 'error';
    step: string;
    message: string;
  }

  interface LogViewerProps {
    logs: LogEntry[];
    streaming?: boolean;
  }

  let { logs, streaming = false }: LogViewerProps = $props();

  let container: HTMLDivElement | null = null;
  let lastScrollCount = $state(0);

  function formatTimestamp(isoTimestamp: string): string {
    const date = new Date(isoTimestamp);
    if (Number.isNaN(date.getTime())) {
      return isoTimestamp;
    }

    return date.toLocaleTimeString([], {
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
      hour12: false,
    });
  }

  const levelClasses: Record<LogEntry['level'], string> = {
    info: 'text-slate-400',
    warn: 'text-yellow-400',
    error: 'text-red-400',
  };

  $effect(() => {
    logs;
    streaming;

    if (!container) {
      return;
    }

    if (logs.length !== lastScrollCount || streaming) {
      lastScrollCount = logs.length;
      requestAnimationFrame(() => {
        if (container) {
          container.scrollTop = container.scrollHeight;
        }
      });
    }
  });
</script>

<div bind:this={container} class="max-h-96 overflow-auto rounded-lg bg-slate-900 p-4">
  {#if streaming}
    <div class="mb-3 flex items-center gap-2 text-xs font-medium text-slate-400">
      <span class="h-2 w-2 rounded-full bg-blue-400 animate-pulse"></span>
      <span>Live</span>
    </div>
  {/if}

  {#if logs.length === 0}
    <div class="py-8 text-center text-sm text-slate-500">No logs yet</div>
  {:else}
    <div class="space-y-1 font-mono text-xs break-all">
      {#each logs as log}
        <div class="flex items-start gap-3">
          <span class="shrink-0 text-slate-500">{formatTimestamp(log.timestamp)}</span>
          <span class={`shrink-0 ${levelClasses[log.level]}`}>[{log.level}]</span>
          <span class="shrink-0 text-slate-300">{log.step}</span>
          <span class="min-w-0 flex-1 text-slate-200">{log.message}</span>
        </div>
      {/each}
    </div>
  {/if}
</div>
