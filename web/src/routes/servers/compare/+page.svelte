<script lang="ts">
  import { goto } from '$app/navigation';
  import { page } from '$app/state';
  import { ArrowLeft } from 'lucide-svelte';
  import { discoveryApi, type CompatibilityReport } from '$lib/api/discovery';
  import { fetchServers, serverStore, type Server } from '$lib/stores/servers';
  import { toast } from '$lib/stores/toast';
  import { Badge, Card, EmptyState, Spinner } from '$lib/components/ui';

  let servers = $state<Server[]>([]);
  let serversLoading = $state(false);
  let sourceID = $state('');
  let targetID = $state('');
  let report = $state<CompatibilityReport | null>(null);
  let checking = $state(false);
  let compareError = $state('');
  let lastAutoCheckKey = $state('');

  const sourceServer = $derived(servers.find((server) => server.id === Number(sourceID)) ?? null);
  const targetServer = $derived(servers.find((server) => server.id === Number(targetID)) ?? null);
  const canCheck = $derived(Boolean(sourceID && targetID && !checking));

  $effect(() => {
    const unsubscribe = serverStore.subscribe((state) => {
      servers = state.servers;
      serversLoading = state.loading;
    });

    if (!servers.length && !serversLoading) {
      void fetchServers();
    }

    return unsubscribe;
  });

  // Read URL params once on mount — not in a reactive effect to avoid loops.
  $effect(() => {
    const querySource = page.url.searchParams.get('source');
    if (querySource && querySource !== sourceID) {
      sourceID = querySource;
    }
  });

  // Auto-check compatibility when both servers are selected.
  $effect(() => {
    if (sourceID && targetID && !checking) {
      const key = `${sourceID}:${targetID}`;
      if (key !== lastAutoCheckKey) {
        lastAutoCheckKey = key;
        void checkCompatibility();
      }
    }
  });

  async function checkCompatibility() {
    const source = Number(sourceID);
    const target = Number(targetID);

    if (!sourceID || !targetID || Number.isNaN(source) || Number.isNaN(target)) {
      compareError = 'Choose both a source server and a target server.';
      report = null;
      return;
    }

    checking = true;
    compareError = '';
    report = null;

    try {
      report = await discoveryApi.getCompatibility(source, target);
      toast.success('Compatibility report generated.');
    } catch (error) {
      compareError = error instanceof Error ? error.message : 'Failed to check compatibility.';
      toast.error(compareError);
    } finally {
      checking = false;
    }
  }

  function navigateToPlan() {
    if (!sourceID || !targetID) return;
    goto(`/plans/new?source=${sourceID}&target=${targetID}`);
  }

</script>

<svelte:head>
  <title>Compare Servers</title>
</svelte:head>

<div class="p-4 sm:p-6 max-w-4xl mx-auto">
  <div class="mb-4 flex items-center justify-between gap-4">
    <a
      href="/"
      onclick={(event) => {
        event.preventDefault();
        goto('/');
      }}
      class="inline-flex items-center gap-2 text-sm text-fg-muted transition hover:text-fg"
    >
      <ArrowLeft size={16} /> Back to Servers
    </a>
  </div>

  <div class="mb-6">
    <h1 class="text-2xl font-bold tracking-tight text-fg">Compare Servers</h1>
    <p class="mt-1 text-sm text-fg-subtle">Check compatibility before planning a migration.</p>
  </div>

  <Card padding="lg">
    <div class="grid gap-4 md:grid-cols-2">
      <div>
        <label class="mb-2 block text-sm font-medium text-fg-muted" for="source-server">Source server</label>
        <select
          id="source-server"
          bind:value={sourceID}
          class="w-full rounded-lg border border-border-strong bg-surface px-3 py-2 text-sm text-fg shadow-sm outline-none transition focus:border-accent focus:ring-2 focus:ring-accent-subtle"
        >
          <option value="">Select a source server</option>
          {#each servers as server}
            <option value={server.id}>{server.name} · {server.host}</option>
          {/each}
        </select>
      </div>

      <div>
        <label class="mb-2 block text-sm font-medium text-fg-muted" for="target-server">Target server</label>
        <select
          id="target-server"
          bind:value={targetID}
          class="w-full rounded-lg border border-border-strong bg-surface px-3 py-2 text-sm text-fg shadow-sm outline-none transition focus:border-accent focus:ring-2 focus:ring-accent-subtle"
        >
          <option value="">Select a target server</option>
          {#each servers as server}
            <option value={server.id}>{server.name} · {server.host}</option>
          {/each}
        </select>
      </div>
    </div>

    <div class="mt-4 flex flex-wrap items-center justify-between gap-3">
      <div class="text-sm text-fg-subtle">
        {#if sourceServer || targetServer}
          {#if sourceServer}
            <span class="font-medium text-fg-muted">Source:</span> {sourceServer.name}
          {/if}
          {#if sourceServer && targetServer}
            <span class="mx-2 text-fg-subtle">•</span>
          {/if}
          {#if targetServer}
            <span class="font-medium text-fg-muted">Target:</span> {targetServer.name}
          {/if}
        {:else if serversLoading}
          Loading available servers...
        {:else}
          Select two servers to generate a compatibility report.
        {/if}
      </div>

      <button
        type="button"
        onclick={checkCompatibility}
        disabled={!canCheck}
        class="inline-flex items-center justify-center gap-2 rounded-lg bg-accent px-4 py-2 text-sm font-medium text-accent-fg transition hover:bg-accent-hover disabled:cursor-not-allowed disabled:opacity-50"
      >
        {#if checking}
          <Spinner size="sm" label="Checking compatibility" />
          Checking...
        {:else}
          Check Compatibility
        {/if}
      </button>
    </div>
  </Card>

  {#if compareError}
    <div class="mt-4 rounded-xl border border-error bg-error/10 px-4 py-3 text-sm text-error" role="alert">
      <p class="font-semibold text-error">Compatibility check failed</p>
      <p class="mt-1">{compareError}</p>
    </div>
  {/if}

  {#if checking}
    <Card padding="lg">
      <div class="flex items-center gap-3 text-fg-muted">
        <Spinner size="md" label="Loading compatibility report" />
        <div>
          <p class="font-medium text-fg">Checking compatibility...</p>
          <p class="text-sm text-fg-subtle">Analyzing source and target snapshots.</p>
        </div>
      </div>
    </Card>
  {/if}

  {#if report}
    <section class="mt-6 space-y-4">
      <div class={`rounded-2xl border p-5 shadow-sm ${report.compatible ? 'border-success bg-success/15' : 'border-error bg-error/15'}`}>
        <p class={`text-lg font-semibold ${report.compatible ? 'text-success' : 'text-error'}`}>
          {report.compatible ? 'Compatible ✓' : 'Not Compatible ✗'}
        </p>
        <p class={`mt-1 text-sm ${report.compatible ? 'text-success' : 'text-error'}`}>
          {report.compatible
            ? 'The selected servers appear ready for migration.'
            : 'One or more blockers must be resolved before migration can continue.'}
        </p>
      </div>

      <div class="grid gap-4">
        <section>
          <h2 class="mb-3 text-sm font-semibold uppercase tracking-wide text-error">Blockers</h2>
          {#if report.blockers.length}
            <div class="space-y-3">
              {#each report.blockers as blocker}
                <Card padding="md">
                  <div class="flex gap-3">
                    <div class="mt-0.5 flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-error/15 text-error">
                      ✗
                    </div>
                    <div>
                      <div class="flex flex-wrap items-center gap-2">
                        <Badge variant="error">{blocker.category}</Badge>
                      </div>
                      <p class="mt-2 text-sm text-fg-muted">{blocker.message}</p>
                    </div>
                  </div>
                </Card>
              {/each}
            </div>
          {:else}
            <EmptyState title="No blockers found" description="There are no blocking compatibility issues." />
          {/if}
        </section>

        <section>
          <h2 class="mb-3 text-sm font-semibold uppercase tracking-wide text-warning">Warnings</h2>
          {#if report.warnings.length}
            <div class="space-y-3">
              {#each report.warnings as warning}
                <Card padding="md">
                  <div class="flex gap-3">
                    <div class="mt-0.5 flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-warning/15 text-warning">
                      ⚠
                    </div>
                    <div>
                      <Badge variant="warning">{warning.category}</Badge>
                      <p class="mt-2 text-sm text-fg-muted">{warning.message}</p>
                    </div>
                  </div>
                </Card>
              {/each}
            </div>
          {:else}
            <EmptyState title="No warnings" description="No compatibility warnings were reported." />
          {/if}
        </section>

      </div>

      {#if report.compatible}
        <div class="flex justify-end">
          <button
            type="button"
            onclick={navigateToPlan}
            class="inline-flex items-center justify-center rounded-lg bg-accent px-4 py-2 text-sm font-medium text-accent-fg transition hover:bg-accent-hover"
          >
            Create Migration Plan
          </button>
        </div>
      {/if}
    </section>
  {/if}
</div>
