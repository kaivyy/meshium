<script lang="ts">
  import { page } from '$app/state';
  import { get } from 'svelte/store';
  import { Badge, Card, LogViewer, PageHeader, ProgressBar, Spinner } from '$lib/components/ui';
  import { toast } from '$lib/stores/toast';
  import {
    cancelJob,
    currentJob,
    error as jobError,
    jobLogs,
    loadJob,
    pauseJob,
    resetJob,
    resumeJob,
    subscribeToJobProgress,
    loading,
  } from '$lib/stores/jobs';
  import { formatBytes, formatDuration, formatDurationBetween, formatSpeed } from '$lib/utils/format';
  import { ArrowRightLeft, CheckCircle, ListChecks, Pause, Play, Search } from 'lucide-svelte';
  import type { Job, JobStatus, JobType } from '$lib/api/jobs';

  const jobID = $derived.by(() => page.params.id);

  let liveConnectionRequested = $state(false);
  let websocket: WebSocket | null = null;

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

  function isLiveStatus(status: JobStatus): boolean {
    return status === 'running' || status === 'paused';
  }

  async function reloadJob(id: string): Promise<void> {
    await loadJob(id);
    const job = get(currentJob);
    liveConnectionRequested = Boolean(job && isLiveStatus(job.status));
  }

  async function handlePause(): Promise<void> {
    if (!jobID) return;

    try {
      await pauseJob(jobID);
      toast.success('Job paused');
      await reloadJob(jobID);
    } catch {
      toast.error('Failed to pause job');
    }
  }

  async function handleResume(): Promise<void> {
    if (!jobID) return;

    try {
      await resumeJob(jobID);
      toast.success('Job resumed');
      await reloadJob(jobID);
    } catch {
      toast.error('Failed to resume job');
    }
  }

  async function handleCancel(): Promise<void> {
    if (!jobID) return;
    liveConnectionRequested = false;

    try {
      await cancelJob(jobID);
      toast.success('Job cancelled');
      await reloadJob(jobID);
    } catch {
      toast.error('Failed to cancel job');
    }
  }

  $effect(() => {
    const id = jobID;
    if (!id) return;

    let cancelled = false;
    resetJob();
    liveConnectionRequested = false;

    void (async () => {
      await loadJob(id);
      if (cancelled) return;
      const job = get(currentJob);
      liveConnectionRequested = Boolean(job && isLiveStatus(job.status));
    })();

    return () => {
      cancelled = true;
      liveConnectionRequested = false;
    };
  });

  $effect(() => {
    const id = jobID;
    if (!id || !liveConnectionRequested) {
      return;
    }

    if (websocket) {
      return;
    }

    let closedManually = false;
    let handledEnd = false;
    const socket = subscribeToJobProgress(id, () => {}, () => {});
    websocket = socket;

    const handleSocketEnd = () => {
      if (handledEnd) {
        return;
      }
      handledEnd = true;

      if (websocket === socket) {
        websocket = null;
      }

      if (closedManually) {
        return;
      }

      liveConnectionRequested = false;
      void reloadJob(id);
    };

    socket.onclose = handleSocketEnd;
    socket.onerror = handleSocketEnd;

    return () => {
      closedManually = true;
      if (websocket === socket) {
        websocket = null;
        socket.close();
      }
    };
  });
</script>

<svelte:head>
  <title>Job {jobID}</title>
</svelte:head>

<div class="p-4 sm:p-6 max-w-6xl mx-auto">
  <PageHeader title="Job Details" backHref="/jobs"></PageHeader>

  {#if $loading && !$currentJob}
    <div class="mt-8 flex items-center gap-3 text-slate-500">
      <Spinner size="md" label="Loading job" />
      <span class="text-sm">Loading job…</span>
    </div>
  {:else if $jobError && !$currentJob}
    <Card>
      <div class="rounded-xl border border-red-200 bg-red-50 p-4 text-sm text-red-700">
        <div class="font-medium text-red-800">Unable to load job</div>
        <div class="mt-1">{$jobError}</div>
      </div>
    </Card>
  {:else if $currentJob}
    {@const job = $currentJob as Job}

    <div class="mt-6 flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
      <div class="flex items-start gap-3">
        <div class="flex h-11 w-11 items-center justify-center rounded-xl bg-blue-50 text-blue-600">
          {#if job.type === 'migration'}
            <ArrowRightLeft size={22} />
          {:else if job.type === 'discovery'}
            <Search size={22} />
          {:else}
            <ListChecks size={22} />
          {/if}
        </div>

        <div>
          <p class="text-xs font-semibold uppercase tracking-wide text-slate-500">{formatJobType(job.type)}</p>
          <h1 class="mt-1 text-xl font-bold text-slate-900">Job {job.id}</h1>
        </div>
      </div>

      <Badge variant={statusVariant(job.status)}>{job.status}</Badge>
    </div>

    {#if job.status === 'done'}
      <Card>
        <div class="flex items-start gap-3 rounded-xl border border-green-200 bg-green-50 p-4">
          <div class="mt-0.5 flex h-8 w-8 items-center justify-center rounded-full bg-green-100 text-green-700">
            <CheckCircle size={18} />
          </div>
          <div class="min-w-0">
            <h2 class="text-sm font-semibold text-green-900">Job completed successfully</h2>
            <div class="mt-3 grid gap-3 sm:grid-cols-2">
              <div>
                <div class="text-xs font-semibold uppercase tracking-wide text-green-700">Duration</div>
                <div class="mt-1 text-sm text-green-900">
                  {job.startedAt && job.finishedAt ? formatDurationBetween(job.startedAt, job.finishedAt) : '—'}
                </div>
              </div>
              <div>
                <div class="text-xs font-semibold uppercase tracking-wide text-green-700">Total bytes</div>
                <div class="mt-1 text-sm text-green-900">{formatBytes(job.progress.bytesTotal)}</div>
              </div>
            </div>
          </div>
        </div>
      </Card>
    {/if}

    {#if job.status === 'failed'}
      <Card>
        <div class="rounded-xl border border-red-200 bg-red-50 p-4 text-sm text-red-700">
          <div class="font-semibold text-red-800">Job failed</div>
          <p class="mt-1 whitespace-pre-wrap">{job.error || 'The job stopped with an unknown error.'}</p>
        </div>
      </Card>
    {/if}

    <Card padding="lg">
      <div class="mb-4">
        <div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <h2 class="text-lg font-semibold text-slate-900">Progress</h2>
            <p class="mt-1 text-sm text-slate-600">{job.progress.currentName || 'Waiting for the next step…'}</p>
          </div>
          <div class="text-xs text-slate-500">
            {job.progress.currentStep}/{job.progress.totalSteps} steps
          </div>
        </div>
      </div>

      <ProgressBar
        value={job.progress.percentage}
        label="Overall progress"
        sublabel={job.status === 'running' ? 'Live updates are streaming in real time.' : 'Progress is updated from the latest job snapshot.'}
        variant={progressVariant(job.status)}
        animated={job.status === 'running'}
      />

      <div class="mt-5 grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <div class="rounded-xl border border-slate-200 bg-slate-50 p-3">
          <div class="text-xs font-semibold uppercase tracking-wide text-slate-500">Bytes done</div>
          <div class="mt-1 text-sm font-medium text-slate-900">
            {formatBytes(job.progress.bytesDone)} / {formatBytes(job.progress.bytesTotal)}
          </div>
        </div>
        <div class="rounded-xl border border-slate-200 bg-slate-50 p-3">
          <div class="text-xs font-semibold uppercase tracking-wide text-slate-500">Speed</div>
          <div class="mt-1 text-sm font-medium text-slate-900">{formatSpeed(job.progress.speedBPS)}</div>
        </div>
        <div class="rounded-xl border border-slate-200 bg-slate-50 p-3">
          <div class="text-xs font-semibold uppercase tracking-wide text-slate-500">ETA</div>
          <div class="mt-1 text-sm font-medium text-slate-900">{formatDuration(job.progress.eta)}</div>
        </div>
        <div class="rounded-xl border border-slate-200 bg-slate-50 p-3">
          <div class="text-xs font-semibold uppercase tracking-wide text-slate-500">Steps</div>
          <div class="mt-1 text-sm font-medium text-slate-900">
            {job.progress.currentStep}/{job.progress.totalSteps}
          </div>
        </div>
      </div>
    </Card>

    {#if !job.status || job.status === 'running' || job.status === 'paused' || job.status === 'queued'}
      <div class="mt-4 flex flex-wrap gap-3">
        {#if job.status === 'running'}
          <button
            type="button"
            onclick={handlePause}
            class="inline-flex items-center gap-2 rounded-lg border border-slate-300 bg-white px-4 py-2 text-sm font-medium text-slate-700 transition-colors hover:bg-slate-50"
          >
            <Pause size={16} />
            Pause
          </button>
          <button
            type="button"
            onclick={handleCancel}
            class="inline-flex items-center gap-2 rounded-lg bg-red-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-red-700"
          >
            Cancel
          </button>
        {:else if job.status === 'paused'}
          <button
            type="button"
            onclick={handleResume}
            class="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-blue-700"
          >
            <Play size={16} />
            Resume
          </button>
          <button
            type="button"
            onclick={handleCancel}
            class="inline-flex items-center gap-2 rounded-lg bg-red-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-red-700"
          >
            Cancel
          </button>
        {:else if job.status === 'queued'}
          <button
            type="button"
            onclick={handleCancel}
            class="inline-flex items-center gap-2 rounded-lg bg-red-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-red-700"
          >
            Cancel
          </button>
        {/if}
      </div>
    {/if}

    <Card padding="lg">
      <div class="mb-4 flex items-center justify-between gap-3">
        <div>
          <h2 class="text-lg font-semibold text-slate-900">Logs</h2>
          <p class="mt-1 text-sm text-slate-500">Live job output and progress events.</p>
        </div>
        {#if job.status === 'running'}
          <div class="inline-flex items-center gap-2 text-xs font-medium text-blue-600">
            <span class="h-2 w-2 rounded-full bg-blue-600 animate-pulse"></span>
            Streaming
          </div>
        {/if}
      </div>

      <LogViewer logs={$jobLogs} streaming={job.status === 'running'} />
    </Card>
  {/if}
</div>
