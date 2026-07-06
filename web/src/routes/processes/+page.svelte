<script lang="ts">
  import { onMount } from 'svelte';
  import { AlertTriangle, Cpu, MemoryStick, Search, Server, ShieldAlert, SquareTerminal, Trash2, ChevronUp, ChevronDown } from 'lucide-svelte';
  import { PageHeader, Card, Badge, Button, EmptyState, Modal, Skeleton, Spinner } from '$lib/components/ui';
  import { toast } from '$lib/stores/toast';
  import type { Server as ServerItem } from '$lib/stores/servers';
  import { api } from '$lib/api/client';
  import { processesApi, type ProcessInfo, type ProcessSignal, type ProcessSort } from '$lib/api/processes';

  const allowedSignals: ProcessSignal[] = ['TERM', 'KILL', 'HUP', 'INT', 'QUIT', 'USR1', 'USR2', 'STOP', 'CONT'];
  const sortOptions: Array<{ value: ProcessSort; label: string }> = [
    { value: 'cpu', label: 'CPU' },
    { value: 'mem', label: 'Memory' },
    { value: 'pid', label: 'PID' }
  ];

  let servers = $state<ServerItem[]>([]);
  let selectedServerId = $state('');
  let processes = $state<ProcessInfo[]>([]);
  let searchQuery = $state('');
  let sortBy = $state<ProcessSort>('cpu');
  let loadingServers = $state(true);
  let loadingProcesses = $state(false);
  let serverError = $state<string | null>(null);
  let processError = $state<string | null>(null);
  let autoRefresh = $state(true);
  let lastUpdated = $state<string | null>(null);
  let killModalOpen = $state(false);
  let killTarget = $state<ProcessInfo | null>(null);
  let killSignal = $state<ProcessSignal>('TERM');
  let killing = $state(false);

  let processLoadToken = 0;

  const selectedServer = $derived.by(() => {
    if (!selectedServerId) return null;
    return servers.find((server) => String(server.id) === selectedServerId) ?? null;
  });

  const filteredProcesses = $derived.by(() => {
    const query = searchQuery.trim().toLowerCase();
    if (!query) return processes;

    return processes.filter((process) => {
      return (
        process.pid.toString().includes(query) ||
        process.command.toLowerCase().includes(query) ||
        process.user.toLowerCase().includes(query)
      );
    });
  });

  const totalProcesses = $derived(processes.length);
  const totalCpu = $derived.by(() => processes.reduce((sum, process) => sum + process.cpu, 0));
  const totalMemory = $derived.by(() => processes.reduce((sum, process) => sum + process.memory, 0));
  const topConsumer = $derived.by(() => {
    if (processes.length === 0) return null;
    return [...processes].sort((a, b) => {
      const aScore = a.cpu + a.memory;
      const bScore = b.cpu + b.memory;
      if (aScore === bScore) return a.pid - b.pid;
      return bScore - aScore;
    })[0];
  });

  const stats = $derived.by(() => [
    {
      label: 'Total processes',
      value: totalProcesses.toString(),
      icon: SquareTerminal,
      color: 'text-fg-muted',
      note: `${filteredProcesses.length} visible`
    },
    {
      label: 'CPU usage',
      value: `${formatPercent(totalCpu)}%`,
      icon: Cpu,
      color: 'text-warning',
      note: 'Aggregate across loaded processes'
    },
    {
      label: 'Memory usage',
      value: `${formatPercent(totalMemory)}%`,
      icon: MemoryStick,
      color: 'text-accent',
      note: 'Aggregate across loaded processes'
    },
    {
      label: 'Top consumer',
      value: topConsumer ? `PID ${topConsumer.pid}` : '—',
      icon: Server,
      color: 'text-info',
      note: topConsumer ? topConsumer.command : 'No processes loaded'
    }
  ]);

  onMount(() => {
    void loadServers();

    const interval = window.setInterval(() => {
      if (autoRefresh && selectedServerId && !loadingProcesses) {
        void refreshProcesses({ silent: true });
      }
    }, 5000);

    return () => window.clearInterval(interval);
  });

  async function loadServers() {
    loadingServers = true;
    serverError = null;

    try {
      const loadedServers = await api.get('/servers') as ServerItem[];
      servers = loadedServers;

      if (loadedServers.length === 0) {
        selectedServerId = '';
        processes = [];
        processError = null;
        lastUpdated = null;
        loadingProcesses = false;
        return;
      }

      const currentSelection = loadedServers.find((server) => String(server.id) === selectedServerId);
      if (!currentSelection) {
        selectedServerId = String(loadedServers[0].id);
      }

      await refreshProcesses({ silent: true });
    } catch (error) {
      const message = error instanceof Error ? error.message : 'Failed to load servers';
      serverError = message;
      toast.error(message);
    } finally {
      loadingServers = false;
    }
  }

  async function refreshProcesses(options: { silent?: boolean } = {}) {
    if (!selectedServerId) {
      processes = [];
      processError = null;
      lastUpdated = null;
      return;
    }

    const requestToken = ++processLoadToken;
    const serverId = Number(selectedServerId);
    const requestedSort = sortBy;

    loadingProcesses = true;
    processError = null;

    try {
      const data = await processesApi.list(serverId, requestedSort, 50);
      if (requestToken !== processLoadToken) return;
      if (String(serverId) !== selectedServerId || requestedSort !== sortBy) return;

      processes = data;
      lastUpdated = new Date().toLocaleTimeString();
    } catch (error) {
      if (requestToken !== processLoadToken) return;
      const message = error instanceof Error ? error.message : 'Failed to load processes';
      processError = message;
      if (!options.silent) {
        toast.error(message);
      }
    } finally {
      if (requestToken === processLoadToken) {
        loadingProcesses = false;
      }
    }
  }

  async function handleServerChange(event: Event) {
    selectedServerId = (event.currentTarget as HTMLSelectElement).value;
    await refreshProcesses();
  }

  async function changeSort(nextSort: ProcessSort) {
    if (sortBy === nextSort) {
      await refreshProcesses();
      return;
    }

    sortBy = nextSort;
    await refreshProcesses();
  }

  function openKillModal(process: ProcessInfo) {
    killTarget = process;
    killSignal = 'TERM';
    killModalOpen = true;
  }

  function closeKillModal() {
    if (killing) return;
    killModalOpen = false;
    killTarget = null;
  }

  async function confirmKill() {
    if (!killTarget || !selectedServerId) return;

    killing = true;
    try {
      await processesApi.kill(Number(selectedServerId), killTarget.pid, killSignal);
      toast.success(`Sent ${killSignal} to PID ${killTarget.pid}`);
      killModalOpen = false;
      killTarget = null;
      await refreshProcesses({ silent: true });
    } catch (error) {
      const message = error instanceof Error ? error.message : 'Failed to kill process';
      toast.error(message);
    } finally {
      killing = false;
    }
  }

  function isHighUsage(value: number) {
    if (value >= 80) return 'text-error bg-error/15';
    if (value >= 50) return 'text-warning bg-warning/15';
    return 'text-success bg-success/15';
  }

  function formatPercent(value: number) {
    return value.toFixed(1);
  }

  function formatKiB(value: number) {
    if (value < 1024) return `${value.toFixed(0)} KiB`;
    const mib = value / 1024;
    if (mib < 1024) return `${mib.toFixed(1)} MiB`;
    return `${(mib / 1024).toFixed(1)} GiB`;
  }

  function formatCommand(command: string) {
    if (command.length <= 96) return command;
    return `${command.slice(0, 93)}…`;
  }


</script>

<div class="p-4 sm:p-6">
  <div class="mx-auto max-w-7xl">
    <PageHeader title="Process Manager" subtitle="Inspect and manage remote processes over SSH.">
      {#snippet actions()}
        <div class="flex flex-wrap items-center gap-2">
          <Button variant={autoRefresh ? 'primary' : 'secondary'} onclick={() => (autoRefresh = !autoRefresh)}>
            {autoRefresh ? 'Auto-refresh on' : 'Auto-refresh off'}
          </Button>
          <Button variant="secondary" loading={loadingProcesses} onclick={() => refreshProcesses()}>
            Refresh
          </Button>
        </div>
      {/snippet}
    </PageHeader>

    <Card>
      <div class="grid gap-4 lg:grid-cols-[minmax(0,1.5fr)_minmax(0,1fr)] lg:items-end">
        <div class="space-y-2">
          <label for="server-select" class="text-sm font-medium text-fg-muted">Server</label>
          {#if loadingServers}
            <div class="flex items-center gap-3 rounded-lg border border-border bg-surface-muted px-4 py-3 text-sm text-fg-subtle">
              <Spinner size="sm" label="Loading servers" />
              Loading servers…
            </div>
          {:else}
            <select
              id="server-select"
              value={selectedServerId}
              onchange={handleServerChange}
              class="w-full rounded-lg border border-border-strong bg-surface px-4 py-3 text-fg shadow-sm outline-none transition focus:border-accent focus:ring-2 focus:ring-accent/20"
            >
              <option value="">Select a server</option>
              {#each servers as server}
                <option value={String(server.id)}>{server.name} · {server.host}</option>
              {/each}
            </select>
          {/if}
        </div>

        <div class="space-y-2">
          <label for="process-search" class="text-sm font-medium text-fg-muted">Search</label>
          <div class="relative">
            <Search size={18} class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-fg-subtle" />
            <input
              id="process-search"
              type="text"
              value={searchQuery}
              oninput={(event) => {
                searchQuery = (event.currentTarget as HTMLInputElement).value;
              }}
              placeholder="Search by command, PID, or user"
              class="w-full rounded-lg border border-border-strong bg-surface py-3 pl-10 pr-4 text-fg shadow-sm outline-none transition focus:border-accent focus:ring-2 focus:ring-accent/20"
            />
          </div>
        </div>
      </div>

      <div class="mt-4 flex flex-wrap items-center justify-between gap-3 border-t border-border pt-4">
        <div class="flex flex-wrap items-center gap-2">
          {#each sortOptions as option}
            <button
              type="button"
              aria-pressed={sortBy === option.value}
              onclick={() => changeSort(option.value)}
              class={`inline-flex items-center gap-2 rounded-lg border px-3 py-2 text-sm font-medium transition ${
                sortBy === option.value
                  ? 'border-accent bg-accent-subtle text-accent'
                  : 'border-border bg-surface text-fg-muted hover:bg-surface-muted'
              }`}
            >
              {option.label}
              {#if sortBy === option.value}
                {#if option.value === 'pid'}
                  <ChevronUp size={16} />
                {:else}
                  <ChevronDown size={16} />
                {/if}
              {/if}
            </button>
          {/each}
        </div>

        <div class="flex items-center gap-2 text-sm text-fg-subtle">
          {#if loadingProcesses}
            <Spinner size="sm" label="Loading processes" />
            Updating…
          {:else if lastUpdated}
            <Badge variant="info" size="sm">Updated {lastUpdated}</Badge>
          {:else}
            <Badge variant="neutral" size="sm">No data loaded</Badge>
          {/if}
          {#if selectedServer}
            <Badge variant="neutral" size="sm">{selectedServer.name}</Badge>
          {/if}
        </div>
      </div>
    </Card>

    {#if serverError}
      <div class="mt-4 rounded-lg border border-error/20 bg-error/10 px-4 py-3 text-sm text-error">
        {serverError}
      </div>
    {/if}

    {#if selectedServerId}
      <div class="mt-6 grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        {#each stats as stat}
          <Card>
            {@const Icon = stat.icon}
            <div class="flex items-start justify-between gap-4">
              <div>
                <p class="text-sm font-medium text-fg-subtle">{stat.label}</p>
                <p class={`mt-2 text-2xl font-bold ${stat.color}`}>{stat.value}</p>
                <p class="mt-1 text-xs text-fg-subtle">{stat.note}</p>
              </div>
              <div class={`rounded-full p-3 ${stat.color === 'text-fg-muted' ? 'bg-surface-muted' : stat.color === 'text-warning' ? 'bg-warning/15' : stat.color === 'text-accent' ? 'bg-accent/15' : 'bg-info/15'}`}>
                <Icon size={20} class={stat.color} />
              </div>
            </div>
          </Card>
        {/each}
      </div>
    {/if}

    <div class="mt-6">
      <Card>
        <div class="flex flex-wrap items-center justify-between gap-3 border-b border-border pb-4">
          <div>
            <h2 class="text-lg font-semibold text-fg">Processes</h2>
            <p class="mt-1 text-sm text-fg-subtle">
              {#if selectedServer}
                {selectedServer.name} · {filteredProcesses.length} shown of {processes.length} loaded
              {:else}
                Select a server to begin.
              {/if}
            </p>
          </div>
          {#if processError}
            <Badge variant="error">{processError}</Badge>
          {/if}
        </div>

        {#if loadingServers}
          <div class="flex flex-col items-center justify-center gap-3 py-12 text-center">
            <Spinner size="lg" label="Loading processes" />
            <div>
              <p class="text-sm font-medium text-fg">Loading process data</p>
              <p class="mt-1 text-sm text-fg-subtle">Choose a server to fetch live process information.</p>
            </div>
          </div>
        {:else if servers.length === 0}
          <div class="py-6">
            <EmptyState
              title="No servers available"
              description="Add a server first, then return here to inspect its running processes."
              icon={emptyServersIcon}
              action={emptyServersAction}
            />
          </div>
        {:else if !selectedServerId}
          <div class="py-6">
            <EmptyState
              title="Select a server"
              description="Choose one of your saved servers to load live process information."
              icon={emptyServersIcon}
            />
          </div>
        {:else if loadingProcesses && processes.length === 0}
          <div class="space-y-3 py-4">
            {#each Array(6) as _}
              <div class="grid gap-3 rounded-lg border border-border bg-surface-muted px-4 py-3 sm:grid-cols-7">
                {#each Array(7) as __}
                  <Skeleton width="100%" height="14px" />
                {/each}
              </div>
            {/each}
          </div>
        {:else if filteredProcesses.length === 0}
          <div class="py-6">
            <EmptyState
              title="No processes found"
              description={processes.length === 0 ? 'The selected server did not return any processes.' : 'Try a different search query or sort order.'}
              icon={emptyProcessesIcon}
            />
          </div>
        {:else}
          <div class="overflow-x-auto">
            <table class="min-w-full divide-y divide-border">
              <thead class="bg-surface-muted">
                <tr>
                  <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-fg-subtle">PID</th>
                  <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-fg-subtle">User</th>
                  <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-fg-subtle">CPU</th>
                  <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-fg-subtle">Memory</th>
                  <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-fg-subtle">RSS</th>
                  <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-fg-subtle">Stat</th>
                  <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-fg-subtle">Command</th>
                  <th class="px-4 py-3 text-right text-xs font-semibold uppercase tracking-wide text-fg-subtle">Action</th>
                </tr>
              </thead>
              <tbody class="divide-y divide-border bg-surface">
                {#each filteredProcesses as process}
                  <tr class="hover:bg-surface-muted">
                    <td class="whitespace-nowrap px-4 py-3 text-sm font-semibold text-fg">{process.pid}</td>
                    <td class="whitespace-nowrap px-4 py-3 text-sm text-fg-muted">{process.user}</td>
                    <td class="whitespace-nowrap px-4 py-3 text-sm">
                      <span class={`inline-flex rounded-full px-2 py-0.5 font-medium ${isHighUsage(process.cpu)}`}>
                        {formatPercent(process.cpu)}%
                      </span>
                    </td>
                    <td class="whitespace-nowrap px-4 py-3 text-sm">
                      <span class={`inline-flex rounded-full px-2 py-0.5 font-medium ${isHighUsage(process.memory)}`}>
                        {formatPercent(process.memory)}%
                      </span>
                    </td>
                    <td class="whitespace-nowrap px-4 py-3 text-sm text-fg-muted">{formatKiB(process.rss)}</td>
                    <td class="whitespace-nowrap px-4 py-3 text-sm text-fg-muted">{process.stat}</td>
                    <td class="max-w-[34rem] px-4 py-3 text-sm text-fg-muted" title={process.command}>
                      <div class="truncate font-mono text-[13px]">{formatCommand(process.command)}</div>
                    </td>
                    <td class="whitespace-nowrap px-4 py-3 text-right">
                      <Button variant="danger" size="sm" onclick={() => openKillModal(process)}>
                        <span class="inline-flex items-center gap-2">
                          <Trash2 size={14} />
                          Kill
                        </span>
                      </Button>
                    </td>
                  </tr>
                {/each}
              </tbody>
            </table>
          </div>
        {/if}
      </Card>
    </div>
  </div>
</div>

{#snippet emptyServersIcon()}
  <Server size={24} />
{/snippet}

{#snippet emptyServersAction()}
  <a
    href="/servers"
    class="inline-flex items-center justify-center rounded-lg bg-accent px-4 py-2 text-sm font-medium text-accent-fg transition hover:bg-accent-hover"
  >
    View servers
  </a>
{/snippet}

{#snippet emptyProcessesIcon()}
  <AlertTriangle size={24} />
{/snippet}

{#snippet killModalBody()}
  {#if killTarget}
    <div class="space-y-4">
      <div class="rounded-lg border border-warning/20 bg-warning/10 p-4 text-sm text-warning">
        <div class="flex items-start gap-3">
          <ShieldAlert size={18} class="mt-0.5 shrink-0" />
          <div>
            <p class="font-medium">This will send a signal to the selected process.</p>
            <p class="mt-1 text-warning">Choose TERM for a graceful shutdown, or KILL to force termination.</p>
          </div>
        </div>
      </div>

      <div class="rounded-lg border border-border bg-surface-muted p-4">
        <p class="text-xs font-semibold uppercase tracking-wide text-fg-subtle">Target process</p>
        <p class="mt-1 font-medium text-fg">PID {killTarget.pid} · {killTarget.user}</p>
        <p class="mt-1 max-h-20 overflow-hidden text-sm text-fg-muted" title={killTarget.command}>
          {killTarget.command}
        </p>
      </div>

      <div>
        <label for="signal-select" class="text-sm font-medium text-fg-muted">Signal</label>
        <select
          id="signal-select"
          bind:value={killSignal}
          class="mt-2 w-full rounded-lg border border-border-strong bg-surface px-4 py-3 text-fg shadow-sm outline-none transition focus:border-accent focus:ring-2 focus:ring-accent/20"
        >
          {#each allowedSignals as signal}
            <option value={signal}>{signal}</option>
          {/each}
        </select>
      </div>
    </div>
  {/if}
{/snippet}

{#snippet killModalFooter()}
  <div class="flex items-center justify-end gap-3">
    <Button variant="secondary" disabled={killing} onclick={closeKillModal}>Cancel</Button>
    <Button variant="danger" loading={killing} onclick={confirmKill}>Kill process</Button>
  </div>
{/snippet}

<Modal
  open={killModalOpen}
  title="Confirm process termination"
  size="md"
  onClose={closeKillModal}
  children={killModalBody}
  footer={killModalFooter}
/>
