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
      case 'low': return 'text-green-400';
      case 'medium': return 'text-yellow-400';
      case 'high': return 'text-orange-400';
      case 'critical': return 'text-red-400';
      default: return 'text-gray-400';
    }
  }

  function riskBg(cls: string): string {
    switch (cls) {
      case 'low': return 'bg-green-500/10 border-green-500/30';
      case 'medium': return 'bg-yellow-500/10 border-yellow-500/30';
      case 'high': return 'bg-orange-500/10 border-orange-500/30';
      case 'critical': return 'bg-red-500/10 border-red-500/30';
      default: return 'bg-gray-500/10 border-gray-500/30';
    }
  }

  function healthColor(score: number): string {
    if (score < 60) return 'text-red-400';
    if (score < 80) return 'text-yellow-400';
    return 'text-green-400';
  }

  function strategyLabel(s: string | undefined): string {
    if (!s) return '—';
    return s.replace(/_/g, ' ').replace(/\b\w/g, (c) => c.toUpperCase());
  }

  function isTerminalState(state: string): boolean {
    return ['completed', 'committed', 'archived', 'rolled_back', 'cancelled'].includes(state.toLowerCase());
  }
</script>

<div class="border-b border-gray-800 px-3 sm:px-4 py-2.5 flex items-center justify-between shrink-0 bg-gray-950 gap-2">
  <div class="flex items-center gap-2 sm:gap-4 min-w-0">
    <a href="/migrations" aria-label="Back to migrations" class="text-gray-400 hover:text-white transition-colors shrink-0">
      <svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="m12 19-7-7 7-7"/><path d="M19 12H5"/></svg>
    </a>
    <div class="min-w-0">
      <h1 class="text-sm font-semibold text-white truncate">
        Migration #{migrationId}
        {#if sourceId && targetId}
          <span class="text-gray-500 font-normal ml-1 hidden sm:inline">Server {sourceId} &rarr; Server {targetId}</span>
        {/if}
      </h1>
    </div>
  </div>

  <div class="flex items-center gap-2 sm:gap-3 shrink-0 overflow-x-auto">
    <!-- Status -->
    <div class="flex items-center gap-1.5">
      <div class="w-2 h-2 rounded-full {pipelineRunning ? 'bg-blue-500 animate-pulse' : isTerminalState(currentState) ? 'bg-green-500' : 'bg-gray-500'}"></div>
      <span class="text-xs font-medium text-gray-300">{currentState || 'Ready'}</span>
    </div>

    <!-- Health -->
    <div class="flex items-center gap-1.5 px-2 py-1 rounded bg-gray-900 border border-gray-800">
      <span class="text-xs text-gray-500">Health</span>
      <span class="text-xs font-bold {healthColor(healthScore)}">{healthScore.toFixed(0)}</span>
    </div>

    <!-- Risk -->
    {#if riskReport}
      <div class="flex items-center gap-1.5 px-2 py-1 rounded border {riskBg(riskReport.riskClass)}">
        <span class="text-xs text-gray-500">Risk</span>
        <span class="text-xs font-bold {riskColor(riskReport.riskClass)}">{riskReport.riskClass}</span>
      </div>
    {/if}

    <!-- Strategy -->
    {#if strategy}
      <div class="hidden md:flex items-center gap-1.5 px-2 py-1 rounded bg-gray-900 border border-gray-800">
        <span class="text-xs text-gray-500">Strategy</span>
        <span class="text-xs font-medium text-gray-300">{strategyLabel(strategy.strategy)}</span>
      </div>
    {/if}

    <!-- Rollback -->
    {#if !isTerminalState(currentState)}
      <div class="hidden md:flex items-center gap-1.5 px-2 py-1 rounded {rollbackAvailable ? 'bg-green-500/10 border border-green-500/30' : 'bg-gray-900 border border-gray-800'}">
        <span class="text-xs text-gray-500">Rollback</span>
        <span class="text-xs font-medium {rollbackAvailable ? 'text-green-400' : 'text-gray-500'}">{rollbackAvailable ? 'Available' : 'N/A'}</span>
      </div>
    {/if}

    <!-- WS Indicator -->
    {#if pipelineRunning || pipelinePaused}
      <span class="flex items-center gap-1.5 text-xs">
        {#if wsConnectionState === 'connected'}
          <span class="h-2 w-2 rounded-full bg-green-500"></span>
          <span class="text-green-400">Live</span>
        {:else if wsConnectionState === 'reconnecting' || wsConnectionState === 'connecting'}
          <span class="h-2 w-2 rounded-full bg-yellow-500 animate-pulse"></span>
          <span class="text-yellow-400">Reconnecting</span>
        {:else}
          <span class="h-2 w-2 rounded-full bg-red-500"></span>
          <span class="text-red-400">Offline</span>
        {/if}
      </span>
    {/if}
  </div>
</div>
