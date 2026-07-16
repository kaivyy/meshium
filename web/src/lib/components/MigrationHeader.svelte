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
  // What the operator actually configured (authoritative from backend config).
  // Drives honest awaiting_cutover copy: if automatic is configured AND the
  // server supports it, say so; otherwise say manual — never the old constant.
  export let autoCutoverConfigured: boolean = false;

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
    const s = state.toLowerCase();
    return ['completed', 'committed', 'archived', 'rolled_back', 'cancelled', 'needs_manual_intervention', 'rollback_degraded'].includes(s);
  }

  // statusVisual maps a raw backend status to an honest dot color + human label.
  // Only a fully clean "completed" is full green. Every "done but not identical"
  // outcome (drift / manual gaps / partial / verification issues) is amber/red —
  // never green — so the header never reads as "healthy" when it isn't.
  function statusVisual(state: string): { dot: string; label: string } {
    const s = (state || '').toLowerCase();
    const pretty = s.replace(/_/g, ' ').replace(/\b\w/g, (c) => c.toUpperCase());
    switch (s) {
      case 'completed':
        return { dot: 'bg-success', label: 'Completed' };
      case 'completed_with_drift':
        return { dot: 'bg-warning', label: 'Completed — drift accepted' };
      case 'completed_with_manual_gaps':
        return { dot: 'bg-warning', label: 'Completed — manual follow-up' };
      case 'completed_partial':
        return { dot: 'bg-warning', label: 'Partially applied' };
      case 'manual_followup_required':
        return { dot: 'bg-warning', label: 'Manual follow-up required' };
      case 'verification_failed':
        return { dot: 'bg-error', label: 'Verification failed' };
      case 'verification_partial':
        return { dot: 'bg-warning', label: 'Verification partial' };
      case 'failed':
      case 'rollback_failed':
        return { dot: 'bg-error', label: pretty };
      case 'rolled_back':
        return { dot: 'bg-fg-subtle', label: 'Rolled back' };
      case 'needs_manual_intervention':
      case 'rollback_degraded':
        return { dot: 'bg-error', label: pretty };
      default:
        return { dot: 'bg-fg-subtle', label: pretty || 'Ready' };
    }
  }

  // operatorGuidance returns actionable copy for the two P0-2 states a raw
  // status string cannot explain. Empty string = no special guidance.
  function operatorGuidance(state: string): string {
    switch (state.toLowerCase()) {
      case 'awaiting_cutover':
        return autoCutoverConfigured
          ? 'Automatic cutover is configured (fenced). Confirm the switch below to commit — the server will move traffic and verify ownership.'
          : 'Manual cutover required: move traffic to the target (DNS / reverse proxy / load balancer), then confirm here to commit.';
      case 'needs_manual_intervention':
        return 'Rollback could not complete safely (unsafe or ambiguous replication topology). Review the target manually before any further action.';
      case 'rollback_degraded':
        return 'Rollback finished with one or more step failures — the target may be partially rolled back. Verify manually.';
      default:
        return '';
    }
  }

  // statusVisual is called inline in markup (legacy `export let` mode — no runes).
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
      <div class="w-2 h-2 rounded-full {pipelineRunning ? 'bg-accent animate-pulse' : statusVisual(currentState).dot}"></div>
      <span class="text-xs font-medium text-fg-muted">{statusVisual(currentState).label}</span>
      {#if operatorGuidance(currentState)}
        <span class="text-xs text-warning hidden lg:inline" title={operatorGuidance(currentState)}>⚠</span>
      {/if}
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
      <span class="flex items-center gap-1.5 text-xs" role="status">
        {#if wsConnectionState === 'connected'}
          <span class="h-2 w-2 rounded-full bg-success"></span>
          <span class="text-success">Live</span>
        {:else if wsConnectionState === 'replaying'}
          <span class="h-2 w-2 rounded-full bg-info animate-pulse"></span>
          <span class="text-info">Replaying history</span>
        {:else if wsConnectionState === 'stale'}
          <span class="h-2 w-2 rounded-full bg-warning"></span>
          <span class="text-warning" title="No live frame received recently — shown data may be out of date">Stale</span>
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
