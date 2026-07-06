<script lang="ts">
  import type { RiskReport, StrategySelection } from '$lib/api/pipeline';

  export let migrationId: number;
  export let currentState: string;
  export let sourceId: number | null;
  export let targetId: number | null;
  export let healthScore: number;
  export let riskReport: RiskReport | null;
  export let strategy: StrategySelection | null;
  export let rollbackAvailable: boolean;
  export let pipelineRunning: boolean;
  export let pipelinePaused: boolean;
  export let wsConnectionState: string;

  function riskColor(cls: string): string {
    switch (cls) {
      case 'low': return 'text-success';
      case 'medium': return 'text-warning';
      case 'high': return 'text-warning';
      case 'critical': return 'text-error';
      default: return 'text-fg-subtle';
    }
  }

  function riskBg(cls: string): string {
    switch (cls) {
      case 'low': return 'bg-success/10 border-success/30';
      case 'medium': return 'bg-warning/10 border-warning/30';
      case 'high': return 'bg-warning/10 border-warning/30';
      case 'critical': return 'bg-error/10 border-error/30';
      default: return 'bg-surface-muted border-border';
    }
  }

  function healthColor(score: number): string {
    if (score < 60) return 'text-error';
    if (score < 80) return 'text-warning';
    return 'text-success';
  }

  function strategyLabel(s: string | undefined): string {
    if (!s) return '—';
    return s.replace(/_/g, ' ').replace(/\b\w/g, (c) => c.toUpperCase());
  }

  function isTerminalState(state: string): boolean {
    return ['completed', 'committed', 'archived', 'rolled_back', 'cancelled'].includes(state.toLowerCase());
  }
</script>

<div class="border-b border-border px-3 sm:px-4 py-2.5 flex items-center justify-between shrink-0 bg-bg gap-2">
  <div class="flex items-center gap-2 sm:gap-4 min-w-0">
    <a href="/migrations" aria-label="Back to migrations" class="text-fg-subtle hover:text-fg transition-colors shrink-0">
      <svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="m12 19-7-7 7-7"/><path d="M19 12H5"/></svg>
    </a>
    <div class="min-w-0">
      <h1 class="text-sm font-semibold text-fg truncate">
        Migration #{migrationId}
        {#if sourceId && targetId}
          <span class="text-fg-subtle font-normal ml-1 hidden sm:inline">Server {sourceId} &rarr; Server {targetId}</span>
        {/if}
      </h1>
    </div>
  </div>

  <div class="flex items-center gap-2 sm:gap-3 shrink-0 overflow-x-auto">
    <!-- Status -->
    <div class="flex items-center gap-1.5">
      <div class="w-2 h-2 rounded-full {pipelineRunning ? 'bg-accent animate-pulse' : isTerminalState(currentState) ? 'bg-success' : 'bg-fg-subtle'}"></div>
      <span class="text-xs font-medium text-fg-muted">{currentState || 'Ready'}</span>
    </div>

    <!-- Health -->
    <div class="flex items-center gap-1.5 px-2 py-1 rounded bg-surface border border-border">
      <span class="text-xs text-fg-subtle">Health</span>
      <span class="text-xs font-bold {healthColor(healthScore)}">{healthScore.toFixed(0)}</span>
    </div>

    <!-- Risk -->
    {#if riskReport}
      <div class="flex items-center gap-1.5 px-2 py-1 rounded border {riskBg(riskReport.riskClass)}">
        <span class="text-xs text-fg-subtle">Risk</span>
        <span class="text-xs font-bold {riskColor(riskReport.riskClass)}">{riskReport.riskClass}</span>
      </div>
    {/if}

    <!-- Strategy -->
    {#if strategy}
      <div class="hidden md:flex items-center gap-1.5 px-2 py-1 rounded bg-surface border border-border">
        <span class="text-xs text-fg-subtle">Strategy</span>
        <span class="text-xs font-medium text-fg-muted">{strategyLabel(strategy.strategy)}</span>
      </div>
    {/if}

    <!-- Rollback -->
    {#if !isTerminalState(currentState)}
      <div class="hidden md:flex items-center gap-1.5 px-2 py-1 rounded {rollbackAvailable ? 'bg-success/10 border border-success/30' : 'bg-surface border border-border'}">
        <span class="text-xs text-fg-subtle">Rollback</span>
        <span class="text-xs font-medium {rollbackAvailable ? 'text-success' : 'text-fg-subtle'}">{rollbackAvailable ? 'Available' : 'N/A'}</span>
      </div>
    {/if}

    <!-- WS Indicator -->
    {#if pipelineRunning || pipelinePaused}
      <span class="flex items-center gap-1.5 text-xs">
        {#if wsConnectionState === 'connected'}
          <span class="h-2 w-2 rounded-full bg-success"></span>
          <span class="text-success">Live</span>
        {:else if wsConnectionState === 'reconnecting' || wsConnectionState === 'connecting'}
          <span class="h-2 w-2 rounded-full bg-warning animate-pulse"></span>
          <span class="text-warning">Reconnecting</span>
        {:else}
          <span class="h-2 w-2 rounded-full bg-error"></span>
          <span class="text-error">Offline</span>
        {/if}
      </span>
    {/if}
  </div>
</div>
