<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { page } from '$app/stores';
  import { migrationApi, wsExecute, wsRollback, wsDryRun, type WSMessage, type MigrationPlan, type MigrationStep, type DryRunResult } from '$lib/api/migrations';
  import { ArrowLeft, Play, Undo2, Trash2, Eye, Download } from 'lucide-svelte';

  const migrationId = parseInt($page.params.id);
  let plan: MigrationPlan | null = null;
  let steps: MigrationStep[] = [];
  let loading = true;
  let executing = false;
  let rollingBack = false;
  let dryRunning = false;
  let dryRunResult: DryRunResult | null = null;
  let progressMessages: WSMessage[] = [];
  let ws: WebSocket | null = null;

  onMount(async () => {
    try {
      plan = await migrationApi.get(migrationId);
      steps = await migrationApi.getSteps(migrationId);
    } catch {
      // handle error
    } finally {
      loading = false;
    }
  });

  onDestroy(() => {
    ws?.close();
  });

  function startExecution() {
    executing = true;
    progressMessages = [];

    ws = wsExecute(
      migrationId,
      (msg: WSMessage) => {
        progressMessages = [...progressMessages, msg];
        if (msg.step === 'execute' && (msg.status === 'complete' || msg.status === 'error')) {
          executing = false;
          refreshPlan();
        }
      },
      () => { executing = false; },
      () => { executing = false; }
    );
  }

  function startRollback() {
    rollingBack = true;
    progressMessages = [];

    ws = wsRollback(
      migrationId,
      (msg: WSMessage) => {
        progressMessages = [...progressMessages, msg];
        if (msg.step === 'rollback' && (msg.status === 'complete' || msg.status === 'error')) {
          rollingBack = false;
          refreshPlan();
        }
      },
      () => { rollingBack = false; },
      () => { rollingBack = false; }
    );
  }

  function startDryRun() {
    dryRunning = true;
    dryRunResult = null;
    progressMessages = [];

    ws = wsDryRun(
      migrationId,
      (msg: WSMessage) => {
        progressMessages = [...progressMessages, msg];
        if (msg.step === 'dryrun' && (msg.status === 'complete' || msg.status === 'error')) {
          dryRunning = false;
        }
      },
      () => {
        dryRunning = false;
        fetchDryRunResult();
      },
      () => { dryRunning = false; }
    );
  }

  async function fetchDryRunResult() {
    try {
      dryRunResult = await migrationApi.dryRun(migrationId);
    } catch {
      // ignore
    }
  }

  async function refreshPlan() {
    try {
      plan = await migrationApi.get(migrationId);
      steps = await migrationApi.getSteps(migrationId);
    } catch {
      // ignore
    }
  }

  async function deleteMigration() {
    if (!confirm('Delete this migration? This cannot be undone.')) return;
    await migrationApi.delete(migrationId);
    window.location.href = '/migrations';
  }

  function exportMigration() {
    window.open(`/api/migrations/${migrationId}/export`, '_blank');
  }

  function statusColor(status: string): string {
    switch (status) {
      case 'completed': case 'success': return 'text-green-600';
      case 'failed': case 'error': return 'text-red-600';
      case 'running': case 'progress': return 'text-blue-600';
      case 'planned': return 'text-slate-500';
      case 'rolled_back': return 'text-yellow-600';
      default: return 'text-slate-500';
    }
  }
</script>

<div class="p-4 sm:p-6">
  <a href="/migrations" class="text-sm text-slate-600 hover:text-slate-900 flex items-center gap-1 mb-4">
    <ArrowLeft size={16} /> Back to Migrations
  </a>

  {#if loading}
    <p class="text-slate-500">Loading...</p>
  {:else if !plan}
    <p class="text-red-500">Migration not found</p>
  {:else}
    <div class="flex flex-col gap-4 mb-6 lg:flex-row lg:items-center lg:justify-between">
      <div>
        <h1 class="text-xl font-bold text-slate-900">Migration #{plan.id}</h1>
        <p class="text-sm text-slate-500">
          Source: Server #{plan.sourceServerId} → Target: Server #{plan.targetServerId}
        </p>
      </div>
      <div class="flex flex-wrap items-center gap-2">
        {#if plan.status === 'planned'}
          <button
            on:click={startDryRun}
            disabled={dryRunning}
            class="flex items-center gap-1 px-4 py-2 bg-purple-600 text-white rounded-lg hover:bg-purple-700 disabled:opacity-50 text-sm font-medium"
          >
            <Eye size={16} /> {dryRunning ? 'Analyzing...' : 'Dry Run'}
          </button>
          <button
            on:click={startExecution}
            disabled={executing}
            class="flex items-center gap-1 px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 disabled:opacity-50 text-sm font-medium"
          >
            <Play size={16} /> {executing ? 'Executing...' : 'Execute'}
          </button>
        {/if}
        {#if plan.status === 'completed' || plan.status === 'failed'}
          <button
            on:click={startRollback}
            disabled={rollingBack}
            class="flex items-center gap-1 px-4 py-2 bg-yellow-600 text-white rounded-lg hover:bg-yellow-700 disabled:opacity-50 text-sm font-medium"
          >
            <Undo2 size={16} /> {rollingBack ? 'Rolling back...' : 'Rollback'}
          </button>
        {/if}
        <button
          on:click={exportMigration}
          class="flex items-center gap-1 px-4 py-2 text-slate-600 border border-slate-200 rounded-lg hover:bg-slate-50 text-sm font-medium"
        >
          <Download size={16} /> Export
        </button>
        <button
          on:click={deleteMigration}
          class="flex items-center gap-1 px-4 py-2 text-red-600 border border-red-200 rounded-lg hover:bg-red-50 text-sm font-medium"
        >
          <Trash2 size={16} /> Delete
        </button>
      </div>
    </div>

    <!-- Status badge -->
    <div class="mb-4">
      <span class="px-3 py-1 rounded-full text-sm font-medium
        {plan.status === 'completed' ? 'bg-green-100 text-green-700' :
         plan.status === 'failed' ? 'bg-red-100 text-red-700' :
         plan.status === 'running' ? 'bg-blue-100 text-blue-700' :
         plan.status === 'rolled_back' ? 'bg-yellow-100 text-yellow-700' :
         'bg-slate-100 text-slate-700'}">
        {plan.status}
      </span>
    </div>

    <!-- Categories -->
    <div class="mb-6">
      <h2 class="text-sm font-semibold mb-2 text-slate-900">Categories</h2>
      <div class="flex flex-wrap gap-2">
        {#each plan.categories as cat}
          <span class="px-2 py-1 bg-blue-50 text-blue-700 text-xs rounded">{cat}</span>
        {/each}
      </div>
    </div>

    <!-- Dry Run Results -->
    {#if dryRunResult}
      <div class="mb-6 bg-purple-50 border border-purple-200 rounded-lg p-4">
        <h2 class="text-sm font-semibold mb-3 text-purple-900">Dry Run Results</h2>
        <div class="grid grid-cols-3 gap-4 mb-4">
          <div class="text-center">
            <div class="text-2xl font-bold text-green-600">{dryRunResult.summary.addCount}</div>
            <div class="text-xs text-slate-500">Additions</div>
          </div>
          <div class="text-center">
            <div class="text-2xl font-bold text-yellow-600">{dryRunResult.summary.modifyCount}</div>
            <div class="text-xs text-slate-500">Modifications</div>
          </div>
          <div class="text-center">
            <div class="text-2xl font-bold text-red-600">{dryRunResult.summary.removeCount}</div>
            <div class="text-xs text-slate-500">Removals</div>
          </div>
        </div>
        {#each dryRunResult.categories as cat}
          <div class="mb-3">
            <h3 class="text-xs font-semibold text-purple-700 mb-1">{cat.category}</h3>
            {#each cat.changes as change}
              <div class="text-xs flex items-center gap-2 py-1">
                <span class="px-1.5 py-0.5 rounded text-white
                  {change.type === 'add' ? 'bg-green-500' :
                   change.type === 'modify' ? 'bg-yellow-500' :
                   'bg-red-500'}">
                  {change.type}
                </span>
                <span class="text-slate-600 break-all">{change.detail}</span>
              </div>
            {/each}
          </div>
        {/each}
      </div>
    {/if}

    <!-- Steps -->
    {#if steps.length > 0}
      <div class="mb-6">
        <h2 class="text-sm font-semibold mb-2 text-slate-900">Steps</h2>
        <div class="space-y-2">
          {#each steps as step}
            <div class="flex flex-wrap items-center gap-2 text-sm">
              <span class="w-2 h-2 rounded-full shrink-0
                {step.status === 'completed' ? 'bg-green-500' :
                 step.status === 'failed' ? 'bg-red-500' :
                 step.status === 'running' ? 'bg-blue-500' :
                 'bg-slate-300'}">
              </span>
              <span class="font-mono text-slate-700">{step.category}:{step.action}</span>
              <span class={statusColor(step.status)}>{step.status}</span>
              {#if step.error}
                <span class="text-red-500 break-all">→ {step.error}</span>
              {/if}
            </div>
          {/each}
        </div>
      </div>
    {/if}

    <!-- Live progress -->
    {#if progressMessages.length > 0}
      <div class="bg-slate-900 text-slate-100 rounded-lg p-4 max-h-96 overflow-auto">
        <h3 class="text-xs font-semibold mb-2 text-slate-400">Live Progress</h3>
        <div class="space-y-1">
          {#each progressMessages as msg}
            <div class="text-xs font-mono break-all">
              <span class={msg.status === 'error' ? 'text-red-400' : msg.status === 'success' || msg.status === 'complete' ? 'text-green-400' : 'text-blue-400'}>
                [{msg.status}]
              </span>
              <span class="text-slate-300">{msg.step}</span>
              {#if msg.value}
                <span class="text-slate-500">→ {msg.value}</span>
              {/if}
              {#if msg.error}
                <span class="text-red-400">→ {msg.error}</span>
              {/if}
            </div>
          {/each}
        </div>
      </div>
    {/if}
  {/if}
</div>
