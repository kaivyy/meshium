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
      database: 'bg-red-100 text-red-800',
      cache: 'bg-yellow-100 text-yellow-800',
      queue: 'bg-purple-100 text-purple-800',
      worker: 'bg-indigo-100 text-indigo-800',
      reverse_proxy: 'bg-blue-100 text-blue-800',
      monitoring: 'bg-teal-100 text-teal-800',
      stateless_application: 'bg-green-100 text-green-800',
      stateful_application: 'bg-orange-100 text-orange-800',
      scheduler: 'bg-pink-100 text-pink-800',
      storage: 'bg-cyan-100 text-cyan-800',
      unknown: 'bg-gray-100 text-gray-800',
    };
    return colors[type] || 'bg-gray-100 text-gray-800';
  }

  function severityColor(severity: string): string {
    const colors: Record<string, string> = {
      info: 'text-blue-600',
      warning: 'text-yellow-600',
      error: 'text-red-600',
      critical: 'text-red-800 font-bold',
    };
    return colors[severity] || 'text-gray-600';
  }

  function riskColor(score: number): string {
    if (score <= 20) return 'text-green-600';
    if (score <= 50) return 'text-yellow-600';
    if (score <= 75) return 'text-orange-600';
    return 'text-red-600';
  }

  $: workloads = plannerResult?.workloads || [];
  $: graph = plannerResult?.dependencyGraph;
  $: compatIssues = plannerResult?.compatibilityIssues || [];
  $: strategy = plannerResult?.strategy;
  $: warnings = plannerResult?.warnings || [];
  $: blockingCount = warnings.filter((w: any) => w.blocking).length;
  $: criticalIssues = compatIssues.filter((i: any) => i.blocking).length;
</script>

{#if loading}
  <div class="flex items-center justify-center py-8">
    <div class="animate-spin rounded-full h-8 w-8 border-b-2 border-blue-600"></div>
    <span class="ml-3 text-gray-500">Analyzing workloads...</span>
  </div>
{:else if plannerResult}
  <!-- Risk Score Header -->
  <div class="mb-6 p-4 rounded-lg border {plannerResult.riskScore > 50 ? 'border-red-200 bg-red-50' : plannerResult.riskScore > 25 ? 'border-yellow-200 bg-yellow-50' : 'border-green-200 bg-green-50'}">
    <div class="flex items-center justify-between">
      <div>
        <h3 class="text-lg font-semibold">Migration Risk Assessment</h3>
        <p class="text-sm text-gray-600">
          {criticalIssues} blocking issue{criticalIssues !== 1 ? 's' : ''} |
          {blockingCount} blocking warning{blockingCount !== 1 ? 's' : ''} |
          {plannerResult.recommendationCount || 0} recommendation{((plannerResult.recommendationCount || 0) !== 1) ? 's' : ''}
        </p>
      </div>
      <div class="text-right">
        <div class="text-3xl font-bold {riskColor(plannerResult.riskScore || 0)}">
          {Math.round(plannerResult.riskScore || 0)}
        </div>
        <div class="text-xs text-gray-500">Risk Score</div>
      </div>
    </div>
  </div>

  <!-- Detected Workloads -->
  {#if workloads.length > 0}
    <Card>
      <h3 class="text-lg font-semibold mb-3">Detected Workloads ({workloads.length})</h3>
      <div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-3">
        {#each workloads as workload}
          <div class="p-3 rounded-lg border border-gray-200 hover:border-gray-300">
            <div class="flex items-center justify-between mb-1">
              <span class="font-medium text-sm truncate">{workload.name}</span>
              <span class="px-2 py-0.5 rounded text-xs font-medium {workloadTypeColor(workload.type)}">
                {workload.type?.replace(/_/g, ' ')}
              </span>
            </div>
            <div class="text-xs text-gray-500">
              Confidence: {Math.round((workload.confidence || 0) * 100)}%
            </div>
            {#if workload.reasons?.length}
              <div class="mt-1 text-xs text-gray-400">
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
      <div class="mb-3 text-sm text-gray-500">
        {graph.nodes?.length || 0} nodes, {graph.edges?.length || 0} dependencies
      </div>
      <div class="space-y-2">
        {#each (graph.edges || []) as edge}
          <div class="flex items-center text-sm p-2 rounded bg-gray-50">
            <span class="font-mono text-blue-600">{edge.from}</span>
            <svg class="w-4 h-4 mx-2 text-gray-400" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 5l7 7-7 7"/>
            </svg>
            <span class="font-mono text-green-600">{edge.to}</span>
            <span class="ml-2 text-xs text-gray-400 truncate">{edge.reason}</span>
          </div>
        {/each}
      </div>
    </Card>
  {/if}

  <!-- Migration Strategy -->
  {#if strategy}
    <Card>
      <h3 class="text-lg font-semibold mb-3">Migration Strategy</h3>
      <div class="p-4 rounded-lg bg-blue-50 border border-blue-200">
        <div class="flex items-center gap-2 mb-2">
          <span class="px-3 py-1 rounded-full text-sm font-semibold bg-blue-600 text-white">
            {(strategy.strategy || '').replace(/_/g, ' ')}
          </span>
          <span class="text-sm text-gray-600">
            Est. downtime: {strategy.estimatedDowntime || 'Unknown'}
          </span>
        </div>
        <p class="text-sm text-gray-700 mb-2">{strategy.reasoning}</p>
        <div class="flex gap-4 text-sm">
          <span class="{strategy.riskLevel === 'low' ? 'text-green-600' : strategy.riskLevel === 'medium' ? 'text-yellow-600' : 'text-red-600'}">
            Risk: {strategy.riskLevel || 'unknown'}
          </span>
          <span class="{strategy.rollbackAvailable ? 'text-green-600' : 'text-red-600'}">
            Rollback: {strategy.rollbackAvailable ? 'Available' : 'Not available'}
          </span>
        </div>
        {#if strategy.requirements?.length}
          <div class="mt-3">
            <h4 class="text-xs font-semibold text-gray-500 uppercase mb-1">Requirements</h4>
            <ul class="text-sm space-y-1">
              {#each strategy.requirements as req}
                <li class="flex items-start"><span class="mr-1">-</span> {req}</li>
              {/each}
            </ul>
          </div>
        {/if}
        {#if strategy.prerequisites?.length}
          <div class="mt-3">
            <h4 class="text-xs font-semibold text-gray-500 uppercase mb-1">Prerequisites</h4>
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
          <div class="p-3 rounded-lg border {issue.blocking ? 'border-red-200 bg-red-50' : 'border-gray-200 bg-gray-50'}">
            <div class="flex items-center justify-between mb-1">
              <div class="flex items-center gap-2">
                <span class="px-2 py-0.5 rounded text-xs font-medium bg-gray-200">{issue.category}</span>
                <span class="{severityColor(issue.severity)} text-sm font-medium">{issue.severity}</span>
              </div>
              {#if issue.blocking}
                <span class="px-2 py-0.5 rounded text-xs font-bold bg-red-100 text-red-700">BLOCKING</span>
              {/if}
            </div>
            <p class="text-sm text-gray-700">{issue.message}</p>
            {#if issue.recommendation}
              <p class="text-xs text-gray-500 mt-1">Recommendation: {issue.recommendation}</p>
            {/if}
            {#if issue.manualAction}
              <p class="text-xs text-blue-600 mt-1">Manual action: {issue.manualAction}</p>
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
          <span class="text-red-600 text-sm font-normal ml-2">{blockingCount} blocking</span>
        {/if}
      </h3>
      <div class="space-y-2">
        {#each warnings as warning}
          <div class="p-3 rounded-lg border {warning.blocking ? 'border-red-200 bg-red-50' : warning.type === 'recommendation' ? 'border-blue-200 bg-blue-50' : 'border-yellow-200 bg-yellow-50'}">
            <div class="flex items-center justify-between mb-1">
              <div class="flex items-center gap-2">
                <span class="px-2 py-0.5 rounded text-xs font-medium bg-gray-200">{warning.type?.replace(/_/g, ' ')}</span>
                <span class="{severityColor(warning.severity)} text-sm">{warning.severity}</span>
              </div>
              {#if warning.blocking}
                <span class="px-2 py-0.5 rounded text-xs font-bold bg-red-100 text-red-700">BLOCKING</span>
              {/if}
            </div>
            <p class="text-sm text-gray-700">{warning.message}</p>
            {#if warning.recommendation}
              <p class="text-xs text-gray-500 mt-1">Recommendation: {warning.recommendation}</p>
            {/if}
            {#if warning.manualAction}
              <p class="text-xs text-blue-600 mt-1">Manual action: {warning.manualAction}</p>
            {/if}
          </div>
        {/each}
      </div>
    </Card>
  {/if}
{:else}
  <div class="text-center py-8 text-gray-400">
    <p>Run discovery to see workload analysis, compatibility checks, and migration strategy.</p>
  </div>
{/if}
