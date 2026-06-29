<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/state';
  import { ArrowLeft, ArrowRight, AlertTriangle, Ban, CheckCircle2, Loader2, RotateCcw, Server, ShieldCheck } from 'lucide-svelte';
  import { discoveryApi, type CompatibilityReport } from '$lib/api/discovery';
  import { plannerApi } from '$lib/api/planner';
  import { Card, EmptyState, Spinner } from '$lib/components/ui';
  import { fetchServers, serverStore } from '$lib/stores/servers';
  import { toast } from '$lib/stores/toast';

  let currentStep = $state(1);
  let sourceID = $state<number | null>(null);
  let targetID = $state<number | null>(null);
  let compatReport = $state<CompatibilityReport | null>(null);
  let compatLoading = $state(false);
  let creating = $state(false);
  let error = $state<string | null>(null);
  let compatRequestedFor = $state<string | null>(null);

  const servers = $derived($serverStore.servers);
  const sourceServer = $derived(servers.find((server) => server.id === sourceID) ?? null);
  const targetServer = $derived(servers.find((server) => server.id === targetID) ?? null);
  const targetServers = $derived(servers.filter((server) => server.id !== sourceID));
  const selectedSourceQuery = $derived(page.url.searchParams.get('source'));
  const selectedTargetQuery = $derived(page.url.searchParams.get('target'));

  const stepLabels = ['Source', 'Target', 'Compatibility', 'Generate'];

  function normalizeServerSelection(value: string): number | null {
    if (!value) return null;
    const parsed = Number(value);
    return Number.isNaN(parsed) ? null : parsed;
  }

  function resetCompatibility(): void {
    compatReport = null;
    error = null;
    compatRequestedFor = null;
  }

  function selectSource(value: string): void {
    sourceID = normalizeServerSelection(value);
    if (targetID !== null && targetID === sourceID) {
      targetID = null;
    }
    resetCompatibility();
  }

  function selectTarget(value: string): void {
    targetID = normalizeServerSelection(value);
    resetCompatibility();
  }

  function canContinueFromStep1(): boolean {
    return sourceID !== null;
  }

  function canContinueFromStep2(): boolean {
    return targetID !== null && targetID !== sourceID;
  }

  function canContinueFromStep3(): boolean {
    return Boolean(compatReport) && compatReport?.blockers.length === 0 && !compatLoading;
  }

  async function loadCompatibility(): Promise<void> {
    if (!sourceID || !targetID || sourceID === targetID) return;

    const key = `${sourceID}:${targetID}`;
    compatRequestedFor = key;
    compatLoading = true;
    error = null;
    compatReport = null;

    try {
      compatReport = await discoveryApi.getCompatibility(sourceID, targetID);
      compatRequestedFor = key;
    } catch (err) {
      compatRequestedFor = key;
      error = err instanceof Error ? err.message : 'Failed to run compatibility check';
      toast.error(error);
    } finally {
      compatLoading = false;
    }
  }

  function runCompatibilityCheck(): void {
    if (!sourceID || !targetID || sourceID === targetID) {
      error = 'Select different source and target servers first.';
      return;
    }

    compatRequestedFor = null;
    void loadCompatibility();
  }

  async function generatePlan(): Promise<void> {
    if (!sourceID || !targetID || sourceID === targetID) {
      error = 'Select different source and target servers first.';
      return;
    }

    creating = true;
    error = null;

    try {
      const plan = await plannerApi.create(sourceID, targetID);
      toast.success('Migration plan created');
      await goto(`/plans/${plan.id}`);
    } catch (err) {
      error = err instanceof Error ? err.message : 'Failed to create migration plan';
      toast.error(error);
    } finally {
      creating = false;
    }
  }

  function previousStep(): void {
    if (currentStep > 1) currentStep -= 1;
  }

  function nextStep(): void {
    if (currentStep === 1 && canContinueFromStep1()) {
      currentStep = 2;
      return;
    }

    if (currentStep === 2 && canContinueFromStep2()) {
      currentStep = 3;
      return;
    }

    if (currentStep === 3 && canContinueFromStep3()) {
      currentStep = 4;
    }
  }

  onMount(async () => {
    if ($serverStore.servers.length === 0) {
      await fetchServers();
    }

    const sourceParam = selectedSourceQuery;
    const targetParam = selectedTargetQuery;

    if (sourceParam) {
      selectSource(sourceParam);
    }

    if (targetParam) {
      selectTarget(targetParam);
      currentStep = 3;
    }
  });

  $effect(() => {
    if (currentStep !== 3) return;
    if (!sourceID || !targetID || sourceID === targetID) return;

    const key = `${sourceID}:${targetID}`;
    if (compatRequestedFor === key) return;

    void loadCompatibility();
  });
</script>

<svelte:head>
  <title>New Plan</title>
</svelte:head>

<div class="p-4 sm:p-6 max-w-4xl mx-auto">
  <div class="mb-6 flex items-center justify-between gap-4">
    <a href="/plans" class="inline-flex items-center gap-2 text-sm text-slate-600 transition-colors hover:text-slate-900">
      <ArrowLeft size={16} />
      Back to plans
    </a>

    <div class="hidden sm:flex items-center gap-2 text-xs font-medium text-slate-500">
      <ShieldCheck size={14} class="text-blue-600" />
      Planning a migration
    </div>
  </div>

  <div class="mb-8">
    <h1 class="text-2xl font-semibold text-slate-900">New Migration Plan</h1>
    <p class="mt-1 text-sm text-slate-500">Choose servers, verify compatibility, and generate a migration plan.</p>
  </div>

  <div class="mb-8 flex items-center gap-2">
    {#each stepLabels as label, index}
      {@const step = index + 1}
      <div class="flex min-w-0 flex-1 items-center gap-2">
        <div
          class={`flex h-8 w-8 shrink-0 items-center justify-center rounded-full text-sm font-semibold ${
            step < currentStep ? 'bg-green-500 text-white' : step === currentStep ? 'bg-blue-600 text-white' : 'bg-slate-200 text-slate-500'
          }`}
        >
          {step < currentStep ? '✓' : step}
        </div>
        <div class="min-w-0">
          <div class={`text-xs font-semibold uppercase tracking-wide ${step === currentStep ? 'text-slate-900' : 'text-slate-500'}`}>{label}</div>
          <div class="text-xs text-slate-500">Step {step}</div>
        </div>
        {#if step < stepLabels.length}
          <div class={`mx-2 h-0.5 flex-1 rounded ${step < currentStep ? 'bg-green-500' : 'bg-slate-200'}`}></div>
        {/if}
      </div>
    {/each}
  </div>

  {#if error}
    <div class="mb-4 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">
      {error}
    </div>
  {/if}

  {#if $serverStore.loading}
    <Card padding="lg">
      <div class="flex items-center justify-center gap-3 py-10 text-slate-500">
        <Spinner size="md" label="Loading servers" />
        <span>Loading available servers...</span>
      </div>
    </Card>
  {:else if servers.length === 0}
    <EmptyState
      title="No servers available"
      description="Add at least two servers before creating a migration plan."
      icon={serversEmptyIcon}
      action={serversEmptyAction}
    />
  {:else if currentStep === 1}
    <Card padding="lg">
      <div class="space-y-5">
        <div>
          <h2 class="text-lg font-semibold text-slate-900">Step 1 · Select source server</h2>
          <p class="mt-1 text-sm text-slate-500">Choose the server you want to migrate from.</p>
        </div>

        <div class="space-y-2">
          <label for="source-server" class="text-sm font-medium text-slate-900">Source server</label>
          <div class="relative">
            <select
              id="source-server"
              class="w-full appearance-none rounded-lg border border-slate-300 bg-white px-4 py-2.5 pr-10 text-sm text-slate-900 shadow-sm outline-none transition focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20"
              value={sourceID ?? ''}
              onchange={(event) => selectSource((event.currentTarget as HTMLSelectElement).value)}
            >
              <option value="">Select a source server</option>
              {#each servers as server}
                <option value={server.id}>{server.name} · {server.host}</option>
              {/each}
            </select>
            <div class="pointer-events-none absolute inset-y-0 right-3 flex items-center text-slate-400">
              <ArrowRight size={16} class="rotate-90" />
            </div>
          </div>
        </div>

        {#if sourceServer}
          <div class="rounded-lg border border-slate-200 bg-slate-50 px-4 py-3 text-sm text-slate-600">
            Selected source: <span class="font-medium text-slate-900">{sourceServer.name}</span> · {sourceServer.host}
          </div>
        {/if}
      </div>
    </Card>
  {:else if currentStep === 2}
    <Card padding="lg">
      <div class="space-y-5">
        <div>
          <h2 class="text-lg font-semibold text-slate-900">Step 2 · Select target server</h2>
          <p class="mt-1 text-sm text-slate-500">Choose the server you want to migrate to. It must be different from the source.</p>
        </div>

        <div class="space-y-2">
          <label for="target-server" class="text-sm font-medium text-slate-900">Target server</label>
          <div class="relative">
            <select
              id="target-server"
              class="w-full appearance-none rounded-lg border border-slate-300 bg-white px-4 py-2.5 pr-10 text-sm text-slate-900 shadow-sm outline-none transition focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20"
              value={targetID ?? ''}
              onchange={(event) => selectTarget((event.currentTarget as HTMLSelectElement).value)}
            >
              <option value="">Select a target server</option>
              {#each targetServers as server}
                <option value={server.id}>{server.name} · {server.host}</option>
              {/each}
            </select>
            <div class="pointer-events-none absolute inset-y-0 right-3 flex items-center text-slate-400">
              <ArrowRight size={16} class="rotate-90" />
            </div>
          </div>
        </div>

        {#if targetServer}
          <div class="rounded-lg border border-slate-200 bg-slate-50 px-4 py-3 text-sm text-slate-600">
            Selected target: <span class="font-medium text-slate-900">{targetServer.name}</span> · {targetServer.host}
          </div>
        {/if}
      </div>
    </Card>
  {:else if currentStep === 3}
    <Card padding="lg">
      <div class="space-y-5">
        <div class="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
          <div>
            <h2 class="text-lg font-semibold text-slate-900">Step 3 · Compatibility check</h2>
            <p class="mt-1 text-sm text-slate-500">Validate the selected servers before creating a plan.</p>
          </div>

          <button
            type="button"
            class="inline-flex items-center gap-2 rounded-lg border border-slate-300 bg-white px-4 py-2 text-sm font-medium text-slate-700 transition-colors hover:bg-slate-50 disabled:cursor-not-allowed disabled:opacity-50"
            onclick={runCompatibilityCheck}
            disabled={compatLoading || !sourceID || !targetID || sourceID === targetID}
          >
            {#if compatLoading}
              <Loader2 size={16} class="animate-spin" />
              Checking...
            {:else}
              <RotateCcw size={16} />
              Run compatibility check
            {/if}
          </button>
        </div>

        {#if compatLoading}
          <div class="flex items-center gap-3 rounded-lg border border-slate-200 bg-slate-50 px-4 py-3 text-sm text-slate-600">
            <Spinner size="sm" label="Checking compatibility" />
            Running compatibility checks...
          </div>
        {:else if compatReport}
          <div class="space-y-4">
            {#if compatReport.blockers.length > 0}
              <div class="rounded-lg border border-red-200 bg-red-50 p-4">
                <div class="mb-2 flex items-center gap-2 text-sm font-semibold text-red-800">
                  <Ban size={16} />
                  Cannot create plan
                </div>
                <div class="space-y-2">
                  {#each compatReport.blockers as blocker}
                    <div class="rounded-lg border border-red-200 bg-white/70 px-3 py-2 text-sm text-red-800">
                      <div class="font-medium">{blocker.code}</div>
                      <div class="text-red-700">{blocker.message}</div>
                    </div>
                  {/each}
                </div>
              </div>
            {/if}

            {#if compatReport.warnings.length > 0}
              <div class="rounded-lg border border-yellow-200 bg-yellow-50 p-4">
                <div class="mb-2 flex items-center gap-2 text-sm font-semibold text-yellow-800">
                  <AlertTriangle size={16} />
                  Warnings
                </div>
                <div class="space-y-2">
                  {#each compatReport.warnings as warning}
                    <div class="rounded-lg border border-yellow-200 bg-white/70 px-3 py-2 text-sm text-yellow-800">
                      <div class="font-medium">{warning.code}</div>
                      <div class="text-yellow-700">{warning.message}</div>
                    </div>
                  {/each}
                </div>
              </div>
            {/if}

            <div class="rounded-lg border border-slate-200 bg-white p-4">
              <div class="mb-3 flex items-center gap-2 text-sm font-semibold text-slate-900">
                <CheckCircle2 size={16} class="text-green-600" />
                Compatibility checks
              </div>
              <div class="space-y-2">
                {#each compatReport.checks as check}
                  <div class={`flex items-start gap-3 rounded-lg border px-3 py-2 text-sm ${check.passed ? 'border-green-200 bg-green-50' : 'border-red-200 bg-red-50'}`}>
                    {#if check.passed}
                      <CheckCircle2 size={16} class="mt-0.5 shrink-0 text-green-600" />
                    {:else}
                      <Ban size={16} class="mt-0.5 shrink-0 text-red-600" />
                    {/if}
                    <div>
                      <div class="font-medium text-slate-900">{check.name}</div>
                      <div class={check.passed ? 'text-green-700' : 'text-red-700'}>{check.message}</div>
                    </div>
                  </div>
                {/each}
              </div>
            </div>
          </div>
        {/if}

        {#if compatReport && compatReport.blockers.length === 0 && compatReport.warnings.length === 0}
          <div class="rounded-lg border border-green-200 bg-green-50 px-4 py-3 text-sm text-green-800">
            Ready to migrate.
          </div>
        {/if}
      </div>
    </Card>
  {:else}
    <Card padding="lg">
      <div class="space-y-5">
        <div>
          <h2 class="text-lg font-semibold text-slate-900">Step 4 · Generate plan</h2>
          <p class="mt-1 text-sm text-slate-500">Review the selected servers, then generate the migration plan.</p>
        </div>

        <div class="grid gap-4 sm:grid-cols-2">
          <div class="rounded-lg border border-slate-200 bg-slate-50 px-4 py-3">
            <div class="text-xs font-semibold uppercase tracking-wide text-slate-500">Source</div>
            <div class="mt-1 text-sm font-medium text-slate-900">{sourceServer ? sourceServer.name : '—'}</div>
            <div class="text-sm text-slate-500">{sourceServer ? sourceServer.host : 'Select a source server'}</div>
          </div>
          <div class="rounded-lg border border-slate-200 bg-slate-50 px-4 py-3">
            <div class="text-xs font-semibold uppercase tracking-wide text-slate-500">Target</div>
            <div class="mt-1 text-sm font-medium text-slate-900">{targetServer ? targetServer.name : '—'}</div>
            <div class="text-sm text-slate-500">{targetServer ? targetServer.host : 'Select a target server'}</div>
          </div>
        </div>

        {#if compatReport}
          <div class="rounded-lg border border-slate-200 bg-slate-50 px-4 py-3 text-sm text-slate-600">
            Compatibility check completed. {compatReport.blockers.length > 0 ? 'Resolve blockers before proceeding.' : 'No blocking issues detected.'}
          </div>
        {/if}

        {#if creating}
          <div class="flex items-center gap-3 rounded-lg border border-slate-200 bg-slate-50 px-4 py-3 text-sm text-slate-600">
            <Spinner size="sm" label="Generating migration plan" />
            Generating migration plan...
          </div>
        {:else}
          <button
            type="button"
            class="inline-flex w-full items-center justify-center gap-2 rounded-lg bg-blue-600 px-4 py-3 text-sm font-medium text-white transition-colors hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-50"
            onclick={() => void generatePlan()}
            disabled={!sourceID || !targetID || sourceID === targetID}
          >
            Generate Plan
            <ArrowRight size={16} />
          </button>
        {/if}
      </div>
    </Card>
  {/if}

  <div class="mt-6 flex items-center justify-between gap-3">
    <button
      type="button"
      class="inline-flex items-center gap-2 rounded-lg border border-slate-300 bg-white px-4 py-2 text-sm font-medium text-slate-700 transition-colors hover:bg-slate-50 disabled:cursor-not-allowed disabled:opacity-50"
      onclick={previousStep}
      disabled={currentStep === 1 || creating || compatLoading}
    >
      <ArrowLeft size={16} />
      Back
    </button>

    {#if currentStep === 1}
      <button
        type="button"
        class="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-50"
        onclick={nextStep}
        disabled={!canContinueFromStep1()}
      >
        Next
        <ArrowRight size={16} />
      </button>
    {:else if currentStep === 2}
      <button
        type="button"
        class="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-50"
        onclick={nextStep}
        disabled={!canContinueFromStep2()}
      >
        Next
        <ArrowRight size={16} />
      </button>
    {:else if currentStep === 3}
      {#if compatLoading}
        <div class="text-sm text-slate-500">Waiting for compatibility check...</div>
      {:else if compatReport && compatReport.blockers.length > 0}
        <div class="text-sm font-medium text-red-600">Cannot create plan until blockers are resolved.</div>
      {:else}
        <button
          type="button"
          class="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-50"
          onclick={nextStep}
          disabled={!canContinueFromStep3()}
        >
          Continue
          <ArrowRight size={16} />
        </button>
      {/if}
    {/if}
  </div>
</div>

{#snippet serversEmptyIcon()}
  <Server size={22} />
{/snippet}

{#snippet serversEmptyAction()}
  <a
    href="/servers"
    class="inline-flex items-center justify-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-blue-700"
  >
    View servers
  </a>
{/snippet}
