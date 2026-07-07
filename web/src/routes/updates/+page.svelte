<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import {
    AlertCircle,
    Check,
    CheckCircle2,
    Clock,
    Download,
    Package,
    RefreshCw,
    Search,
    Server,
    ShieldAlert,
    ShieldCheck,
    Trash2,
    Wrench,
    Zap,
    ZapOff,
  } from 'lucide-svelte';
  import { fetchServers, serverStore } from '$lib/stores/servers';
  import { toast } from '$lib/stores/toast';
  import { Badge, Card, EmptyState, Modal, PageHeader, ProgressBar, Spinner } from '$lib/components/ui';
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
  let installAllProgress = $state(0);
  let installAllStatus = $state('');
  let installAllSecurity = $state(false);
  let recentlyUpdated = $state<Set<string>>(new Set());
  let showInstallModal = $state(false);
  let installPackageName = $state('');
  let installModalBusy = $state(false);
  let packageSearch = $state('');
  let pollInterval: ReturnType<typeof setInterval> | null = null;

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
  const isUpToDate = $derived(totalUpdates === 0 && status !== null && !statusLoading);
  const isInstalling = $derived(installAllBusy || actionBusyPackage !== null);

  onMount(() => {
    if (servers.length === 0) {
      void fetchServers();
    }
  });

  onDestroy(() => {
    if (pollInterval) clearInterval(pollInterval);
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
    recentlyUpdated = new Set();
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
    recentlyUpdated = new Set();
    await loadServerData(selectedServerId);
  }

  function startInstallProgress(securityOnly: boolean) {
    installAllBusy = true;
    installAllProgress = 0;
    installAllSecurity = securityOnly;
    installAllStatus = 'Preparing...';

    // Simulate progress steps since backend is synchronous
    const steps = [
      { pct: 15, msg: 'Checking available updates...' },
      { pct: 30, msg: 'Resolving dependencies...' },
      { pct: 50, msg: 'Downloading packages...' },
      { pct: 70, msg: 'Installing packages...' },
      { pct: 85, msg: 'Configuring packages...' },
      { pct: 95, msg: 'Finalizing installation...' },
    ];

    let stepIdx = 0;
    if (pollInterval) clearInterval(pollInterval);
    pollInterval = setInterval(() => {
      if (stepIdx < steps.length) {
        installAllProgress = steps[stepIdx].pct;
        installAllStatus = steps[stepIdx].msg;
        stepIdx++;
      }
    }, 800);
  }

  function stopInstallProgress() {
    if (pollInterval) {
      clearInterval(pollInterval);
      pollInterval = null;
    }
    installAllProgress = 100;
    installAllStatus = 'Complete';
    setTimeout(() => {
      installAllBusy = false;
      installAllProgress = 0;
      installAllStatus = '';
    }, 1500);
  }

  async function installAllUpdates(securityOnly: boolean) {
    if (selectedServerId === null) return;

    startInstallProgress(securityOnly);

    try {
      const result = await updatesApi.installUpdates(selectedServerId, { securityOnly });
      const updatedCount = result.updated ?? 0;

      // Mark all currently visible updates as recently updated
      const updatedSet = new Set(recentlyUpdated);
      for (const pkg of updateRows) {
        if (!securityOnly || pkg.security) {
          updatedSet.add(pkg.name);
        }
      }
      recentlyUpdated = updatedSet;

      stopInstallProgress();

      toast.success(
        securityOnly
          ? `Installed ${updatedCount} security update${updatedCount === 1 ? '' : 's'}`
          : `Installed ${updatedCount} update${updatedCount === 1 ? '' : 's'}`
      );

      await loadServerData(selectedServerId);
    } catch (error) {
      stopInstallProgress();
      toast.error(error instanceof Error ? error.message : 'Failed to install updates');
    }
  }

  async function installPackageByName(packageName: string) {
    if (selectedServerId === null) return;

    actionBusyPackage = packageName;
    try {
      await updatesApi.installPackage(selectedServerId, packageName);
      const updatedSet = new Set(recentlyUpdated);
      updatedSet.add(packageName);
      recentlyUpdated = updatedSet;
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
      const updatedSet = new Set(recentlyUpdated);
      updatedSet.add(packageName);
      recentlyUpdated = updatedSet;
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
          class="inline-flex items-center gap-2 rounded-lg border border-border-strong bg-surface px-4 py-2 text-sm font-medium text-fg-muted transition hover:bg-surface-muted focus-visible:ring-2 focus-visible:ring-accent focus-visible:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-50"
          onclick={() => void refreshAll()}
          disabled={!canManage || statusLoading || packagesLoading || isInstalling}
          aria-label="Refresh update status"
        >
          {#if statusLoading || packagesLoading}
            <Spinner size="sm" label="Loading" />
          {:else}
            <RefreshCw size={16} />
          {/if}
          Refresh
        </button>

        <button
          type="button"
          class="inline-flex items-center gap-2 rounded-lg bg-accent px-4 py-2 text-sm font-medium text-accent-fg transition hover:bg-accent-hover focus-visible:ring-2 focus-visible:ring-accent focus-visible:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-50"
          onclick={() => openInstallModal()}
          disabled={!canManage || isInstalling}
          aria-label="Install a new package"
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
      <!-- Server selector + stats -->
      <div class="grid gap-4 lg:grid-cols-[1.5fr_1fr]">
        <Card padding="lg">
          <div class="flex flex-col gap-4 sm:flex-row sm:items-end sm:justify-between">
            <div class="flex-1">
              <label class="mb-2 block text-sm font-medium text-fg-muted" for="server-select">Server</label>
              <select
                id="server-select"
                class="w-full rounded-lg border border-border-strong bg-surface px-3 py-2 text-sm text-fg shadow-sm outline-none transition focus:border-accent focus:ring-2 focus:ring-accent/20"
                value={selectedServerId ?? ''}
                onchange={handleServerChange}
                disabled={isInstalling}
              >
                {#each servers as server}
                  <option value={server.id}>{server.name} · {server.host}:{server.port}</option>
                {/each}
              </select>
              {#if selectedServer}
                <p class="mt-2 text-sm text-fg-subtle">{selectedServer.username} · {selectedServer.environment || 'no environment'} · {selectedServer.region || 'no region'}</p>
              {/if}
            </div>

            <div class="flex flex-wrap gap-2">
              {#if isUpToDate && !isInstalling}
                <div class="inline-flex items-center gap-2 rounded-lg border border-success/20 bg-success/15 px-4 py-2 text-sm font-medium text-success">
                  <CheckCircle2 size={16} />
                  Up to Date
                </div>
              {:else if totalUpdates > 0 && !isInstalling}
                <button
                  type="button"
                  class="inline-flex items-center gap-2 rounded-lg border border-error/20 bg-error/15 px-4 py-2 text-sm font-medium text-error transition hover:bg-error/25 focus-visible:ring-2 focus-visible:ring-error focus-visible:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-50"
                  onclick={() => void installAllUpdates(true)}
                  disabled={!canManage || isInstalling}
                  aria-label="Install security updates only"
                >
                  <ShieldAlert size={16} />
                  Install Security
                </button>

                <button
                  type="button"
                  class="inline-flex items-center gap-2 rounded-lg bg-fg px-4 py-2 text-sm font-medium text-bg transition hover:bg-fg-muted focus-visible:ring-2 focus-visible:ring-border-strong focus-visible:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-50"
                  onclick={() => void installAllUpdates(false)}
                  disabled={!canManage || isInstalling}
                  aria-label="Install all available updates"
                >
                  <Wrench size={16} />
                  Install All
                </button>
              {:else if isInstalling}
                <div class="inline-flex items-center gap-2 rounded-lg border border-info/20 bg-info/15 px-4 py-2 text-sm font-medium text-info">
                  <Spinner size="sm" label="Installing" />
                  Installing...
                </div>
              {/if}
            </div>
          </div>

          <!-- Real-time install progress bar -->
          {#if installAllBusy}
            <div class="mt-4 rounded-xl border border-info/20 bg-info/15 p-4">
              <div class="mb-2 flex items-center justify-between">
                <div class="flex items-center gap-2 text-sm font-medium text-info">
                  <Download size={16} class="animate-bounce" />
                  {installAllStatus}
                </div>
                <span class="text-sm font-semibold text-info">{installAllProgress}%</span>
              </div>
              <ProgressBar value={installAllProgress} variant="default" animated />
              <p class="mt-2 text-xs text-info">
                {installAllSecurity ? 'Installing security updates only' : 'Installing all available updates'} on {selectedServer?.name ?? 'server'}...
              </p>
            </div>
          {/if}
        </Card>

        <Card padding="lg">
          <div class="flex items-center justify-between gap-3">
            <div>
              <p class="text-sm font-medium text-fg-subtle">Package manager</p>
              <div class="mt-1 flex items-center gap-2">
                <Badge variant={managerVariant(packageManager)} size="sm">{packageManager}</Badge>
                {#if statusLoading}
                  <span class="text-xs text-fg-subtle">Refreshing…</span>
                {/if}
              </div>
            </div>
            <div class="rounded-full bg-surface-muted p-3 text-fg-subtle" aria-hidden="true">
              <Package size={20} />
            </div>
          </div>

          <div class="mt-5 grid grid-cols-2 gap-3 sm:grid-cols-4">
            <div class="rounded-xl bg-surface-muted p-3">
              <p class="text-xs uppercase tracking-wide text-fg-subtle">Updates</p>
              <p class="mt-1 text-2xl font-bold text-fg">{totalUpdates}</p>
            </div>
            <div class="rounded-xl bg-error/15 p-3">
              <p class="text-xs uppercase tracking-wide text-error">Security</p>
              <p class="mt-1 text-2xl font-bold text-error">{securityUpdates}</p>
            </div>
            <div class="rounded-xl bg-info/15 p-3">
              <p class="text-xs uppercase tracking-wide text-info">Packages</p>
              <p class="mt-1 text-2xl font-bold text-info">{totalPackages}</p>
            </div>
            <div class="rounded-xl bg-surface-muted p-3">
              <p class="text-xs uppercase tracking-wide text-fg-subtle">Checked</p>
              <p class="mt-1 text-sm font-medium text-fg-muted">{lastChecked ? formatCheckedAt(lastChecked) : 'Never'}</p>
            </div>
          </div>

          {#if isUpToDate}
            <div class="mt-4 flex items-center gap-2 rounded-lg border border-success/20 bg-success/15 px-3 py-2 text-sm text-success">
              <CheckCircle2 size={16} />
              All packages are up to date. Last checked {lastChecked ? formatRelativeTime(lastChecked) : 'never'}.
            </div>
          {/if}
        </Card>
      </div>

      {#if statusError || packagesError}
        <div class="mt-4 rounded-xl border border-error/20 bg-error/15 px-4 py-3 text-sm text-error" role="alert">
          <div class="flex items-start gap-2">
            <AlertCircle size={16} class="mt-0.5 shrink-0" />
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

      <!-- Available Updates Section -->
      <div class="mt-6">
        <Card padding="lg">
          <div class="mb-4 flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
            <div>
              <h2 class="text-lg font-semibold text-fg">Available updates</h2>
              <p class="text-sm text-fg-subtle">
                {#if isUpToDate}
                  <span class="inline-flex items-center gap-1 text-success">
                    <CheckCircle2 size={14} /> System is up to date — no updates available.
                  </span>
                {:else}
                  {totalUpdates} update{totalUpdates === 1 ? '' : 's'} available on {selectedServer?.name || 'the selected server'}.
                  {#if securityUpdates > 0}
                    <span class="font-medium text-error">{securityUpdates} security update{securityUpdates === 1 ? '' : 's'}</span>
                  {/if}
                {/if}
              </p>
            </div>
            <button
              type="button"
              class="inline-flex items-center gap-2 rounded-lg border border-border-strong px-3 py-2 text-sm font-medium text-fg-muted transition hover:bg-surface-muted focus-visible:ring-2 focus-visible:ring-accent focus-visible:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-50"
              onclick={() => void refreshAll()}
              disabled={!canManage || statusLoading || isInstalling}
              aria-label="Check for updates again"
            >
              {#if statusLoading}
                <Spinner size="sm" label="Loading" />
              {:else}
                <RefreshCw size={16} />
              {/if}
              Check Again
            </button>
          </div>

          {#if statusLoading}
            <div class="flex items-center justify-center gap-3 py-12 text-fg-subtle">
              <Spinner size="md" label="Checking for updates" />
              <span class="text-sm">Checking for updates...</span>
            </div>
          {:else if updateRows.length === 0}
            <div class="rounded-xl border border-success/20 bg-success/15 p-8">
              <div class="flex flex-col items-center text-center">
                <div class="flex h-16 w-16 items-center justify-center rounded-full bg-success/15 text-success">
                  <CheckCircle2 size={32} />
                </div>
                <h3 class="mt-4 text-lg font-semibold text-success">System is up to date</h3>
                <p class="mt-1 text-sm text-success">No updates are currently available for this server.</p>
                <p class="mt-2 text-xs text-success">Last checked: {lastChecked ? formatCheckedAt(lastChecked) : 'Never'}</p>
                <button
                  type="button"
                  class="mt-4 inline-flex items-center gap-2 rounded-lg border border-success/30 bg-surface px-4 py-2 text-sm font-medium text-success transition hover:bg-success/15 focus-visible:ring-2 focus-visible:ring-success focus-visible:ring-offset-2"
                  onclick={() => void refreshAll()}
                >
                  <RefreshCw size={16} />
                  Check Again
                </button>
              </div>
            </div>
          {:else}
            <div class="hidden overflow-x-auto rounded-xl border border-border md:block">
              <table class="min-w-full divide-y divide-border bg-surface">
                <thead class="bg-surface-muted">
                  <tr>
                    <th scope="col" class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-fg-subtle">Package</th>
                    <th scope="col" class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-fg-subtle">Current</th>
                    <th scope="col" class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-fg-subtle">Available</th>
                    <th scope="col" class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-fg-subtle">Type</th>
                    <th scope="col" class="px-4 py-3 text-right text-xs font-semibold uppercase tracking-wide text-fg-subtle">Action</th>
                  </tr>
                </thead>
                <tbody class="divide-y divide-border">
                  {#each updateRows as update (update.name)}
                    {@const isUpdated = recentlyUpdated.has(update.name)}
                    {@const isBusy = actionBusyPackage === update.name}
                    <tr class={isUpdated ? 'bg-success/15' : 'hover:bg-surface-muted'}>
                      <td class="px-4 py-4 align-top">
                        <div class="flex items-start gap-3">
                          <div class={`mt-0.5 rounded-full p-2 ${update.security ? 'bg-error/15 text-error' : 'bg-surface-muted text-fg-subtle'}`} aria-hidden="true">
                            {#if isUpdated}
                              <CheckCircle2 size={16} class="text-success" />
                            {:else if update.security}
                              <ShieldAlert size={16} />
                            {:else}
                              <Package size={16} />
                            {/if}
                          </div>
                          <div>
                            <div class="font-medium text-fg">{update.name}</div>
                            <div class="mt-1 text-xs text-fg-subtle">
                              {#if update.architecture}
                                <span>{update.architecture}</span>
                              {/if}
                              {#if update.repository}
                                <span>{update.architecture ? ' · ' : ''}{update.repository}</span>
                              {/if}
                            </div>
                            {#if isUpdated}
                              <span class="mt-1 inline-flex items-center gap-1 text-xs font-medium text-success">
                                <Check size={12} /> Updated
                              </span>
                            {/if}
                          </div>
                        </div>
                      </td>
                      <td class="px-4 py-4 align-top text-sm text-fg-muted">{update.currentVersion || '—'}</td>
                      <td class="px-4 py-4 align-top text-sm font-medium text-fg">{update.availableVersion || '—'}</td>
                      <td class="px-4 py-4 align-top">
                        <Badge variant={updateTypeVariant(update.type)} size="sm">{updateTypeLabel(update.type)}</Badge>
                      </td>
                      <td class="px-4 py-4 align-top text-right">
                        {#if isUpdated}
                          <span class="inline-flex items-center gap-1.5 rounded-lg bg-success/15 px-3 py-2 text-sm font-medium text-success">
                            <CheckCircle2 size={16} />
                            Up to Date
                          </span>
                        {:else}
                          <button
                            type="button"
                            class="inline-flex items-center gap-2 rounded-lg bg-accent px-3 py-2 text-sm font-medium text-accent-fg transition hover:bg-accent-hover focus-visible:ring-2 focus-visible:ring-accent focus-visible:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-50"
                            onclick={() => void installPackageByName(update.name)}
                            disabled={isBusy || isInstalling}
                            aria-label="Install {update.name}"
                          >
                            {#if isBusy}
                              <Spinner size="sm" label="Installing" />
                            {:else}
                              <Download size={16} />
                            {/if}
                            Install
                          </button>
                        {/if}
                      </td>
                    </tr>
                  {/each}
                </tbody>
              </table>
            </div>

            <!-- Mobile: stacked cards -->
            <div class="space-y-3 md:hidden">
              {#each updateRows as update (update.name)}
                {@const isUpdated = recentlyUpdated.has(update.name)}
                {@const isBusy = actionBusyPackage === update.name}
                <div class={`rounded-xl border border-border p-4 ${isUpdated ? 'bg-success/15' : 'bg-surface'}`}>
                  <div class="flex items-start justify-between gap-2">
                    <div class="flex min-w-0 items-start gap-3">
                      <div class={`mt-0.5 rounded-full p-2 ${update.security ? 'bg-error/15 text-error' : 'bg-surface-muted text-fg-subtle'}`} aria-hidden="true">
                        {#if isUpdated}
                          <CheckCircle2 size={16} class="text-success" />
                        {:else if update.security}
                          <ShieldAlert size={16} />
                        {:else}
                          <Package size={16} />
                        {/if}
                      </div>
                      <div class="min-w-0">
                        <div class="break-words font-medium text-fg">{update.name}</div>
                        <div class="mt-1 text-xs text-fg-subtle">
                          {#if update.architecture}
                            <span>{update.architecture}</span>
                          {/if}
                          {#if update.repository}
                            <span>{update.architecture ? ' · ' : ''}{update.repository}</span>
                          {/if}
                        </div>
                        {#if isUpdated}
                          <span class="mt-1 inline-flex items-center gap-1 text-xs font-medium text-success">
                            <Check size={12} /> Updated
                          </span>
                        {/if}
                      </div>
                    </div>
                    <Badge variant={updateTypeVariant(update.type)} size="sm">{updateTypeLabel(update.type)}</Badge>
                  </div>
                  <dl class="mt-3 grid grid-cols-2 gap-x-4 gap-y-2 text-sm">
                    <div>
                      <dt class="text-xs uppercase tracking-wide text-fg-subtle">Current</dt>
                      <dd class="break-words text-fg-muted">{update.currentVersion || '—'}</dd>
                    </div>
                    <div>
                      <dt class="text-xs uppercase tracking-wide text-fg-subtle">Available</dt>
                      <dd class="break-words font-medium text-fg">{update.availableVersion || '—'}</dd>
                    </div>
                  </dl>
                  <div class="mt-3 flex flex-wrap gap-2">
                    {#if isUpdated}
                      <span class="inline-flex items-center gap-1.5 rounded-lg bg-success/15 px-3 py-2 text-sm font-medium text-success">
                        <CheckCircle2 size={16} />
                        Up to Date
                      </span>
                    {:else}
                      <button
                        type="button"
                        class="inline-flex items-center gap-2 rounded-lg bg-accent px-3 py-2 text-sm font-medium text-accent-fg transition hover:bg-accent-hover focus-visible:ring-2 focus-visible:ring-accent focus-visible:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-50"
                        onclick={() => void installPackageByName(update.name)}
                        disabled={isBusy || isInstalling}
                        aria-label="Install {update.name}"
                      >
                        {#if isBusy}
                          <Spinner size="sm" label="Installing" />
                        {:else}
                          <Download size={16} />
                        {/if}
                        Install
                      </button>
                    {/if}
                  </div>
                </div>
              {/each}
            </div>

            {#if !isInstalling && totalUpdates > 0}
              <div class="mt-4 flex flex-wrap items-center justify-end gap-2">
                {#if securityUpdates > 0}
                  <button
                    type="button"
                    class="inline-flex items-center gap-2 rounded-lg border border-error/20 bg-error/15 px-4 py-2 text-sm font-medium text-error transition hover:bg-error/25 focus-visible:ring-2 focus-visible:ring-error focus-visible:ring-offset-2"
                    onclick={() => void installAllUpdates(true)}
                  >
                    <ShieldAlert size={16} />
                    Install {securityUpdates} Security Update{securityUpdates === 1 ? '' : 's'}
                  </button>
                {/if}
                <button
                  type="button"
                  class="inline-flex items-center gap-2 rounded-lg bg-fg px-4 py-2 text-sm font-medium text-bg transition hover:bg-fg-muted focus-visible:ring-2 focus-visible:ring-border-strong focus-visible:ring-offset-2"
                  onclick={() => void installAllUpdates(false)}
                >
                  <Wrench size={16} />
                  Install All {totalUpdates} Updates
                </button>
              </div>
            {/if}
          {/if}
        </Card>
      </div>

      <!-- Package Management Section -->
      <div class="mt-6">
        <Card padding="lg">
          <div class="mb-4 flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
            <div>
              <h2 class="text-lg font-semibold text-fg">Package management</h2>
              <p class="text-sm text-fg-subtle">Search installed packages, inspect details, and remove packages when needed.</p>
            </div>
            <div class="flex w-full gap-2 sm:w-auto sm:min-w-[22rem]">
              <div class="relative flex-1">
                <Search size={16} class="absolute left-3 top-1/2 -translate-y-1/2 text-fg-subtle" aria-hidden="true" />
                <input
                  type="text"
                  value={packageSearch}
                  oninput={(event) => (packageSearch = (event.currentTarget as HTMLInputElement).value)}
                  placeholder="Search installed packages..."
                  class="w-full rounded-lg border border-border-strong bg-surface py-2 pl-9 pr-3 text-sm text-fg shadow-sm outline-none transition focus:border-accent focus:ring-2 focus:ring-accent/20"
                  aria-label="Search installed packages"
                />
              </div>
              <button
                type="button"
                class="inline-flex items-center gap-2 rounded-lg border border-border-strong px-3 py-2 text-sm font-medium text-fg-muted transition hover:bg-surface-muted focus-visible:ring-2 focus-visible:ring-accent focus-visible:ring-offset-2"
                onclick={() => openInstallModal()}
                aria-label="Install a new package"
              >
                <Download size={16} />
                Install
              </button>
            </div>
          </div>

          {#if packagesLoading}
            <div class="flex items-center justify-center gap-3 py-12 text-fg-subtle">
              <Spinner size="md" label="Loading packages" />
              <span class="text-sm">Loading installed packages...</span>
            </div>
          {:else if visiblePackages.length === 0}
            <EmptyState
              title="No packages found"
              description={packageSearch.trim() ? 'Try a different search term.' : 'This server did not return any installed packages.'}
              icon={packageEmptyIcon}
              action={packageEmptyAction}
            />
          {:else}
            <div class="overflow-x-auto rounded-xl border border-border">
              <table class="min-w-full divide-y divide-border bg-surface">
                <thead class="bg-surface-muted">
                  <tr>
                    <th scope="col" class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-fg-subtle">Package</th>
                    <th scope="col" class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-fg-subtle">Version</th>
                    <th scope="col" class="hidden px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-fg-subtle md:table-cell">Size</th>
                    <th scope="col" class="hidden px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-fg-subtle lg:table-cell">Description</th>
                    <th scope="col" class="px-4 py-3 text-right text-xs font-semibold uppercase tracking-wide text-fg-subtle">Action</th>
                  </tr>
                </thead>
                <tbody class="divide-y divide-border">
                  {#each visiblePackages as pkg (pkg.name)}
                    {@const isUpdated = recentlyUpdated.has(pkg.name)}
                    {@const isBusy = actionBusyPackage === pkg.name}
                    <tr class={isUpdated ? 'bg-success/15' : 'hover:bg-surface-muted'}>
                      <td class="px-4 py-4 align-top">
                        <div class="flex items-start gap-3">
                          <div class="rounded-full bg-surface-muted p-2 text-fg-subtle" aria-hidden="true">
                            {#if isUpdated}
                              <CheckCircle2 size={16} class="text-success" />
                            {:else}
                              <Package size={16} />
                            {/if}
                          </div>
                          <div>
                            <div class="font-medium text-fg">{pkg.name}</div>
                            <div class="mt-1 text-xs text-fg-subtle">{pkg.packageManager}</div>
                          </div>
                        </div>
                      </td>
                      <td class="px-4 py-4 align-top text-sm text-fg-muted">{pkg.version || '—'}</td>
                      <td class="hidden px-4 py-4 align-top text-sm text-fg-muted md:table-cell">{pkg.size || '—'}</td>
                      <td class="hidden px-4 py-4 align-top text-sm text-fg-muted lg:table-cell">
                        <div class="max-w-xs truncate" title={pkg.description}>{pkg.description || '—'}</div>
                      </td>
                      <td class="px-4 py-4 align-top">
                        <div class="flex items-center justify-end gap-2">
                          {#if isUpdated}
                            <span class="inline-flex items-center gap-1 text-xs font-medium text-success">
                              <Check size={12} /> Updated
                            </span>
                          {/if}
                          <button
                            type="button"
                            class="inline-flex items-center gap-2 rounded-lg border border-border-strong px-3 py-2 text-sm font-medium text-fg-muted transition hover:bg-surface-muted focus-visible:ring-2 focus-visible:ring-accent focus-visible:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-50"
                            onclick={() => void installPackageByName(pkg.name)}
                            disabled={isBusy || isInstalling}
                            aria-label="Reinstall {pkg.name}"
                          >
                            {#if isBusy}
                              <Spinner size="sm" label="Loading" />
                            {:else}
                              <Download size={16} />
                            {/if}
                            Install
                          </button>
                          <button
                            type="button"
                            class="inline-flex items-center gap-2 rounded-lg border border-error/20 px-3 py-2 text-sm font-medium text-error transition hover:bg-error/15 focus-visible:ring-2 focus-visible:ring-error focus-visible:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-50"
                            onclick={() => void removePackage(pkg.name)}
                            disabled={isBusy || isInstalling}
                            aria-label="Remove {pkg.name}"
                          >
                            <Trash2 size={16} />
                            <span class="hidden sm:inline">Remove</span>
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
    class="inline-flex items-center justify-center gap-2 rounded-lg bg-accent px-4 py-2 text-sm font-medium text-accent-fg transition hover:bg-accent-hover focus-visible:ring-2 focus-visible:ring-accent focus-visible:ring-offset-2"
  >
    Add Server
  </a>
{/snippet}

{#snippet packageEmptyIcon()}
  <Package size={22} />
{/snippet}

{#snippet packageEmptyAction()}
  <button
    type="button"
    class="inline-flex items-center justify-center gap-2 rounded-lg border border-border-strong bg-surface px-4 py-2 text-sm font-medium text-fg-muted transition hover:bg-surface-muted focus-visible:ring-2 focus-visible:ring-accent focus-visible:ring-offset-2"
    onclick={() => openInstallModal()}
  >
    <Download size={16} />
    Install Package
  </button>
{/snippet}

{#snippet installModalBody()}
  <div class="space-y-3">
    <p class="text-sm text-fg-muted">Enter a package name to install on {selectedServer?.name ?? 'the selected server'}.</p>
    <input
      type="text"
      value={installPackageName}
      oninput={(event) => (installPackageName = (event.currentTarget as HTMLInputElement).value)}
      placeholder="nginx"
      class="w-full rounded-lg border border-border-strong bg-surface px-3 py-2 text-sm text-fg shadow-sm outline-none transition focus:border-accent focus:ring-2 focus:ring-accent/20"
      autocomplete="off"
      spellcheck="false"
      aria-label="Package name"
    />
  </div>
{/snippet}

{#snippet installModalFooter()}
  <div class="flex items-center justify-end gap-3">
    <button
      type="button"
      class="rounded-lg border border-border-strong px-4 py-2 text-sm font-medium text-fg-muted transition hover:bg-surface-muted focus-visible:ring-2 focus-visible:ring-accent focus-visible:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-50"
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
      class="inline-flex items-center gap-2 rounded-lg bg-accent px-4 py-2 text-sm font-medium text-accent-fg transition hover:bg-accent-hover focus-visible:ring-2 focus-visible:ring-accent focus-visible:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-50"
      onclick={() => void confirmInstallModal()}
      disabled={installModalBusy || !installPackageName.trim()}
    >
      {#if installModalBusy}
        <Spinner size="sm" label="Loading" />
      {:else}
        <Download size={16} />
      {/if}
      Install
    </button>
  </div>
{/snippet}
