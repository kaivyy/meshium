<script lang="ts">
  import Card from './ui/Card.svelte';
  import Badge from './ui/Badge.svelte';

  export let plannerResult: {
    workloads?: any[];
    dependencyGraph?: { nodes?: any[]; edges?: any[] };
    compatibilityIssues?: any[];
    strategy?: any;
    warnings?: any[];
    riskScore?: number;
    blockingIssues?: number;
    recommendationCount?: number;
  } | null = null;

  export let loading = false;

  function workloadTypeColor(type: string): string {
    const colors: Record<string, string> = {
      database: 'bg-error/15 text-error',
      cache: 'bg-warning/15 text-warning',
      queue: 'bg-accent/15 text-accent',
      worker: 'bg-accent/15 text-accent',
      reverse_proxy: 'bg-info/15 text-info',
      monitoring: 'bg-info/15 text-info',
      stateless_application: 'bg-success/15 text-success',
      stateful_application: 'bg-warning/15 text-warning',
      scheduler: 'bg-accent/15 text-accent',
      storage: 'bg-info/15 text-info',
      unknown: 'bg-surface-muted text-fg-muted',
    };
    return colors[type] || 'bg-surface-muted text-fg-muted';
  }

  function severityColor(severity: string): string {
    const colors: Record<string, string> = {
      info: 'text-info',
      warning: 'text-warning',
      error: 'text-error',
      critical: 'text-error font-bold',
    };
    return colors[severity] || 'text-fg-muted';
  }

  function riskColor(score: number): string {
    if (score <= 20) return 'text-success';
    if (score <= 50) return 'text-warning';
    if (score <= 75) return 'text-warning';
    return 'text-error';
  }

  $: workloads = plannerResult?.workloads || [];
  $: graph = plannerResult?.dependencyGraph;
  $: compatIssues = plannerResult?.compatibilityIssues || [];
  $: strategy = plannerResult?.strategy;
  $: warnings = plannerResult?.warnings || [];
  $: blockingCount = warnings.filter((w: any) => w.blocking).length;
  $: criticalIssues = compatIssues.filter((i: any) => i.blocking).length;
  $: riskScore = plannerResult?.riskScore || 0;
</script>

{#if loading}
  <div class="flex items-center justify-center py-8">
    <div class="animate-spin rounded-full h-8 w-8 border-b-2 border-accent"></div>
    <span class="ml-3 text-fg-subtle">Analyzing workloads...</span>
  </div>
{:else if plannerResult}
  <!-- Risk Score Header -->
  <div class="mb-6 p-4 rounded-lg border {riskScore > 50 ? 'border-error/30 bg-error/10' : riskScore > 25 ? 'border-warning/30 bg-warning/10' : 'border-success/30 bg-success/10'}">
    <div class="flex items-center justify-between">
      <div>
        <h3 class="text-lg font-semibold text-fg">Migration Risk Assessment</h3>
        <p class="text-sm text-fg-muted">
          {criticalIssues} blocking issue{criticalIssues !== 1 ? 's' : ''} |
          {blockingCount} blocking warning{blockingCount !== 1 ? 's' : ''} |
          {plannerResult.recommendationCount || 0} recommendation{((plannerResult.recommendationCount || 0) !== 1) ? 's' : ''}
        </p>
      </div>
      <div class="text-right">
        <div class="text-3xl font-bold {riskColor(riskScore)}">
          {Math.round(riskScore)}
        </div>
        <div class="text-xs text-fg-subtle">Risk Score</div>
      </div>
    </div>
  </div>

  <!-- Detected Workloads -->
  {#if workloads.length > 0}
    <Card>
      <h3 class="text-lg font-semibold mb-3">Detected Workloads ({workloads.length})</h3>
      <div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-3">
        {#each workloads as workload}
          <div class="p-3 rounded-lg border border-border hover:border-border-strong">
            <div class="flex items-center justify-between mb-1">
              <span class="font-medium text-sm truncate">{workload.name}</span>
              <span class="px-2 py-0.5 rounded text-xs font-medium {workloadTypeColor(workload.type)}">
                {workload.type?.replace(/_/g, ' ')}
              </span>
            </div>
            <div class="text-xs text-fg-subtle">
              Confidence: {Math.round((workload.confidence || 0) * 100)}%
            </div>
            {#if workload.reasons?.length}
              <div class="mt-1 text-xs text-fg-subtle">
                {workload.reasons[0]}
              </div>
            {/if}
          </div>
        {/each}
      </div>
    </Card>
  {/if}

  <!-- Dependency Graph -->
  {#if graph && (graph.nodes?.length || 0) > 0}
    <Card>
      <h3 class="text-lg font-semibold mb-3">Dependency Graph</h3>
      <div class="mb-3 text-sm text-fg-subtle">
        {graph.nodes?.length || 0} nodes, {graph.edges?.length || 0} dependencies
      </div>
      <div class="space-y-2">
        {#each (graph.edges || []) as edge}
          <div class="flex items-center text-sm p-2 rounded bg-surface-muted">
            <span class="font-mono text-info">{edge.from}</span>
            <svg class="w-4 h-4 mx-2 text-fg-subtle" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 5l7 7-7 7"/>
            </svg>
            <span class="font-mono text-success">{edge.to}</span>
            <span class="ml-2 text-xs text-fg-subtle truncate">{edge.reason}</span>
          </div>
        {/each}
      </div>
    </Card>
  {/if}

  <!-- Migration Strategy -->
  {#if strategy}
    <Card>
      <h3 class="text-lg font-semibold mb-3">Migration Strategy</h3>
      <div class="p-4 rounded-lg bg-info/10 border border-info/30">
        <div class="flex items-center gap-2 mb-2">
          <span class="px-3 py-1 rounded-full text-sm font-semibold bg-accent text-accent-fg">
            {(strategy.strategy || '').replace(/_/g, ' ')}
          </span>
          <span class="text-sm text-fg-muted">
            Est. downtime: {strategy.estimatedDowntime || 'Unknown'}
          </span>
        </div>
        <p class="text-sm text-fg-muted mb-2">{strategy.reasoning}</p>
        <div class="flex gap-4 text-sm">
          <span class="{strategy.riskLevel === 'low' ? 'text-success' : strategy.riskLevel === 'medium' ? 'text-warning' : 'text-error'}">
            Risk: {strategy.riskLevel || 'unknown'}
          </span>
          <span class="{strategy.rollbackAvailable ? 'text-success' : 'text-error'}">
            Rollback: {strategy.rollbackAvailable ? 'Available' : 'Not available'}
          </span>
        </div>
        {#if strategy.requirements?.length}
          <div class="mt-3">
            <h4 class="text-xs font-semibold text-fg-subtle uppercase mb-1">Requirements</h4>
            <ul class="text-sm space-y-1">
              {#each strategy.requirements as req}
                <li class="flex items-start"><span class="mr-1">-</span> {req}</li>
              {/each}
            </ul>
          </div>
        {/if}
        {#if strategy.prerequisites?.length}
          <div class="mt-3">
            <h4 class="text-xs font-semibold text-fg-subtle uppercase mb-1">Prerequisites</h4>
            <ul class="text-sm space-y-1">
              {#each strategy.prerequisites as pre}
                <li class="flex items-start"><span class="mr-1">-</span> {pre}</li>
              {/each}
            </ul>
          </div>
        {/if}
      </div>
    </Card>
  {/if}

  <!-- Compatibility Issues -->
  {#if compatIssues.length > 0}
    <Card>
      <h3 class="text-lg font-semibold mb-3">Compatibility Report ({compatIssues.length})</h3>
      <div class="space-y-2">
        {#each compatIssues as issue}
          <div class="p-3 rounded-lg border {issue.blocking ? 'border-error/30 bg-error/10' : 'border-border bg-surface-muted'}">
            <div class="flex items-center justify-between mb-1">
              <div class="flex items-center gap-2">
                <span class="px-2 py-0.5 rounded text-xs font-medium bg-surface-muted text-fg-muted">{issue.category}</span>
                <span class="{severityColor(issue.severity)} text-sm font-medium">{issue.severity}</span>
              </div>
              {#if issue.blocking}
                <span class="px-2 py-0.5 rounded text-xs font-bold bg-error/15 text-error">BLOCKING</span>
              {/if}
            </div>
            <p class="text-sm text-fg-muted">{issue.message}</p>
            {#if issue.recommendation}
              <p class="text-xs text-fg-subtle mt-1">Recommendation: {issue.recommendation}</p>
            {/if}
            {#if issue.manualAction}
              <p class="text-xs text-info mt-1">Manual action: {issue.manualAction}</p>
            {/if}
          </div>
        {/each}
      </div>
    </Card>
  {/if}

  <!-- Planner Warnings -->
  {#if warnings.length > 0}
    <Card>
      <h3 class="text-lg font-semibold mb-3">
        Warnings & Recommendations ({warnings.length})
        {#if blockingCount > 0}
          <span class="text-error text-sm font-normal ml-2">{blockingCount} blocking</span>
        {/if}
      </h3>
      <div class="space-y-2">
        {#each warnings as warning}
          <div class="p-3 rounded-lg border {warning.blocking ? 'border-error/30 bg-error/10' : warning.type === 'recommendation' ? 'border-info/30 bg-info/10' : 'border-warning/30 bg-warning/10'}">
            <div class="flex items-center justify-between mb-1">
              <div class="flex items-center gap-2">
                <span class="px-2 py-0.5 rounded text-xs font-medium bg-surface-muted text-fg-muted">{warning.type?.replace(/_/g, ' ')}</span>
                <span class="{severityColor(warning.severity)} text-sm">{warning.severity}</span>
              </div>
              {#if warning.blocking}
                <span class="px-2 py-0.5 rounded text-xs font-bold bg-error/15 text-error">BLOCKING</span>
              {/if}
            </div>
            <p class="text-sm text-fg-muted">{warning.message}</p>
            {#if warning.recommendation}
              <p class="text-xs text-fg-subtle mt-1">Recommendation: {warning.recommendation}</p>
            {/if}
            {#if warning.manualAction}
              <p class="text-xs text-info mt-1">Manual action: {warning.manualAction}</p>
            {/if}
          </div>
        {/each}
      </div>
    </Card>
  {/if}
{:else}
  <div class="text-center py-8 text-fg-subtle">
    <p>Run discovery to see workload analysis, compatibility checks, and migration strategy.</p>
  </div>
{/if}
