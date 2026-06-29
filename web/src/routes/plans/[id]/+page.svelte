<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/state';
  import {
    AlertTriangle,
    ArrowLeft,
    ArrowRight,
    Ban,
    CheckCircle2,
    Cog,
    Database,
    FileText,
    Globe,
    Image as ImageIcon,
    Package,
    Play,
    Trash2,
    Undo2,
    Settings,
  } from 'lucide-svelte';
  import { plannerApi, type MigrationPlan, type RiskLevel, type StepType } from '$lib/api/planner';
  import { Badge, Card, EmptyState, Modal, PageHeader, Spinner } from '$lib/components/ui';
  import { fetchServers, serverStore } from '$lib/stores/servers';
  import { toast } from '$lib/stores/toast';
  import { formatBytes, formatDuration, formatRelativeTime } from '$lib/utils/format';

  type StepWithEstimate = MigrationPlan['steps'][number];

  const planId = $derived(page.params.id);
  const servers = $derived($serverStore.servers);

  let plan = $state<MigrationPlan | null>(null);
  let loading = $state(true);
  let executing = $state(false);
  let regenerating = $state(false);
  let deleting = $state(false);
  let error = $state<string | null>(null);
  let deleteModalOpen = $state(false);

  const stepTypeMeta: Record<StepType, { label: string; icon: typeof Package }> = {
    docker_volume: { label: 'Docker Volume', icon: Package },
    docker_image: { label: 'Docker Image', icon: ImageIcon },
    database: { label: 'Database', icon: Database },
    file: { label: 'File', icon: FileText },
    config: { label: 'Config', icon: Settings },
    nginx: { label: 'Nginx', icon: Globe },
    service: { label: 'Service', icon: Cog },
  };

  function riskVariant(risk: RiskLevel): 'success' | 'info' | 'warning' | 'error' {
    switch (risk) {
      case 'low':
        return 'success';
      case 'medium':
        return 'info';
      case 'high':
        return 'warning';
      case 'critical':
        return 'error';
    }
  }

  function parseDuration(value: unknown): number {
    if (typeof value === 'number' && Number.isFinite(value)) return value;
    if (typeof value !== 'string') return 0;

    const trimmed = value.trim();
    if (!trimmed) return 0;
    if (/^\d+(?:\.\d+)?$/.test(trimmed)) return Number(trimmed);

    const match = trimmed.match(/(?:(\d+(?:\.\d+)?)h)?(?:(\d+(?:\.\d+)?)m)?(?:(\d+(?:\.\d+)?)s)?/);
    if (!match) return Number(trimmed) || 0;

    const hours = Number(match[1] ?? 0);
    const minutes = Number(match[2] ?? 0);
    const seconds = Number(match[3] ?? 0);
    return hours * 3600 + minutes * 60 + seconds;
  }

  function formatEstimateDuration(value: unknown): string {
    return formatDuration(Math.max(0, Math.round(parseDuration(value))));
  }

  function resolveServerId(hostname: string): number | null {
    return servers.find((server) => server.host === hostname || server.name === hostname)?.id ?? null;
  }

  async function loadPlan(): Promise<void> {
    loading = true;
    error = null;

    try {
      plan = await plannerApi.get(planId);
    } catch (err) {
      error = err instanceof Error ? err.message : 'Failed to load plan';
      toast.error(error);
    } finally {
      loading = false;
    }
  }

  async function executePlan(): Promise<void> {
    if (!plan) return;

    executing = true;
    error = null;

    try {
      const { jobID } = await plannerApi.execute(plan.id);
      toast.success('Migration job submitted');
      await goto(`/jobs/${jobID}`);
    } catch (err) {
      error = err instanceof Error ? err.message : 'Failed to execute plan';
      toast.error('Failed to execute plan');
    } finally {
      executing = false;
    }
  }

  async function regeneratePlan(): Promise<void> {
    if (!plan) return;

    const sourceID = resolveServerId(plan.source.hostname);
    const targetID = resolveServerId(plan.target.hostname);

    if (!sourceID || !targetID) {
      error = 'This plan does not map cleanly to known servers for regeneration.';
      toast.error(error);
      return;
    }

    regenerating = true;
    error = null;

    try {
      const nextPlan = await plannerApi.create(sourceID, targetID);
      toast.success('Migration plan regenerated');
      await goto(`/plans/${nextPlan.id}`);
    } catch (err) {
      error = err instanceof Error ? err.message : 'Failed to regenerate plan';
      toast.error(error);
    } finally {
      regenerating = false;
    }
  }

  async function deletePlan(): Promise<void> {
    if (!plan) return;

    deleting = true;
    error = null;

    try {
      await plannerApi.delete(plan.id);
      toast.success('Migration plan deleted');
      deleteModalOpen = false;
      await goto('/plans');
    } catch (err) {
      error = err instanceof Error ? err.message : 'Failed to delete plan';
      toast.error(error);
    } finally {
      deleting = false;
    }
  }

  onMount(() => {
    if ($serverStore.servers.length === 0) {
      void fetchServers();
    }

    void loadPlan();
  });
</script>

<svelte:head>
  <title>{plan ? `Plan ${plan.id}` : 'Migration Plan'}</title>
</svelte:head>

<div class="p-4 sm:p-6 max-w-6xl mx-auto">
  <PageHeader title="Plan Details" backHref="/plans"></PageHeader>

  {#if loading}
    <div class="flex items-center gap-3 py-10 text-slate-500">
      <Spinner size="md" label="Loading plan" />
      <span>Loading migration plan...</span>
    </div>
  {:else if error && !plan}
    <div class="rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">
      {error}
    </div>
  {:else if !plan}
    <EmptyState
      title="Plan not found"
      description="The migration plan you requested could not be loaded."
      icon={emptyIcon}
      action={emptyAction}
    />
  {:else}
    <div class="space-y-6">
      <div class="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
        <div class="min-w-0">
          <div class="mb-2 flex flex-wrap items-center gap-2 text-sm text-slate-500">
            <span>{plan.source.hostname}</span>
            <ArrowRight size={14} />
            <span>{plan.target.hostname}</span>
          </div>
          <h1 class="text-2xl font-semibold text-slate-900">Migration Plan #{plan.id}</h1>
          <p class="mt-1 text-sm text-slate-500">Created {formatRelativeTime(plan.createdAt)}</p>
        </div>

        <div class="flex flex-wrap items-center gap-3">
          <Badge variant={riskVariant(plan.riskLevel)}>{plan.riskLevel}</Badge>
          <div class="rounded-lg border border-slate-200 bg-white px-4 py-2 text-sm text-slate-600 shadow-sm">
            <span class="font-medium text-slate-900">{formatBytes(plan.totalEstimate.sizeBytes)}</span>
            <span class="mx-2 text-slate-300">·</span>
            <span>{formatEstimateDuration(plan.totalEstimate.durationMin)} – {formatEstimateDuration(plan.totalEstimate.durationMax)}</span>
          </div>
        </div>
      </div>

      <div class="flex flex-wrap items-center gap-3">
        <button
          type="button"
          class="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-50"
          onclick={() => void executePlan()}
          disabled={executing || plan.blockers.length > 0}
        >
          {#if executing}
            <Spinner size="sm" label="Executing plan" />
            Executing...
          {:else}
            <Play size={16} />
            Execute Plan
          {/if}
        </button>

        <button
          type="button"
          class="inline-flex items-center gap-2 rounded-lg border border-slate-300 bg-white px-4 py-2 text-sm font-medium text-slate-700 transition-colors hover:bg-slate-50 disabled:cursor-not-allowed disabled:opacity-50"
          onclick={() => void regeneratePlan()}
          disabled={regenerating}
        >
          {#if regenerating}
            <Spinner size="sm" label="Regenerating plan" />
            Re-generating...
          {:else}
            <Undo2 size={16} />
            Re-generate
          {/if}
        </button>

        <button
          type="button"
          class="inline-flex items-center gap-2 rounded-lg border border-red-200 bg-white px-4 py-2 text-sm font-medium text-red-600 transition-colors hover:bg-red-50"
          onclick={() => {
            deleteModalOpen = true;
          }}
        >
          <Trash2 size={16} />
          Delete Plan
        </button>
      </div>

      {#if error}
        <div class="rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">
          {error}
        </div>
      {/if}

      {#if plan.blockers.length > 0 || plan.warnings.length > 0}
        <div class="grid gap-4 lg:grid-cols-2">
          {#if plan.blockers.length > 0}
            <Card padding="lg">
              <div class="mb-4 flex items-center gap-2 text-sm font-semibold text-red-800">
                <Ban size={16} />
                Blockers
              </div>
              <div class="space-y-2">
                {#each plan.blockers as blocker}
                  <div class="rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-800">
                    <div class="font-medium">{blocker.code}</div>
                    <div class="mt-1 text-red-700">{blocker.message}</div>
                  </div>
                {/each}
              </div>
            </Card>
          {/if}

          {#if plan.warnings.length > 0}
            <Card padding="lg">
              <div class="mb-4 flex items-center gap-2 text-sm font-semibold text-yellow-800">
                <AlertTriangle size={16} />
                Warnings
              </div>
              <div class="space-y-2">
                {#each plan.warnings as warning}
                  <div class="rounded-lg border border-yellow-200 bg-yellow-50 px-4 py-3 text-sm text-yellow-800">
                    <div class="font-medium">{warning.code}</div>
                    <div class="mt-1 text-yellow-700">{warning.message}</div>
                  </div>
                {/each}
              </div>
            </Card>
          {/if}
        </div>
      {:else}
        <div class="rounded-xl border border-green-200 bg-green-50 px-4 py-3 text-green-800">
          <div class="flex items-center gap-2 text-sm font-semibold">
            <CheckCircle2 size={16} />
            All clear
          </div>
          <div class="mt-1 text-sm text-green-700">No blockers or warnings found. The plan is ready to execute.</div>
        </div>
      {/if}

      <Card padding="lg">
        <div class="mb-5 flex items-center justify-between gap-3">
          <div>
            <h2 class="text-lg font-semibold text-slate-900">Steps</h2>
            <p class="mt-1 text-sm text-slate-500">Ordered execution plan with dependency and risk details.</p>
          </div>
        </div>

        <ol class="space-y-3">
          {#each plan.steps as step}
            {@const meta = stepTypeMeta[step.type]}
            {@const StepIcon = meta.icon}
            <li class={`rounded-lg border border-slate-200 bg-white p-4 shadow-sm ${step.dependsOn.length > 0 ? 'ml-6 border-l-4 border-l-blue-200 pl-5' : ''}`}>
              <div class="flex flex-col gap-3 lg:flex-row lg:items-start lg:justify-between">
                <div class="min-w-0">
                  <div class="flex flex-wrap items-center gap-2">
                    <div class="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-slate-100 text-sm font-semibold text-slate-700">
                      {step.order}
                    </div>
                    <div class="min-w-0">
                      <div class="font-medium text-slate-900">{step.name}</div>
                      {#if step.dependsOn.length > 0}
                        <div class="mt-1 text-sm text-slate-500">Depends on step {step.dependsOn.join(', ')}</div>
                      {/if}
                    </div>
                  </div>
                </div>

                <div class="flex flex-wrap items-center gap-2">
                  <Badge variant="neutral">
                    <span class="inline-flex items-center gap-1.5">
                      <StepIcon size={14} />
                      {meta.label}
                    </span>
                  </Badge>
                  <Badge variant={riskVariant(step.riskLevel)}>{step.riskLevel}</Badge>
                  {#if step.reversible}
                    <Badge variant="success">
                      <span class="inline-flex items-center gap-1.5">
                        <Undo2 size={14} />
                        Reversible
                      </span>
                    </Badge>
                  {/if}
                </div>
              </div>

              <div class="mt-4 grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
                <div>
                  <div class="text-xs font-semibold uppercase tracking-wide text-slate-500">Size</div>
                  <div class="mt-1 text-sm font-medium text-slate-900">{formatBytes(step.estimate.sizeBytes)}</div>
                </div>
                <div>
                  <div class="text-xs font-semibold uppercase tracking-wide text-slate-500">Duration</div>
                  <div class="mt-1 text-sm font-medium text-slate-900">
                    {formatEstimateDuration(step.estimate.durationMin)} – {formatEstimateDuration(step.estimate.durationMax)}
                  </div>
                </div>
                <div>
                  <div class="text-xs font-semibold uppercase tracking-wide text-slate-500">Confidence</div>
                  <div class="mt-1 text-sm font-medium text-slate-900">{Math.round(step.estimate.confidence * 100)}%</div>
                </div>
                <div>
                  <div class="text-xs font-semibold uppercase tracking-wide text-slate-500">Dependencies</div>
                  <div class="mt-1 text-sm font-medium text-slate-900">{step.dependsOn.length || 'None'}</div>
                </div>
              </div>
            </li>
          {/each}
        </ol>
      </Card>
    </div>

    <Modal
      open={deleteModalOpen}
      title="Delete migration plan?"
      onClose={() => {
        if (!deleting) deleteModalOpen = false;
      }}
      children={deleteModalBody}
      footer={deleteModalFooter}
    />
  {/if}
</div>

{#snippet emptyIcon()}
  <Package size={22} />
{/snippet}

{#snippet emptyAction()}
  <a
    href="/plans"
    class="inline-flex items-center justify-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-blue-700"
  >
    <ArrowLeft size={16} />
    Back to plans
  </a>
{/snippet}

{#snippet deleteModalBody()}
  <div class="space-y-3">
    <p class="text-sm text-slate-600">This action will permanently remove the plan from the system.</p>

    {#if plan}
      <div class="rounded-lg border border-slate-200 bg-slate-50 px-4 py-3 text-sm text-slate-700">
        <div class="font-medium text-slate-900">{plan.source.hostname} → {plan.target.hostname}</div>
        <div class="mt-1 text-slate-500">{plan.steps.length} steps · {formatBytes(plan.totalEstimate.sizeBytes)}</div>
      </div>
    {/if}
  </div>
{/snippet}

{#snippet deleteModalFooter()}
  <div class="flex items-center justify-end gap-3">
    <button
      type="button"
      class="rounded-lg border border-slate-300 px-4 py-2 text-sm font-medium text-slate-700 transition-colors hover:bg-slate-50 disabled:cursor-not-allowed disabled:opacity-50"
      onclick={() => {
        if (!deleting) deleteModalOpen = false;
      }}
      disabled={deleting}
    >
      Cancel
    </button>
    <button
      type="button"
      class="inline-flex items-center gap-2 rounded-lg bg-red-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-red-700 disabled:cursor-not-allowed disabled:opacity-50"
      onclick={() => void deletePlan()}
      disabled={deleting}
    >
      {#if deleting}
        <Spinner size="sm" label="Deleting plan" />
        Deleting...
      {:else}
        <Trash2 size={16} />
        Delete Plan
      {/if}
    </button>
  </div>
{/snippet}
