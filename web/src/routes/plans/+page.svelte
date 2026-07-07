<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { ArrowRight, Plus, Trash2 } from 'lucide-svelte';
  import { plannerApi, type MigrationPlanSummary, type RiskLevel } from '$lib/api/planner';
  import { Badge, DataTable, EmptyState, Modal, PageHeader, Skeleton } from '$lib/components/ui';
  import { toast } from '$lib/stores/toast';
  import { formatBytes, formatRelativeTime } from '$lib/utils/format';

  type PlanRow = MigrationPlanSummary & {
    sourceTarget: string;
    createdDisplay: string;
    totalSizeDisplay: string;
  };

  type PlanCellSnippetProps = {
    column: { key: string; label: string };
    row: Record<string, unknown>;
  };

  const columns = [
    { key: 'sourceTarget', label: 'Source → Target' },
    { key: 'riskLevel', label: 'Risk Level' },
    { key: 'stepCount', label: 'Steps', align: 'center' as const },
    { key: 'createdDisplay', label: 'Created' },
    { key: 'totalSizeDisplay', label: 'Total Size', align: 'right' as const },
    { key: 'actions', label: 'Actions', align: 'right' as const },
  ];

  let plans = $state<MigrationPlanSummary[]>([]);
  let loading = $state(true);
  let deleteTarget = $state<MigrationPlanSummary | null>(null);
  let deleting = $state(false);
  let error = $state<string | null>(null);

  const rows = $derived(
    plans.map((plan) => ({
      ...plan,
      sourceTarget: `${plan.sourceHost} → ${plan.targetHost}`,
      createdDisplay: formatRelativeTime(plan.createdAt),
      totalSizeDisplay: formatBytes(plan.totalSizeBytes),
    }))
  );

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

  async function loadPlans(): Promise<void> {
    loading = true;
    error = null;

    try {
      plans = await plannerApi.list();
    } catch (err) {
      error = err instanceof Error ? err.message : 'Failed to load migration plans';
      toast.error(error);
    } finally {
      loading = false;
    }
  }

  async function confirmDelete(): Promise<void> {
    if (!deleteTarget) return;

    deleting = true;
    try {
      await plannerApi.delete(deleteTarget.id);
      toast.success('Migration plan deleted');
      deleteTarget = null;
      await loadPlans();
    } catch (err) {
      toast.error(err instanceof Error ? err.message : 'Failed to delete migration plan');
    } finally {
      deleting = false;
    }
  }

  function openPlan(row: Record<string, unknown>): void {
    goto(`/plans/${String(row.id)}`);
  }
</script>

<svelte:head>
  <title>Migration Plans</title>
</svelte:head>

<div class="p-4 sm:p-6 max-w-6xl mx-auto">
  <PageHeader title="Plans" subtitle="Migration plans with risk assessment and estimates.">
    {#snippet actions()}
      <a href="/plans/new" class="inline-flex items-center justify-center gap-2 rounded-lg bg-accent px-4 py-2 text-sm font-medium text-accent-fg transition-colors hover:bg-accent-hover">
        <Plus size={18} /> New Plan
      </a>
    {/snippet}
  </PageHeader>

  {#if error}
    <div class="mb-4 rounded-lg border border-error bg-error/10 px-4 py-3 text-sm text-error">
      {error}
    </div>
  {/if}

  {#if loading && rows.length === 0}
    <div class="overflow-x-auto rounded-2xl border border-border bg-surface shadow-sm">
      <div class="space-y-3 p-4">
        {#each Array(3) as _, index}
          <div class="grid grid-cols-[200px_80px_40px_100px_80px] items-center gap-4 rounded-lg border border-border bg-surface-muted px-4 py-3">
            <Skeleton width="200px" />
            <Skeleton width="80px" height="20px" rounded />
            <Skeleton width="40px" />
            <Skeleton width="100px" />
            <Skeleton width="80px" />
          </div>
        {/each}
      </div>
    </div>
  {:else}
    <DataTable
      columns={columns}
      data={rows as unknown as Record<string, unknown>[]}
      loading={loading}
      rowKey="id"
      onRowClick={openPlan}
      empty={emptyState}
      cell={planCell}
    />
  {/if}

  <Modal
    open={deleteTarget !== null}
    title="Delete migration plan?"
    onClose={() => {
      if (!deleting) deleteTarget = null;
    }}
    children={deleteModalBody}
    footer={deleteModalFooter}
  />
</div>

{#snippet emptyState()}
  <EmptyState
    title="No plans yet"
    description="Create your first plan to compare servers and prepare a migration."
    icon={plansEmptyIcon}
    action={emptyAction}
  />
{/snippet}

{#snippet plansEmptyIcon()}
  <ArrowRight size={22} />
{/snippet}

{#snippet emptyAction()}
  <a
    href="/plans/new"
    class="inline-flex items-center justify-center gap-2 rounded-lg bg-accent px-4 py-2 text-sm font-medium text-accent-fg transition-colors hover:bg-accent-hover"
  >
    <Plus size={18} />
    Create your first plan
  </a>
{/snippet}

{#snippet planCell({ column, row }: PlanCellSnippetProps)}
  {@const plan = row as unknown as PlanRow}

  {#if column.key === 'sourceTarget'}
    <div class="font-medium text-fg">{plan.sourceTarget}</div>
  {:else if column.key === 'riskLevel'}
    <Badge variant={riskVariant(plan.riskLevel)}>{plan.riskLevel}</Badge>
  {:else if column.key === 'stepCount'}
    <span class="font-medium text-fg-muted">{plan.stepCount}</span>
  {:else if column.key === 'createdDisplay'}
    <span class="text-fg-muted">{plan.createdDisplay}</span>
  {:else if column.key === 'totalSizeDisplay'}
    <span class="text-fg-muted">{plan.totalSizeDisplay}</span>
  {:else if column.key === 'actions'}
    <div class="flex justify-end">
      <button
        type="button"
        class="inline-flex items-center gap-1 rounded-lg border border-error px-3 py-1.5 text-sm font-medium text-error transition-colors hover:bg-error/10"
        onclick={(event) => {
          event.stopPropagation();
          deleteTarget = plan;
        }}
      >
        <Trash2 size={14} />
        Delete
      </button>
    </div>
  {/if}
{/snippet}

{#snippet deleteModalBody()}
  <div class="space-y-3">
    <p class="text-sm text-fg-muted">
      This will permanently delete the selected migration plan.
    </p>

    {#if deleteTarget}
      <div class="rounded-lg border border-border bg-surface-muted px-4 py-3 text-sm text-fg-muted">
        <div class="font-medium text-fg">{deleteTarget.sourceHost} → {deleteTarget.targetHost}</div>
        <div class="mt-1 text-fg-subtle">
          {deleteTarget.stepCount} steps · {formatBytes(deleteTarget.totalSizeBytes)}
        </div>
      </div>
    {/if}
  </div>
{/snippet}

{#snippet deleteModalFooter()}
  <div class="flex items-center justify-end gap-3">
    <button
      type="button"
      class="rounded-lg border border-border-strong px-4 py-2 text-sm font-medium text-fg-muted transition-colors hover:bg-surface-muted"
      onclick={() => {
        if (!deleting) deleteTarget = null;
      }}
      disabled={deleting}
    >
      Cancel
    </button>
    <button
      type="button"
      class="inline-flex items-center gap-2 rounded-lg bg-error px-4 py-2 text-sm font-medium text-accent-fg transition-colors hover:bg-error/90 disabled:cursor-not-allowed disabled:opacity-50"
      onclick={() => void confirmDelete()}
      disabled={deleting}
    >
      {#if deleting}
        Deleting...
      {:else}
        <Trash2 size={16} />
        Delete Plan
      {/if}
    </button>
  </div>
{/snippet}
