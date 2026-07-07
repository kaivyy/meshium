<script lang="ts">
  import { onMount } from 'svelte';
  import {
    ArrowDown,
    ArrowUp,
    Cog,
    Container,
    Filter,
    GitCompare,
    HardDrive,
    Network,
    Package,
    PencilLine,
    RefreshCw,
    Server,
    Users
  } from 'lucide-svelte';
  import { APIError } from '$lib/api/client';
  import {
    driftApi,
    type DriftCategory,
    type DriftChange,
    type DriftReport,
    type DriftSeverity,
    type DriftSummary
  } from '$lib/api/drift';
  import { fetchServers, serverStore } from '$lib/stores/servers';
  import { toast } from '$lib/stores/toast';
  import { Badge, Card, EmptyState, PageHeader, Spinner } from '$lib/components/ui';

  let selectedServerID = $state('');
  let compareSourceID = $state('');
  let compareTargetID = $state('');

  let report = $state<DriftReport | null>(null);
  let compareReport = $state<DriftReport | null>(null);
  let history = $state<DriftSummary[]>([]);

  let loadingReport = $state(false);
  let loadingHistory = $state(false);
  let checkingNow = $state(false);
  let comparing = $state(false);

  let reportError = $state('');
  let historyError = $state('');
  let compareError = $state('');

  let categoryFilter = $state<'all' | DriftCategory>('all');
  let severityFilter = $state<'all' | DriftSeverity>('all');
  let selectedServerLoaded = $state('');

  const servers = $derived($serverStore.servers);
  const serversLoading = $derived($serverStore.loading);
  const selectedServer = $derived(servers.find((server) => String(server.id) === selectedServerID) ?? null);
  const compareSourceServer = $derived(servers.find((server) => String(server.id) === compareSourceID) ?? null);
  const compareTargetServer = $derived(servers.find((server) => String(server.id) === compareTargetID) ?? null);
  const summary = $derived(
    report?.summary ?? {
      packagesAdded: 0,
      packagesRemoved: 0,
      servicesChanged: 0,
      dockerChanged: 0,
      usersChanged: 0,
      networkChanged: 0,
      diskChanged: 0
    }
  );
  const compareSummary = $derived(
    compareReport?.summary ?? {
      packagesAdded: 0,
      packagesRemoved: 0,
      servicesChanged: 0,
      dockerChanged: 0,
      usersChanged: 0,
      networkChanged: 0,
      diskChanged: 0
    }
  );
  const filteredChanges = $derived(
    report
      ? report.changes.filter((change) => {
          if (categoryFilter !== 'all' && change.category !== categoryFilter) {
            return false;
          }
          if (severityFilter !== 'all' && change.severity !== severityFilter) {
            return false;
          }
          return true;
        })
      : []
  );
  const canCompare = $derived(
    Boolean(compareSourceID && compareTargetID && compareSourceID !== compareTargetID && !comparing)
  );

  const reportReady = $derived(Boolean(report && !reportError));

  onMount(() => {
    if ($serverStore.servers.length === 0) {
      void fetchServers();
    }
  });

  $effect(() => {
    if (!selectedServerID && servers.length > 0) {
      selectedServerID = String(servers[0].id);
    }
  });

  $effect(() => {
    if (selectedServerID && selectedServerID !== selectedServerLoaded) {
      selectedServerLoaded = selectedServerID;
      void loadSelectedServer();
    }
  });

  $effect(() => {
    if (!compareSourceID && selectedServerID) {
      compareSourceID = selectedServerID;
    }
    if (!compareTargetID && servers.length > 1) {
      const other = servers.find((server) => String(server.id) !== selectedServerID);
      if (other) {
        compareTargetID = String(other.id);
      }
    }
  });

  async function loadSelectedServer() {
    const serverID = Number(selectedServerID);
    if (!selectedServerID || Number.isNaN(serverID)) {
      report = null;
      history = [];
      reportError = '';
      historyError = '';
      return;
    }

    loadingReport = true;
    loadingHistory = true;
    reportError = '';
    historyError = '';

    try {
      const [latestResult, historyResult] = await Promise.allSettled([
        driftApi.getLatest(serverID),
        driftApi.getHistory(serverID, 10)
      ]);

      if (latestResult.status === 'fulfilled') {
        report = latestResult.value;
      } else {
        report = null;
        const details = describeError(latestResult.reason);
        reportError =
          details.code === 'SNAPSHOT_NOT_FOUND'
            ? 'Collect at least two snapshots to calculate drift.'
            : details.message;
      }

      if (historyResult.status === 'fulfilled') {
        history = historyResult.value;
      } else {
        history = [];
        historyError = describeError(historyResult.reason).message;
      }
    } finally {
      loadingReport = false;
      loadingHistory = false;
    }
  }

  async function handleCheckNow() {
    const serverID = Number(selectedServerID);
    if (!selectedServerID || Number.isNaN(serverID)) {
      return;
    }

    checkingNow = true;
    reportError = '';
    historyError = '';

    try {
      const latest = await driftApi.checkDrift(serverID);
      report = latest;
      toast.success(`Drift check complete for ${latest.serverName}.`);

      try {
        history = await driftApi.getHistory(serverID, 10);
      } catch (historyErr) {
        historyError = describeError(historyErr).message;
        toast.warning('Drift report updated, but history could not be refreshed.');
      }
    } catch (error) {
      const details = describeError(error);
      report = null;
      reportError =
        details.code === 'SNAPSHOT_NOT_FOUND'
          ? 'Collect at least two snapshots to calculate drift.'
          : details.message;
      toast.error(reportError);
    } finally {
      checkingNow = false;
    }
  }

  async function handleCompareServers() {
    const source = Number(compareSourceID);
    const target = Number(compareTargetID);
    if (Number.isNaN(source) || Number.isNaN(target) || source === target) {
      compareError = 'Choose two different servers to compare.';
      compareReport = null;
      return;
    }

    comparing = true;
    compareError = '';

    try {
      compareReport = await driftApi.compareServers(source, target);
      toast.success('Server comparison ready.');
    } catch (error) {
      compareReport = null;
      compareError = describeError(error).message;
      toast.error(compareError);
    } finally {
      comparing = false;
    }
  }

  function describeError(error: unknown): { message: string; code: string } {
    if (error instanceof APIError) {
      return { message: error.message, code: error.code };
    }
    if (error instanceof Error) {
      return { message: error.message, code: 'UNKNOWN' };
    }
    return { message: 'Unexpected error', code: 'UNKNOWN' };
  }

  function categoryIcon(category: DriftCategory) {
    switch (category) {
      case 'packages':
        return Package;
      case 'services':
        return Cog;
      case 'docker':
        return Container;
      case 'users':
        return Users;
      case 'network':
        return Network;
      case 'disk':
        return HardDrive;
      case 'system':
      default:
        return Server;
    }
  }

  function changeTypeMeta(type: DriftChange['type']) {
    switch (type) {
      case 'added':
        return {
          label: 'Added',
          badge: 'success' as const,
          icon: ArrowUp,
          circleClass: 'bg-success/10 text-success'
        };
      case 'removed':
        return {
          label: 'Removed',
          badge: 'error' as const,
          icon: ArrowDown,
          circleClass: 'bg-error/10 text-error'
        };
      case 'modified':
      default:
        return {
          label: 'Modified',
          badge: 'warning' as const,
          icon: PencilLine,
          circleClass: 'bg-warning/10 text-warning'
        };
    }
  }

  function severityVariant(severity: DriftSeverity) {
    switch (severity) {
      case 'critical':
        return 'error' as const;
      case 'warning':
        return 'warning' as const;
      case 'info':
      default:
        return 'info' as const;
    }
  }

  function formatTimestamp(value?: string) {
    if (!value) return '—';
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) return value;
    return date.toLocaleString();
  }

  function formatSummaryTime(summary: DriftSummary) {
    return formatTimestamp(summary.comparedAt || summary.snapshotB || summary.snapshotA);
  }

  function totalHistoryChanges(summary: DriftSummary) {
    return (
      summary.totalChanges ??
      summary.packagesAdded +
        summary.packagesRemoved +
        summary.servicesChanged +
        summary.dockerChanged +
        summary.usersChanged +
        summary.networkChanged +
        summary.diskChanged
    );
  }

  function formatRange(summary: DriftSummary) {
    return `${formatTimestamp(summary.snapshotA)} → ${formatTimestamp(summary.snapshotB)}`;
  }
</script>

<svelte:head>
  <title>Drift Detection</title>
</svelte:head>

<div class="p-4 sm:p-6">
  <div class="mx-auto max-w-7xl space-y-6">
    <PageHeader
      title="Drift Detection"
      subtitle="Compare discovery snapshots to see what changed on a server over time."
    >
      {#snippet actions()}
        <button
          type="button"
          onclick={handleCheckNow}
          disabled={!selectedServerID || checkingNow || loadingReport}
          class="inline-flex items-center justify-center gap-2 rounded-lg bg-accent px-4 py-2 text-sm font-medium text-accent-fg transition hover:bg-accent-hover disabled:cursor-not-allowed disabled:opacity-50"
        >
          {#if checkingNow}
            <Spinner size="sm" label="Checking drift" />
          {:else}
            <RefreshCw size={16} />
          {/if}
          Check Drift Now
        </button>
      {/snippet}
    </PageHeader>

    <Card padding="lg">
      <div class="grid gap-6 lg:grid-cols-[1.6fr_1fr]">
        <div>
          <label class="mb-2 block text-sm font-medium text-fg-muted" for="server-select">Server</label>
          <select
            id="server-select"
            bind:value={selectedServerID}
            class="w-full rounded-lg border border-border-strong bg-surface px-3 py-2 text-sm text-fg shadow-sm outline-none transition focus:border-accent focus:ring-2 focus:ring-accent/20"
          >
            <option value="">Select a server</option>
            {#each servers as server}
              <option value={server.id}>{server.name} · {server.host}</option>
            {/each}
          </select>

          <div class="mt-3 flex flex-wrap items-center gap-2 text-xs text-fg-subtle">
            {#if selectedServer}
              <span class="rounded-full bg-surface-muted px-2.5 py-1 font-medium text-fg-muted">
                {selectedServer.host}:{selectedServer.port}
              </span>
              <span class="rounded-full bg-surface-muted px-2.5 py-1 font-medium text-fg-muted">
                {selectedServer.username}
              </span>
              {#if selectedServer.environment}
                <span class="rounded-full bg-accent-subtle px-2.5 py-1 font-medium text-accent">
                  {selectedServer.environment}
                </span>
              {/if}
              {#if selectedServer.region}
                <span class="rounded-full bg-surface-muted px-2.5 py-1 font-medium text-fg-muted">
                  {selectedServer.region}
                </span>
              {/if}
            {:else if serversLoading}
              <span class="inline-flex items-center gap-2">
                <Spinner size="sm" label="Loading servers" /> Loading servers...
              </span>
            {:else}
              <span>No servers available.</span>
            {/if}
          </div>
        </div>

        <div class="rounded-2xl border border-border bg-surface-muted p-4">
          <div class="flex items-center justify-between gap-3">
            <div>
              <p class="text-sm font-medium text-fg-muted">Current snapshot status</p>
              <p class="text-xs text-fg-subtle">{report?.serverName || selectedServer?.name || '—'}</p>
            </div>
            <Badge variant={reportReady ? 'success' : 'neutral'}>
              {reportReady ? 'Ready' : loadingReport ? 'Loading' : 'Waiting'}
            </Badge>
          </div>

          <div class="mt-4 space-y-2 text-sm text-fg-muted">
            <div class="flex items-center justify-between gap-3">
              <span>Latest comparison</span>
              <span class="font-medium text-fg">{formatTimestamp(report?.snapshotB)}</span>
            </div>
            <div class="flex items-center justify-between gap-3">
              <span>Previous snapshot</span>
              <span class="font-medium text-fg">{formatTimestamp(report?.snapshotA)}</span>
            </div>
            <div class="flex items-center justify-between gap-3">
              <span>Total changes</span>
              <span class="font-medium text-fg">{report?.totalChanges ?? 0}</span>
            </div>
          </div>
        </div>
      </div>
    </Card>

    {#if reportError}
      <div class="rounded-xl border border-error/20 bg-error/15 px-4 py-3 text-sm text-error" role="alert">
        {reportError}
      </div>
    {/if}

    {#snippet emptyServerIcon()}
      <Server size={24} />
    {/snippet}

    {#if !selectedServerID && !serversLoading}
      <EmptyState
        title="Select a server"
        description="Choose a server to load its latest drift report and snapshot history."
        icon={emptyServerIcon}
      />
    {/if}

    {#if selectedServerID && loadingReport && !report}
      <Card padding="lg">
        <div class="flex items-center gap-3 text-fg-muted">
          <Spinner size="md" label="Loading drift report" />
          <div>
            <p class="font-medium text-fg">Loading drift data...</p>
            <p class="text-sm text-fg-subtle">Fetching the latest snapshots and history.</p>
          </div>
        </div>
      </Card>
    {/if}

    {#if report}
      <section class="space-y-4">
        <div class="grid gap-4 sm:grid-cols-2 xl:grid-cols-5">
          <Card padding="md">
            <div class="flex items-center gap-3">
              <div class="flex h-10 w-10 items-center justify-center rounded-full bg-success/10 text-success">
                <Package size={18} />
              </div>
              <div>
                <p class="text-sm font-medium text-fg">Packages</p>
                <p class="text-xs text-fg-subtle">+{summary.packagesAdded} / -{summary.packagesRemoved}</p>
              </div>
            </div>
          </Card>

          <Card padding="md">
            <div class="flex items-center gap-3">
              <div class="flex h-10 w-10 items-center justify-center rounded-full bg-info/10 text-info">
                <Cog size={18} />
              </div>
              <div>
                <p class="text-sm font-medium text-fg">Services</p>
                <p class="text-xs text-fg-subtle">{summary.servicesChanged} changed</p>
              </div>
            </div>
          </Card>

          <Card padding="md">
            <div class="flex items-center gap-3">
              <div class="flex h-10 w-10 items-center justify-center rounded-full bg-accent/10 text-accent">
                <Container size={18} />
              </div>
              <div>
                <p class="text-sm font-medium text-fg">Docker</p>
                <p class="text-xs text-fg-subtle">{summary.dockerChanged} changed</p>
              </div>
            </div>
          </Card>

          <Card padding="md">
            <div class="flex items-center gap-3">
              <div class="flex h-10 w-10 items-center justify-center rounded-full bg-warning/10 text-warning">
                <Users size={18} />
              </div>
              <div>
                <p class="text-sm font-medium text-fg">Users</p>
                <p class="text-xs text-fg-subtle">{summary.usersChanged} changed</p>
              </div>
            </div>
          </Card>

          <Card padding="md">
            <div class="flex items-center gap-3">
              <div class="flex h-10 w-10 items-center justify-center rounded-full bg-info/10 text-info">
                <Network size={18} />
              </div>
              <div>
                <p class="text-sm font-medium text-fg">Network</p>
                <p class="text-xs text-fg-subtle">{summary.networkChanged} changed</p>
              </div>
            </div>
          </Card>

          <Card padding="md">
            <div class="flex items-center gap-3">
              <div class="flex h-10 w-10 items-center justify-center rounded-full bg-surface-muted text-fg-muted">
                <HardDrive size={18} />
              </div>
              <div>
                <p class="text-sm font-medium text-fg">Disk</p>
                <p class="text-xs text-fg-subtle">{summary.diskChanged} changed</p>
              </div>
            </div>
          </Card>

          <Card padding="md">
            <div class="flex items-center gap-3">
              <div class="flex h-10 w-10 items-center justify-center rounded-full bg-surface-muted text-fg-muted">
                <Server size={18} />
              </div>
              <div>
                <p class="text-sm font-medium text-fg">Total</p>
                <p class="text-xs text-fg-subtle">{report.totalChanges} changes</p>
              </div>
            </div>
          </Card>
        </div>

        <Card padding="lg">
          <div class="flex flex-col gap-4 lg:flex-row lg:items-end lg:justify-between">
            <div>
              <div class="flex items-center gap-2">
                <Filter size={16} class="text-fg-subtle" />
                <h2 class="text-lg font-semibold text-fg">Changes timeline</h2>
              </div>
              <p class="mt-1 text-sm text-fg-subtle">
                Showing {filteredChanges.length} of {report.changes.length} recorded drift changes.
              </p>
            </div>

            <div class="grid gap-3 sm:grid-cols-2">
              <label class="block">
                <span class="mb-1 block text-xs font-medium uppercase tracking-wide text-fg-subtle">Category</span>
                <select
                  bind:value={categoryFilter}
                  class="w-full rounded-lg border border-border-strong bg-surface px-3 py-2 text-sm text-fg outline-none transition focus:border-accent focus:ring-2 focus:ring-accent/20"
                >
                  <option value="all">All categories</option>
                  <option value="packages">Packages</option>
                  <option value="services">Services</option>
                  <option value="docker">Docker</option>
                  <option value="users">Users</option>
                  <option value="network">Network</option>
                  <option value="disk">Disk</option>
                  <option value="system">System</option>
                </select>
              </label>

              <label class="block">
                <span class="mb-1 block text-xs font-medium uppercase tracking-wide text-fg-subtle">Severity</span>
                <select
                  bind:value={severityFilter}
                  class="w-full rounded-lg border border-border-strong bg-surface px-3 py-2 text-sm text-fg outline-none transition focus:border-accent focus:ring-2 focus:ring-accent/20"
                >
                  <option value="all">All severities</option>
                  <option value="info">Info</option>
                  <option value="warning">Warning</option>
                  <option value="critical">Critical</option>
                </select>
              </label>
            </div>
          </div>

          {#if filteredChanges.length}
            <div class="mt-5 space-y-3">
              {#each filteredChanges as change}
                {@const CategoryIcon = categoryIcon(change.category)}
                {@const typeMeta = changeTypeMeta(change.type)}
                {@const TypeIcon = typeMeta.icon}

                <Card padding="md" hoverable>
                  <div class="flex gap-4">
                    <div class="flex h-12 w-12 shrink-0 items-center justify-center rounded-full bg-surface-muted text-fg-muted">
                      <CategoryIcon size={18} />
                    </div>

                    <div class="min-w-0 flex-1">
                      <div class="flex flex-wrap items-center gap-2">
                        <p class="font-medium text-fg">{change.name}</p>
                        <Badge variant={severityVariant(change.severity)}>{change.severity}</Badge>
                        <Badge variant={typeMeta.badge}>{typeMeta.label}</Badge>
                      </div>
                      <p class="mt-1 text-xs uppercase tracking-wide text-fg-subtle">{change.category}</p>

                      <div class="mt-3 grid gap-3 md:grid-cols-2">
                        <div class="rounded-lg bg-surface-muted p-3">
                          <div class="text-xs font-medium uppercase tracking-wide text-fg-subtle">Old value</div>
                          <div class="mt-1 whitespace-pre-wrap text-sm text-fg-muted">
                            {change.oldValue || '—'}
                          </div>
                        </div>
                        <div class="rounded-lg bg-surface-muted p-3">
                          <div class="text-xs font-medium uppercase tracking-wide text-fg-subtle">New value</div>
                          <div class="mt-1 whitespace-pre-wrap text-sm text-fg-muted">
                            {change.newValue || '—'}
                          </div>
                        </div>
                      </div>
                    </div>

                    <div class="hidden shrink-0 items-start sm:flex">
                      <span class={`inline-flex h-9 w-9 items-center justify-center rounded-full ${typeMeta.circleClass}`}>
                        <TypeIcon size={16} />
                      </span>
                    </div>
                  </div>
                </Card>
              {/each}
            </div>
          {:else}
            <div class="mt-5">
              <EmptyState
                title="No matching changes"
                description="Relax the filters to see the recorded drift entries."
              />
            </div>
          {/if}
        </Card>
      </section>
    {/if}

    <Card padding="lg">
      <div class="flex flex-col gap-4 lg:flex-row lg:items-center lg:justify-between">
        <div>
          <div class="flex items-center gap-2">
            <GitCompare size={16} class="text-fg-subtle" />
            <h2 class="text-lg font-semibold text-fg">Server comparison</h2>
          </div>
          <p class="mt-1 text-sm text-fg-subtle">
            Compare two servers' latest snapshots before planning a migration.
          </p>
        </div>

        <button
          type="button"
          onclick={handleCompareServers}
          disabled={!canCompare}
          class="inline-flex items-center justify-center gap-2 rounded-lg bg-fg px-4 py-2 text-sm font-medium text-bg transition hover:bg-fg-muted disabled:cursor-not-allowed disabled:opacity-50"
        >
          {#if comparing}
            <Spinner size="sm" label="Comparing servers" />
            Comparing...
          {:else}
            <GitCompare size={16} />
            Compare Servers
          {/if}
        </button>
      </div>

      <div class="mt-4 grid gap-4 md:grid-cols-2">
        <label class="block">
          <span class="mb-1 block text-xs font-medium uppercase tracking-wide text-fg-subtle">Source server</span>
          <select
            bind:value={compareSourceID}
            class="w-full rounded-lg border border-border-strong bg-surface px-3 py-2 text-sm text-fg outline-none transition focus:border-accent focus:ring-2 focus:ring-accent/20"
          >
            <option value="">Select source server</option>
            {#each servers as server}
              <option value={server.id}>{server.name} · {server.host}</option>
            {/each}
          </select>
        </label>

        <label class="block">
          <span class="mb-1 block text-xs font-medium uppercase tracking-wide text-fg-subtle">Target server</span>
          <select
            bind:value={compareTargetID}
            class="w-full rounded-lg border border-border-strong bg-surface px-3 py-2 text-sm text-fg outline-none transition focus:border-accent focus:ring-2 focus:ring-accent/20"
          >
            <option value="">Select target server</option>
            {#each servers as server}
              <option value={server.id}>{server.name} · {server.host}</option>
            {/each}
          </select>
        </label>
      </div>

      <div class="mt-3 flex flex-wrap items-center gap-2 text-sm text-fg-subtle">
        {#if compareSourceServer}
          <span class="rounded-full bg-accent-subtle px-2.5 py-1 font-medium text-accent">
            Source: {compareSourceServer.name}
          </span>
        {/if}
        {#if compareTargetServer}
          <span class="rounded-full bg-surface-muted px-2.5 py-1 font-medium text-fg-muted">
            Target: {compareTargetServer.name}
          </span>
        {/if}
        {#if compareSourceID && compareTargetID && compareSourceID === compareTargetID}
          <span class="rounded-full bg-warning/15 px-2.5 py-1 font-medium text-warning">
            Choose two different servers
          </span>
        {/if}
      </div>

      {#if compareError}
        <div class="mt-4 rounded-xl border border-error/20 bg-error/15 px-4 py-3 text-sm text-error" role="alert">
          {compareError}
        </div>
      {/if}

      {#if compareReport}
        <div class="mt-6 space-y-4">
          <div class="rounded-2xl border border-border bg-surface-muted p-4">
            <div class="flex flex-wrap items-center justify-between gap-3">
              <div>
                <p class="text-sm font-medium text-fg-muted">Comparison result</p>
                <p class="text-xs text-fg-subtle">
                  {compareReport.serverName} · {formatTimestamp(compareReport.snapshotA)} → {formatTimestamp(compareReport.snapshotB)}
                </p>
              </div>
              <Badge variant={compareReport.totalChanges ? 'warning' : 'success'}>
                {compareReport.totalChanges} changes
              </Badge>
            </div>

            <div class="mt-4 grid gap-3 sm:grid-cols-2 xl:grid-cols-5">
              <div class="rounded-xl bg-surface p-3 shadow-sm">
                <p class="text-xs uppercase tracking-wide text-fg-subtle">Packages</p>
                <p class="mt-1 text-sm font-semibold text-fg">
                  +{compareSummary.packagesAdded} / -{compareSummary.packagesRemoved}
                </p>
              </div>
              <div class="rounded-xl bg-surface p-3 shadow-sm">
                <p class="text-xs uppercase tracking-wide text-fg-subtle">Services</p>
                <p class="mt-1 text-sm font-semibold text-fg">{compareSummary.servicesChanged} changed</p>
              </div>
              <div class="rounded-xl bg-surface p-3 shadow-sm">
                <p class="text-xs uppercase tracking-wide text-fg-subtle">Docker</p>
                <p class="mt-1 text-sm font-semibold text-fg">{compareSummary.dockerChanged} changed</p>
              </div>
              <div class="rounded-xl bg-surface p-3 shadow-sm">
                <p class="text-xs uppercase tracking-wide text-fg-subtle">Users</p>
                <p class="mt-1 text-sm font-semibold text-fg">{compareSummary.usersChanged} changed</p>
              </div>
              <div class="rounded-xl bg-surface p-3 shadow-sm">
                <p class="text-xs uppercase tracking-wide text-fg-subtle">Network / Disk</p>
                <p class="mt-1 text-sm font-semibold text-fg">
                  {compareSummary.networkChanged} / {compareSummary.diskChanged}
                </p>
              </div>
            </div>
          </div>

          <div class="space-y-2">
            {#each compareReport.changes.slice(0, 10) as change}
              {@const CategoryIcon = categoryIcon(change.category)}
              {@const typeMeta = changeTypeMeta(change.type)}
              {@const TypeIcon = typeMeta.icon}

              <div class="flex items-start gap-3 rounded-xl border border-border bg-surface p-3">
                <div class="flex h-10 w-10 shrink-0 items-center justify-center rounded-full bg-surface-muted text-fg-muted">
                  <CategoryIcon size={18} />
                </div>
                <div class="min-w-0 flex-1">
                  <div class="flex flex-wrap items-center gap-2">
                    <p class="font-medium text-fg">{change.name}</p>
                    <Badge variant={severityVariant(change.severity)}>{change.severity}</Badge>
                    <Badge variant={typeMeta.badge}>{typeMeta.label}</Badge>
                  </div>
                  <p class="mt-1 text-xs text-fg-subtle">{change.category}</p>
                  <div class="mt-2 flex flex-wrap items-center gap-x-2 gap-y-1 text-sm text-fg-muted">
                    <span class="break-all">{change.oldValue || '—'}</span>
                    <span class="text-fg-subtle">→</span>
                    <span class="break-all">{change.newValue || '—'}</span>
                  </div>
                </div>
                <span class={`hidden h-8 w-8 shrink-0 items-center justify-center rounded-full sm:inline-flex ${typeMeta.circleClass}`}>
                  <TypeIcon size={14} />
                </span>
              </div>
            {/each}
          </div>
        </div>
      {/if}
    </Card>

    <Card padding="lg">
      <div class="flex items-center justify-between gap-3">
        <div>
          <h2 class="text-lg font-semibold text-fg">Drift history</h2>
          <p class="mt-1 text-sm text-fg-subtle">Past comparisons between consecutive snapshots.</p>
        </div>
        <Badge variant="neutral">{history.length} entries</Badge>
      </div>

      {#if historyError}
        <div class="mt-4 rounded-xl border border-error/20 bg-error/15 px-4 py-3 text-sm text-error" role="alert">
          {historyError}
        </div>
      {/if}

      {#if loadingHistory && history.length === 0}
        <div class="mt-4 flex items-center gap-3 text-fg-muted">
          <Spinner size="md" label="Loading drift history" />
          <div>
            <p class="font-medium text-fg">Loading history...</p>
            <p class="text-sm text-fg-subtle">Fetching previous snapshot comparisons.</p>
          </div>
        </div>
      {:else if history.length}
        <div class="mt-4 space-y-3">
          {#each history as entry}
            <div class="rounded-xl border border-border bg-surface p-4">
              <div class="flex flex-wrap items-center justify-between gap-3">
                <div>
                  <p class="font-medium text-fg">{formatSummaryTime(entry)}</p>
                  <p class="text-xs text-fg-subtle">{formatRange(entry)}</p>
                </div>
                <Badge variant={totalHistoryChanges(entry) ? 'warning' : 'success'}>
                  {totalHistoryChanges(entry)} changes
                </Badge>
              </div>

              <div class="mt-3 grid gap-2 sm:grid-cols-2 xl:grid-cols-5">
                <div class="rounded-lg bg-surface-muted p-3 text-sm">
                  <span class="block text-xs uppercase tracking-wide text-fg-subtle">Packages</span>
                  <span class="font-medium text-fg">+{entry.packagesAdded} / -{entry.packagesRemoved}</span>
                </div>
                <div class="rounded-lg bg-surface-muted p-3 text-sm">
                  <span class="block text-xs uppercase tracking-wide text-fg-subtle">Services</span>
                  <span class="font-medium text-fg">{entry.servicesChanged}</span>
                </div>
                <div class="rounded-lg bg-surface-muted p-3 text-sm">
                  <span class="block text-xs uppercase tracking-wide text-fg-subtle">Docker</span>
                  <span class="font-medium text-fg">{entry.dockerChanged}</span>
                </div>
                <div class="rounded-lg bg-surface-muted p-3 text-sm">
                  <span class="block text-xs uppercase tracking-wide text-fg-subtle">Users</span>
                  <span class="font-medium text-fg">{entry.usersChanged}</span>
                </div>
                <div class="rounded-lg bg-surface-muted p-3 text-sm">
                  <span class="block text-xs uppercase tracking-wide text-fg-subtle">Network / Disk</span>
                  <span class="font-medium text-fg">{entry.networkChanged} / {entry.diskChanged}</span>
                </div>
              </div>
            </div>
          {/each}
        </div>
      {:else}
        <div class="mt-4">
          <EmptyState
            title="No drift history"
            description="Generate at least two discovery snapshots to populate the drift history."
          />
        </div>
      {/if}
    </Card>
  </div>
</div>
