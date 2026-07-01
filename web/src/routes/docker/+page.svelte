<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import {
    Container,
    Search,
    RefreshCw,
    Filter,
    ChevronDown,
    ChevronUp,
    Box,
    Layers,
    Play,
    Square,
    RotateCcw,
    Trash2,
    Download,
    FileText
  } from 'lucide-svelte';
  import { api } from '$lib/api/client';
  import { dockerApi } from '$lib/api/docker';
  import { type ServerSnapshot, type ContainerInfo, type ImageInfo } from '$lib/api/discovery';
  import { invalidateAll, invalidateSnapshot, loadSnapshot, loadSnapshots, snapshotsStore } from '$lib/stores/snapshots';
  import { type Server } from '$lib/stores/servers';
  import { Badge, Card, EmptyState, PageHeader, Skeleton, Spinner, Modal } from '$lib/components/ui';
  import { toast } from '$lib/stores/toast';

  type ActionKind = 'start' | 'stop' | 'restart' | 'remove';

  interface AggregatedContainer {
    container: ContainerInfo;
    serverId: number;
    serverName: string;
  }

  interface AggregatedImage {
    image: ImageInfo;
    serverId: number;
    serverName: string;
  }

  interface ServerOption {
    id: number;
    name: string;
  }

  const DEFAULT_LOG_LINES = 100;

  let servers = $state([] as Server[]);
  let loading = $state(true);
  let searchQuery = $state('');
  let showFilters = $state(false);
  let selectedServerId = $state('all');
  let filterState = $state('all');
  let activeTab = $state<'containers' | 'images'>('containers');
  let actionLoading = $state({} as Record<string, boolean>);

  let pullModalOpen = $state(false);
  let pullServerId = $state('all');
  let pullImage = $state('');
  let pullLoading = $state(false);

  let logsModalOpen = $state(false);
  let logsLoading = $state(false);
  let logsError = $state('');
  let logsTitle = $state('');
  let logsContent = $state('');
  let logsServerId = $state(0);
  let logsContainerName = $state('');

  let removeModalOpen = $state(false);
  let removeTarget = $state<AggregatedContainer | null>(null);
  let removeForce = $state(false);
  let removeLoading = $state(false);

  const allContainers = $derived.by(() => {
    const result: AggregatedContainer[] = [];
    servers.forEach((server) => {
      const snap: ServerSnapshot | null | undefined = $snapshotsStore[server.id];
      if (snap?.docker?.containers) {
        snap.docker.containers.forEach((container) => {
          result.push({
            container,
            serverId: server.id,
            serverName: server.name
          });
        });
      }
    });
    return result;
  });

  const allImages = $derived.by(() => {
    const result: AggregatedImage[] = [];
    servers.forEach((server) => {
      const snap: ServerSnapshot | null | undefined = $snapshotsStore[server.id];
      if (snap?.docker?.images) {
        snap.docker.images.forEach((image) => {
          result.push({
            image,
            serverId: server.id,
            serverName: server.name
          });
        });
      }
    });
    return result;
  });

  const availableServers = $derived.by(() => {
    const map = new Map();
    allContainers.forEach((item) => map.set(item.serverId, item.serverName));
    allImages.forEach((item) => map.set(item.serverId, item.serverName));
    return Array.from(map.entries())
      .map(([id, name]) => ({ id, name }))
      .sort((a, b) => a.name.localeCompare(b.name));
  });

  const hasMultipleServers = $derived(availableServers.length > 1);

  $effect(() => {
    if (selectedServerId === 'all' && availableServers.length === 1) {
      selectedServerId = String(availableServers[0].id);
    }
  });

  const filteredContainers = $derived.by(() => {
    let result = allContainers;

    if (searchQuery) {
      const q = searchQuery.toLowerCase();
      result = result.filter((item) =>
        item.container.name.toLowerCase().includes(q) ||
        item.container.image.toLowerCase().includes(q) ||
        item.serverName.toLowerCase().includes(q)
      );
    }

    if (selectedServerId !== 'all') {
      result = result.filter((item) => item.serverId === Number(selectedServerId));
    }

    if (filterState !== 'all') {
      result = result.filter((item) => item.container.state.toLowerCase() === filterState);
    }

    return result;
  });

  const filteredImages = $derived.by(() => {
    let result = allImages;

    if (searchQuery) {
      const q = searchQuery.toLowerCase();
      result = result.filter((item) =>
        item.image.repository.toLowerCase().includes(q) ||
        item.image.tag.toLowerCase().includes(q) ||
        item.serverName.toLowerCase().includes(q)
      );
    }

    if (selectedServerId !== 'all') {
      result = result.filter((item) => item.serverId === Number(selectedServerId));
    }

    return result;
  });

  const containerStats = $derived.by(() => {
    const stats = { running: 0, exited: 0, total: allContainers.length };
    allContainers.forEach((item) => {
      const state = item.container.state.toLowerCase();
      if (state === 'running') stats.running += 1;
      if (state === 'exited') stats.exited += 1;
    });
    return stats;
  });

  onMount(async () => {
    await loadServers();
  });

  async function loadServers() {
    loading = true;
    try {
      const data = await api.get('/servers') as Server[];
      servers = data;
      await loadSnapshots(data.map((server) => server.id));
    } catch {
      toast.error('Failed to load servers');
    } finally {
      loading = false;
    }
  }

  async function refreshAll() {
    invalidateAll();
    await loadServers();
  }

  function containerStateVariant(state: string): 'success' | 'warning' | 'error' | 'neutral' {
    const s = state.toLowerCase();
    if (s === 'running') return 'success';
    if (s === 'exited' || s === 'dead' || s === 'created' || s === 'paused') return 'neutral';
    if (s === 'error' || s === 'restarting') return 'error';
    return 'neutral';
  }

  function formatPorts(ports: { hostPort: number; containerPort: number; protocol: string }[]): string {
    if (!ports || ports.length === 0) return '—';
    return ports.map((port) => `${port.hostPort}:${port.containerPort}/${port.protocol}`).join(', ');
  }

  function formatUptime(status: string): string {
    const match = status.match(/^Up\s+(.*)$/i);
    return match?.[1] ?? status;
  }

  function actionKey(serverId: number, name: string, action: ActionKind): string {
    return `${action}:${serverId}:${name}`;
  }

  function isActionLoading(serverId: number, name: string, action: ActionKind): boolean {
    return actionLoading[actionKey(serverId, name, action)] ?? false;
  }

  async function refreshSnapshot(serverId: number) {
    invalidateSnapshot(serverId);
    await loadSnapshot(serverId);
  }

  function getErrorMessage(error: unknown, fallback: string): string {
    if (error instanceof Error && error.message) {
      return error.message;
    }
    return fallback;
  }

  async function performContainerAction(item: AggregatedContainer, action: ActionKind) {
    const key = actionKey(item.serverId, item.container.name, action);
    actionLoading[key] = true;
    try {
      let response: { message: string };
      if (action === 'start') {
        response = await dockerApi.startContainer(item.serverId, item.container.name);
      } else if (action === 'stop') {
        response = await dockerApi.stopContainer(item.serverId, item.container.name);
      } else if (action === 'restart') {
        response = await dockerApi.restartContainer(item.serverId, item.container.name);
      } else {
        response = await dockerApi.removeContainer(item.serverId, item.container.name, false);
      }

      toast.success(response.message || `${action}ed ${item.container.name}`);
      await refreshSnapshot(item.serverId);
    } catch (error) {
      toast.error(getErrorMessage(error, `Failed to ${action} container`));
    } finally {
      actionLoading[key] = false;
    }
  }

  function openRemoveModal(item: AggregatedContainer) {
    removeTarget = item;
    removeForce = false;
    removeModalOpen = true;
  }

  async function confirmRemove() {
    if (!removeTarget) return;

    const item = removeTarget;
    const key = actionKey(item.serverId, item.container.name, 'remove');
    actionLoading[key] = true;
    removeLoading = true;
    try {
      const response = await dockerApi.removeContainer(item.serverId, item.container.name, removeForce);
      toast.success(response.message || `Removed ${item.container.name}`);
      removeModalOpen = false;
      removeTarget = null;
      await refreshSnapshot(item.serverId);
    } catch (error) {
      toast.error(getErrorMessage(error, 'Failed to remove container'));
    } finally {
      actionLoading[key] = false;
      removeLoading = false;
    }
  }

  function openPullModal() {
    if (availableServers.length === 0) {
      toast.warning('No Docker-enabled servers are available');
      return;
    }

    pullImage = '';
    pullServerId = selectedServerId !== 'all'
      ? selectedServerId
      : String(availableServers[0].id);
    pullModalOpen = true;
  }

  async function confirmPullImage() {
    const serverId = pullServerId === 'all' ? null : Number(pullServerId);
    if (!serverId || Number.isNaN(serverId)) {
      toast.error('Please choose a server');
      return;
    }

    if (!pullImage.trim()) {
      toast.error('Please enter an image name');
      return;
    }

    pullLoading = true;
    try {
      const response = await dockerApi.pullImage(serverId, pullImage.trim());
      toast.success(`Pulled ${response.image}`);
      pullModalOpen = false;
      await refreshSnapshot(serverId);
    } catch (error) {
      toast.error(getErrorMessage(error, 'Failed to pull image'));
    } finally {
      pullLoading = false;
    }
  }

  async function openLogsModal(item: AggregatedContainer) {
    logsModalOpen = true;
    logsLoading = true;
    logsError = '';
    logsTitle = `${item.container.name} · ${item.serverName}`;
    logsServerId = item.serverId;
    logsContainerName = item.container.name;
    logsContent = '';

    try {
      const response = await dockerApi.getContainerLogs(item.serverId, item.container.name, DEFAULT_LOG_LINES);
      logsContent = response.logs;
    } catch (error) {
      logsError = getErrorMessage(error, 'Failed to load logs');
      toast.error(logsError);
    } finally {
      logsLoading = false;
    }
  }

  function closeLogsModal() {
    logsModalOpen = false;
    logsLoading = false;
    logsError = '';
    logsTitle = '';
    logsContent = '';
    logsServerId = 0;
    logsContainerName = '';
  }

  function closeRemoveModal() {
    removeModalOpen = false;
    removeTarget = null;
    removeForce = false;
    removeLoading = false;
  }
</script>

<svelte:head><title>Docker - Meshium</title></svelte:head>

<div class="mx-auto max-w-7xl p-4 sm:p-6">
  <PageHeader title="Docker" subtitle="Manage Docker containers and images across your servers.">
    {#snippet actions()}
      <div class="flex items-center gap-2">
        <button
          type="button"
          onclick={openPullModal}
          class="inline-flex items-center gap-2 rounded-lg border border-slate-300 bg-white px-4 py-2 text-sm font-medium text-slate-700 hover:bg-slate-50"
        >
          <Download size={16} />
          Pull Image
        </button>
        <button
          type="button"
          onclick={refreshAll}
          disabled={loading}
          class="inline-flex items-center gap-2 rounded-lg border border-slate-300 bg-white px-4 py-2 text-sm font-medium text-slate-700 hover:bg-slate-50 disabled:opacity-60"
        >
          {#if loading}
            <Spinner size="sm" label="Refreshing" />
          {:else}
            <RefreshCw size={16} />
          {/if}
          Refresh
        </button>
      </div>
    {/snippet}
  </PageHeader>

  <div class="mb-6 grid gap-4 sm:grid-cols-3">
    <Card padding="lg">
      <div class="flex items-center gap-3">
        <div class="flex h-10 w-10 items-center justify-center rounded-lg bg-blue-50 text-blue-600">
          <Container size={20} />
        </div>
        <div>
          <p class="text-2xl font-bold text-slate-900">{containerStats.total}</p>
          <p class="text-xs text-slate-500">Total Containers</p>
        </div>
      </div>
    </Card>
    <Card padding="lg">
      <div class="flex items-center gap-3">
        <div class="flex h-10 w-10 items-center justify-center rounded-lg bg-green-50 text-green-600">
          <Box size={20} />
        </div>
        <div>
          <p class="text-2xl font-bold text-slate-900">{containerStats.running}</p>
          <p class="text-xs text-slate-500">Running</p>
        </div>
      </div>
    </Card>
    <Card padding="lg">
      <div class="flex items-center gap-3">
        <div class="flex h-10 w-10 items-center justify-center rounded-lg bg-purple-50 text-purple-600">
          <Layers size={20} />
        </div>
        <div>
          <p class="text-2xl font-bold text-slate-900">{allImages.length}</p>
          <p class="text-xs text-slate-500">Images</p>
        </div>
      </div>
    </Card>
  </div>

  <div class="mb-6 border-b border-slate-200">
    <div class="-mb-px flex gap-2">
      <button
        type="button"
        onclick={() => activeTab = 'containers'}
        class={`rounded-t-lg border px-4 py-2 text-sm font-medium transition ${activeTab === 'containers' ? 'border-slate-200 border-b-white bg-white text-slate-900' : 'border-transparent text-slate-500 hover:bg-slate-50'}`}
      >
        Containers ({filteredContainers.length})
      </button>
      <button
        type="button"
        onclick={() => activeTab = 'images'}
        class={`rounded-t-lg border px-4 py-2 text-sm font-medium transition ${activeTab === 'images' ? 'border-slate-200 border-b-white bg-white text-slate-900' : 'border-transparent text-slate-500 hover:bg-slate-50'}`}
      >
        Images ({filteredImages.length})
      </button>
    </div>
  </div>

  <div class="mb-6">
    <Card>
      <div class="flex flex-col gap-4">
      <div class="flex flex-col gap-3 lg:flex-row lg:items-center">
        <div class="relative flex-1">
          <Search size={16} class="absolute left-3 top-1/2 -translate-y-1/2 text-slate-400" />
          <input
            type="text"
            bind:value={searchQuery}
            placeholder="Search containers, images, servers..."
            class="w-full rounded-lg border border-slate-300 bg-white py-2 pl-10 pr-4 text-sm outline-none focus:border-blue-500"
          />
        </div>

        {#if hasMultipleServers}
          <label class="block min-w-0 lg:w-72">
            <span class="mb-1 block text-xs font-semibold uppercase tracking-wide text-slate-500">Server</span>
            <select bind:value={selectedServerId} class="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-sm outline-none focus:border-blue-500">
              <option value="all">All servers</option>
              {#each availableServers as server}
                <option value={server.id}>{server.name}</option>
              {/each}
            </select>
          </label>
        {/if}

        <button
          type="button"
          onclick={() => showFilters = !showFilters}
          class="inline-flex items-center gap-2 rounded-lg border border-slate-300 bg-white px-4 py-2 text-sm font-medium text-slate-700 hover:bg-slate-50"
        >
          <Filter size={16} />
          Filters
          {#if showFilters}
            <ChevronUp size={16} />
          {:else}
            <ChevronDown size={16} />
          {/if}
        </button>
      </div>

      {#if showFilters}
        <div class="grid gap-4 sm:grid-cols-2">
          <label class="block">
            <span class="mb-1 block text-xs font-semibold uppercase tracking-wide text-slate-500">State</span>
            <select bind:value={filterState} class="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-sm outline-none focus:border-blue-500">
              <option value="all">All states</option>
              <option value="running">Running</option>
              <option value="exited">Exited</option>
              <option value="created">Created</option>
              <option value="dead">Dead</option>
              <option value="paused">Paused</option>
            </select>
          </label>

          <div class="flex items-end">
            <button
              type="button"
              onclick={() => { searchQuery = ''; selectedServerId = 'all'; filterState = 'all'; }}
              class="inline-flex items-center gap-2 rounded-lg border border-slate-300 bg-white px-4 py-2 text-sm font-medium text-slate-700 hover:bg-slate-50"
            >
              Reset Filters
            </button>
          </div>
        </div>
      {/if}
    </div>
  </Card>
  </div>

  {#if loading && servers.length === 0}
    <div class="space-y-3">
      {#each Array(5) as _}
        <Card>
          <div class="flex items-center gap-4">
            <Skeleton width="200px" />
            <Skeleton width="100px" />
            <Skeleton width="150px" />
            <Skeleton width="100px" />
          </div>
        </Card>
      {/each}
    </div>
  {:else if activeTab === 'containers'}
    {#if filteredContainers.length === 0}
      <EmptyState
        title="No containers found"
        description={allContainers.length === 0 ? 'No Docker containers detected across your servers.' : 'Try adjusting your filters.'}
        icon={emptyIcon}
      />
    {:else}
      <div class="overflow-hidden rounded-2xl border border-slate-200 bg-white shadow-sm">
        <table class="min-w-full divide-y divide-slate-200">
          <thead class="bg-slate-50">
            <tr>
              <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500">Name</th>
              <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500">Image</th>
              <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500">State</th>
              <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500">Ports</th>
              <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500">Uptime</th>
              <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500">Server</th>
              <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500">Actions</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-slate-100">
            {#each filteredContainers as item (item.container.name + item.serverId)}
              <tr class="cursor-pointer hover:bg-slate-50" onclick={() => goto(`/servers/${item.serverId}`)}>
                <td class="px-4 py-3 font-medium text-slate-900">{item.container.name}</td>
                <td class="px-4 py-3 text-slate-600">{item.container.image}</td>
                <td class="px-4 py-3"><Badge variant={containerStateVariant(item.container.state)}>{item.container.state}</Badge></td>
                <td class="px-4 py-3 text-xs text-slate-600">{formatPorts(item.container.ports)}</td>
                <td class="px-4 py-3 text-xs text-slate-600">{formatUptime(item.container.status)}</td>
                <td class="px-4 py-3 text-slate-600">{item.serverName}</td>
                <td class="px-4 py-3">
                  <div class="flex flex-wrap gap-2">
                    {#if item.container.state.toLowerCase() !== 'running'}
                      <button
                        type="button"
                        disabled={isActionLoading(item.serverId, item.container.name, 'start')}
                        onclick={(event) => { event.stopPropagation(); performContainerAction(item, 'start'); }}
                        class="inline-flex items-center gap-1 rounded-lg border border-slate-300 bg-white px-3 py-1.5 text-xs font-medium text-slate-700 hover:bg-slate-50 disabled:opacity-60"
                      >
                        {#if isActionLoading(item.serverId, item.container.name, 'start')}
                          <Spinner size="sm" label="Loading" />
                        {:else}
                          <Play size={14} />
                        {/if}
                        Start
                      </button>
                    {:else}
                      <button
                        type="button"
                        disabled={isActionLoading(item.serverId, item.container.name, 'stop')}
                        onclick={(event) => { event.stopPropagation(); performContainerAction(item, 'stop'); }}
                        class="inline-flex items-center gap-1 rounded-lg border border-slate-300 bg-white px-3 py-1.5 text-xs font-medium text-slate-700 hover:bg-slate-50 disabled:opacity-60"
                      >
                        {#if isActionLoading(item.serverId, item.container.name, 'stop')}
                          <Spinner size="sm" label="Loading" />
                        {:else}
                          <Square size={14} />
                        {/if}
                        Stop
                      </button>
                      <button
                        type="button"
                        disabled={isActionLoading(item.serverId, item.container.name, 'restart')}
                        onclick={(event) => { event.stopPropagation(); performContainerAction(item, 'restart'); }}
                        class="inline-flex items-center gap-1 rounded-lg border border-slate-300 bg-white px-3 py-1.5 text-xs font-medium text-slate-700 hover:bg-slate-50 disabled:opacity-60"
                      >
                        {#if isActionLoading(item.serverId, item.container.name, 'restart')}
                          <Spinner size="sm" label="Loading" />
                        {:else}
                          <RotateCcw size={14} />
                        {/if}
                        Restart
                      </button>
                    {/if}

                    <button
                      type="button"
                      onclick={(event) => { event.stopPropagation(); openLogsModal(item); }}
                      class="inline-flex items-center gap-1 rounded-lg border border-slate-300 bg-white px-3 py-1.5 text-xs font-medium text-slate-700 hover:bg-slate-50"
                    >
                      <FileText size={14} />
                      Logs
                    </button>

                    <button
                      type="button"
                      disabled={isActionLoading(item.serverId, item.container.name, 'remove')}
                      onclick={(event) => { event.stopPropagation(); openRemoveModal(item); }}
                      class="inline-flex items-center gap-1 rounded-lg bg-red-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-red-700 disabled:opacity-60"
                    >
                      {#if isActionLoading(item.serverId, item.container.name, 'remove')}
                        <Spinner size="sm" label="Loading" />
                      {:else}
                        <Trash2 size={14} />
                      {/if}
                      Remove
                    </button>
                  </div>
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  {:else}
    {#if filteredImages.length === 0}
      <EmptyState
        title="No images found"
        description={allImages.length === 0 ? 'No Docker images detected across your servers.' : 'Try adjusting your filters.'}
        icon={emptyIcon}
      />
    {:else}
      <div class="overflow-hidden rounded-2xl border border-slate-200 bg-white shadow-sm">
        <table class="min-w-full divide-y divide-slate-200">
          <thead class="bg-slate-50">
            <tr>
              <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500">Repository</th>
              <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500">Tag</th>
              <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500">Size</th>
              <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500">Server</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-slate-100">
            {#each filteredImages as item (item.image.id + item.serverId)}
              <tr class="cursor-pointer hover:bg-slate-50" onclick={() => goto(`/servers/${item.serverId}`)}>
                <td class="px-4 py-3 font-medium text-slate-900">{item.image.repository}</td>
                <td class="px-4 py-3 text-slate-600">{item.image.tag}</td>
                <td class="px-4 py-3 text-slate-600">{item.image.size}</td>
                <td class="px-4 py-3 text-slate-600">{item.serverName}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  {/if}
</div>

{#if pullModalOpen}
  <Modal open={true} title="Pull Image" onClose={() => { pullModalOpen = false; pullImage = ''; pullServerId = 'all'; }}>
    <div class="space-y-4">
      <div>
        <label for="docker-pull-image" class="mb-1 block text-sm font-medium text-slate-700">Image</label>
        <input
          id="docker-pull-image"
          type="text"
          bind:value={pullImage}
          placeholder="nginx:latest"
          class="w-full rounded-lg border border-slate-300 px-3 py-2 text-sm outline-none focus:border-blue-500"
          onkeydown={(event) => { if (event.key === 'Enter') confirmPullImage(); }}
        />
      </div>
      <div>
        <label for="docker-pull-server" class="mb-1 block text-sm font-medium text-slate-700">Server</label>
        <select id="docker-pull-server" bind:value={pullServerId} class="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-sm outline-none focus:border-blue-500">
          <option value="all" disabled>Select a server</option>
          {#each availableServers as server}
            <option value={server.id}>{server.name}</option>
          {/each}
        </select>
      </div>
      <p class="text-xs text-slate-500">Docker images are pulled on the selected server, then the snapshot cache is refreshed.</p>
    </div>
    <div class="mt-4 flex justify-end gap-2">
      <button type="button" onclick={() => { pullModalOpen = false; pullImage = ''; pullServerId = 'all'; }} class="rounded-lg border border-slate-300 bg-white px-4 py-2 text-sm font-medium text-slate-700 hover:bg-slate-50">
        Cancel
      </button>
      <button type="button" onclick={confirmPullImage} disabled={pullLoading} class="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-60">
        {#if pullLoading}
          <Spinner size="sm" label="Loading" />
        {:else}
          <Download size={14} />
        {/if}
        Pull
      </button>
    </div>
  </Modal>
{/if}

{#if logsModalOpen}
  <Modal open={true} title={logsTitle || 'Container Logs'} size="lg" onClose={closeLogsModal}>
    <div class="space-y-3">
      {#if logsLoading}
        <div class="flex items-center justify-center py-12">
          <Spinner label="Loading logs" />
        </div>
      {:else if logsError}
        <div class="rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">
          {logsError}
        </div>
      {:else}
        <div class="flex items-center justify-between rounded-lg bg-slate-50 px-3 py-2 text-xs text-slate-500">
          <span>{logsContainerName}</span>
          <span>Server #{logsServerId} · Tail {DEFAULT_LOG_LINES}</span>
        </div>
        <pre class="max-h-[60vh] overflow-auto rounded-lg bg-slate-900 p-4 text-xs leading-6 text-slate-100 whitespace-pre-wrap">{logsContent || 'No logs returned.'}</pre>
      {/if}
    </div>
    <div class="mt-4 flex justify-end gap-2">
      <button type="button" onclick={closeLogsModal} class="rounded-lg border border-slate-300 bg-white px-4 py-2 text-sm font-medium text-slate-700 hover:bg-slate-50">
        Close
      </button>
    </div>
  </Modal>
{/if}

{#if removeModalOpen && removeTarget}
  <Modal open={true} title="Confirm Remove" onClose={closeRemoveModal}>
    <div class="space-y-3">
      <div class="flex items-start gap-3">
        <div class="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg bg-red-50 text-red-600">
          <Trash2 size={20} />
        </div>
        <div>
          <p class="text-sm font-medium text-slate-900">Remove container {removeTarget.container.name}?</p>
          <p class="mt-1 text-sm text-slate-500">
            This will delete the container from {removeTarget.serverName}.
            {#if removeForce}
              Force remove is enabled.
            {/if}
          </p>
        </div>
      </div>

      <label class="flex items-center gap-2 text-sm text-slate-600">
        <input type="checkbox" bind:checked={removeForce} class="rounded border-slate-300" />
        Force remove with <span class="font-mono text-xs">docker rm -f</span>
      </label>
    </div>
    <div class="mt-4 flex justify-end gap-2">
      <button type="button" onclick={closeRemoveModal} class="rounded-lg border border-slate-300 bg-white px-4 py-2 text-sm font-medium text-slate-700 hover:bg-slate-50" disabled={removeLoading}>
        Cancel
      </button>
      <button type="button" onclick={confirmRemove} disabled={removeLoading} class="inline-flex items-center gap-2 rounded-lg bg-red-600 px-4 py-2 text-sm font-medium text-white hover:bg-red-700 disabled:opacity-60">
        {#if removeLoading}
          <Spinner size="sm" label="Loading" />
        {:else}
          <Trash2 size={14} />
        {/if}
        Remove
      </button>
    </div>
  </Modal>
{/if}

{#snippet emptyIcon()}<Container size={22} />{/snippet}
