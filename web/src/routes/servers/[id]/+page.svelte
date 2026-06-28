<script lang="ts">
  import { goto } from '$app/navigation';
  import { page } from '$app/state';
  import { onDestroy } from 'svelte';
  import { ArrowLeft, ArrowRightLeft, Clock, Cpu, HardDrive, MemoryStick, MoreVertical, Network, Play, RefreshCw } from 'lucide-svelte';
  import { api } from '$lib/api/client';
  import { discoveryApi, type ServerSnapshot, type ServerConnectionInfo } from '$lib/api/discovery';
  import { wsConnect, type WSMessage } from '$lib/api/websocket';
  import { Badge, Card, DropdownMenu, EmptyState, Spinner } from '$lib/components/ui';
  import { formatBytes, formatDateTime, formatDuration, formatRelativeTime } from '$lib/utils/format';
  import { toast } from '$lib/stores/toast';
  import { deleteServer, type Server } from '$lib/stores/servers';
  import { loadSnapshot as loadSnapshotFromStore, invalidateSnapshot } from '$lib/stores/snapshots';
  import { getSnapshot as getCachedSnapshot } from '$lib/stores/snapshots';

  type TabKey = 'overview' | 'docker' | 'services' | 'databases' | 'network' | 'nginx';

  let serverId = $derived(Number(page.params.id));
  let server = $state<Server | null>(null);
  let info = $state<ServerConnectionInfo | null>(null);
  let snapshot = $state<ServerSnapshot | null>(null);
  let loading = $state(true);
  let loadingInfo = $state(false);
  let loadingSnapshot = $state(false);
  let rescanning = $state(false);
  let connecting = $state(false);
  let wsError = $state('');
  let wsSteps = $state<{ step: string; status: string; value?: string; error?: string }[]>([]);
  let activeTab = $state<TabKey>('overview');
  let showOnlyActive = $state(false);
  let ws: WebSocket | null = null;
  let connectionId = 0;
  let serverRequestId = 0;
  let infoRequestId = 0;
  let snapshotRequestId = 0;
  let refreshTimer: ReturnType<typeof setInterval> | null = null;

  const tabs: { id: TabKey; label: string }[] = [
    { id: 'overview', label: 'Overview' },
    { id: 'docker', label: 'Docker' },
    { id: 'services', label: 'Services' },
    { id: 'databases', label: 'Databases' },
    { id: 'network', label: 'Network' },
    { id: 'nginx', label: 'Nginx' }
  ];

  const sortedPorts = $derived((snapshot?.networkPorts ?? []).slice().sort((a, b) => a.port - b.port));
  const visibleServices = $derived(
    snapshot
      ? showOnlyActive
        ? snapshot.services.filter((service) => service.activeState === 'active')
        : snapshot.services
      : []
  );
  const dockerContainers = $derived(snapshot?.docker?.containers ?? []);
  const dockerImages = $derived(snapshot?.docker?.images ?? []);
  const composeProjects = $derived(snapshot?.docker?.composeProjects ?? []);
  const databases = $derived(snapshot?.databases ?? []);
  const nginxVHosts = $derived(snapshot?.nginx?.vhosts ?? []);
  const lastScanned = $derived(snapshot ? formatRelativeTime(snapshot.capturedAt) : '');

  $effect(() => {
    const id = serverId;
    if (!Number.isFinite(id)) {
      loading = false;
      return;
    }

    server = null;
    info = null;
    snapshot = null;
    wsError = '';
    wsSteps = [];
    connecting = false;
    connectionId += 1;
    ws?.close();
    ws = null;
    activeTab = 'overview';
    showOnlyActive = false;

    void loadServer(id);
    void loadInfo(id);
    void loadSnapshot(id);

    // Auto-refresh snapshot every 30s for realtime data
    if (refreshTimer) clearInterval(refreshTimer);
    refreshTimer = setInterval(() => {
      if (Number.isFinite(id)) {
        invalidateSnapshot(id);
        void loadSnapshot(id);
      }
    }, 30000);
  });

  async function loadServer(id: number) {
    const requestId = ++serverRequestId;
    loading = true;

    try {
      const nextServer = (await api.get(`/servers/${id}`)) as Server;
      if (requestId === serverRequestId) {
        server = nextServer;
      }
    } catch {
      if (requestId === serverRequestId) {
        server = null;
      }
    } finally {
      if (requestId === serverRequestId) {
        loading = false;
      }
    }
  }

  async function loadInfo(id: number) {
    const requestId = ++infoRequestId;
    loadingInfo = true;

    try {
      const nextInfo = await discoveryApi.getInfo(id);
      if (requestId === infoRequestId) {
        info = nextInfo;
      }
    } catch {
      // Keep the previous info snapshot if a refresh fails.
    } finally {
      if (requestId === infoRequestId) {
        loadingInfo = false;
      }
    }
  }

  async function loadSnapshot(id: number) {
    const requestId = ++snapshotRequestId;
    loadingSnapshot = true;

    try {
      await loadSnapshotFromStore(id);
      const snap = getCachedSnapshot(id);
      if (requestId === snapshotRequestId) {
        snapshot = snap ?? null;
      }
    } catch {
      // Keep the previous snapshot if a refresh fails.
    } finally {
      if (requestId === snapshotRequestId) {
        loadingSnapshot = false;
      }
    }
  }

  function handleConnect() {
    const id = serverId;
    if (!Number.isFinite(id)) return;

    const currentConnection = ++connectionId;
    connecting = true;
    wsError = '';
    wsSteps = [];
    ws?.close();

    ws = wsConnect(
      id,
      (msg: WSMessage) => {
        if (currentConnection !== connectionId) return;

        if (msg.status === 'error' || msg.error) {
          wsError = msg.error || `Connection test failed during ${msg.step}`;
        }

        if (msg.step === 'done' || msg.status === 'complete') {
          connecting = false;
          void loadInfo(id);
          return;
        }

        wsSteps = [
          ...wsSteps,
          {
            step: msg.step,
            status: msg.status,
            value: msg.value !== undefined ? String(msg.value) : undefined,
            error: msg.error
          }
        ];
      },
      () => {
        if (currentConnection !== connectionId) return;
        connecting = false;
        if (!wsSteps.length && !wsError) {
          wsError = 'Unable to open the WebSocket connection.';
        }
      },
      () => {
        if (currentConnection !== connectionId) return;
        connecting = false;
        if (!wsSteps.length && !wsError) {
          wsError = 'The WebSocket connection closed before any connection steps were received.';
        }
      }
    );
  }

  async function handleRescan() {
    const id = serverId;
    if (!Number.isFinite(id)) return;

    rescanning = true;
    try {
      await discoveryApi.triggerDiscovery(id);
      toast.success('Scan started, check Jobs for progress');
      invalidateSnapshot(id);
      await loadSnapshot(id);
    } catch {
      toast.error('Failed to start scan');
    } finally {
      rescanning = false;
    }
  }

  async function handleDeleteServer() {
    const id = serverId;
    if (!Number.isFinite(id)) return;
    if (!confirm('Delete this server? This cannot be undone.')) return;

    try {
      await deleteServer(id);
      toast.success('Server deleted');
      await goto('/');
    } catch {
      toast.error('Failed to delete server');
    }
  }

  function buildMigrateMenuItems(id: number) {
    return [
      { label: 'Migrate FROM this server', href: `/migrations/new?source=${id}` },
      { label: 'Migrate TO this server', href: `/migrations/new?target=${id}` },
    ];
  }

  function buildMoreMenuItems(id: number) {
    return [
      { label: 'Compare with another server', href: `/servers/compare?source=${id}` },
      { label: 'Create migration plan', href: `/plans/new?source=${id}` },
      { label: 'Edit server', href: `/servers/${id}/edit` },
      { label: '', divider: true },
      { label: 'Delete server', danger: true, onclick: () => void handleDeleteServer() },
    ];
  }

  function openComparePage() {
    const id = serverId;
    if (!Number.isFinite(id)) return;
    goto(`/servers/compare?source=${id}`);
  }

  function statusVariant(status: string) {
    const normalized = status.toLowerCase();

    if (normalized === 'active' || normalized === 'running') {
      return 'success';
    }

    if (normalized === 'failed' || normalized === 'error') {
      return 'error';
    }

    return 'neutral';
  }

  function containerVariant(state: string) {
    const normalized = state.toLowerCase();

    if (normalized === 'running') return 'success';
    if (normalized === 'exited' || normalized === 'dead' || normalized === 'created') return 'neutral';
    if (normalized === 'error') return 'error';
    return 'neutral';
  }

  function formatSummaryLabel(status: string) {
    return status
      .split(/[_-]/g)
      .filter(Boolean)
      .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
      .join(' ');
  }

  function databaseLabel(type: string) {
    const normalized = type.toLowerCase();

    if (normalized.includes('mysql')) return 'MySQL';
    if (normalized.includes('postgres')) return 'Postgres';
    if (normalized.includes('redis')) return 'Redis';
    if (normalized.includes('mongo')) return 'Mongo';
    return type;
  }

  function databaseVariant(type: string) {
    const normalized = type.toLowerCase();

    if (normalized.includes('mysql') || normalized.includes('postgres')) return 'info';
    if (normalized.includes('redis')) return 'warning';
    if (normalized.includes('mongo')) return 'neutral';
    return 'neutral';
  }

  function containerPorts(ports: { hostPort: number; containerPort: number; protocol: string }[]) {
    if (!ports || ports.length === 0) return '—';
    return ports.map(p => `${p.hostPort}:${p.containerPort}/${p.protocol}`).join(', ');
  }

  function containerUptime(status: string) {
    const match = status.match(/^Up\s+(.*)$/i);
    return match?.[1] ?? status;
  }

  function formatComposeServices(services: string[]) {
    return services.length ? services.join(', ') : '—';
  }

  function formatDbSize(sizeMb: number) {
    return sizeMb > 0 ? formatBytes(sizeMb * 1024 * 1024) : '—';
  }

  onDestroy(() => {
    connectionId += 1;
    ws?.close();
    if (refreshTimer) clearInterval(refreshTimer);
  });
</script>

{#snippet migrateTrigger()}
  <span class="inline-flex items-center justify-center gap-2 rounded-lg border border-slate-300 bg-white px-4 py-2 text-sm font-medium text-slate-700 transition hover:bg-slate-50">
    <ArrowRightLeft size={16} />
    Migrate
  </span>
{/snippet}

{#snippet moreTrigger()}
  <span class="inline-flex items-center justify-center gap-2 rounded-lg border border-slate-300 bg-white px-4 py-2 text-sm font-medium text-slate-700 transition hover:bg-slate-50">
    <MoreVertical size={16} />
    More
  </span>
{/snippet}

<div class="p-4 sm:p-6 max-w-6xl mx-auto">
  <a href="/" class="mb-3 inline-flex items-center gap-2 text-sm text-slate-600 transition hover:text-slate-900">
    <ArrowLeft size={16} /> Back to Servers
  </a>

  {#if server}
    <div class="mb-6 flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
      <div>
        <div class="flex items-center gap-3">
          <h1 class="text-2xl font-bold tracking-tight text-slate-900">{server.name}</h1>
          <span class="rounded-full bg-slate-100 px-2.5 py-1 text-xs font-medium text-slate-600">
            {server.environment || 'no environment'}
          </span>
        </div>
        <p class="mt-1 text-sm text-slate-500">
          {server.host}:{server.port} · {server.username}
        </p>
        {#if snapshot}
          <p class="mt-1 text-xs text-slate-500">Last scanned: {lastScanned}</p>
        {/if}
        {#if server.description}
          <p class="mt-2 max-w-3xl text-sm text-slate-600">{server.description}</p>
        {/if}
      </div>

      <div class="flex flex-wrap items-center gap-3">
        <button
          type="button"
          onclick={handleConnect}
          disabled={connecting}
          class="inline-flex items-center justify-center gap-2 rounded-lg border border-slate-300 bg-white px-4 py-2 text-sm font-medium text-slate-700 transition hover:bg-slate-50 disabled:cursor-not-allowed disabled:opacity-50"
        >
          <Play size={18} />
          {connecting ? 'Connecting...' : 'Test Connection'}
        </button>

        <DropdownMenu items={buildMigrateMenuItems(server.id)} label="Migrate this server" trigger={migrateTrigger} />
        <DropdownMenu items={buildMoreMenuItems(server.id)} label="More actions" trigger={moreTrigger} />
      </div>
    </div>

    <div class="mb-6 border-b border-slate-200">
      <div class="-mb-px flex flex-wrap gap-2" role="tablist" aria-label="Server detail tabs">
        {#each tabs as tab}
          <button
            type="button"
            role="tab"
            onclick={() => (activeTab = tab.id)}
            class={`rounded-t-lg border px-4 py-2 text-sm font-medium transition ${activeTab === tab.id
              ? 'border-slate-200 border-b-white bg-white text-slate-900'
              : 'border-transparent text-slate-500 hover:bg-slate-50 hover:text-slate-900'}`}
            aria-selected={activeTab === tab.id}
          >
            {tab.label}
          </button>
        {/each}
      </div>
    </div>

    <div class="space-y-6">
      {#if activeTab === 'overview'}
        {#if wsSteps.length > 0 || wsError}
          <Card padding="lg">
            <div class="mb-4 flex items-center justify-between gap-4">
              <div>
                <h2 class="text-sm font-semibold uppercase tracking-wide text-slate-500">Connection Test</h2>
                <p class="mt-1 text-sm text-slate-500">Streaming connection steps from the backend.</p>
              </div>
              {#if connecting}
                <span class="text-sm text-slate-500">Running...</span>
              {/if}
            </div>

            {#if wsError}
              <div class="mb-4 rounded-xl border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700" role="alert">
                <p class="font-semibold text-red-900">Connection test failed</p>
                <p class="mt-1">{wsError}</p>
              </div>
            {/if}

            {#if wsSteps.length > 0}
              <div class="space-y-2">
                {#each wsSteps as step}
                  <div class="flex flex-wrap items-center gap-2 rounded-lg bg-slate-50 px-3 py-2 text-sm">
                    <span class={`h-2.5 w-2.5 rounded-full ${step.status === 'success' ? 'bg-emerald-500' : 'bg-rose-500'}`}></span>
                    <span class="font-mono text-slate-700">{step.step}</span>
                    <span class="text-slate-400">·</span>
                    <span class="text-slate-500">{step.status}</span>
                    {#if step.value}
                      <span class="break-all text-slate-400">→ {step.value}</span>
                    {/if}
                    {#if step.error}
                      <span class="break-all text-rose-600">→ {step.error}</span>
                    {/if}
                  </div>
                {/each}
              </div>
            {/if}
          </Card>
        {/if}

        <Card padding="lg">
          <div class="mb-4 flex flex-wrap items-center justify-between gap-3">
            <div>
              <h2 class="text-sm font-semibold uppercase tracking-wide text-slate-500">System Information</h2>
              <p class="mt-1 text-sm text-slate-500">Cached data from the latest successful connection test.</p>
            </div>
            <div class="flex flex-wrap items-center gap-2">
              <button
                type="button"
                onclick={handleRescan}
                disabled={rescanning || loadingSnapshot}
                class="inline-flex items-center justify-center gap-2 rounded-lg border border-slate-300 bg-white px-4 py-2 text-sm font-medium text-slate-700 transition hover:bg-slate-50 disabled:cursor-not-allowed disabled:opacity-50"
              >
                <RefreshCw size={16} />
                {rescanning ? 'Re-scanning...' : 'Re-scan'}
              </button>
              <button
                type="button"
                onclick={openComparePage}
                class="inline-flex items-center justify-center rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white transition hover:bg-blue-700"
              >
                Compare with another server
              </button>
            </div>
          </div>

          {#if loadingInfo && !info}
            <div class="flex items-center gap-3 rounded-xl border border-dashed border-slate-300 bg-slate-50 px-4 py-6 text-slate-600">
              <Spinner size="md" label="Loading system information" />
              <div>
                <p class="font-medium text-slate-900">Loading system information...</p>
                <p class="text-sm text-slate-500">Refreshing the latest connection snapshot.</p>
              </div>
            </div>
          {:else if info}
            <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3">
              <div class="rounded-xl border border-slate-200 bg-slate-50 p-4">
                <div class="mb-2 flex items-center gap-2 text-slate-500">
                  <Cpu size={18} />
                  <span class="text-sm font-semibold text-slate-900">CPU</span>
                </div>
                <p class="text-sm text-slate-600">{info.cpuModel || 'Unknown'}</p>
                <p class="mt-1 text-xs text-slate-500">{info.cpuCores || 0} cores</p>
              </div>

              <div class="rounded-xl border border-slate-200 bg-slate-50 p-4">
                <div class="mb-2 flex items-center gap-2 text-slate-500">
                  <MemoryStick size={18} />
                  <span class="text-sm font-semibold text-slate-900">Memory</span>
                </div>
                <p class="text-sm text-slate-600">{info.ramTotalMb || 0} MB</p>
                <p class="mt-1 text-xs text-slate-500">{info.virtualization || 'Unknown virtualization'}</p>
              </div>

              <div class="rounded-xl border border-slate-200 bg-slate-50 p-4">
                <div class="mb-2 flex items-center gap-2 text-slate-500">
                  <HardDrive size={18} />
                  <span class="text-sm font-semibold text-slate-900">Storage</span>
                </div>
                <p class="text-sm text-slate-600">{info.diskTotalGb || 0} GB</p>
                <p class="mt-1 text-xs text-slate-500">Latency: {info.latencyMs || 0} ms</p>
              </div>

              <div class="rounded-xl border border-slate-200 bg-slate-50 p-4">
                <div class="mb-2 flex items-center gap-2 text-slate-500">
                  <Network size={18} />
                  <span class="text-sm font-semibold text-slate-900">Network</span>
                </div>
                <p class="text-sm text-slate-600">Public: {info.publicIp || 'Unknown'}</p>
                <p class="mt-1 text-sm text-slate-600">Private: {info.privateIp || 'Unknown'}</p>
              </div>

              <div class="rounded-xl border border-slate-200 bg-slate-50 p-4">
                <div class="mb-2 flex items-center gap-2 text-slate-500">
                  <Clock size={18} />
                  <span class="text-sm font-semibold text-slate-900">System</span>
                </div>
                <p class="text-sm text-slate-600">{info.os || 'Unknown OS'}</p>
                <p class="mt-1 text-xs text-slate-500">Kernel {info.kernel || 'Unknown'} · {info.architecture || 'Unknown'}</p>
              </div>

              <div class="rounded-xl border border-slate-200 bg-slate-50 p-4">
                <div class="mb-2 flex items-center gap-2 text-slate-500">
                  <span class="text-sm font-semibold text-slate-900">Status</span>
                </div>
                <p class="text-sm text-slate-600">SSH: {info.sshStatus || 'Unknown'}</p>
                <p class="mt-1 text-sm text-slate-600">Timezone: {info.timezone || 'Unknown'}</p>
                <p class="mt-1 text-sm text-slate-600">Provider: {info.provider || 'Unknown'}</p>
              </div>
            </div>
          {:else}
            <EmptyState
              title="No cached system information"
              description="Run a connection test to collect the latest system data."
            />
          {/if}
        </Card>
      {/if}

      {#if activeTab === 'docker'}
        <Card padding="lg">
          <div class="mb-4 flex items-center justify-between gap-3">
            <div>
              <h2 class="text-sm font-semibold uppercase tracking-wide text-slate-500">Docker</h2>
              <p class="mt-1 text-sm text-slate-500">Detected containers, images, and compose projects.</p>
            </div>
            {#if snapshot?.docker}
              <span class="text-sm text-slate-500">Docker {snapshot.docker.version || 'unknown'}</span>
            {/if}
          </div>

          {#if loadingSnapshot && !snapshot}
            <div class="flex items-center gap-3 rounded-xl border border-dashed border-slate-300 bg-slate-50 px-4 py-6 text-slate-600">
              <Spinner size="md" label="Loading Docker data" />
              <div>
                <p class="font-medium text-slate-900">Loading Docker snapshot...</p>
                <p class="text-sm text-slate-500">Please wait while discovery data is retrieved.</p>
              </div>
            </div>
          {:else if !snapshot?.docker}
            <EmptyState
              title="Docker not installed"
              description="No Docker snapshot is available for this server."
            />
          {:else if !dockerContainers.length && !dockerImages.length && !composeProjects.length}
            <EmptyState
              title="No Docker resources detected"
              description="The latest snapshot did not find containers, images, or compose projects."
            />
          {:else}
            <div class="space-y-6">
              <section>
                <h3 class="mb-3 text-sm font-semibold uppercase tracking-wide text-slate-500">Containers</h3>
                {#if dockerContainers.length}
                  <div class="overflow-hidden rounded-xl border border-slate-200 bg-white shadow-sm">
                    <table class="min-w-full divide-y divide-slate-200 text-left text-sm">
                      <thead class="bg-slate-50 text-slate-500">
                        <tr>
                          <th class="px-4 py-3 font-medium">Name</th>
                          <th class="px-4 py-3 font-medium">Image</th>
                          <th class="px-4 py-3 font-medium">Status</th>
                          <th class="px-4 py-3 font-medium">Ports</th>
                          <th class="px-4 py-3 font-medium">Uptime</th>
                        </tr>
                      </thead>
                      <tbody class="divide-y divide-slate-200 bg-white">
                        {#each dockerContainers as container}
                          <tr>
                            <td class="px-4 py-3 font-medium text-slate-900">{container.name}</td>
                            <td class="px-4 py-3 text-slate-600">{container.image}</td>
                            <td class="px-4 py-3">
                              <Badge variant={containerVariant(container.state)}>{formatSummaryLabel(container.state)}</Badge>
                            </td>
                            <td class="px-4 py-3 text-slate-600">{containerPorts(container.ports)}</td>
                            <td class="px-4 py-3 text-slate-600">{containerUptime(container.status)}</td>
                          </tr>
                        {/each}
                      </tbody>
                    </table>
                  </div>
                {:else}
                  <EmptyState title="No containers found" description="This server does not appear to have any running containers." />
                {/if}
              </section>

              <section>
                <h3 class="mb-3 text-sm font-semibold uppercase tracking-wide text-slate-500">Images</h3>
                {#if dockerImages.length}
                  <div class="overflow-hidden rounded-xl border border-slate-200 bg-white shadow-sm">
                    <table class="min-w-full divide-y divide-slate-200 text-left text-sm">
                      <thead class="bg-slate-50 text-slate-500">
                        <tr>
                          <th class="px-4 py-3 font-medium">Name</th>
                          <th class="px-4 py-3 font-medium">Tag</th>
                          <th class="px-4 py-3 font-medium">Size</th>
                        </tr>
                      </thead>
                      <tbody class="divide-y divide-slate-200 bg-white">
                        {#each dockerImages as image}
                          <tr>
                            <td class="px-4 py-3 font-medium text-slate-900">{image.repository}</td>
                            <td class="px-4 py-3 text-slate-600">{image.tag}</td>
                            <td class="px-4 py-3 text-slate-600">{image.size}</td>
                          </tr>
                        {/each}
                      </tbody>
                    </table>
                  </div>
                {:else}
                  <EmptyState title="No images found" description="The snapshot does not include any Docker images." />
                {/if}
              </section>

              {#if composeProjects.length}
                <section>
                  <h3 class="mb-3 text-sm font-semibold uppercase tracking-wide text-slate-500">Compose Projects</h3>
                  <div class="grid gap-4 md:grid-cols-2">
                    {#each composeProjects as project}
                      <Card padding="md">
                        <div class="flex items-start justify-between gap-4">
                          <div>
                            <p class="font-medium text-slate-900">{project.name}</p>
                            <p class="mt-1 text-sm text-slate-500">Config: {project.configFiles}</p>
                          </div>
                        </div>
                        <p class="mt-3 text-sm text-slate-600">Services: {formatComposeServices(project.services)}</p>
                      </Card>
                    {/each}
                  </div>
                </section>
              {/if}
            </div>
          {/if}
        </Card>
      {/if}

      {#if activeTab === 'services'}
        <Card padding="lg">
          <div class="mb-4 flex flex-wrap items-center justify-between gap-3">
            <div>
              <h2 class="text-sm font-semibold uppercase tracking-wide text-slate-500">Services</h2>
              <p class="mt-1 text-sm text-slate-500">Systemd services discovered on the server.</p>
            </div>
            <label class="inline-flex items-center gap-2 text-sm text-slate-600">
              <input
                type="checkbox"
                bind:checked={showOnlyActive}
                class="h-4 w-4 rounded border-slate-300 text-blue-600 focus:ring-blue-500"
              />
              Show only active
            </label>
          </div>

          {#if loadingSnapshot && !snapshot}
            <div class="flex items-center gap-3 rounded-xl border border-dashed border-slate-300 bg-slate-50 px-4 py-6 text-slate-600">
              <Spinner size="md" label="Loading services" />
              <div>
                <p class="font-medium text-slate-900">Loading services...</p>
                <p class="text-sm text-slate-500">Waiting for discovery data.</p>
              </div>
            </div>
          {:else if !snapshot?.services.length}
            <EmptyState title="No services detected" description="No systemd services were found in the latest snapshot." />
          {:else if !visibleServices.length}
            <EmptyState title="No active services" description="Toggle the filter off to view inactive services." />
          {:else}
            <div class="overflow-hidden rounded-xl border border-slate-200 bg-white shadow-sm">
              <table class="min-w-full divide-y divide-slate-200 text-left text-sm">
                <thead class="bg-slate-50 text-slate-500">
                  <tr>
                    <th class="px-4 py-3 font-medium">Name</th>
                    <th class="px-4 py-3 font-medium">Load State</th>
                    <th class="px-4 py-3 font-medium">Active State</th>
                    <th class="px-4 py-3 font-medium">Type</th>
                    <th class="px-4 py-3 font-medium">Description</th>
                  </tr>
                </thead>
                <tbody class="divide-y divide-slate-200 bg-white">
                  {#each visibleServices as service}
                    <tr>
                      <td class="px-4 py-3 font-medium text-slate-900">{service.name}</td>
                      <td class="px-4 py-3 text-slate-600">{service.loadState}</td>
                      <td class="px-4 py-3"><Badge variant={statusVariant(service.activeState)}>{service.activeState}</Badge></td>
                      <td class="px-4 py-3 text-slate-600">{service.type}</td>
                      <td class="px-4 py-3 text-slate-600">{service.description}</td>
                    </tr>
                  {/each}
                </tbody>
              </table>
            </div>
          {/if}
        </Card>
      {/if}

      {#if activeTab === 'databases'}
        <Card padding="lg">
          <div class="mb-4">
            <h2 class="text-sm font-semibold uppercase tracking-wide text-slate-500">Databases</h2>
            <p class="mt-1 text-sm text-slate-500">Detected database instances and storage paths.</p>
          </div>

          {#if loadingSnapshot && !snapshot}
            <div class="flex items-center gap-3 rounded-xl border border-dashed border-slate-300 bg-slate-50 px-4 py-6 text-slate-600">
              <Spinner size="md" label="Loading database data" />
              <div>
                <p class="font-medium text-slate-900">Loading databases...</p>
                <p class="text-sm text-slate-500">Please wait while discovery data loads.</p>
              </div>
            </div>
          {:else if !databases.length}
            <EmptyState title="No databases detected" description="The latest snapshot did not find any database instances." />
          {:else}
            <div class="overflow-hidden rounded-xl border border-slate-200 bg-white shadow-sm">
              <table class="min-w-full divide-y divide-slate-200 text-left text-sm">
                <thead class="bg-slate-50 text-slate-500">
                  <tr>
                    <th class="px-4 py-3 font-medium">Type</th>
                    <th class="px-4 py-3 font-medium">Version</th>
                    <th class="px-4 py-3 font-medium">Port</th>
                    <th class="px-4 py-3 font-medium">Data Directory</th>
                    <th class="px-4 py-3 font-medium">Size</th>
                  </tr>
                </thead>
                <tbody class="divide-y divide-slate-200 bg-white">
                  {#each databases as database}
                    <tr>
                      <td class="px-4 py-3"><Badge variant={databaseVariant(database.type)}>{databaseLabel(database.type)}</Badge></td>
                      <td class="px-4 py-3 text-slate-600">{database.version}</td>
                      <td class="px-4 py-3 text-slate-600">{database.port}</td>
                      <td class="px-4 py-3 text-slate-600">{database.dataDir}</td>
                      <td class="px-4 py-3 text-slate-600">{formatDbSize(database.sizeMb)}</td>
                    </tr>
                  {/each}
                </tbody>
              </table>
            </div>
          {/if}
        </Card>
      {/if}

      {#if activeTab === 'network'}
        <Card padding="lg">
          <div class="mb-4">
            <h2 class="text-sm font-semibold uppercase tracking-wide text-slate-500">Network</h2>
            <p class="mt-1 text-sm text-slate-500">Open ports sorted by port number.</p>
          </div>

          {#if loadingSnapshot && !snapshot}
            <div class="flex items-center gap-3 rounded-xl border border-dashed border-slate-300 bg-slate-50 px-4 py-6 text-slate-600">
              <Spinner size="md" label="Loading network data" />
              <div>
                <p class="font-medium text-slate-900">Loading open ports...</p>
                <p class="text-sm text-slate-500">Discovery data is still being fetched.</p>
              </div>
            </div>
          {:else if !sortedPorts.length}
            <EmptyState title="No open ports detected" description="No listening ports were found in the latest snapshot." />
          {:else}
            <div class="overflow-hidden rounded-xl border border-slate-200 bg-white shadow-sm">
              <table class="min-w-full divide-y divide-slate-200 text-left text-sm">
                <thead class="bg-slate-50 text-slate-500">
                  <tr>
                    <th class="px-4 py-3 font-medium">Port</th>
                    <th class="px-4 py-3 font-medium">Protocol</th>
                    <th class="px-4 py-3 font-medium">Process</th>
                    <th class="px-4 py-3 font-medium">PID</th>
                    <th class="px-4 py-3 font-medium">Bind Address</th>
                  </tr>
                </thead>
                <tbody class="divide-y divide-slate-200 bg-white">
                  {#each sortedPorts as port}
                    <tr>
                      <td class="px-4 py-3 font-medium text-slate-900">{port.port}</td>
                      <td class="px-4 py-3 text-slate-600">{port.protocol}</td>
                      <td class="px-4 py-3 text-slate-600">{port.process}</td>
                      <td class="px-4 py-3 text-slate-600">{port.pid}</td>
                      <td class="px-4 py-3 text-slate-600">{port.address}</td>
                    </tr>
                  {/each}
                </tbody>
              </table>
            </div>
          {/if}
        </Card>
      {/if}

      {#if activeTab === 'nginx'}
        <Card padding="lg">
          <div class="mb-4 flex items-center justify-between gap-3">
            <div>
              <h2 class="text-sm font-semibold uppercase tracking-wide text-slate-500">Nginx</h2>
              <p class="mt-1 text-sm text-slate-500">Virtual hosts and TLS certificate status.</p>
            </div>
            {#if snapshot?.nginx}
              <span class="text-sm text-slate-500">Nginx {snapshot.nginx.version || 'unknown'}</span>
            {/if}
          </div>

          {#if loadingSnapshot && !snapshot}
            <div class="flex items-center gap-3 rounded-xl border border-dashed border-slate-300 bg-slate-50 px-4 py-6 text-slate-600">
              <Spinner size="md" label="Loading Nginx data" />
              <div>
                <p class="font-medium text-slate-900">Loading Nginx snapshot...</p>
                <p class="text-sm text-slate-500">Please wait while the discovery scan finishes.</p>
              </div>
            </div>
          {:else if !snapshot?.nginx}
            <EmptyState title="Nginx not installed" description="No Nginx snapshot is available for this server." />
          {:else if !nginxVHosts.length}
            <EmptyState title="No virtual hosts detected" description="The latest snapshot did not find any Nginx virtual hosts." />
          {:else}
            <div class="space-y-3">
              {#each nginxVHosts as vhost}
                <Card padding="md">
                  <div class="grid gap-4 lg:grid-cols-4 lg:items-center">
                    <div>
                      <p class="text-xs font-semibold uppercase tracking-wide text-slate-500">Domain</p>
                      <p class="mt-1 font-medium text-slate-900">{vhost.serverName}</p>
                    </div>

                    <div>
                      <p class="text-xs font-semibold uppercase tracking-wide text-slate-500">Listen</p>
                      <p class="mt-1 text-sm text-slate-600">{vhost.listen}</p>
                    </div>

                    <div>
                      <p class="text-xs font-semibold uppercase tracking-wide text-slate-500">Root</p>
                      <p class="mt-1 text-sm text-slate-600 break-all">{vhost.root || '—'}</p>
                    </div>

                    <div>
                      <p class="text-xs font-semibold uppercase tracking-wide text-slate-500">Upstream</p>
                      <p class="mt-1 text-sm text-slate-600 break-all">{vhost.proxyPass || '—'}</p>
                    </div>
                  </div>
                </Card>
              {/each}
            </div>
          {/if}
        </Card>
      {/if}
    </div>
  {:else if loading}
    <div class="rounded-lg border border-dashed border-slate-300 bg-white px-4 py-8 text-center text-slate-500 shadow-sm">
      Loading server details...
    </div>
  {:else}
    <div class="rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">
      Server not found.
    </div>
  {/if}
</div>
