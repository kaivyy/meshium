<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import {
    Calendar,
    Clock3,
    Filter,
    Pencil,
    Plus,
    RefreshCw,
    Search,
    Server as ServerIcon,
    Shield,
    Trash2,
    Check,
    Ban
  } from 'lucide-svelte';
  import { api } from '$lib/api/client';
  import { createCronJob, deleteCronJob, listCronJobs, type CronJob, type CronJobRequest, updateCronJob } from '$lib/api/cron';
  import { type Server } from '$lib/stores/servers';
  import { Badge, Card, EmptyState, Modal, PageHeader, Skeleton, Spinner } from '$lib/components/ui';
  import { toast } from '$lib/stores/toast';

  type SourceFilter = 'all' | 'user' | 'system' | 'cron.d' | 'cron.hourly' | 'cron.daily' | 'cron.weekly' | 'cron.monthly';

  const cronScheduleRegex = /^(\S+\s+){4}\S+$/;

  const schedulePresets = [
    { label: 'Every minute', value: '* * * * *' },
    { label: 'Every 5 minutes', value: '*/5 * * * *' },
    { label: 'Hourly', value: '0 * * * *' },
    { label: 'Daily', value: '0 0 * * *' },
    { label: 'Weekly', value: '0 0 * * 0' },
    { label: 'Monthly', value: '0 0 1 * *' }
  ];

  const sourceOrder: Record<string, number> = {
    user: 0,
    system: 1,
    'cron.d': 2,
    'cron.hourly': 3,
    'cron.daily': 4,
    'cron.weekly': 5,
    'cron.monthly': 6
  };

  let servers = $state([] as Server[]);
  let loadingServers = $state(true);
  let selectedServerId = $state('');
  let jobs = $state([] as CronJob[]);
  let loadingJobs = $state(false);
  let searchQuery = $state('');
  let sourceFilter = $state<SourceFilter>('all');
  let showDisabled = $state(true);
  let editableOnly = $state(false);
  let showAddModal = $state(false);
  let showEditModal = $state(false);
  let showDeleteModal = $state(false);
  let activeJob = $state<CronJob | null>(null);
  let saving = $state(false);
  let deleting = $state(false);
  let form = $state<CronJobRequest>({ schedule: '', command: '', comment: '' });

  let selectedServer = $derived.by(() => servers.find((server) => String(server.id) === selectedServerId) ?? null);

  let sourceOptions = $derived.by(() => {
    const sources = Array.from(new Set(jobs.map((job) => job.source))).sort((a, b) => {
      const aOrder = sourceOrder[a] ?? 99;
      const bOrder = sourceOrder[b] ?? 99;
      return aOrder - bOrder || a.localeCompare(b);
    });
    return ['all', ...sources] as SourceFilter[];
  });

  let filteredJobs = $derived.by(() => {
    const query = searchQuery.trim().toLowerCase();
    let list = [...jobs];

    if (sourceFilter !== 'all') {
      list = list.filter((job) => job.source === sourceFilter);
    }

    if (!showDisabled) {
      list = list.filter((job) => job.enabled);
    }

    if (editableOnly) {
      list = list.filter((job) => job.source === 'user');
    }

    if (query) {
      list = list.filter((job) => {
        const haystack = [job.id, job.schedule, job.command, job.user, job.source, job.comment]
          .join(' ')
          .toLowerCase();
        return haystack.includes(query);
      });
    }

    return list.sort((a, b) => {
      const aOrder = sourceOrder[a.source] ?? 99;
      const bOrder = sourceOrder[b.source] ?? 99;
      if (aOrder !== bOrder) return aOrder - bOrder;

      const aId = Number.parseInt(a.id, 10);
      const bId = Number.parseInt(b.id, 10);
      if (!Number.isNaN(aId) && !Number.isNaN(bId) && aId !== bId) return aId - bId;

      return a.command.localeCompare(b.command);
    });
  });

  let stats = $derived.by(() => {
    const total = jobs.length;
    const editable = jobs.filter((job) => job.source === 'user').length;
    const disabled = jobs.filter((job) => !job.enabled).length;
    const system = jobs.filter((job) => job.source !== 'user').length;
    return { total, editable, disabled, system };
  });

  let scheduleIsValid = $derived.by(() => cronScheduleRegex.test(form.schedule.trim()));
  let commandIsValid = $derived.by(() => form.command.trim().length > 0);

  onMount(async () => {
    await loadServers();
  });

  async function loadServers() {
    loadingServers = true;
    try {
      servers = await api.get('/servers') as Server[];
      if (servers.length > 0) {
        const selectedExists = selectedServerId && servers.some((server) => String(server.id) === selectedServerId);
        if (!selectedExists) {
          selectedServerId = String(servers[0].id);
        }
        await loadJobs(Number(selectedServerId));
      } else {
        selectedServerId = '';
        jobs = [];
      }
    } catch (error) {
      toast.error(error instanceof Error ? error.message : 'Failed to load servers');
    } finally {
      loadingServers = false;
    }
  }

  async function loadJobs(serverId?: number) {
    const id = serverId ?? (selectedServerId ? Number(selectedServerId) : null);
    if (!id) {
      jobs = [];
      return;
    }

    loadingJobs = true;
    try {
      jobs = await listCronJobs(id);
    } catch (error) {
      jobs = [];
      toast.error(error instanceof Error ? error.message : 'Failed to load cron jobs');
    } finally {
      loadingJobs = false;
    }
  }

  async function handleServerChange(event: Event) {
    const value = (event.currentTarget as HTMLSelectElement).value;
    selectedServerId = value;
    await loadJobs(value ? Number(value) : undefined);
  }

  function openAddModal() {
    form = { schedule: '', command: '', comment: '' };
    activeJob = null;
    showAddModal = true;
  }

  function openEditModal(job: CronJob) {
    activeJob = job;
    form = {
      schedule: job.schedule,
      command: job.command,
      comment: job.comment ?? ''
    };
    showEditModal = true;
  }

  function openDeleteModal(job: CronJob) {
    activeJob = job;
    showDeleteModal = true;
  }

  function applyPreset(schedule: string) {
    form.schedule = schedule;
  }

  async function submitJob(mode: 'add' | 'edit') {
    if (!selectedServerId) return;

    saving = true;
    try {
      if (mode === 'add') {
        await createCronJob(Number(selectedServerId), form);
        toast.success('Cron job added');
        showAddModal = false;
      } else if (activeJob) {
        await updateCronJob(Number(selectedServerId), activeJob.id, form);
        toast.success('Cron job updated');
        showEditModal = false;
      }
      activeJob = null;
      await loadJobs(Number(selectedServerId));
    } catch (error) {
      toast.error(error instanceof Error ? error.message : 'Failed to save cron job');
    } finally {
      saving = false;
    }
  }

  async function confirmDelete() {
    if (!selectedServerId || !activeJob) return;

    deleting = true;
    try {
      await deleteCronJob(Number(selectedServerId), activeJob.id);
      toast.success('Cron job deleted');
      showDeleteModal = false;
      activeJob = null;
      await loadJobs(Number(selectedServerId));
    } catch (error) {
      toast.error(error instanceof Error ? error.message : 'Failed to delete cron job');
    } finally {
      deleting = false;
    }
  }

  function canModify(job: CronJob) {
    return job.source === 'user';
  }

  function sourceLabel(source: string) {
    switch (source) {
      case 'user':
        return 'User crontab';
      case 'system':
        return '/etc/crontab';
      case 'cron.d':
        return '/etc/cron.d';
      case 'cron.hourly':
        return 'Hourly';
      case 'cron.daily':
        return 'Daily';
      case 'cron.weekly':
        return 'Weekly';
      case 'cron.monthly':
        return 'Monthly';
      default:
        return source;
    }
  }

  function sourceBadgeVariant(source: string): 'success' | 'info' | 'neutral' {
    return source === 'user' ? 'success' : source === 'system' ? 'info' : 'neutral';
  }

  function statusBadgeVariant(enabled: boolean): 'success' | 'warning' {
    return enabled ? 'success' : 'warning';
  }

  function formatJobCommand(command: string) {
    return command.length > 100 ? `${command.slice(0, 97)}...` : command;
  }

  function openServersPage() {
    goto('/servers');
  }
</script>

<svelte:head>
  <title>Cron Manager - Meshium</title>
</svelte:head>

<div class="mx-auto max-w-7xl p-4 sm:p-6">
  <PageHeader title="Cron Manager" subtitle="View and manage cron jobs on remote servers.">
    {#snippet actions()}
      <div class="flex flex-wrap items-center gap-2">
        <button
          type="button"
          class="inline-flex items-center gap-2 rounded-lg border border-slate-300 bg-white px-3 py-2 text-sm font-medium text-slate-700 hover:bg-slate-50 disabled:opacity-60"
          onclick={() => loadJobs()}
          disabled={loadingJobs || !selectedServerId}
        >
          {#if loadingJobs}<Spinner size="sm" label="Refreshing" />{:else}<RefreshCw size={16} />{/if}
          Refresh
        </button>
        <button
          type="button"
          class="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-3 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-60"
          onclick={openAddModal}
          disabled={!selectedServerId}
        >
          <Plus size={16} /> Add job
        </button>
      </div>
    {/snippet}
  </PageHeader>

  <div class="mb-6 grid gap-4 md:grid-cols-2 xl:grid-cols-4">
    <Card padding="lg">
      <div class="flex items-center gap-3">
        <div class="flex h-10 w-10 items-center justify-center rounded-lg bg-blue-50 text-blue-600">
          <Clock3 size={20} />
        </div>
        <div>
          <p class="text-2xl font-bold text-slate-900">{stats.total}</p>
          <p class="text-xs text-slate-500">Total jobs</p>
        </div>
      </div>
    </Card>
    <Card padding="lg">
      <div class="flex items-center gap-3">
        <div class="flex h-10 w-10 items-center justify-center rounded-lg bg-green-50 text-green-600">
          <Check size={20} />
        </div>
        <div>
          <p class="text-2xl font-bold text-slate-900">{stats.editable}</p>
          <p class="text-xs text-slate-500">Editable jobs</p>
        </div>
      </div>
    </Card>
    <Card padding="lg">
      <div class="flex items-center gap-3">
        <div class="flex h-10 w-10 items-center justify-center rounded-lg bg-amber-50 text-amber-600">
          <Ban size={20} />
        </div>
        <div>
          <p class="text-2xl font-bold text-slate-900">{stats.disabled}</p>
          <p class="text-xs text-slate-500">Disabled jobs</p>
        </div>
      </div>
    </Card>
    <Card padding="lg">
      <div class="flex items-center gap-3">
        <div class="flex h-10 w-10 items-center justify-center rounded-lg bg-slate-100 text-slate-600">
          <Shield size={20} />
        </div>
        <div>
          <p class="text-2xl font-bold text-slate-900">{stats.system}</p>
          <p class="text-xs text-slate-500">System jobs</p>
        </div>
      </div>
    </Card>
  </div>

  <div class="mb-6 grid gap-4 lg:grid-cols-[1.5fr_1fr]">
    <Card padding="lg">
      <div class="flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between">
        <div class="min-w-0 flex-1">
          <label for="cron-server-select" class="mb-1 block text-xs font-semibold uppercase tracking-wide text-slate-500">Server</label>
          <select
            id="cron-server-select"
            class="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-sm outline-none focus:border-blue-500"
            value={selectedServerId}
            onchange={handleServerChange}
            disabled={loadingServers}
          >
            <option value="">{loadingServers ? 'Loading servers...' : 'Select a server'}</option>
            {#each servers as server (server.id)}
              <option value={server.id}>{server.name} · {server.host}</option>
            {/each}
          </select>
        </div>

        {#if selectedServer}
          <div class="rounded-xl bg-slate-50 px-4 py-3 text-sm text-slate-600">
            <div class="flex items-center gap-2 font-medium text-slate-900">
              <ServerIcon size={16} />
              {selectedServer.name}
            </div>
            <div class="mt-1 font-mono text-xs text-slate-500">{selectedServer.username}@{selectedServer.host}:{selectedServer.port || 22}</div>
          </div>
        {/if}
      </div>
    </Card>

    <Card padding="lg">
      <div class="flex items-center gap-3">
        <div class="flex h-10 w-10 items-center justify-center rounded-lg bg-blue-50 text-blue-600">
          <Calendar size={20} />
        </div>
        <div>
          <p class="text-sm font-semibold text-slate-900">Quick schedule presets</p>
          <p class="text-xs text-slate-500">Use one of the common schedules below.</p>
        </div>
      </div>
      <div class="mt-4 flex flex-wrap gap-2">
        {#each schedulePresets as preset}
          <button
            type="button"
            class={`rounded-lg border px-3 py-2 text-xs font-medium transition-colors ${form.schedule === preset.value ? 'border-blue-500 bg-blue-50 text-blue-700' : 'border-slate-300 bg-white text-slate-700 hover:bg-slate-50'}`}
            onclick={() => applyPreset(preset.value)}
          >
            {preset.label}
          </button>
        {/each}
      </div>
    </Card>
  </div>

  <div class="mb-6">
    <Card padding="lg">
      <div class="flex flex-col gap-4">
        <div class="flex flex-col gap-3 lg:flex-row lg:items-center">
          <div class="relative flex-1">
            <Search size={16} class="absolute left-3 top-1/2 -translate-y-1/2 text-slate-400" />
            <input
              type="text"
              bind:value={searchQuery}
              placeholder="Search schedules, commands, comments, users..."
              class="w-full rounded-lg border border-slate-300 bg-white py-2 pl-10 pr-4 text-sm outline-none focus:border-blue-500"
            />
          </div>
          <div class="flex flex-wrap items-center gap-2">
            <label class="inline-flex items-center gap-2 rounded-lg border border-slate-300 bg-white px-3 py-2 text-sm text-slate-700">
              <Filter size={16} />
              <select bind:value={sourceFilter} class="bg-transparent text-sm outline-none">
                {#each sourceOptions as option}
                  <option value={option}>{option === 'all' ? 'All sources' : sourceLabel(option)}</option>
                {/each}
              </select>
            </label>
            <label class="inline-flex items-center gap-2 rounded-lg border border-slate-300 bg-white px-3 py-2 text-sm text-slate-700">
              <input type="checkbox" bind:checked={editableOnly} />
              Editable only
            </label>
            <label class="inline-flex items-center gap-2 rounded-lg border border-slate-300 bg-white px-3 py-2 text-sm text-slate-700">
              <input type="checkbox" bind:checked={showDisabled} />
              Show disabled
            </label>
          </div>
        </div>
      </div>
    </Card>
  </div>

  {#if loadingServers && servers.length === 0}
    <div class="space-y-3">
      {#each Array(4) as _}
        <Card padding="lg">
          <div class="space-y-3">
            <Skeleton width="40%" />
            <Skeleton height="1.5rem" />
            <Skeleton height="3rem" />
          </div>
        </Card>
      {/each}
    </div>
  {:else if !selectedServer}
    <EmptyState
      title="Select a server to manage cron jobs"
      description={servers.length === 0 ? 'No servers available yet. Add a server first.' : 'Choose a server from the dropdown to load its cron jobs.'}
      icon={serverIcon}
      action={servers.length === 0 ? emptyAction : serverAction}
    />
  {:else if filteredJobs.length === 0}
    <EmptyState
      title="No cron jobs found"
      description={jobs.length === 0 ? 'This server does not have any visible cron jobs yet.' : 'No cron jobs match your current filters.'}
      icon={clockIcon}
      action={addAction}
    />
  {:else}
    <Card padding="lg">
      <div class="overflow-x-auto">
        <table class="min-w-full divide-y divide-slate-200 text-left text-sm">
          <thead class="text-xs uppercase tracking-wide text-slate-500">
            <tr>
              <th class="py-3 pr-4">Schedule</th>
              <th class="py-3 pr-4">Command</th>
              <th class="py-3 pr-4">Source</th>
              <th class="py-3 pr-4">Status</th>
              <th class="py-3 pr-4">User</th>
              <th class="py-3 pr-4 text-right">Actions</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-slate-100">
            {#each filteredJobs as job (job.source + '-' + job.id + '-' + job.command)}
              <tr class={job.enabled ? 'bg-white' : 'bg-slate-50/70'}>
                <td class="py-4 pr-4 align-top font-mono text-sm text-slate-900">{job.schedule}</td>
                <td class="py-4 pr-4 align-top">
                  <div class="max-w-2xl">
                    <p class="font-mono text-sm text-slate-900">{formatJobCommand(job.command)}</p>
                    {#if job.comment}
                      <p class="mt-1 text-xs text-slate-500 whitespace-pre-line">{job.comment}</p>
                    {/if}
                  </div>
                </td>
                <td class="py-4 pr-4 align-top">
                  <Badge variant={sourceBadgeVariant(job.source)} size="sm">{sourceLabel(job.source)}</Badge>
                </td>
                <td class="py-4 pr-4 align-top">
                  {#if job.enabled}
                    <Badge variant={statusBadgeVariant(job.enabled)} size="sm">Enabled</Badge>
                  {:else}
                    <Badge variant={statusBadgeVariant(job.enabled)} size="sm">Disabled</Badge>
                  {/if}
                </td>
                <td class="py-4 pr-4 align-top font-mono text-xs text-slate-500">{job.user || '—'}</td>
                <td class="py-4 pl-4 align-top">
                  <div class="flex justify-end gap-2">
                    <button
                      type="button"
                      class="inline-flex items-center gap-1 rounded-lg border border-slate-300 bg-white px-3 py-1.5 text-xs font-medium text-slate-700 hover:bg-slate-50 disabled:opacity-40"
                      onclick={() => openEditModal(job)}
                      disabled={!canModify(job)}
                    >
                      <Pencil size={14} /> Edit
                    </button>
                    <button
                      type="button"
                      class="inline-flex items-center gap-1 rounded-lg border border-slate-300 bg-white px-3 py-1.5 text-xs font-medium text-red-700 hover:bg-red-50 disabled:opacity-40"
                      onclick={() => openDeleteModal(job)}
                      disabled={!canModify(job)}
                    >
                      <Trash2 size={14} /> Delete
                    </button>
                  </div>
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    </Card>
  {/if}
</div>

<Modal
  open={showAddModal}
  title="Add cron job"
  size="lg"
  onClose={() => (showAddModal = false)}
>
  {#snippet children()}
    <div class="space-y-4">
      <label class="block">
        <span class="mb-1 block text-xs font-semibold uppercase tracking-wide text-slate-500">Schedule</span>
        <input bind:value={form.schedule} class={`w-full rounded-lg border px-3 py-2 font-mono text-sm outline-none ${form.schedule.trim() && !scheduleIsValid ? 'border-red-300 focus:border-red-500' : 'border-slate-300 focus:border-blue-500'}`} placeholder="0 * * * *" />
        <p class={`mt-1 text-xs ${form.schedule.trim() && !scheduleIsValid ? 'text-red-600' : 'text-slate-500'}`}>Five-field cron expressions only. Example: <span class="font-mono">0 0 * * *</span></p>
      </label>
      <label class="block">
        <span class="mb-1 block text-xs font-semibold uppercase tracking-wide text-slate-500">Command</span>
        <textarea bind:value={form.command} rows="4" class={`w-full rounded-lg border px-3 py-2 font-mono text-sm outline-none ${form.command.trim() ? 'border-slate-300 focus:border-blue-500' : 'border-red-300 focus:border-red-500'}`} placeholder="/usr/local/bin/backup.sh"></textarea>
        <p class={`mt-1 text-xs ${form.command.trim() ? 'text-slate-500' : 'text-red-600'}`}>This is stored exactly as entered and executed by the remote shell.</p>
      </label>
      <label class="block">
        <span class="mb-1 block text-xs font-semibold uppercase tracking-wide text-slate-500">Comment</span>
        <textarea bind:value={form.comment} rows="3" class="w-full rounded-lg border border-slate-300 px-3 py-2 text-sm outline-none focus:border-blue-500" placeholder="Optional note about this job"></textarea>
      </label>
    </div>
  {/snippet}
  {#snippet footer()}
    <div class="flex items-center justify-end gap-2">
      <button type="button" class="rounded-lg border border-slate-300 px-4 py-2 text-sm font-medium text-slate-700 hover:bg-slate-50" onclick={() => (showAddModal = false)}>
        Cancel
      </button>
      <button type="button" class="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-60" onclick={() => submitJob('add')} disabled={saving || !scheduleIsValid || !commandIsValid}>
        {#if saving}<Spinner size="sm" label="Saving" />{:else}<Plus size={16} />{/if}
        Save job
      </button>
    </div>
  {/snippet}
</Modal>

<Modal
  open={showEditModal}
  title={activeJob ? `Edit cron job #${activeJob.id}` : 'Edit cron job'}
  size="lg"
  onClose={() => (showEditModal = false)}
>
  {#snippet children()}
    <div class="space-y-4">
      <div class="rounded-lg bg-slate-50 px-4 py-3 text-xs text-slate-600">
        <div class="flex flex-wrap items-center gap-2">
          <Badge variant="info" size="sm">{activeJob ? sourceLabel(activeJob.source) : 'Cron job'}</Badge>
          <span class="font-mono">Line {activeJob?.id}</span>
        </div>
      </div>
      <label class="block">
        <span class="mb-1 block text-xs font-semibold uppercase tracking-wide text-slate-500">Schedule</span>
        <input bind:value={form.schedule} class={`w-full rounded-lg border px-3 py-2 font-mono text-sm outline-none ${form.schedule.trim() && !scheduleIsValid ? 'border-red-300 focus:border-red-500' : 'border-slate-300 focus:border-blue-500'}`} placeholder="0 * * * *" />
        <p class={`mt-1 text-xs ${form.schedule.trim() && !scheduleIsValid ? 'text-red-600' : 'text-slate-500'}`}>Five-field cron expressions only.</p>
      </label>
      <label class="block">
        <span class="mb-1 block text-xs font-semibold uppercase tracking-wide text-slate-500">Command</span>
        <textarea bind:value={form.command} rows="4" class={`w-full rounded-lg border px-3 py-2 font-mono text-sm outline-none ${form.command.trim() ? 'border-slate-300 focus:border-blue-500' : 'border-red-300 focus:border-red-500'}`}></textarea>
      </label>
      <label class="block">
        <span class="mb-1 block text-xs font-semibold uppercase tracking-wide text-slate-500">Comment</span>
        <textarea bind:value={form.comment} rows="3" class="w-full rounded-lg border border-slate-300 px-3 py-2 text-sm outline-none focus:border-blue-500"></textarea>
      </label>
    </div>
  {/snippet}
  {#snippet footer()}
    <div class="flex items-center justify-end gap-2">
      <button type="button" class="rounded-lg border border-slate-300 px-4 py-2 text-sm font-medium text-slate-700 hover:bg-slate-50" onclick={() => (showEditModal = false)}>
        Cancel
      </button>
      <button type="button" class="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-60" onclick={() => submitJob('edit')} disabled={saving || !activeJob || !scheduleIsValid || !commandIsValid}>
        {#if saving}<Spinner size="sm" label="Saving" />{:else}<Pencil size={16} />{/if}
        Update job
      </button>
    </div>
  {/snippet}
</Modal>

<Modal
  open={showDeleteModal}
  title="Delete cron job"
  size="sm"
  onClose={() => (showDeleteModal = false)}
>
  {#snippet children()}
    <p class="text-sm text-slate-600">
      {#if activeJob}
        Remove <span class="font-mono font-medium text-slate-900">{activeJob.schedule}</span> for
        <span class="font-mono font-medium text-slate-900">{formatJobCommand(activeJob.command)}</span>?
      {/if}
    </p>
    <p class="mt-2 text-xs text-slate-500">This only removes jobs from the editable user crontab.</p>
  {/snippet}
  {#snippet footer()}
    <div class="flex items-center justify-end gap-2">
      <button type="button" class="rounded-lg border border-slate-300 px-4 py-2 text-sm font-medium text-slate-700 hover:bg-slate-50" onclick={() => (showDeleteModal = false)}>
        Cancel
      </button>
      <button type="button" class="inline-flex items-center gap-2 rounded-lg bg-red-600 px-4 py-2 text-sm font-medium text-white hover:bg-red-700 disabled:opacity-60" onclick={confirmDelete} disabled={deleting || !activeJob}>
        {#if deleting}<Spinner size="sm" label="Deleting" />{:else}<Trash2 size={16} />{/if}
        Delete job
      </button>
    </div>
  {/snippet}
</Modal>

{#snippet serverIcon()}
  <ServerIcon size={22} />
{/snippet}

{#snippet clockIcon()}
  <Clock3 size={22} />
{/snippet}

{#snippet serverAction()}
  <button type="button" class="rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700" onclick={openServersPage}>
    Go to servers
  </button>
{/snippet}

{#snippet emptyAction()}
  <button type="button" class="rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700" onclick={openServersPage}>
    Add a server
  </button>
{/snippet}

{#snippet addAction()}
  <button type="button" class="rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700" onclick={openAddModal} disabled={!selectedServerId}>
    Add cron job
  </button>
{/snippet}
