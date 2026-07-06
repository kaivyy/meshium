<script lang="ts">
  import type { ReplicationStatus, QueueState, ContainerHealthInfo } from '$lib/api/pipeline';

  export let replicationLag: number;
  export let healthScore: number;
  export let replicationStatus: ReplicationStatus[];
  export let queueStates: QueueState[];
  export let containerHealth: ContainerHealthInfo[];
  export let rollbackAvailable: boolean;
  export let actionLoading: boolean;
  export let onCutover: () => void;

  // ── Checklist Items ──
  $: targetContainersHealthy = containerHealth.length > 0 && containerHealth.every((c) => c.healthy);
  $: mysqlSynced = replicationStatus.length > 0 && replicationStatus.every((r) => r.status === 'running' || r.status === 'synced');
  $: redisSynced = queueStates.some((q) => q.queueType === 'redis' && q.synced) || queueStates.length === 0;
  $: workerAPaused = queueStates.some((q) => q.paused) || queueStates.length === 0;
  $: workerBReady = containerHealth.some((c) => c.name.toLowerCase().includes('worker')) ?
    containerHealth.filter((c) => c.name.toLowerCase().includes('worker')).every((c) => c.healthy) : true;
  $: replicationLagOk = replicationLag <= 5;
  $: healthScoreOk = healthScore >= 80;
  $: allChecksPassed = targetContainersHealthy && mysqlSynced && redisSynced && workerAPaused && workerBReady && replicationLagOk && healthScoreOk && rollbackAvailable;

  let checks = [
    { label: 'Target containers healthy', get done() { return targetContainersHealthy; } },
    { label: 'MySQL synced', get done() { return mysqlSynced; } },
    { label: 'Redis/BullMQ synced', get done() { return redisSynced; } },
    { label: 'Worker A paused', get done() { return workerAPaused; } },
    { label: 'Worker B ready', get done() { return workerBReady; } },
    { label: 'Replication lag < 5s', get done() { return replicationLagOk; } },
    { label: 'Health score >= 80', get done() { return healthScoreOk; } },
    { label: 'Rollback available', get done() { return rollbackAvailable; } },
  ];
</script>

<div class="bg-surface rounded-xl p-6 border border-border">
  <div class="flex items-center gap-3 mb-5">
    <div class="w-10 h-10 rounded-lg bg-warning/20 flex items-center justify-center">
      <svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="text-warning"><path d="M13 2 3 14h9l-1 8 10-12h-9l1-8z"/></svg>
    </div>
    <div>
      <h2 class="text-lg font-semibold text-fg">Cutover Readiness</h2>
      <p class="text-sm text-fg-muted">All checks must pass before cutover can start</p>
    </div>
  </div>

  <div class="space-y-2.5 mb-6">
    {#each checks as check}
      <div class="flex items-center gap-3">
        <div class="w-6 h-6 rounded-full flex items-center justify-center {check.done ? 'bg-success/20' : 'bg-surface-muted'}">
          {#if check.done}
            <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" class="text-success"><polyline points="20 6 9 17 4 12"/></svg>
          {:else}
            <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="text-fg-subtle"><circle cx="12" cy="12" r="10"/></svg>
          {/if}
        </div>
        <span class="text-sm {check.done ? 'text-fg-muted' : 'text-fg-subtle'}">{check.label}</span>
        {#if !check.done}
          <span class="ml-auto text-xs text-warning">Pending</span>
        {/if}
      </div>
    {/each}
  </div>

  <div class="flex items-center gap-3">
    <button
      on:click={onCutover}
      disabled={!allChecksPassed || actionLoading}
      class="px-6 py-2.5 bg-warning text-accent-fg hover:bg-warning/90 disabled:opacity-40 disabled:cursor-not-allowed rounded-lg font-medium transition-colors"
    >
      {actionLoading ? 'Cutover in progress...' : 'Start Cutover'}
    </button>
    {#if !allChecksPassed}
      <span class="text-xs text-fg-subtle">{checks.filter((c) => !c.done).length} checks remaining</span>
    {/if}
  </div>
</div>
