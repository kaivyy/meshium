<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import { Search, RefreshCw, Server as ServerIcon, Play, Square, RotateCcw, ToggleLeft, ToggleRight, AlertCircle } from 'lucide-svelte';
  import { api } from '$lib/api/client';
  import { servicesApi, type ServiceInfo, type ServiceStatus } from '$lib/api/services';
  import { type Server } from '$lib/stores/servers';
  import { toast } from '$lib/stores/toast';
  import { Badge, Card, EmptyState, PageHeader, Skeleton, Spinner } from '$lib/components/ui';
  import { formatRelativeTime } from '$lib/utils/format';

  type ServiceAction = 'start' | 'stop' | 'restart' | 'enable' | 'disable';

  let servers = $state([] as Server[]);
  let selectedServerId = $state<number | null>(null);
  let services = $state([] as ServiceInfo[]);
  let loading = $state(true);
  let searchQuery = $state('');
  let autoRefresh = $state(true);
  let lastRefresh = $state<Date | null>(null);
  let refreshTimer: ReturnType<typeof setInterval> | null = null;
  let activeAction = $state<string | null>(null);

  const selectedServer = $derived.by(() => servers.find((server) => server.id === selectedServerId) ?? null);

  const filteredServices = $derived.by(() => {
    const query = searchQuery.trim().toLowerCase();
    if (!query) return services;

    return services.filter((service) => service.name.toLowerCase().includes(query));
  });

  const stats = $derived.by(() => {
    let total = services.length;
    let active = 0;
    let failed = 0;
    let inactive = 0;

    for (const service of services) {
      if (service.activeState === 'active') active += 1;
      else if (service.activeState === 'failed') failed += 1;
      else inactive += 1;
    }

    return { total, active, failed, inactive };
  });

  onMount(async () => {
    await loadServers();
    startAutoRefresh();
  });

  onDestroy(() => {
    if (refreshTimer) clearInterval(refreshTimer);
  });

  function startAutoRefresh() {
    if (refreshTimer) clearInterval(refreshTimer);
    refreshTimer = setInterval(async () => {
      if (autoRefresh && selectedServerId) {
        await loadServices(selectedServerId, { silent: true, notify: false });
      }
    }, 30000);
  }

  async function loadServers() {
    loading = true;
    try {
      const data = await api.get('/servers') as Server[];
      servers = data;

      if (data.length === 0) {
        selectedServerId = null;
        services = [];
        lastRefresh = null;
        return;
      }

      if (!selectedServerId || !data.some((server) => server.id === selectedServerId)) {
        selectedServerId = data[0].id;
        services = [];
        lastRefresh = null;
      }

      await loadServices(selectedServerId, { silent: false, notify: true });
    } catch {
      toast.error('Failed to load servers');
    } finally {
      loading = false;
    }
  }

  async function loadServices(serverId: number | null = selectedServerId, options: { silent?: boolean; notify?: boolean } = {}) {
    if (!serverId) {
      services = [];
      lastRefresh = null;
      return;
    }

    if (!options.silent) {
      loading = true;
    }

    try {
      services = await servicesApi.listServices(serverId);
      lastRefresh = new Date();
    } catch (error) {
      if (options.notify !== false) {
        toast.error(error instanceof Error ? error.message : 'Failed to load services');
      }
    } finally {
      if (!options.silent) {
        loading = false;
      }
    }
  }

  async function handleServerChange(event: Event) {
    const value = (event.currentTarget as HTMLSelectElement).value;
    const serverId = Number(value);
    selectedServerId = Number.isNaN(serverId) ? null : serverId;
    services = [];
    lastRefresh = null;
    await loadServices(selectedServerId, { silent: false, notify: true });
  }

  async function refreshNow() {
    await loadServices(selectedServerId, { silent: false, notify: true });
  }

  function actionKey(serviceName: string, action: ServiceAction) {
    return `${serviceName}:${action}`;
  }

  function isBusy(serviceName: string, action: ServiceAction) {
    return activeAction === actionKey(serviceName, action);
  }

  function serviceBusy(serviceName: string) {
    return activeAction?.startsWith(`${serviceName}:`) ?? false;
  }

  function setUpdatedService(updated: ServiceStatus) {
    services = services.map((service) => (service.name === updated.name ? updated : service));
    lastRefresh = new Date();
  }

  async function performAction(service: ServiceInfo, action: ServiceAction) {
    if (!selectedServerId) return;

    activeAction = actionKey(service.name, action);
    try {
      let updated: ServiceStatus;
      if (action === 'start') updated = await servicesApi.startService(selectedServerId, service.name);
      else if (action === 'stop') updated = await servicesApi.stopService(selectedServerId, service.name);
      else if (action === 'restart') updated = await servicesApi.restartService(selectedServerId, service.name);
      else if (action === 'enable') updated = await servicesApi.enableService(selectedServerId, service.name);
      else updated = await servicesApi.disableService(selectedServerId, service.name);

      setUpdatedService(updated);
      toast.success(`${service.name} ${actionPastTense(action)}`);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : `Failed to ${action} ${service.name}`);
    } finally {
      activeAction = null;
    }
  }

  function actionPastTense(action: ServiceAction): string {
    switch (action) {
      case 'start': return 'started';
      case 'stop': return 'stopped';
      case 'restart': return 'restarted';
      case 'enable': return 'enabled';
      case 'disable': return 'disabled';
      default: return action;
    }
  }

  function stateVariant(service: ServiceInfo): 'success' | 'warning' | 'error' | 'neutral' {
    if (service.activeState === 'active') return 'success';
    if (service.activeState === 'failed') return 'error';
    if (service.activeState === 'activating' || service.activeState === 'reloading' || service.activeState === 'deactivating') return 'warning';
    return 'neutral';
  }

  function enabledVariant(enabled: boolean): 'success' | 'neutral' {
    return enabled ? 'success' : 'neutral';
  }

  function canStart(service: ServiceInfo) {
    return service.activeState !== 'active' && service.activeState !== 'activating' && service.activeState !== 'reloading';
  }

  function canStop(service: ServiceInfo) {
    return service.activeState === 'active' || service.activeState === 'reloading';
  }

  function canRestart(service: ServiceInfo) {
    return service.activeState === 'active' || service.activeState === 'failed';
  }

  function canToggleEnabled(service: ServiceInfo) {
    return true;
  }

  function formatServiceDescription(service: ServiceInfo) {
    return service.description || 'No description available';
  }
</script>

<svelte:head><title>Services - Meshium</title></svelte:head>

<div class="p-4 sm:p-6 max-w-7xl mx-auto">
  <PageHeader title="Services" subtitle="Start, stop, restart, enable, and disable systemd services on your remote servers.">
    {#snippet actions()}
      <div class="flex items-center gap-3">
        {#if lastRefresh}
          <span class="hidden text-xs text-slate-400 sm:inline">Updated {formatRelativeTime(lastRefresh.toISOString())}</span>
        {/if}
        <button
          type="button"
          onclick={() => {
            autoRefresh = !autoRefresh;
            startAutoRefresh();
          }}
          class="inline-flex items-center gap-2 rounded-lg border border-slate-300 bg-white px-3 py-2 text-xs font-medium text-slate-600 hover:bg-slate-50"
        >
          <span class={`inline-block h-2 w-2 rounded-full ${autoRefresh ? 'bg-green-500' : 'bg-slate-300'}`}></span>
          Auto {autoRefresh ? 'ON' : 'OFF'}
        </button>
        <button
          type="button"
          onclick={refreshNow}
          disabled={loading || selectedServerId === null}
          class="inline-flex items-center gap-2 rounded-lg border border-slate-300 bg-white px-4 py-2 text-sm font-medium text-slate-700 hover:bg-slate-50 disabled:cursor-not-allowed disabled:opacity-60"
        >
          {#if loading}<Spinner size="sm" label="Refreshing" />{:else}<RefreshCw size={16} />{/if}
          Refresh
        </button>
      </div>
    {/snippet}
  </PageHeader>

  <Card padding="lg" class="mb-6">
    <div class="grid gap-4 lg:grid-cols-2">
      <label class="block">
        <span class="mb-1 block text-xs font-semibold uppercase tracking-wide text-slate-500">Server</span>
        <select
          value={selectedServerId ?? ''}
          onchange={handleServerChange}
          class="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-sm text-slate-900 outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20"
        >
          {#each servers as server}
            <option value={server.id}>{server.name} — {server.host}:{server.port}</option>
          {/each}
        </select>
      </label>

      <label class="block">
        <span class="mb-1 block text-xs font-semibold uppercase tracking-wide text-slate-500">Search services</span>
        <div class="relative">
          <Search size={16} class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-slate-400" />
          <input
            type="text"
            bind:value={searchQuery}
            placeholder="Filter by service name"
            class="w-full rounded-lg border border-slate-300 bg-white py-2 pl-10 pr-4 text-sm text-slate-900 outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20"
          />
        </div>
      </label>
    </div>

    {#if selectedServer}
      <div class="mt-4 flex flex-wrap items-center gap-2 text-xs text-slate-500">
        <span class="font-medium text-slate-700">{selectedServer.name}</span>
        <span>·</span>
        <span>{selectedServer.host}:{selectedServer.port}</span>
        <span>·</span>
        <span>{filteredServices.length} matching services</span>
      </div>
    {/if}
  </Card>

  <div class="mb-6 grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
    <Card padding="lg">
      <div class="flex items-center gap-3">
        <div class="flex h-10 w-10 items-center justify-center rounded-lg bg-blue-50 text-blue-600"><ServerIcon size={20} /></div>
        <div>
          <p class="text-2xl font-bold text-slate-900">{stats.total}</p>
          <p class="text-xs text-slate-500">Total Services</p>
        </div>
      </div>
    </Card>
    <Card padding="lg">
      <div class="flex items-center gap-3">
        <div class="flex h-10 w-10 items-center justify-center rounded-lg bg-green-50 text-green-600"><Play size={20} /></div>
        <div>
          <p class="text-2xl font-bold text-slate-900">{stats.active}</p>
          <p class="text-xs text-slate-500">Active</p>
        </div>
      </div>
    </Card>
    <Card padding="lg">
      <div class="flex items-center gap-3">
        <div class="flex h-10 w-10 items-center justify-center rounded-lg bg-red-50 text-red-600"><AlertCircle size={20} /></div>
        <div>
          <p class="text-2xl font-bold text-slate-900">{stats.failed}</p>
          <p class="text-xs text-slate-500">Failed</p>
        </div>
      </div>
    </Card>
    <Card padding="lg">
      <div class="flex items-center gap-3">
        <div class="flex h-10 w-10 items-center justify-center rounded-lg bg-slate-50 text-slate-500"><Square size={20} /></div>
        <div>
          <p class="text-2xl font-bold text-slate-900">{stats.inactive}</p>
          <p class="text-xs text-slate-500">Inactive</p>
        </div>
      </div>
    </Card>
  </div>

  {#if loading && services.length === 0}
    <Card padding="lg">
      <div class="space-y-3">
        <Skeleton width="45%" />
        <Skeleton width="70%" />
        <div class="space-y-2 pt-2">
          {#each Array(5) as _}
            <div class="grid grid-cols-5 gap-3 rounded-lg border border-slate-100 p-3">
              <Skeleton width="80%" />
              <Skeleton width="100%" />
              <Skeleton width="80px" height="20px" rounded />
              <Skeleton width="80px" height="20px" rounded />
              <Skeleton width="180px" height="20px" rounded />
            </div>
          {/each}
        </div>
      </div>
    </Card>
  {:else if selectedServerId === null}
    <EmptyState title="No servers available" description="Add a server first, then return here to manage its services." icon={emptyIcon} />
  {:else if filteredServices.length === 0}
    <EmptyState
      title={searchQuery ? 'No matching services' : 'No services found'}
      description={searchQuery ? 'Try a different search term.' : 'This server did not return any systemd services.'}
      icon={emptyIcon}
    />
  {:else}
    <Card padding="sm">
      <div class="overflow-x-auto">
        <table class="min-w-full divide-y divide-slate-200">
          <thead class="bg-slate-50">
            <tr>
              <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500">Name</th>
              <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500">Description</th>
              <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500">State</th>
              <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500">Enabled</th>
              <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500">Actions</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-slate-100 bg-white">
            {#each filteredServices as service (service.name)}
              <tr class="align-top hover:bg-slate-50">
                <td class="whitespace-nowrap px-4 py-4">
                  <div class="font-medium text-slate-900">{service.name}</div>
                  <div class="mt-1 text-xs text-slate-400">{service.type || 'service'}</div>
                </td>
                <td class="px-4 py-4 text-sm text-slate-600">{formatServiceDescription(service)}</td>
                <td class="whitespace-nowrap px-4 py-4">
                  <div class="flex flex-col gap-1">
                    <Badge variant={stateVariant(service)} size="sm">{service.activeState}</Badge>
                    {#if service.subState && service.subState !== service.activeState}
                      <span class="text-xs text-slate-400">{service.subState}</span>
                    {/if}
                  </div>
                </td>
                <td class="whitespace-nowrap px-4 py-4">
                  <Badge variant={enabledVariant(service.enabled)} size="sm">{service.enabled ? 'Enabled' : 'Disabled'}</Badge>
                </td>
                <td class="px-4 py-4">
                  <div class="flex flex-wrap gap-2">
                    {#if canStart(service)}
                      <button
                        type="button"
                        onclick={() => performAction(service, 'start')}
                        disabled={serviceBusy(service.name)}
                        class="inline-flex items-center gap-1.5 rounded-lg border border-slate-300 bg-white px-3 py-1.5 text-xs font-medium text-slate-700 hover:bg-slate-50 disabled:cursor-not-allowed disabled:opacity-60"
                      >
                        {#if isBusy(service.name, 'start')}<Spinner size="sm" label="" />{:else}<Play size={12} />{/if}
                        Start
                      </button>
                    {/if}

                    {#if canStop(service)}
                      <button
                        type="button"
                        onclick={() => performAction(service, 'stop')}
                        disabled={serviceBusy(service.name)}
                        class="inline-flex items-center gap-1.5 rounded-lg border border-slate-300 bg-white px-3 py-1.5 text-xs font-medium text-slate-700 hover:bg-slate-50 disabled:cursor-not-allowed disabled:opacity-60"
                      >
                        {#if isBusy(service.name, 'stop')}<Spinner size="sm" label="" />{:else}<Square size={12} />{/if}
                        Stop
                      </button>
                    {/if}

                    {#if canRestart(service)}
                      <button
                        type="button"
                        onclick={() => performAction(service, 'restart')}
                        disabled={serviceBusy(service.name)}
                        class="inline-flex items-center gap-1.5 rounded-lg border border-slate-300 bg-white px-3 py-1.5 text-xs font-medium text-slate-700 hover:bg-slate-50 disabled:cursor-not-allowed disabled:opacity-60"
                      >
                        {#if isBusy(service.name, 'restart')}<Spinner size="sm" label="" />{:else}<RotateCcw size={12} />{/if}
                        Restart
                      </button>
                    {/if}

                    {#if canToggleEnabled(service)}
                      {#if service.enabled}
                        <button
                          type="button"
                          onclick={() => performAction(service, 'disable')}
                          disabled={serviceBusy(service.name)}
                          class="inline-flex items-center gap-1.5 rounded-lg border border-slate-300 bg-white px-3 py-1.5 text-xs font-medium text-slate-700 hover:bg-slate-50 disabled:cursor-not-allowed disabled:opacity-60"
                        >
                          {#if isBusy(service.name, 'disable')}<Spinner size="sm" label="" />{:else}<ToggleLeft size={12} />{/if}
                          Disable
                        </button>
                      {:else}
                        <button
                          type="button"
                          onclick={() => performAction(service, 'enable')}
                          disabled={serviceBusy(service.name)}
                          class="inline-flex items-center gap-1.5 rounded-lg border border-slate-300 bg-white px-3 py-1.5 text-xs font-medium text-slate-700 hover:bg-slate-50 disabled:cursor-not-allowed disabled:opacity-60"
                        >
                          {#if isBusy(service.name, 'enable')}<Spinner size="sm" label="" />{:else}<ToggleRight size={12} />{/if}
                          Enable
                        </button>
                      {/if}
                    {/if}
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

{#snippet emptyIcon()}<ServerIcon size={22} />{/snippet}
