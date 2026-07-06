<script lang="ts">
  export let healthScore: number;
  export let replicationLag: number;
  export let observationElapsed: number;
  export let observationDuration: number;
  export let observationRemaining: number;
  export let observationProgress: number;
  export let stepStatus: string;
  export let actionLoading: boolean;
  export let onCommit: () => void;
  export let onRollback: () => void;

  // Simulated traffic split (in real implementation, this would come from API)
  $: trafficA = stepStatus === 'completed' ? 0 : Math.max(0, 100 - observationProgress);
  $: trafficB = stepStatus === 'completed' ? 100 : Math.min(100, observationProgress);

  function formatTime(secs: number): string {
    if (secs <= 0) return 'Done';
    const m = Math.floor(secs / 60);
    const s = secs % 60;
    return m > 0 ? `${m}m ${s}s` : `${s}s`;
  }
</script>

<div class="bg-surface rounded-xl p-6 border border-border">
  <div class="flex items-center justify-between mb-5">
    <div class="flex items-center gap-3">
      <div class="w-10 h-10 rounded-lg bg-success/15 flex items-center justify-center">
        <svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="text-success"><path d="M2 12s3-7 10-7 10 7 10 7-3 7-10 7-10-7-10-7Z"/><circle cx="12" cy="12" r="3"/></svg>
      </div>
      <div>
        <h2 class="text-lg font-semibold text-fg">Observation Window</h2>
        <p class="text-sm text-fg-muted">Post-cutover monitoring &mdash; watching for errors</p>
      </div>
    </div>
    <div class="text-right">
      <div class="text-sm font-mono text-fg-muted">{formatTime(observationRemaining)} remaining</div>
      <div class="text-xs text-fg-subtle">{formatTime(observationElapsed)} / {formatTime(observationDuration)}</div>
      <div class="w-32 h-1.5 bg-surface-muted rounded-full overflow-hidden mt-1">
        <div class="h-full bg-success rounded-full transition-all" style="width: {observationProgress}%"></div>
      </div>
    </div>
  </div>

  <!-- Traffic Split -->
  <div class="mb-5">
    <div class="flex items-center justify-between mb-2">
      <span class="text-xs text-fg-muted">Traffic</span>
      <div class="flex items-center gap-4 text-xs font-mono">
        <span class="text-fg-subtle">A {trafficA.toFixed(0)}%</span>
        <span class="text-success">B {trafficB.toFixed(0)}%</span>
      </div>
    </div>
    <div class="h-3 bg-surface-muted rounded-full overflow-hidden flex">
      <div class="h-full bg-border-strong transition-all duration-1000" style="width: {trafficA}%"></div>
      <div class="h-full bg-success transition-all duration-1000" style="width: {trafficB}%"></div>
    </div>
  </div>

  <!-- Metrics Grid -->
  <div class="grid grid-cols-2 sm:grid-cols-4 gap-3 mb-5">
    <div class="bg-surface-muted rounded-lg p-3 text-center">
      <div class="text-2xl font-bold {healthScore < 80 ? 'text-warning' : 'text-success'}">{healthScore.toFixed(0)}</div>
      <div class="text-xs text-fg-muted">Health</div>
    </div>
    <div class="bg-surface-muted rounded-lg p-3 text-center">
      <div class="text-2xl font-bold text-success">{replicationLag}s</div>
      <div class="text-xs text-fg-muted">Queue Lag</div>
    </div>
    <div class="bg-surface-muted rounded-lg p-3 text-center">
      <div class="text-2xl font-bold text-info">0.1%</div>
      <div class="text-xs text-fg-muted">Error Rate</div>
    </div>
    <div class="bg-surface-muted rounded-lg p-3 text-center">
      <div class="text-2xl font-bold text-info">120ms</div>
      <div class="text-xs text-fg-muted">P95 Latency</div>
    </div>
  </div>

  <!-- Actions -->
  {#if stepStatus === 'completed'}
    <div class="flex items-center gap-3">
      <button
        on:click={onCommit}
        disabled={actionLoading}
        class="px-6 py-2.5 bg-success hover:bg-success/90 text-accent-fg disabled:opacity-50 rounded-lg font-medium transition-colors"
      >
        {actionLoading ? 'Committing...' : 'Commit Migration'}
      </button>
      <button
        on:click={onRollback}
        disabled={actionLoading}
        class="px-6 py-2.5 bg-error hover:bg-error/90 text-accent-fg disabled:opacity-50 rounded-lg font-medium transition-colors"
      >
        Rollback
      </button>
    </div>
  {:else}
    <p class="text-sm text-fg-muted text-center">Observing target server health. Auto-rollback will trigger if health drops below threshold.</p>
  {/if}
</div>
