<script lang="ts">
  import type { AuditEntry, WSMessageExtended, MigrationEvent, PlannerWarning, DependencyGraph } from '$lib/api/pipeline';
  import DependencyGraphView from './DependencyGraphView.svelte';

  export let auditTrail: AuditEntry[];
  export let wsMessages: WSMessageExtended[];
  export let events: MigrationEvent[];
  export let warnings: PlannerWarning[];
  export let dependencyGraph: DependencyGraph | null;
  export let riskReport: RiskReport | null;
  export let onRefreshAudit: () => void;
  export let onExportReport: () => void;

  type TabName = 'timeline' | 'logs' | 'events' | 'graph' | 'warnings' | 'report';
  let activeTab: TabName = 'timeline';

  const tabs: { id: TabName; label: string; icon: string }[] = [
    { id: 'timeline', label: 'Timeline', icon: '🕐' },
    { id: 'logs', label: 'Logs', icon: '📋' },
    { id: 'events', label: 'Events', icon: '⚡' },
    { id: 'graph', label: 'Dependency Graph', icon: '🔗' },
    { id: 'warnings', label: 'Warnings', icon: '⚠️' },
    { id: 'report', label: 'Report', icon: '📊' },
  ];

  function severityColor(sev: string): string {
    switch (sev) {
      case 'critical': return 'bg-error/20 text-error border-error/30';
      case 'high': return 'bg-warning/20 text-warning border-warning/30';
      case 'warning': return 'bg-warning/20 text-warning border-warning/30';
      default: return 'bg-info/20 text-info border-info/30';
    }
  }

  function warningTypeColor(type: string): string {
    switch (type) {
      case 'blocking': return 'bg-error/20 text-error';
      case 'risk': return 'bg-warning/20 text-warning';
      case 'manual_step': return 'bg-warning/20 text-warning';
      case 'recommendation': return 'bg-info/20 text-info';
      case 'rollback_note': return 'bg-accent/20 text-accent';
      default: return 'bg-surface-muted text-fg-subtle';
    }
  }

  import type { RiskReport } from '$lib/api/pipeline';
</script>

<div class="bg-surface rounded-xl border border-border flex flex-col" style="min-height: 200px;">
  <!-- Tab Bar -->
  <div class="flex items-center border-b border-border px-2 shrink-0">
    {#each tabs as tab}
      <button
        on:click={() => (activeTab = tab.id)}
        class="px-3 py-2.5 text-xs font-medium border-b-2 transition-colors {activeTab === tab.id ? 'border-accent text-accent' : 'border-transparent text-fg-subtle hover:text-fg-muted'}"
      >
        {tab.label}
        {#if tab.id === 'warnings' && warnings.length > 0}
          <span class="ml-1 px-1.5 py-0.5 rounded-full text-xs bg-warning/20 text-warning">{warnings.length}</span>
        {/if}
      </button>
    {/each}
  </div>

  <!-- Tab Content -->
  <div class="flex-1 overflow-y-auto p-3 max-h-64">
    {#if activeTab === 'timeline'}
      {#if auditTrail.length > 0}
        <div class="space-y-1.5">
          {#each auditTrail.slice(-30).reverse() as entry}
            <div class="flex items-start gap-2 text-xs">
              <span class="text-fg-subtle shrink-0 w-16">{new Date(entry.createdAt).toLocaleTimeString()}</span>
              <span class="px-1.5 py-0.5 rounded bg-surface-muted text-fg-muted shrink-0">{entry.eventType}</span>
              {#if entry.previousState && entry.newState}
                <span class="text-fg-subtle">{entry.previousState} &rarr; {entry.newState}</span>
              {/if}
            </div>
          {/each}
        </div>
      {:else}
        <div class="text-center py-6 text-fg-subtle text-sm">
          No timeline events yet
          <button on:click={onRefreshAudit} class="block mx-auto mt-2 text-xs text-accent hover:text-accent-hover">Load Timeline</button>
        </div>
      {/if}

    {:else if activeTab === 'logs'}
      <div class="font-mono text-xs space-y-1">
        {#if wsMessages.length > 0}
          {#each wsMessages.slice(-50) as msg}
            <div class="flex items-start gap-2">
              <span class="px-1.5 py-0.5 rounded shrink-0 {msg.status === 'error' ? 'bg-error/20 text-error' : msg.status === 'success' || msg.status === 'complete' ? 'bg-success/20 text-success' : 'bg-info/20 text-info'}">{msg.status}</span>
              <span class="text-fg-muted">[{msg.step}] {msg.value || msg.error || ''}</span>
            </div>
          {/each}
        {:else}
          <p class="text-fg-subtle text-center py-6">No logs yet. Start the pipeline to see real-time logs.</p>
        {/if}
      </div>

    {:else if activeTab === 'events'}
      {#if events.length > 0}
        <div class="space-y-1.5">
          {#each events.slice(-30).reverse() as event}
            <div class="flex items-start gap-2 text-xs">
              <span class="text-fg-subtle shrink-0 w-16">{new Date(event.timestamp).toLocaleTimeString()}</span>
              <span class="px-1.5 py-0.5 rounded shrink-0 {severityColor(event.level)}">{event.level}</span>
              <span class="text-fg-muted">{event.message}</span>
            </div>
          {/each}
        </div>
      {:else}
        <p class="text-fg-subtle text-center py-6 text-sm">No events recorded yet.</p>
      {/if}

    {:else if activeTab === 'graph'}
      <DependencyGraphView graph={dependencyGraph} />

    {:else if activeTab === 'warnings'}
      {#if warnings.length > 0}
        <div class="space-y-2">
          {#each warnings as warning}
            <div class="flex items-start gap-3 p-2.5 rounded-lg bg-surface-muted">
              <span class="px-2 py-0.5 rounded text-xs font-medium shrink-0 {warningTypeColor(warning.type)}">{warning.type}</span>
              <div class="flex-1">
                <div class="text-sm text-fg-muted">{warning.message}</div>
                {#if warning.recommendation}
                  <div class="text-xs text-fg-subtle mt-1">{warning.recommendation}</div>
                {/if}
              </div>
              {#if warning.blocking}
                <span class="text-xs text-error shrink-0">BLOCKING</span>
              {/if}
            </div>
          {/each}
        </div>
      {:else}
        <p class="text-fg-subtle text-center py-6 text-sm">No warnings. Run planner to check for issues.</p>
      {/if}

    {:else if activeTab === 'report'}
      <div class="space-y-3">
        {#if riskReport}
          <div class="grid grid-cols-4 gap-2">
            <div class="bg-surface-muted rounded p-2 text-center">
              <div class="text-xs text-fg-subtle">Risk Score</div>
              <div class="text-lg font-bold text-fg">{riskReport.riskScore.toFixed(0)}</div>
            </div>
            <div class="bg-surface-muted rounded p-2 text-center">
              <div class="text-xs text-fg-subtle">Data Size</div>
              <div class="text-sm font-mono text-fg">{(riskReport.dataSizeBytes / 1024 / 1024).toFixed(0)}MB</div>
            </div>
            <div class="bg-surface-muted rounded p-2 text-center">
              <div class="text-xs text-fg-subtle">Containers</div>
              <div class="text-lg font-bold text-fg">{riskReport.containerCount}</div>
            </div>
            <div class="bg-surface-muted rounded p-2 text-center">
              <div class="text-xs text-fg-subtle">Volumes</div>
              <div class="text-lg font-bold text-fg">{riskReport.volumeCount}</div>
            </div>
          </div>
        {/if}
        <button on:click={onExportReport} class="px-4 py-2 bg-accent hover:bg-accent-hover text-accent-fg rounded-lg text-sm font-medium transition-colors">
          Export Full Report
        </button>
      </div>
    {/if}
  </div>
</div>
