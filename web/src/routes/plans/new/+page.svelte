<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/state';
  import { ArrowLeft, ArrowRight, AlertTriangle, Ban, Loader2, RotateCcw, Server, ShieldCheck } from 'lucide-svelte';
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
    <a href="/plans" class="inline-flex items-center gap-2 text-sm text-fg-muted transition-colors hover:text-fg">
      <ArrowLeft size={16} />
      Back to plans
    </a>

    <div class="hidden sm:flex items-center gap-2 text-xs font-medium text-fg-subtle">
      <ShieldCheck size={14} class="text-accent" />
      Planning a migration
    </div>
  </div>

  <div class="mb-8">
    <h1 class="text-2xl font-semibold text-fg">New Migration Plan</h1>
    <p class="mt-1 text-sm text-fg-subtle">Choose servers, verify compatibility, and generate a migration plan.</p>
  </div>

  <div class="mb-8 flex items-center gap-2">
    {#each stepLabels as label, index}
      {@const step = index + 1}
      <div class="flex min-w-0 flex-1 items-center gap-2">
        <div
          class={`flex h-8 w-8 shrink-0 items-center justify-center rounded-full text-sm font-semibold ${
            step < currentStep ? 'bg-success text-accent-fg' : step === currentStep ? 'bg-accent text-accent-fg' : 'bg-surface-muted text-fg-subtle'
          }`}
        >
          {step < currentStep ? '✓' : step}
        </div>
        <div class="hidden min-w-0 sm:block">
          <div class={`text-xs font-semibold uppercase tracking-wide ${step === currentStep ? 'text-fg' : 'text-fg-subtle'}`}>{label}</div>
          <div class="text-xs text-fg-subtle">Step {step}</div>
        </div>
        {#if step < stepLabels.length}
          <div class={`mx-2 h-0.5 flex-1 rounded ${step < currentStep ? 'bg-success' : 'bg-surface-muted'}`}></div>
        {/if}
      </div>
    {/each}
  </div>

  {#if error}
    <div class="mb-4 rounded-lg border border-error bg-error/10 px-4 py-3 text-sm text-error">
      {error}
    </div>
  {/if}

  {#if $serverStore.loading}
    <Card padding="lg">
      <div class="flex items-center justify-center gap-3 py-10 text-fg-subtle">
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
          <h2 class="text-lg font-semibold text-fg">Step 1 · Select source server</h2>
          <p class="mt-1 text-sm text-fg-subtle">Choose the server you want to migrate from.</p>
        </div>

        <div class="space-y-2">
          <label for="source-server" class="text-sm font-medium text-fg">Source server</label>
          <div class="relative">
            <select
              id="source-server"
              class="w-full appearance-none rounded-lg border border-border-strong bg-surface px-4 py-2.5 pr-10 text-sm text-fg shadow-sm outline-none transition focus:border-accent focus:ring-2 focus:ring-accent/20"
              value={sourceID ?? ''}
              onchange={(event) => selectSource((event.currentTarget as HTMLSelectElement).value)}
            >
              <option value="">Select a source server</option>
              {#each servers as server}
                <option value={server.id}>{server.name} · {server.host}</option>
              {/each}
            </select>
            <div class="pointer-events-none absolute inset-y-0 right-3 flex items-center text-fg-subtle">
              <ArrowRight size={16} class="rotate-90" />
            </div>
          </div>
        </div>

        {#if sourceServer}
          <div class="rounded-lg border border-border bg-surface-muted px-4 py-3 text-sm text-fg-muted">
            Selected source: <span class="font-medium text-fg">{sourceServer.name}</span> · {sourceServer.host}
          </div>
        {/if}
      </div>
    </Card>
  {:else if currentStep === 2}
    <Card padding="lg">
      <div class="space-y-5">
        <div>
          <h2 class="text-lg font-semibold text-fg">Step 2 · Select target server</h2>
          <p class="mt-1 text-sm text-fg-subtle">Choose the server you want to migrate to. It must be different from the source.</p>
        </div>

        <div class="space-y-2">
          <label for="target-server" class="text-sm font-medium text-fg">Target server</label>
          <div class="relative">
            <select
              id="target-server"
              class="w-full appearance-none rounded-lg border border-border-strong bg-surface px-4 py-2.5 pr-10 text-sm text-fg shadow-sm outline-none transition focus:border-accent focus:ring-2 focus:ring-accent/20"
              value={targetID ?? ''}
              onchange={(event) => selectTarget((event.currentTarget as HTMLSelectElement).value)}
            >
              <option value="">Select a target server</option>
              {#each targetServers as server}
                <option value={server.id}>{server.name} · {server.host}</option>
              {/each}
            </select>
            <div class="pointer-events-none absolute inset-y-0 right-3 flex items-center text-fg-subtle">
              <ArrowRight size={16} class="rotate-90" />
            </div>
          </div>
        </div>

        {#if targetServer}
          <div class="rounded-lg border border-border bg-surface-muted px-4 py-3 text-sm text-fg-muted">
            Selected target: <span class="font-medium text-fg">{targetServer.name}</span> · {targetServer.host}
          </div>
        {/if}
      </div>
    </Card>
  {:else if currentStep === 3}
    <Card padding="lg">
      <div class="space-y-5">
        <div class="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
          <div>
            <h2 class="text-lg font-semibold text-fg">Step 3 · Compatibility check</h2>
            <p class="mt-1 text-sm text-fg-subtle">Validate the selected servers before creating a plan.</p>
          </div>

          <button
            type="button"
            class="inline-flex items-center gap-2 rounded-lg border border-border-strong bg-surface px-4 py-2 text-sm font-medium text-fg-muted transition-colors hover:bg-surface-muted disabled:cursor-not-allowed disabled:opacity-50"
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
          <div class="flex items-center gap-3 rounded-lg border border-border bg-surface-muted px-4 py-3 text-sm text-fg-muted">
            <Spinner size="sm" label="Checking compatibility" />
            Running compatibility checks...
          </div>
        {:else if compatReport}
          <div class="space-y-4">
            {#if compatReport.blockers.length > 0}
              <div class="rounded-lg border border-error bg-error/10 p-4">
                <div class="mb-2 flex items-center gap-2 text-sm font-semibold text-error">
                  <Ban size={16} />
                  Cannot create plan
                </div>
                <div class="space-y-2">
                  {#each compatReport.blockers as blocker}
                    <div class="rounded-lg border border-error bg-surface/70 px-3 py-2 text-sm text-error">
                      <div class="font-medium">{blocker.category}</div>
                      <div class="text-error">{blocker.message}</div>
                    </div>
                  {/each}
                </div>
              </div>
            {/if}

            {#if compatReport.warnings.length > 0}
              <div class="rounded-lg border border-warning bg-warning/10 p-4">
                <div class="mb-2 flex items-center gap-2 text-sm font-semibold text-warning">
                  <AlertTriangle size={16} />
                  Warnings
                </div>
                <div class="space-y-2">
                  {#each compatReport.warnings as warning}
                    <div class="rounded-lg border border-warning bg-surface/70 px-3 py-2 text-sm text-warning">
                      <div class="font-medium">{warning.category}</div>
                      <div class="text-warning">{warning.message}</div>
                    </div>
                  {/each}
                </div>
              </div>
            {/if}

          </div>
        {/if}

        {#if compatReport && compatReport.blockers.length === 0 && compatReport.warnings.length === 0}
          <div class="rounded-lg border border-success bg-success/10 px-4 py-3 text-sm text-success">
            Ready to migrate.
          </div>
        {/if}
      </div>
    </Card>
  {:else}
    <Card padding="lg">
      <div class="space-y-5">
        <div>
          <h2 class="text-lg font-semibold text-fg">Step 4 · Generate plan</h2>
          <p class="mt-1 text-sm text-fg-subtle">Review the selected servers, then generate the migration plan.</p>
        </div>

        <div class="grid gap-4 sm:grid-cols-2">
          <div class="rounded-lg border border-border bg-surface-muted px-4 py-3">
            <div class="text-xs font-semibold uppercase tracking-wide text-fg-subtle">Source</div>
            <div class="mt-1 text-sm font-medium text-fg">{sourceServer ? sourceServer.name : '—'}</div>
            <div class="text-sm text-fg-subtle">{sourceServer ? sourceServer.host : 'Select a source server'}</div>
          </div>
          <div class="rounded-lg border border-border bg-surface-muted px-4 py-3">
            <div class="text-xs font-semibold uppercase tracking-wide text-fg-subtle">Target</div>
            <div class="mt-1 text-sm font-medium text-fg">{targetServer ? targetServer.name : '—'}</div>
            <div class="text-sm text-fg-subtle">{targetServer ? targetServer.host : 'Select a target server'}</div>
          </div>
        </div>

        {#if compatReport}
          <div class="rounded-lg border border-border bg-surface-muted px-4 py-3 text-sm text-fg-muted">
            Compatibility check completed. {compatReport.blockers.length > 0 ? 'Resolve blockers before proceeding.' : 'No blocking issues detected.'}
          </div>
        {/if}

        {#if creating}
          <div class="flex items-center gap-3 rounded-lg border border-border bg-surface-muted px-4 py-3 text-sm text-fg-muted">
            <Spinner size="sm" label="Generating migration plan" />
            Generating migration plan...
          </div>
        {:else}
          <button
            type="button"
            class="inline-flex w-full items-center justify-center gap-2 rounded-lg bg-accent px-4 py-3 text-sm font-medium text-accent-fg transition-colors hover:bg-accent-hover disabled:cursor-not-allowed disabled:opacity-50"
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
      class="inline-flex items-center gap-2 rounded-lg border border-border-strong bg-surface px-4 py-2 text-sm font-medium text-fg-muted transition-colors hover:bg-surface-muted disabled:cursor-not-allowed disabled:opacity-50"
      onclick={previousStep}
      disabled={currentStep === 1 || creating || compatLoading}
    >
      <ArrowLeft size={16} />
      Back
    </button>

    {#if currentStep === 1}
      <button
        type="button"
        class="inline-flex items-center gap-2 rounded-lg bg-accent px-4 py-2 text-sm font-medium text-accent-fg transition-colors hover:bg-accent-hover disabled:cursor-not-allowed disabled:opacity-50"
        onclick={nextStep}
        disabled={!canContinueFromStep1()}
      >
        Next
        <ArrowRight size={16} />
      </button>
    {:else if currentStep === 2}
      <button
        type="button"
        class="inline-flex items-center gap-2 rounded-lg bg-accent px-4 py-2 text-sm font-medium text-accent-fg transition-colors hover:bg-accent-hover disabled:cursor-not-allowed disabled:opacity-50"
        onclick={nextStep}
        disabled={!canContinueFromStep2()}
      >
        Next
        <ArrowRight size={16} />
      </button>
    {:else if currentStep === 3}
      {#if compatLoading}
        <div class="text-sm text-fg-subtle">Waiting for compatibility check...</div>
      {:else if compatReport && compatReport.blockers.length > 0}
        <div class="text-sm font-medium text-error">Cannot create plan until blockers are resolved.</div>
      {:else}
        <button
          type="button"
          class="inline-flex items-center gap-2 rounded-lg bg-accent px-4 py-2 text-sm font-medium text-accent-fg transition-colors hover:bg-accent-hover disabled:cursor-not-allowed disabled:opacity-50"
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
    class="inline-flex items-center justify-center gap-2 rounded-lg bg-accent px-4 py-2 text-sm font-medium text-accent-fg transition-colors hover:bg-accent-hover"
  >
    View servers
  </a>
{/snippet}
