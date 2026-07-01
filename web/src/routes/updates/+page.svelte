<script lang="ts">
  import { onMount } from 'svelte';
  import {
    AlertCircle,
    Download,
    Package,
    RefreshCw,
    Search,
    Server,
    ShieldAlert,
    ShieldCheck,
    Trash2,
    Wrench,
    Loader2
  } from 'lucide-svelte';
  import { fetchServers, serverStore } from '$lib/stores/servers';
  import { toast } from '$lib/stores/toast';
  import { Badge, Card, EmptyState, Modal, PageHeader, Skeleton, Spinner } from '$lib/components/ui';
  import { formatDateTime, formatRelativeTime } from '$lib/utils/format';
  import { updatesApi, type PackageInfo, type PackageUpdate, type UpdateStatus } from '$lib/api/updates';

  const servers = $derived($serverStore.servers);

  let selectedServerId = $state<number | null>(null);
  let loadedServerId = $state<number | null>(null);
  let activeLoadToken = 0;

  let status = $state<UpdateStatus | null>(null);
  let installedPackages = $state<PackageInfo[]>([]);
  let statusLoading = $state(false);
  let packagesLoading = $state(false);
  let statusError = $state<string | null>(null);
  let packagesError = $state<string | null>(null);

  let actionBusyPackage = $state<string | null>(null);
  let installAllBusy = $state(false);
  let showInstallModal = $state(false);
  let installPackageName = $state('');
  let installModalBusy = $state(false);
  let packageSearch = $state('');

  const selectedServer = $derived(servers.find((server) => server.id === selectedServerId) ?? null);
  const updateRows = $derived(status?.packages ?? []);
  const visiblePackages = $derived(
    installedPackages.filter((pkg) => {
      const query = packageSearch.trim().toLowerCase();
      if (!query) return true;
      return [pkg.name, pkg.version, pkg.description, pkg.size, pkg.packageManager]
        .join(' ')
        .toLowerCase()
        .includes(query);
    })
  );
  const hasServers = $derived(servers.length > 0);
  const canManage = $derived(selectedServerId !== null && selectedServer !== null);
  const totalPackages = $derived(visiblePackages.length);
  const totalUpdates = $derived(status?.totalUpdates ?? 0);
  const securityUpdates = $derived(status?.securityUpdates ?? 0);
  const packageManager = $derived(status?.packageManager ?? 'unknown');
  const lastChecked = $derived(status?.lastChecked ?? '');

  onMount(() => {
    if (servers.length === 0) {
      void fetchServers();
    }
  });

  $effect(() => {
    if (!hasServers) {
      selectedServerId = null;
      loadedServerId = null;
      status = null;
      installedPackages = [];
      return;
    }

    if (selectedServerId === null || !servers.some((server) => server.id === selectedServerId)) {
      loadedServerId = null;
      selectedServerId = servers[0].id;
    }
  });

  $effect(() => {
    if (selectedServerId === null) return;
    if (selectedServerId === loadedServerId) return;
    void loadServerData(selectedServerId);
  });

  function handleServerChange(event: Event) {
    const value = (event.currentTarget as HTMLSelectElement).value;
    selectedServerId = value ? Number(value) : null;
  }

  async function loadServerData(serverId: number) {
    const loadId = ++activeLoadToken;
    statusLoading = true;
    packagesLoading = true;
    statusError = null;
    packagesError = null;

    const [statusResult, packagesResult] = await Promise.allSettled([
      updatesApi.getStatus(serverId),
      updatesApi.listInstalledPackages(serverId)
    ]);

    if (loadId !== activeLoadToken) return;

    if (statusResult.status === 'fulfilled') {
      status = statusResult.value;
    } else {
      status = null;
      const message = statusResult.reason instanceof Error ? statusResult.reason.message : 'Failed to load update status';
      statusError = message;
      toast.error(message);
    }

    if (packagesResult.status === 'fulfilled') {
      installedPackages = packagesResult.value;
    } else {
      installedPackages = [];
      const message = packagesResult.reason instanceof Error ? packagesResult.reason.message : 'Failed to load installed packages';
      packagesError = message;
      toast.error(message);
    }

    loadedServerId = serverId;
    statusLoading = false;
    packagesLoading = false;
  }

  async function refreshAll() {
    if (selectedServerId === null) return;
    await loadServerData(selectedServerId);
  }

  async function installAllUpdates(securityOnly: boolean) {
    if (selectedServerId === null) return;

    installAllBusy = true;
    try {
      const result = await updatesApi.installUpdates(selectedServerId, { securityOnly });
      toast.success(
        securityOnly
          ? `Installed ${result.updated ?? 0} security update${(result.updated ?? 0) === 1 ? '' : 's'}`
          : `Installed ${result.updated ?? 0} update${(result.updated ?? 0) === 1 ? '' : 's'}`
      );
      await loadServerData(selectedServerId);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : 'Failed to install updates');
    } finally {
      installAllBusy = false;
    }
  }

  async function installPackageByName(packageName: string) {
    if (selectedServerId === null) return;

    actionBusyPackage = packageName;
    try {
      await updatesApi.installPackage(selectedServerId, packageName);
      toast.success(`Installed ${packageName}`);
      await loadServerData(selectedServerId);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : `Failed to install ${packageName}`);
    } finally {
      actionBusyPackage = null;
    }
  }

  async function removePackage(packageName: string) {
    if (selectedServerId === null) return;

    const confirmed = window.confirm(`Remove ${packageName} from ${selectedServer?.name ?? 'this server'}?`);
    if (!confirmed) return;

    actionBusyPackage = packageName;
    try {
      await updatesApi.removePackage(selectedServerId, packageName);
      toast.success(`Removed ${packageName}`);
      await loadServerData(selectedServerId);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : `Failed to remove ${packageName}`);
    } finally {
      actionBusyPackage = null;
    }
  }

  function openInstallModal(prefill = '') {
    installPackageName = prefill;
    showInstallModal = true;
  }

  async function confirmInstallModal() {
    const packageName = installPackageName.trim();
    if (!packageName || selectedServerId === null) return;

    installModalBusy = true;
    try {
      await updatesApi.installPackage(selectedServerId, packageName);
      toast.success(`Installed ${packageName}`);
      showInstallModal = false;
      installPackageName = '';
      await loadServerData(selectedServerId);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : `Failed to install ${packageName}`);
    } finally {
      installModalBusy = false;
    }
  }

  function updateTypeVariant(type: PackageUpdate['type']) {
    if (type === 'security') return 'error';
    if (type === 'bugfix') return 'warning';
    if (type === 'enhancement') return 'info';
    return 'neutral';
  }

  function updateTypeLabel(type: PackageUpdate['type']) {
    if (type === 'security') return 'Security';
    if (type === 'bugfix') return 'Bugfix';
    if (type === 'enhancement') return 'Enhancement';
    return 'Normal';
  }

  function managerVariant(manager: string) {
    if (manager === 'apt' || manager === 'dnf' || manager === 'yum' || manager === 'pacman') return 'info';
    return 'neutral';
  }

  function packageCardIcon(update: PackageUpdate) {
    return update.security ? ShieldAlert : ShieldCheck;
  }

  function formatCheckedAt(value: string) {
    if (!value) return 'Never';
    try {
      return `${formatDateTime(value)} · ${formatRelativeTime(value)}`;
    } catch {
      return value;
    }
  }
</script>

<svelte:head>
  <title>System Updates - Meshium</title>
</svelte:head>

<div class="p-4 sm:p-6">
  <div class="mx-auto max-w-7xl">
    <PageHeader title="System Updates" subtitle="Check package updates, install security fixes, and manage installed packages across your servers.">
      {#snippet actions()}
        <button
          type="button"
          class="inline-flex items-center gap-2 rounded-lg border border-slate-300 bg-white px-4 py-2 text-sm font-medium text-slate-700 transition hover:bg-slate-50 disabled:cursor-not-allowed disabled:opacity-50"
          onclick={() => void refreshAll()}
          disabled={!canManage || statusLoading || packagesLoading}
        >
          {#if statusLoading || packagesLoading}
            <Spinner size="sm" />
          {:else}
            <RefreshCw size={16} />
          {/if}
          Refresh
        </button>

        <button
          type="button"
          class="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white transition hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-50"
          onclick={() => openInstallModal()}
          disabled={!canManage}
        >
          <Download size={16} />
          Install Package
        </button>
      {/snippet}
    </PageHeader>

    {#if !hasServers}
      <EmptyState
        title="No servers available"
        description="Add a server first, then use system updates to manage packages remotely."
        icon={serverIcon}
        action={addServerAction}
      />
    {:else}
      <div class="grid gap-4 lg:grid-cols-[1.5fr_1fr]">
        <Card padding="lg">
          <div class="flex flex-col gap-4 sm:flex-row sm:items-end sm:justify-between">
            <div class="flex-1">
              <label class="mb-2 block text-sm font-medium text-slate-700" for="server-select">Server</label>
              <select
                id="server-select"
                class="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-sm text-slate-900 shadow-sm outline-none transition focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20"
                value={selectedServerId ?? ''}
                onchange={handleServerChange}
              >
                {#each servers as server}
                  <option value={server.id}>{server.name} · {server.host}:{server.port}</option>
                {/each}
              </select>
              {#if selectedServer}
                <p class="mt-2 text-sm text-slate-500">{selectedServer.username} · {selectedServer.environment || 'no environment'} · {selectedServer.region || 'no region'}</p>
              {/if}
            </div>

            <div class="flex flex-wrap gap-2">
              <button
                type="button"
                class="inline-flex items-center gap-2 rounded-lg border border-slate-300 px-4 py-2 text-sm font-medium text-slate-700 transition hover:bg-slate-50 disabled:cursor-not-allowed disabled:opacity-50"
                onclick={() => void installAllUpdates(true)}
                disabled={!canManage || installAllBusy || statusLoading}
              >
                {#if installAllBusy}
                  <Spinner size="sm" />
                {:else}
                  <ShieldAlert size={16} />
                {/if}
                Install Security
              </button>

              <button
                type="button"
                class="inline-flex items-center gap-2 rounded-lg bg-slate-900 px-4 py-2 text-sm font-medium text-white transition hover:bg-slate-800 disabled:cursor-not-allowed disabled:opacity-50"
                onclick={() => void installAllUpdates(false)}
                disabled={!canManage || installAllBusy || statusLoading}
              >
                {#if installAllBusy}
                  <Spinner size="sm" />
                {:else}
                  <Wrench size={16} />
                {/if}
                Install All
              </button>
            </div>
          </div>
        </Card>

        <Card padding="lg">
          <div class="flex items-center justify-between gap-3">
            <div>
              <p class="text-sm font-medium text-slate-500">Package manager</p>
              <div class="mt-1 flex items-center gap-2">
                <Badge variant={managerVariant(packageManager)} size="sm">{packageManager}</Badge>
                {#if statusLoading}
                  <span class="text-xs text-slate-400">Refreshing…</span>
                {/if}
              </div>
            </div>
            <div class="rounded-full bg-slate-100 p-3 text-slate-500">
              <Package size={20} />
            </div>
          </div>

          <div class="mt-5 grid grid-cols-2 gap-3 sm:grid-cols-4">
            <div class="rounded-xl bg-slate-50 p-3">
              <p class="text-xs uppercase tracking-wide text-slate-500">Updates</p>
              <p class="mt-1 text-2xl font-bold text-slate-900">{totalUpdates}</p>
            </div>
            <div class="rounded-xl bg-red-50 p-3">
              <p class="text-xs uppercase tracking-wide text-red-600">Security</p>
              <p class="mt-1 text-2xl font-bold text-red-700">{securityUpdates}</p>
            </div>
            <div class="rounded-xl bg-blue-50 p-3">
              <p class="text-xs uppercase tracking-wide text-blue-600">Packages</p>
              <p class="mt-1 text-2xl font-bold text-blue-700">{totalPackages}</p>
            </div>
            <div class="rounded-xl bg-slate-50 p-3">
              <p class="text-xs uppercase tracking-wide text-slate-500">Checked</p>
              <p class="mt-1 text-sm font-medium text-slate-700">{lastChecked ? formatCheckedAt(lastChecked) : 'Never'}</p>
            </div>
          </div>
        </Card>
      </div>

      {#if statusError || packagesError}
        <div class="mt-4 rounded-xl border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">
          <div class="flex items-start gap-2">
            <AlertCircle size={16} class="mt-0.5" />
            <div>
              {#if statusError}
                <p>Update status: {statusError}</p>
              {/if}
              {#if packagesError}
                <p>Installed packages: {packagesError}</p>
              {/if}
            </div>
          </div>
        </div>
      {/if}

      <div class="mt-6 grid gap-6">
        <Card padding="lg">
          <div class="mb-4 flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
            <div>
              <h2 class="text-lg font-semibold text-slate-900">Available updates</h2>
              <p class="text-sm text-slate-500">Update packages detected on {selectedServer?.name || 'the selected server'}.</p>
            </div>
            <button
              type="button"
              class="inline-flex items-center gap-2 rounded-lg border border-slate-300 px-3 py-2 text-sm font-medium text-slate-700 transition hover:bg-slate-50 disabled:cursor-not-allowed disabled:opacity-50"
              onclick={() => void refreshAll()}
              disabled={!canManage || statusLoading}
            >
              {#if statusLoading}
                <Spinner size="sm" />
              {:else}
                <RefreshCw size={16} />
              {/if}
              Check Again
            </button>
          </div>

          {#if statusLoading}
            <div class="space-y-3">
              {#each Array(3) as _}
                <div class="rounded-xl border border-slate-200 bg-white p-4">
                  <div class="flex items-start gap-3">
                    <Skeleton width="36px" height="36px" rounded />
                    <div class="flex-1 space-y-2">
                      <Skeleton width="40%" />
                      <Skeleton width="70%" />
                      <Skeleton width="55%" />
                    </div>
                  </div>
                </div>
              {/each}
            </div>
          {:else if updateRows.length === 0}
            <EmptyState
              title="System is up to date"
              description="No updates are currently available for this server."
              icon={upToDateIcon}
              action={upToDateAction}
            />
          {:else}
            <div class="overflow-hidden rounded-xl border border-slate-200">
              <table class="min-w-full divide-y divide-slate-200 bg-white">
                <thead class="bg-slate-50">
                  <tr>
                    <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500">Package</th>
                    <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500">Current</th>
                    <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500">Available</th>
                    <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500">Type</th>
                    <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500">Action</th>
                  </tr>
                </thead>
                <tbody class="divide-y divide-slate-100">
                  {#each updateRows as update (update.name)}
                    {@const Icon = packageCardIcon(update)}
                    <tr class="hover:bg-slate-50">
                      <td class="px-4 py-4 align-top">
                        <div class="flex items-start gap-3">
                          <div class={`mt-0.5 rounded-full p-2 ${update.security ? 'bg-red-50 text-red-600' : 'bg-slate-100 text-slate-500'}`}>
                            <Icon size={16} />
                          </div>
                          <div>
                            <div class="font-medium text-slate-900">{update.name}</div>
                            <div class="mt-1 text-xs text-slate-500">
                              {#if update.architecture}
                                <span>{update.architecture}</span>
                              {/if}
                              {#if update.repository}
                                <span>{update.architecture ? ' · ' : ''}{update.repository}</span>
                              {/if}
                            </div>
                          </div>
                        </div>
                      </td>
                      <td class="px-4 py-4 align-top text-sm text-slate-700">{update.currentVersion || '—'}</td>
                      <td class="px-4 py-4 align-top text-sm text-slate-700">{update.availableVersion || '—'}</td>
                      <td class="px-4 py-4 align-top">
                        <Badge variant={updateTypeVariant(update.type)} size="sm">{updateTypeLabel(update.type)}</Badge>
                      </td>
                      <td class="px-4 py-4 align-top">
                        <button
                          type="button"
                          class="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-3 py-2 text-sm font-medium text-white transition hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-50"
                          onclick={() => void installPackageByName(update.name)}
                          disabled={actionBusyPackage === update.name}
                        >
                          {#if actionBusyPackage === update.name}
                            <Spinner size="sm" />
                          {:else}
                            <Download size={16} />
                          {/if}
                          Install
                        </button>
                      </td>
                    </tr>
                  {/each}
                </tbody>
              </table>
            </div>
          {/if}
        </Card>

        <Card padding="lg">
          <div class="mb-4 flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
            <div>
              <h2 class="text-lg font-semibold text-slate-900">Package management</h2>
              <p class="text-sm text-slate-500">Search installed packages, inspect details, and remove packages when needed.</p>
            </div>
            <div class="flex w-full gap-2 sm:w-auto sm:min-w-[22rem]">
              <div class="relative flex-1">
                <Search size={16} class="absolute left-3 top-1/2 -translate-y-1/2 text-slate-400" />
                <input
                  type="text"
                  value={packageSearch}
                  oninput={(event) => (packageSearch = (event.currentTarget as HTMLInputElement).value)}
                  placeholder="Search installed packages..."
                  class="w-full rounded-lg border border-slate-300 bg-white py-2 pl-9 pr-3 text-sm text-slate-900 shadow-sm outline-none transition focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20"
                />
              </div>
              <button
                type="button"
                class="inline-flex items-center gap-2 rounded-lg border border-slate-300 px-3 py-2 text-sm font-medium text-slate-700 transition hover:bg-slate-50"
                onclick={() => openInstallModal()}
              >
                <Download size={16} />
                Install
              </button>
            </div>
          </div>

          {#if packagesLoading}
            <div class="space-y-3">
              {#each Array(4) as _}
                <div class="rounded-xl border border-slate-200 bg-white p-4">
                  <div class="flex items-center gap-3">
                    <Skeleton width="28px" height="28px" rounded />
                    <div class="flex-1 space-y-2">
                      <Skeleton width="40%" />
                      <Skeleton width="65%" />
                    </div>
                    <Skeleton width="84px" height="36px" rounded />
                  </div>
                </div>
              {/each}
            </div>
          {:else if visiblePackages.length === 0}
            <EmptyState
              title="No packages found"
              description={packageSearch.trim() ? 'Try a different search term.' : 'This server did not return any installed packages.'}
              icon={packageEmptyIcon}
              action={packageEmptyAction}
            />
          {:else}
            <div class="overflow-hidden rounded-xl border border-slate-200">
              <table class="min-w-full divide-y divide-slate-200 bg-white">
                <thead class="bg-slate-50">
                  <tr>
                    <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500">Package</th>
                    <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500">Version</th>
                    <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500">Size</th>
                    <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500">Description</th>
                    <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500">Action</th>
                  </tr>
                </thead>
                <tbody class="divide-y divide-slate-100">
                  {#each visiblePackages as pkg (pkg.name)}
                    <tr class="hover:bg-slate-50">
                      <td class="px-4 py-4 align-top">
                        <div class="flex items-start gap-3">
                          <div class="rounded-full bg-slate-100 p-2 text-slate-500">
                            <Package size={16} />
                          </div>
                          <div>
                            <div class="font-medium text-slate-900">{pkg.name}</div>
                            <div class="mt-1 text-xs text-slate-500">{pkg.packageManager}</div>
                          </div>
                        </div>
                      </td>
                      <td class="px-4 py-4 align-top text-sm text-slate-700">{pkg.version || '—'}</td>
                      <td class="px-4 py-4 align-top text-sm text-slate-700">{pkg.size || '—'}</td>
                      <td class="px-4 py-4 align-top text-sm text-slate-600">{pkg.description || '—'}</td>
                      <td class="px-4 py-4 align-top">
                        <div class="flex items-center gap-2">
                          <button
                            type="button"
                            class="inline-flex items-center gap-2 rounded-lg border border-slate-300 px-3 py-2 text-sm font-medium text-slate-700 transition hover:bg-slate-50 disabled:cursor-not-allowed disabled:opacity-50"
                            onclick={() => void installPackageByName(pkg.name)}
                            disabled={actionBusyPackage === pkg.name}
                          >
                            {#if actionBusyPackage === pkg.name}
                              <Spinner size="sm" />
                            {:else}
                              <Download size={16} />
                            {/if}
                            Install
                          </button>
                          <button
                            type="button"
                            class="inline-flex items-center gap-2 rounded-lg border border-red-200 px-3 py-2 text-sm font-medium text-red-700 transition hover:bg-red-50 disabled:cursor-not-allowed disabled:opacity-50"
                            onclick={() => void removePackage(pkg.name)}
                            disabled={actionBusyPackage === pkg.name}
                          >
                            <Trash2 size={16} />
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
        </Card>
      </div>
    {/if}
  </div>
</div>

<Modal
  open={showInstallModal}
  title="Install package"
  onClose={() => {
    if (!installModalBusy) {
      showInstallModal = false;
      installPackageName = '';
    }
  }}
  children={installModalBody}
  footer={installModalFooter}
/>

{#snippet serverIcon()}
  <Server size={22} />
{/snippet}

{#snippet addServerAction()}
  <a
    href="/servers/new"
    class="inline-flex items-center justify-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white transition hover:bg-blue-700"
  >
    Add Server
  </a>
{/snippet}

{#snippet upToDateIcon()}
  <ShieldCheck size={22} />
{/snippet}

{#snippet upToDateAction()}
  <button
    type="button"
    class="inline-flex items-center justify-center gap-2 rounded-lg border border-slate-300 bg-white px-4 py-2 text-sm font-medium text-slate-700 transition hover:bg-slate-50"
    onclick={() => void refreshAll()}
  >
    <RefreshCw size={16} />
    Check Again
  </button>
{/snippet}

{#snippet packageEmptyIcon()}
  <Package size={22} />
{/snippet}

{#snippet packageEmptyAction()}
  <button
    type="button"
    class="inline-flex items-center justify-center gap-2 rounded-lg border border-slate-300 bg-white px-4 py-2 text-sm font-medium text-slate-700 transition hover:bg-slate-50"
    onclick={() => openInstallModal()}
  >
    <Download size={16} />
    Install Package
  </button>
{/snippet}

{#snippet installModalBody()}
  <div class="space-y-3">
    <p class="text-sm text-slate-600">Enter a package name to install on {selectedServer?.name ?? 'the selected server'}.</p>
    <input
      type="text"
      value={installPackageName}
      oninput={(event) => (installPackageName = (event.currentTarget as HTMLInputElement).value)}
      placeholder="nginx"
      class="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-sm text-slate-900 shadow-sm outline-none transition focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20"
      autocomplete="off"
      spellcheck="false"
    />
  </div>
{/snippet}

{#snippet installModalFooter()}
  <div class="flex items-center justify-end gap-3">
    <button
      type="button"
      class="rounded-lg border border-slate-300 px-4 py-2 text-sm font-medium text-slate-700 transition hover:bg-slate-50 disabled:cursor-not-allowed disabled:opacity-50"
      onclick={() => {
        if (!installModalBusy) {
          showInstallModal = false;
          installPackageName = '';
        }
      }}
      disabled={installModalBusy}
    >
      Cancel
    </button>
    <button
      type="button"
      class="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white transition hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-50"
      onclick={() => void confirmInstallModal()}
      disabled={installModalBusy || !installPackageName.trim()}
    >
      {#if installModalBusy}
        <Spinner size="sm" />
      {:else}
        <Download size={16} />
      {/if}
      Install
    </button>
  </div>
{/snippet}
