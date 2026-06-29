<script lang="ts">
  import { goto } from '$app/navigation';
  import { jobs, loading, error as jobsError, loadJobs, activeJobCount } from '$lib/stores/jobs';
  import { formatDurationBetween, formatRelativeTime } from '$lib/utils/format';
  import { Badge, Card, DataTable, EmptyState, PageHeader, ProgressBar, Skeleton, Spinner } from '$lib/components/ui';
  import type { Job, JobFilter, JobStatus, JobType } from '$lib/api/jobs';
  import { ArrowRightLeft, Briefcase, ListChecks, RefreshCw, Search } from 'lucide-svelte';

  const typeOptions: Array<{ value: 'all' | JobType; label: string }> = [
    { value: 'all', label: 'All types' },
    { value: 'migration', label: 'Migration' },
    { value: 'discovery', label: 'Discovery' },
    { value: 'compat_check', label: 'Compatibility check' },
  ];

  const statusOptions: Array<{ value: 'all' | JobStatus; label: string }> = [
    { value: 'all', label: 'All statuses' },
    { value: 'queued', label: 'Queued' },
    { value: 'running', label: 'Running' },
    { value: 'paused', label: 'Paused' },
    { value: 'done', label: 'Done' },
    { value: 'failed', label: 'Failed' },
    { value: 'cancelled', label: 'Cancelled' },
  ];

  const columns = [
    { key: 'type', label: 'Type', width: '18%' },
    { key: 'status', label: 'Status', width: '12%' },
    { key: 'progress', label: 'Progress', width: '28%' },
    { key: 'created', label: 'Created', width: '14%' },
    { key: 'duration', label: 'Duration', width: '14%' },
    { key: 'actions', label: 'Actions', width: '14%', align: 'right' as const },
  ];

  type CellSnippetProps = {
    column: { key: string; label: string };
    row: Record<string, unknown>;
    value: unknown;
  };

  let typeFilter = $state<'all' | JobType>('all');
  let statusFilter = $state<'all' | JobStatus>('all');

  const activeJobs = $derived.by(() => activeJobCount($jobs));
  const jobRows = $derived.by(() => $jobs as unknown as Record<string, unknown>[]);

  function buildFilter(): JobFilter {
    return {
      ...(typeFilter === 'all' ? {} : { type: typeFilter }),
      ...(statusFilter === 'all' ? {} : { status: statusFilter }),
    };
  }

  function refreshJobs(): void {
    void loadJobs(buildFilter());
  }

  function statusVariant(status: JobStatus): 'success' | 'error' | 'info' | 'neutral' | 'warning' {
    switch (status) {
      case 'done':
        return 'success';
      case 'failed':
        return 'error';
      case 'running':
        return 'info';
      case 'paused':
        return 'warning';
      case 'queued':
      case 'cancelled':
      default:
        return 'neutral';
    }
  }

  function formatJobType(type: JobType): string {
    switch (type) {
      case 'migration':
        return 'Migration';
      case 'discovery':
        return 'Discovery';
      case 'compat_check':
        return 'Compatibility check';
      default:
        return type;
    }
  }

  function progressVariant(status: JobStatus): 'default' | 'success' | 'warning' | 'error' {
    switch (status) {
      case 'done':
        return 'success';
      case 'failed':
        return 'error';
      case 'paused':
        return 'warning';
      default:
        return 'default';
    }
  }

  $effect(() => {
    typeFilter;
    statusFilter;
    refreshJobs();
  });

  $effect(() => {
    activeJobs;

    if (activeJobs <= 0) {
      return;
    }

    const interval = window.setInterval(() => {
      refreshJobs();
    }, 5000);

    return () => window.clearInterval(interval);
  });
</script>

<svelte:head>
  <title>Jobs</title>
</svelte:head>

<div class="p-4 sm:p-6 max-w-6xl mx-auto">
  <PageHeader title="Jobs" subtitle="Track running and completed jobs.">
    {#snippet actions()}
      <button type="button" onclick={refreshJobs} disabled={$loading} class="inline-flex items-center justify-center gap-2 rounded-lg border border-slate-300 bg-white px-4 py-2 text-sm font-medium text-slate-700 transition-colors hover:bg-slate-50 disabled:cursor-not-allowed disabled:opacity-60">
        {#if $loading}<Spinner size="sm" label="Refreshing jobs" />{:else}<RefreshCw size={16} />{/if}
        Refresh
      </button>
    {/snippet}
  </PageHeader>

  {#if $jobsError}
    <div class="mb-4 rounded-xl border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">
      {$jobsError}
    </div>
  {/if}

  <Card>
    <div class="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
      <label class="block">
        <span class="mb-1 block text-xs font-semibold uppercase tracking-wide text-slate-500">Type</span>
        <select
          bind:value={typeFilter}
          class="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-sm text-slate-900 outline-none transition-colors focus:border-blue-500"
        >
          {#each typeOptions as option}
            <option value={option.value}>{option.label}</option>
          {/each}
        </select>
      </label>

      <label class="block">
        <span class="mb-1 block text-xs font-semibold uppercase tracking-wide text-slate-500">Status</span>
        <select
          bind:value={statusFilter}
          class="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-sm text-slate-900 outline-none transition-colors focus:border-blue-500"
        >
          {#each statusOptions as option}
            <option value={option.value}>{option.label}</option>
          {/each}
        </select>
      </label>

      <div class="flex items-end justify-between rounded-lg border border-slate-200 bg-slate-50 px-4 py-3 text-sm text-slate-600 md:col-span-2 xl:col-span-1">
        <div>
          <div class="text-xs font-semibold uppercase tracking-wide text-slate-500">Active jobs</div>
          <div class="mt-1 text-lg font-semibold text-slate-900">{activeJobs}</div>
        </div>
        <div class="text-right text-xs text-slate-500">
          Auto-refreshes while running or queued
        </div>
      </div>
    </div>
  </Card>

  <div class="mt-6">
    {#if $loading && jobRows.length === 0}
      <div class="overflow-hidden rounded-2xl border border-slate-200 bg-white shadow-sm">
        <div class="space-y-3 p-4">
          {#each Array(3) as _, index}
            <div class="flex flex-wrap items-center gap-4 rounded-lg border border-slate-100 bg-slate-50 px-4 py-3">
              <div class="flex items-center gap-2 min-w-[200px]">
                <Skeleton width="24px" height="24px" rounded />
                <Skeleton width="120px" />
              </div>
              <Skeleton width="80px" height="20px" rounded />
              <Skeleton width="200px" />
              <Skeleton width="100px" />
            </div>
          {/each}
        </div>
      </div>
    {:else}
      <DataTable
        {columns}
        data={jobRows}
        loading={$loading}
        rowKey="id"
        onRowClick={(row) => {
          void goto(`/jobs/${String(row.id)}`);
        }}
        empty={empty}
        cell={cell}
      />
    {/if}
  </div>
</div>

{#snippet empty()}
  <EmptyState
    title="No jobs yet"
    description="Jobs are created when you run a migration, discovery scan, or compatibility check."
    icon={jobIcon}
    action={emptyAction}
  />
{/snippet}

{#snippet emptyAction()}
  <a href="/migrations" class="inline-flex items-center justify-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-blue-700">
    Go to Migrations →
  </a>
{/snippet}

{#snippet jobIcon()}
  <Briefcase size={22} />
{/snippet}

{#snippet cell({ column, row, value }: CellSnippetProps)}
  {@const job = row as unknown as Job}
  {#if column.key === 'type'}
    <div class="flex items-center gap-2 text-slate-700">
      {#if job.type === 'migration'}
        <ArrowRightLeft size={16} class="shrink-0 text-slate-400" />
      {:else if job.type === 'discovery'}
        <Search size={16} class="shrink-0 text-slate-400" />
      {:else}
        <ListChecks size={16} class="shrink-0 text-slate-400" />
      {/if}
      <span class="font-medium text-slate-900">{formatJobType(job.type)}</span>
    </div>
  {:else if column.key === 'status'}
    <Badge variant={statusVariant(job.status)}>{job.status}</Badge>
  {:else if column.key === 'progress'}
    <div class="flex items-center gap-3">
      <div class="min-w-0 flex-1">
        <ProgressBar value={job.progress.percentage} variant={progressVariant(job.status)} />
      </div>
      <span class="shrink-0 text-xs font-medium text-slate-500">{job.progress.percentage}%</span>
    </div>
  {:else if column.key === 'created'}
    <span class="text-slate-600">{formatRelativeTime(job.createdAt)}</span>
  {:else if column.key === 'duration'}
    <span class="text-slate-600">{job.startedAt && job.finishedAt ? formatDurationBetween(job.startedAt, job.finishedAt) : '—'}</span>
  {:else if column.key === 'actions'}
    <div class="flex justify-end">
      <button
        type="button"
        onclick={(event) => {
          event.stopPropagation();
          void goto(`/jobs/${job.id}`);
        }}
        class="rounded-lg border border-slate-300 bg-white px-3 py-1.5 text-xs font-medium text-slate-700 transition-colors hover:bg-slate-50"
      >
        Open
      </button>
    </div>
  {:else}
    {String(value ?? '—')}
  {/if}
{/snippet}
