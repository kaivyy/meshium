<script lang="ts">
  import { goto } from '$app/navigation';
  import { page } from '$app/state';
  import { onDestroy } from 'svelte';
  import { ArrowLeft, ArrowRightLeft, Clock, Cpu, HardDrive, Key, MemoryStick, MoreVertical, Network, Play, RefreshCw, Shield, ShieldCheck, ShieldAlert, Trash2 } from 'lucide-svelte';
  import { api } from '$lib/api/client';
  import { discoveryApi, type ServerSnapshot, type ServerConnectionInfo } from '$lib/api/discovery';
  import { wsConnect, type WSMessage } from '$lib/api/websocket';
  import { Badge, Card, DropdownMenu, EmptyState, Spinner } from '$lib/components/ui';
  import { formatBytes, formatDateTime, formatDuration, formatRelativeTime } from '$lib/utils/format';
  import { toast } from '$lib/stores/toast';
  import { deleteServer, type Server, installKey, verifyKey, rotateKey, getFingerprint, testAuth, getConnectionHistory, getConnectionMetrics, removePassword, clearSSHKey, type KeyInstallResult, type KeyVerifyResult, type KeyRotationResult, type FingerprintResult, type AuthTestResult, type ConnectionHistoryEntry, type ConnectionMetrics } from '$lib/stores/servers';
  import { loadSnapshot as loadSnapshotFromStore, invalidateSnapshot } from '$lib/stores/snapshots';
  import { getSnapshot as getCachedSnapshot } from '$lib/stores/snapshots';

  type TabKey = 'overview' | 'ssh' | 'docker' | 'services' | 'databases' | 'network' | 'nginx';

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

  // SSH key management state
  let sshActionLoading = $state(false);
  let sshActionMessage = $state('');
  let sshActionError = $state('');
  let fingerprintData = $state<FingerprintResult | null>(null);
  let authTestResult = $state<AuthTestResult | null>(null);
  let connectionHistory = $state<ConnectionHistoryEntry[]>([]);
  let connectionMetrics = $state<ConnectionMetrics | null>(null);
  let installKeyResult = $state<KeyInstallResult | null>(null);
  let verifyKeyResult = $state<KeyVerifyResult | null>(null);
  let rotateKeyResult = $state<KeyRotationResult | null>(null);
  let installKeyType = $state<'rsa' | 'ed25519' | 'ecdsa'>('ed25519');
  let rotateKeyType = $state<'rsa' | 'ed25519' | 'ecdsa'>('ed25519');

  const tabs: { id: TabKey; label: string }[] = [
    { id: 'overview', label: 'Overview' },
    { id: 'ssh', label: 'SSH' },
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

  // --- SSH Key Management Actions ---

  async function handleInstallKey() {
    const id = serverId;
    if (!Number.isFinite(id)) return;
    sshActionLoading = true;
    sshActionMessage = '';
    sshActionError = '';
    installKeyResult = null;
    try {
      const result = await installKey(id, installKeyType);
      installKeyResult = result;
      if (result.success) {
        sshActionMessage = result.alreadyInstalled
          ? `Key already installed. Fingerprint: ${result.fingerprint}`
          : `Key installed successfully. Fingerprint: ${result.fingerprint}`;
        toast.success('SSH key installed');
        await loadServer(id);
      } else {
        sshActionError = result.message || 'Failed to install key';
      }
    } catch (e) {
      sshActionError = e instanceof Error ? e.message : 'Failed to install key';
    } finally {
      sshActionLoading = false;
    }
  }

  async function handleVerifyKey() {
    const id = serverId;
    if (!Number.isFinite(id)) return;
    sshActionLoading = true;
    sshActionMessage = '';
    sshActionError = '';
    verifyKeyResult = null;
    try {
      const result = await verifyKey(id);
      verifyKeyResult = result;
      if (result.success) {
        sshActionMessage = `Key verified. Installed: ${result.installed ? 'Yes' : 'No'}. Fingerprint: ${result.fingerprint}`;
        toast.success('Key verified');
      } else {
        sshActionError = result.message || 'Key verification failed';
      }
    } catch (e) {
      sshActionError = e instanceof Error ? e.message : 'Failed to verify key';
    } finally {
      sshActionLoading = false;
    }
  }

  async function handleRotateKey() {
    const id = serverId;
    if (!Number.isFinite(id)) return;
    if (!confirm('Rotate the SSH key? The old key will be removed from the server.')) return;
    sshActionLoading = true;
    sshActionMessage = '';
    sshActionError = '';
    rotateKeyResult = null;
    try {
      const result = await rotateKey(id, rotateKeyType);
      rotateKeyResult = result;
      if (result.success) {
        sshActionMessage = `Key rotated. New fingerprint: ${result.newFingerprint}`;
        toast.success('SSH key rotated');
        await loadServer(id);
      } else {
        sshActionError = result.message || 'Failed to rotate key';
      }
    } catch (e) {
      sshActionError = e instanceof Error ? e.message : 'Failed to rotate key';
    } finally {
      sshActionLoading = false;
    }
  }

  async function handleTestAuth() {
    const id = serverId;
    if (!Number.isFinite(id)) return;
    sshActionLoading = true;
    sshActionMessage = '';
    sshActionError = '';
    authTestResult = null;
    try {
      const result = await testAuth(id);
      authTestResult = result;
      if (result.success) {
        sshActionMessage = `Authentication successful via ${result.authMethod} in ${result.latencyMs}ms`;
        toast.success('Authentication test passed');
      } else {
        sshActionError = result.message || 'Authentication test failed';
      }
    } catch (e) {
      sshActionError = e instanceof Error ? e.message : 'Failed to test authentication';
    } finally {
      sshActionLoading = false;
    }
  }

  async function handleLoadFingerprint() {
    const id = serverId;
    if (!Number.isFinite(id)) return;
    sshActionLoading = true;
    try {
      fingerprintData = await getFingerprint(id);
    } catch (e) {
      sshActionError = e instanceof Error ? e.message : 'Failed to get fingerprint';
    } finally {
      sshActionLoading = false;
    }
  }

  async function handleLoadHistory() {
    const id = serverId;
    if (!Number.isFinite(id)) return;
    try {
      connectionHistory = await getConnectionHistory(id, 50);
      connectionMetrics = await getConnectionMetrics(id);
    } catch {
      // ignore
    }
  }

  async function handleRemovePassword() {
    const id = serverId;
    if (!Number.isFinite(id)) return;
    if (!confirm('Remove the stored password? Key-based authentication will be required.')) return;
    try {
      await removePassword(id);
      toast.success('Password removed');
      await loadServer(id);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : 'Failed to remove password');
    }
  }

  async function handleClearKey() {
    const id = serverId;
    if (!Number.isFinite(id)) return;
    if (!confirm('Clear the stored SSH key? You will need password authentication to reconnect.')) return;
    try {
      await clearSSHKey(id);
      toast.success('SSH key cleared');
      await loadServer(id);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : 'Failed to clear key');
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
  <span class="inline-flex items-center justify-center gap-2 rounded-lg border border-border-strong bg-surface px-4 py-2 text-sm font-medium text-fg-muted transition hover:bg-surface-muted">
    <ArrowRightLeft size={16} />
    Migrate
  </span>
{/snippet}

{#snippet moreTrigger()}
  <span class="inline-flex items-center justify-center gap-2 rounded-lg border border-border-strong bg-surface px-4 py-2 text-sm font-medium text-fg-muted transition hover:bg-surface-muted">
    <MoreVertical size={16} />
    More
  </span>
{/snippet}

<div class="p-4 sm:p-6 max-w-6xl mx-auto">
  <a href="/" class="mb-3 inline-flex items-center gap-2 text-sm text-fg-muted transition hover:text-fg">
    <ArrowLeft size={16} /> Back to Servers
  </a>

  {#if server}
    <div class="mb-6 flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
      <div>
        <div class="flex items-center gap-3">
          <h1 class="text-2xl font-bold tracking-tight text-fg">{server.name}</h1>
          <span class="rounded-full bg-surface-muted px-2.5 py-1 text-xs font-medium text-fg-muted">
            {server.environment || 'no environment'}
          </span>
          {#if server.credentialStatus === 'valid'}
            <span class="inline-flex items-center gap-1 rounded-full bg-success/15 px-2.5 py-1 text-xs font-medium text-success">
              <ShieldCheck size={12} /> Valid
            </span>
          {:else if server.credentialStatus === 'invalid' || server.credentialStatus === 'expired' || server.credentialStatus === 'locked'}
            <span class="inline-flex items-center gap-1 rounded-full bg-error/15 px-2.5 py-1 text-xs font-medium text-error">
              <ShieldAlert size={12} /> {server.credentialStatus}
            </span>
          {/if}
        </div>
        <p class="mt-1 text-sm text-fg-subtle">
          {server.host}:{server.port} · {server.username}
        </p>
        {#if snapshot}
          <p class="mt-1 text-xs text-fg-subtle">Last scanned: {lastScanned}</p>
        {/if}
        {#if server.description}
          <p class="mt-2 max-w-3xl text-sm text-fg-muted">{server.description}</p>
        {/if}
      </div>

      <div class="flex flex-wrap items-center gap-3">
        <button
          type="button"
          onclick={handleConnect}
          disabled={connecting}
          class="inline-flex items-center justify-center gap-2 rounded-lg border border-border-strong bg-surface px-4 py-2 text-sm font-medium text-fg-muted transition hover:bg-surface-muted disabled:cursor-not-allowed disabled:opacity-50"
        >
          <Play size={18} />
          {connecting ? 'Connecting...' : 'Test Connection'}
        </button>

        <DropdownMenu items={buildMigrateMenuItems(server.id)} label="Migrate this server" trigger={migrateTrigger} />
        <DropdownMenu items={buildMoreMenuItems(server.id)} label="More actions" trigger={moreTrigger} />
      </div>
    </div>

    <div class="mb-6 border-b border-border">
      <div class="-mb-px flex flex-wrap gap-2" role="tablist" aria-label="Server detail tabs">
        {#each tabs as tab}
          <button
            type="button"
            role="tab"
            onclick={() => (activeTab = tab.id)}
            class={`rounded-t-lg border px-4 py-2 text-sm font-medium transition ${activeTab === tab.id
              ? 'border-border border-b-surface bg-surface text-fg'
              : 'border-transparent text-fg-subtle hover:bg-surface-muted hover:text-fg'}`}
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
                <h2 class="text-sm font-semibold uppercase tracking-wide text-fg-subtle">Connection Test</h2>
                <p class="mt-1 text-sm text-fg-subtle">Streaming connection steps from the backend.</p>
              </div>
              {#if connecting}
                <span class="text-sm text-fg-subtle">Running...</span>
              {/if}
            </div>

            {#if wsError}
              <div class="mb-4 rounded-xl border border-error bg-error/10 px-4 py-3 text-sm text-error" role="alert">
                <p class="font-semibold text-error">Connection test failed</p>
                <p class="mt-1">{wsError}</p>
              </div>
            {/if}

            {#if wsSteps.length > 0}
              <div class="space-y-2">
                {#each wsSteps as step}
                  <div class="flex flex-wrap items-center gap-2 rounded-lg bg-surface-muted px-3 py-2 text-sm">
                    <span class={`h-2.5 w-2.5 rounded-full ${step.status === 'success' ? 'bg-success' : 'bg-error'}`}></span>
                    <span class="font-mono text-fg-muted">{step.step}</span>
                    <span class="text-fg-subtle">·</span>
                    <span class="text-fg-subtle">{step.status}</span>
                    {#if step.value}
                      <span class="break-all text-fg-subtle">→ {step.value}</span>
                    {/if}
                    {#if step.error}
                      <span class="break-all text-error">→ {step.error}</span>
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
              <h2 class="text-sm font-semibold uppercase tracking-wide text-fg-subtle">System Information</h2>
              <p class="mt-1 text-sm text-fg-subtle">Cached data from the latest successful connection test.</p>
            </div>
            <div class="flex flex-wrap items-center gap-2">
              <button
                type="button"
                onclick={handleRescan}
                disabled={rescanning || loadingSnapshot}
                class="inline-flex items-center justify-center gap-2 rounded-lg border border-border-strong bg-surface px-4 py-2 text-sm font-medium text-fg-muted transition hover:bg-surface-muted disabled:cursor-not-allowed disabled:opacity-50"
              >
                <RefreshCw size={16} />
                {rescanning ? 'Re-scanning...' : 'Re-scan'}
              </button>
              <button
                type="button"
                onclick={openComparePage}
                class="inline-flex items-center justify-center rounded-lg bg-accent px-4 py-2 text-sm font-medium text-accent-fg transition hover:bg-accent-hover"
              >
                Compare with another server
              </button>
            </div>
          </div>

          {#if loadingInfo && !info}
            <div class="flex items-center gap-3 rounded-xl border border-dashed border-border-strong bg-surface-muted px-4 py-6 text-fg-muted">
              <Spinner size="md" label="Loading system information" />
              <div>
                <p class="font-medium text-fg">Loading system information...</p>
                <p class="text-sm text-fg-subtle">Refreshing the latest connection snapshot.</p>
              </div>
            </div>
          {:else if info}
            <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3">
              <div class="rounded-xl border border-border bg-surface-muted p-4">
                <div class="mb-2 flex items-center gap-2 text-fg-subtle">
                  <Cpu size={18} />
                  <span class="text-sm font-semibold text-fg">CPU</span>
                </div>
                <p class="text-sm text-fg-muted">{info.cpuModel || 'Unknown'}</p>
                <p class="mt-1 text-xs text-fg-subtle">{info.cpuCores || 0} cores</p>
              </div>

              <div class="rounded-xl border border-border bg-surface-muted p-4">
                <div class="mb-2 flex items-center gap-2 text-fg-subtle">
                  <MemoryStick size={18} />
                  <span class="text-sm font-semibold text-fg">Memory</span>
                </div>
                <p class="text-sm text-fg-muted">{info.ramTotalMb || 0} MB</p>
                <p class="mt-1 text-xs text-fg-subtle">{info.virtualization || 'Unknown virtualization'}</p>
              </div>

              <div class="rounded-xl border border-border bg-surface-muted p-4">
                <div class="mb-2 flex items-center gap-2 text-fg-subtle">
                  <HardDrive size={18} />
                  <span class="text-sm font-semibold text-fg">Storage</span>
                </div>
                <p class="text-sm text-fg-muted">{info.diskTotalGb || 0} GB</p>
                <p class="mt-1 text-xs text-fg-subtle">Latency: {info.latencyMs || 0} ms</p>
              </div>

              <div class="rounded-xl border border-border bg-surface-muted p-4">
                <div class="mb-2 flex items-center gap-2 text-fg-subtle">
                  <Network size={18} />
                  <span class="text-sm font-semibold text-fg">Network</span>
                </div>
                <p class="text-sm text-fg-muted">Public: {info.publicIp || 'Unknown'}</p>
                <p class="mt-1 text-sm text-fg-muted">Private: {info.privateIp || 'Unknown'}</p>
              </div>

              <div class="rounded-xl border border-border bg-surface-muted p-4">
                <div class="mb-2 flex items-center gap-2 text-fg-subtle">
                  <Clock size={18} />
                  <span class="text-sm font-semibold text-fg">System</span>
                </div>
                <p class="text-sm text-fg-muted">{info.os || 'Unknown OS'}</p>
                <p class="mt-1 text-xs text-fg-subtle">Kernel {info.kernel || 'Unknown'} · {info.architecture || 'Unknown'}</p>
              </div>

              <div class="rounded-xl border border-border bg-surface-muted p-4">
                <div class="mb-2 flex items-center gap-2 text-fg-subtle">
                  <span class="text-sm font-semibold text-fg">Status</span>
                </div>
                <p class="text-sm text-fg-muted">SSH: {info.sshStatus || 'Unknown'}</p>
                <p class="mt-1 text-sm text-fg-muted">Timezone: {info.timezone || 'Unknown'}</p>
                <p class="mt-1 text-sm text-fg-muted">Provider: {info.provider || 'Unknown'}</p>
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

      {#if activeTab === 'ssh'}
        <div class="space-y-6">
          {#if sshActionMessage}
            <div class="rounded-lg border border-success bg-success/10 px-4 py-3 text-sm text-success">
              {sshActionMessage}
            </div>
          {/if}
          {#if sshActionError}
            <div class="rounded-lg border border-error bg-error/10 px-4 py-3 text-sm text-error">
              {sshActionError}
            </div>
          {/if}

          <!-- Credential Status -->
          <Card padding="lg">
            <div class="mb-4 flex items-center gap-2">
              <Shield size={20} class="text-fg-subtle" />
              <h2 class="text-sm font-semibold uppercase tracking-wide text-fg-subtle">Credential Status</h2>
            </div>
            <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
              <div class="rounded-xl border border-border bg-surface-muted p-4">
                <p class="text-xs font-semibold uppercase tracking-wide text-fg-subtle">Auth Method</p>
                <p class="mt-1 text-sm font-medium text-fg">{server.authMethod || 'password'}</p>
              </div>
              <div class="rounded-xl border border-border bg-surface-muted p-4">
                <p class="text-xs font-semibold uppercase tracking-wide text-fg-subtle">Status</p>
                <div class="mt-1 flex items-center gap-2">
                  {#if server.credentialStatus === 'valid'}
                    <ShieldCheck size={16} class="text-success" />
                    <span class="text-sm font-medium text-success">Valid</span>
                  {:else if server.credentialStatus === 'invalid' || server.credentialStatus === 'expired' || server.credentialStatus === 'locked'}
                    <ShieldAlert size={16} class="text-error" />
                    <span class="text-sm font-medium text-error">{server.credentialStatus}</span>
                  {:else}
                    <Shield size={16} class="text-fg-subtle" />
                    <span class="text-sm font-medium text-fg-subtle">Unknown</span>
                  {/if}
                </div>
              </div>
              <div class="rounded-xl border border-border bg-surface-muted p-4">
                <p class="text-xs font-semibold uppercase tracking-wide text-fg-subtle">Key Type</p>
                <p class="mt-1 text-sm font-medium text-fg">{server.keyType || '—'}</p>
              </div>
              <div class="rounded-xl border border-border bg-surface-muted p-4">
                <p class="text-xs font-semibold uppercase tracking-wide text-fg-subtle">Fingerprint</p>
                <p class="mt-1 break-all text-xs font-mono text-fg-muted">{server.fingerprint || '—'}</p>
              </div>
            </div>
            {#if server.lastSuccess || server.lastFailure}
              <div class="mt-4 grid grid-cols-1 gap-4 sm:grid-cols-2">
                <div class="text-sm text-fg-muted">
                  <span class="font-medium">Last success:</span> {server.lastSuccess ? formatRelativeTime(server.lastSuccess) : 'Never'}
                </div>
                <div class="text-sm text-fg-muted">
                  <span class="font-medium">Last failure:</span> {server.lastFailure ? formatRelativeTime(server.lastFailure) : 'Never'}
                </div>
              </div>
            {/if}
          </Card>

          <!-- Key Management Actions -->
          <Card padding="lg">
            <div class="mb-4 flex items-center gap-2">
              <Key size={20} class="text-fg-subtle" />
              <h2 class="text-sm font-semibold uppercase tracking-wide text-fg-subtle">Key Management</h2>
            </div>
            <div class="space-y-4">
              <!-- Install Key -->
              <div class="rounded-xl border border-border bg-surface-muted p-4">
                <div class="flex flex-wrap items-center justify-between gap-3">
                  <div>
                    <p class="text-sm font-medium text-fg">Install SSH Key</p>
                    <p class="mt-1 text-xs text-fg-subtle">Generate a new key pair and install the public key on the server using password auth</p>
                  </div>
                  <div class="flex items-center gap-2">
                    <select
                      bind:value={installKeyType}
                      class="rounded-lg border border-border-strong bg-surface px-3 py-2 text-sm text-fg outline-none focus:border-accent focus:ring-2 focus:ring-accent/20"
                    >
                      <option value="ed25519">ED25519</option>
                      <option value="rsa">RSA</option>
                      <option value="ecdsa">ECDSA</option>
                    </select>
                    <button
                      type="button"
                      onclick={handleInstallKey}
                      disabled={sshActionLoading}
                      class="inline-flex items-center justify-center gap-2 rounded-lg bg-accent px-4 py-2 text-sm font-medium text-accent-fg transition hover:bg-accent-hover disabled:cursor-not-allowed disabled:opacity-50"
                    >
                      {sshActionLoading ? 'Working...' : 'Install Key'}
                    </button>
                  </div>
                </div>
                {#if installKeyResult}
                  <div class="mt-3 rounded-lg bg-surface px-3 py-2 text-sm">
                    <p class="text-fg-muted">Fingerprint: <code class="font-mono text-xs">{installKeyResult.fingerprint}</code></p>
                    <p class="text-fg-muted">Key type: {installKeyResult.keyType}</p>
                    {#if installKeyResult.alreadyInstalled}
                      <p class="text-warning">Key was already installed</p>
                    {/if}
                  </div>
                {/if}
              </div>

              <!-- Verify Key -->
              <div class="rounded-xl border border-border bg-surface-muted p-4">
                <div class="flex flex-wrap items-center justify-between gap-3">
                  <div>
                    <p class="text-sm font-medium text-fg">Verify SSH Key</p>
                    <p class="mt-1 text-xs text-fg-subtle">Test that the stored key can authenticate and is installed on the server</p>
                  </div>
                  <button
                    type="button"
                    onclick={handleVerifyKey}
                    disabled={sshActionLoading}
                    class="inline-flex items-center justify-center gap-2 rounded-lg border border-border-strong bg-surface px-4 py-2 text-sm font-medium text-fg-muted transition hover:bg-surface-muted disabled:cursor-not-allowed disabled:opacity-50"
                  >
                    Verify Key
                  </button>
                </div>
                {#if verifyKeyResult}
                  <div class="mt-3 rounded-lg bg-surface px-3 py-2 text-sm">
                    <p class="text-fg-muted">Installed: <span class="font-medium">{verifyKeyResult.installed ? 'Yes' : 'No'}</span></p>
                    <p class="text-fg-muted">Fingerprint: <code class="font-mono text-xs">{verifyKeyResult.fingerprint}</code></p>
                  </div>
                {/if}
              </div>

              <!-- Rotate Key -->
              <div class="rounded-xl border border-border bg-surface-muted p-4">
                <div class="flex flex-wrap items-center justify-between gap-3">
                  <div>
                    <p class="text-sm font-medium text-fg">Rotate SSH Key</p>
                    <p class="mt-1 text-xs text-fg-subtle">Generate a new key, install it, and remove the old key from the server</p>
                  </div>
                  <div class="flex items-center gap-2">
                    <select
                      bind:value={rotateKeyType}
                      class="rounded-lg border border-border-strong bg-surface px-3 py-2 text-sm text-fg outline-none focus:border-accent focus:ring-2 focus:ring-accent/20"
                    >
                      <option value="ed25519">ED25519</option>
                      <option value="rsa">RSA</option>
                      <option value="ecdsa">ECDSA</option>
                    </select>
                    <button
                      type="button"
                      onclick={handleRotateKey}
                      disabled={sshActionLoading}
                      class="inline-flex items-center justify-center gap-2 rounded-lg border border-warning bg-warning/10 px-4 py-2 text-sm font-medium text-warning transition hover:bg-warning/20 disabled:cursor-not-allowed disabled:opacity-50"
                    >
                      Rotate Key
                    </button>
                  </div>
                </div>
                {#if rotateKeyResult}
                  <div class="mt-3 rounded-lg bg-surface px-3 py-2 text-sm">
                    <p class="text-fg-muted">Old fingerprint: <code class="font-mono text-xs">{rotateKeyResult.oldFingerprint}</code></p>
                    <p class="text-fg-muted">New fingerprint: <code class="font-mono text-xs">{rotateKeyResult.newFingerprint}</code></p>
                  </div>
                {/if}
              </div>

              <!-- Test Auth -->
              <div class="rounded-xl border border-border bg-surface-muted p-4">
                <div class="flex flex-wrap items-center justify-between gap-3">
                  <div>
                    <p class="text-sm font-medium text-fg">Test Authentication</p>
                    <p class="mt-1 text-xs text-fg-subtle">Attempt to connect using the configured credentials and record the result</p>
                  </div>
                  <button
                    type="button"
                    onclick={handleTestAuth}
                    disabled={sshActionLoading}
                    class="inline-flex items-center justify-center gap-2 rounded-lg border border-border-strong bg-surface px-4 py-2 text-sm font-medium text-fg-muted transition hover:bg-surface-muted disabled:cursor-not-allowed disabled:opacity-50"
                  >
                    <Play size={16} />
                    Test Auth
                  </button>
                </div>
                {#if authTestResult}
                  <div class="mt-3 rounded-lg bg-surface px-3 py-2 text-sm">
                    <p class={authTestResult.success ? 'text-success' : 'text-error'}>
                      {authTestResult.success ? 'Success' : 'Failed'} via {authTestResult.authMethod} in {authTestResult.latencyMs}ms
                    </p>
                    {#if authTestResult.message}
                      <p class="text-fg-subtle">{authTestResult.message}</p>
                    {/if}
                  </div>
                {/if}
              </div>

              <!-- Fingerprint -->
              <div class="rounded-xl border border-border bg-surface-muted p-4">
                <div class="flex flex-wrap items-center justify-between gap-3">
                  <div>
                    <p class="text-sm font-medium text-fg">View Fingerprint</p>
                    <p class="mt-1 text-xs text-fg-subtle">Display the SHA256 and MD5 fingerprints of the stored key</p>
                  </div>
                  <button
                    type="button"
                    onclick={handleLoadFingerprint}
                    disabled={sshActionLoading}
                    class="inline-flex items-center justify-center gap-2 rounded-lg border border-border-strong bg-surface px-4 py-2 text-sm font-medium text-fg-muted transition hover:bg-surface-muted disabled:cursor-not-allowed disabled:opacity-50"
                  >
                    Get Fingerprint
                  </button>
                </div>
                {#if fingerprintData}
                  <div class="mt-3 space-y-1 rounded-lg bg-surface px-3 py-2 text-sm">
                    <p class="text-fg-muted">SHA256: <code class="font-mono text-xs break-all">{fingerprintData.sha256}</code></p>
                    <p class="text-fg-muted">MD5: <code class="font-mono text-xs break-all">{fingerprintData.md5}</code></p>
                    <p class="text-fg-muted">Type: {fingerprintData.keyType}</p>
                  </div>
                {/if}
              </div>
            </div>
          </Card>

          <!-- Danger Zone -->
          <Card padding="lg">
            <div class="mb-4 flex items-center gap-2">
              <Trash2 size={20} class="text-error" />
              <h2 class="text-sm font-semibold uppercase tracking-wide text-error">Danger Zone</h2>
            </div>
            <div class="space-y-3">
              <div class="flex flex-wrap items-center justify-between gap-3 rounded-xl border border-border bg-surface-muted p-4">
                <div>
                  <p class="text-sm font-medium text-fg">Remove Password</p>
                  <p class="mt-1 text-xs text-fg-subtle">Delete the stored password — key auth will be required</p>
                </div>
                <button
                  type="button"
                  onclick={handleRemovePassword}
                  class="inline-flex items-center justify-center gap-2 rounded-lg border border-error bg-surface px-4 py-2 text-sm font-medium text-error transition hover:bg-error/10"
                >
                  Remove
                </button>
              </div>
              <div class="flex flex-wrap items-center justify-between gap-3 rounded-xl border border-border bg-surface-muted p-4">
                <div>
                  <p class="text-sm font-medium text-fg">Clear SSH Key</p>
                  <p class="mt-1 text-xs text-fg-subtle">Delete the stored SSH key — password auth will be required</p>
                </div>
                <button
                  type="button"
                  onclick={handleClearKey}
                  class="inline-flex items-center justify-center gap-2 rounded-lg border border-error bg-surface px-4 py-2 text-sm font-medium text-error transition hover:bg-error/10"
                >
                  Clear
                </button>
              </div>
            </div>
          </Card>

          <!-- Connection History -->
          <Card padding="lg">
            <div class="mb-4 flex items-center justify-between gap-3">
              <div class="flex items-center gap-2">
                <Clock size={20} class="text-fg-subtle" />
                <h2 class="text-sm font-semibold uppercase tracking-wide text-fg-subtle">Connection History</h2>
              </div>
              <button
                type="button"
                onclick={handleLoadHistory}
                class="inline-flex items-center justify-center gap-2 rounded-lg border border-border-strong bg-surface px-4 py-2 text-sm font-medium text-fg-muted transition hover:bg-surface-muted"
              >
                Load History
              </button>
            </div>
            {#if connectionMetrics}
              <div class="mb-4 grid grid-cols-2 gap-4 sm:grid-cols-4">
                <div class="rounded-xl border border-border bg-surface-muted p-3">
                  <p class="text-xs font-semibold uppercase tracking-wide text-fg-subtle">Total</p>
                  <p class="mt-1 text-lg font-bold text-fg">{connectionMetrics.totalAttempts}</p>
                </div>
                <div class="rounded-xl border border-border bg-surface-muted p-3">
                  <p class="text-xs font-semibold uppercase tracking-wide text-fg-subtle">Success</p>
                  <p class="mt-1 text-lg font-bold text-success">{connectionMetrics.successCount}</p>
                </div>
                <div class="rounded-xl border border-border bg-surface-muted p-3">
                  <p class="text-xs font-semibold uppercase tracking-wide text-fg-subtle">Failed</p>
                  <p class="mt-1 text-lg font-bold text-error">{connectionMetrics.failureCount}</p>
                </div>
                <div class="rounded-xl border border-border bg-surface-muted p-3">
                  <p class="text-xs font-semibold uppercase tracking-wide text-fg-subtle">Success Rate</p>
                  <p class="mt-1 text-lg font-bold text-fg">{(connectionMetrics.successRate * 100).toFixed(1)}%</p>
                </div>
              </div>
            {/if}
            {#if connectionHistory.length > 0}
              <div class="overflow-hidden rounded-xl border border-border bg-surface shadow-sm">
                <table class="min-w-full divide-y divide-border text-left text-sm">
                  <thead class="bg-surface-muted text-fg-subtle">
                    <tr>
                      <th class="px-4 py-3 font-medium">Time</th>
                      <th class="px-4 py-3 font-medium">Result</th>
                      <th class="px-4 py-3 font-medium">Method</th>
                      <th class="px-4 py-3 font-medium">Duration</th>
                      <th class="px-4 py-3 font-medium">Reason</th>
                    </tr>
                  </thead>
                  <tbody class="divide-y divide-border bg-surface">
                    {#each connectionHistory as entry}
                      <tr>
                        <td class="px-4 py-3 text-fg-muted">{formatRelativeTime(entry.createdAt)}</td>
                        <td class="px-4 py-3">
                          {#if entry.success}
                            <Badge variant="success">Success</Badge>
                          {:else}
                            <Badge variant="error">Failed</Badge>
                          {/if}
                        </td>
                        <td class="px-4 py-3 text-fg-muted">{entry.authMethod || '—'}</td>
                        <td class="px-4 py-3 text-fg-muted">{entry.durationMs ? `${entry.durationMs}ms` : '—'}</td>
                        <td class="px-4 py-3 text-fg-muted">{entry.reason || '—'}</td>
                      </tr>
                    {/each}
                  </tbody>
                </table>
              </div>
            {:else if connectionMetrics}
              <EmptyState title="No connection history" description="No connection attempts have been recorded yet." />
            {/if}
          </Card>
        </div>
      {/if}

      {#if activeTab === 'docker'}
        <Card padding="lg">
          <div class="mb-4 flex items-center justify-between gap-3">
            <div>
              <h2 class="text-sm font-semibold uppercase tracking-wide text-fg-subtle">Docker</h2>
              <p class="mt-1 text-sm text-fg-subtle">Detected containers, images, and compose projects.</p>
            </div>
            {#if snapshot?.docker}
              <span class="text-sm text-fg-subtle">Docker {snapshot.docker.version || 'unknown'}</span>
            {/if}
          </div>

          {#if loadingSnapshot && !snapshot}
            <div class="flex items-center gap-3 rounded-xl border border-dashed border-border-strong bg-surface-muted px-4 py-6 text-fg-muted">
              <Spinner size="md" label="Loading Docker data" />
              <div>
                <p class="font-medium text-fg">Loading Docker snapshot...</p>
                <p class="text-sm text-fg-subtle">Please wait while discovery data is retrieved.</p>
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
                <h3 class="mb-3 text-sm font-semibold uppercase tracking-wide text-fg-subtle">Containers</h3>
                {#if dockerContainers.length}
                  <div class="overflow-hidden rounded-xl border border-border bg-surface shadow-sm">
                    <table class="min-w-full divide-y divide-border text-left text-sm">
                      <thead class="bg-surface-muted text-fg-subtle">
                        <tr>
                          <th class="px-4 py-3 font-medium">Name</th>
                          <th class="px-4 py-3 font-medium">Image</th>
                          <th class="px-4 py-3 font-medium">Status</th>
                          <th class="px-4 py-3 font-medium">Ports</th>
                          <th class="px-4 py-3 font-medium">Uptime</th>
                        </tr>
                      </thead>
                      <tbody class="divide-y divide-border bg-surface">
                        {#each dockerContainers as container}
                          <tr>
                            <td class="px-4 py-3 font-medium text-fg">{container.name}</td>
                            <td class="px-4 py-3 text-fg-muted">{container.image}</td>
                            <td class="px-4 py-3">
                              <Badge variant={containerVariant(container.state)}>{formatSummaryLabel(container.state)}</Badge>
                            </td>
                            <td class="px-4 py-3 text-fg-muted">{containerPorts(container.ports)}</td>
                            <td class="px-4 py-3 text-fg-muted">{containerUptime(container.status)}</td>
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
                <h3 class="mb-3 text-sm font-semibold uppercase tracking-wide text-fg-subtle">Images</h3>
                {#if dockerImages.length}
                  <div class="overflow-hidden rounded-xl border border-border bg-surface shadow-sm">
                    <table class="min-w-full divide-y divide-border text-left text-sm">
                      <thead class="bg-surface-muted text-fg-subtle">
                        <tr>
                          <th class="px-4 py-3 font-medium">Name</th>
                          <th class="px-4 py-3 font-medium">Tag</th>
                          <th class="px-4 py-3 font-medium">Size</th>
                        </tr>
                      </thead>
                      <tbody class="divide-y divide-border bg-surface">
                        {#each dockerImages as image}
                          <tr>
                            <td class="px-4 py-3 font-medium text-fg">{image.repository}</td>
                            <td class="px-4 py-3 text-fg-muted">{image.tag}</td>
                            <td class="px-4 py-3 text-fg-muted">{image.size}</td>
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
                  <h3 class="mb-3 text-sm font-semibold uppercase tracking-wide text-fg-subtle">Compose Projects</h3>
                  <div class="grid gap-4 md:grid-cols-2">
                    {#each composeProjects as project}
                      <Card padding="md">
                        <div class="flex items-start justify-between gap-4">
                          <div>
                            <p class="font-medium text-fg">{project.name}</p>
                            <p class="mt-1 text-sm text-fg-subtle">Config: {project.configFiles}</p>
                          </div>
                        </div>
                        <p class="mt-3 text-sm text-fg-muted">Services: {formatComposeServices(project.services)}</p>
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
              <h2 class="text-sm font-semibold uppercase tracking-wide text-fg-subtle">Services</h2>
              <p class="mt-1 text-sm text-fg-subtle">Systemd services discovered on the server.</p>
            </div>
            <label class="inline-flex items-center gap-2 text-sm text-fg-muted">
              <input
                type="checkbox"
                bind:checked={showOnlyActive}
                class="h-4 w-4 rounded border-border-strong text-accent focus:ring-accent"
              />
              Show only active
            </label>
          </div>

          {#if loadingSnapshot && !snapshot}
            <div class="flex items-center gap-3 rounded-xl border border-dashed border-border-strong bg-surface-muted px-4 py-6 text-fg-muted">
              <Spinner size="md" label="Loading services" />
              <div>
                <p class="font-medium text-fg">Loading services...</p>
                <p class="text-sm text-fg-subtle">Waiting for discovery data.</p>
              </div>
            </div>
          {:else if !snapshot?.services.length}
            <EmptyState title="No services detected" description="No systemd services were found in the latest snapshot." />
          {:else if !visibleServices.length}
            <EmptyState title="No active services" description="Toggle the filter off to view inactive services." />
          {:else}
            <div class="overflow-hidden rounded-xl border border-border bg-surface shadow-sm">
              <table class="min-w-full divide-y divide-border text-left text-sm">
                <thead class="bg-surface-muted text-fg-subtle">
                  <tr>
                    <th class="px-4 py-3 font-medium">Name</th>
                    <th class="px-4 py-3 font-medium">Load State</th>
                    <th class="px-4 py-3 font-medium">Active State</th>
                    <th class="px-4 py-3 font-medium">Type</th>
                    <th class="px-4 py-3 font-medium">Description</th>
                  </tr>
                </thead>
                <tbody class="divide-y divide-border bg-surface">
                  {#each visibleServices as service}
                    <tr>
                      <td class="px-4 py-3 font-medium text-fg">{service.name}</td>
                      <td class="px-4 py-3 text-fg-muted">{service.loadState}</td>
                      <td class="px-4 py-3"><Badge variant={statusVariant(service.activeState)}>{service.activeState}</Badge></td>
                      <td class="px-4 py-3 text-fg-muted">{service.type}</td>
                      <td class="px-4 py-3 text-fg-muted">{service.description}</td>
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
            <h2 class="text-sm font-semibold uppercase tracking-wide text-fg-subtle">Databases</h2>
            <p class="mt-1 text-sm text-fg-subtle">Detected database instances and storage paths.</p>
          </div>

          {#if loadingSnapshot && !snapshot}
            <div class="flex items-center gap-3 rounded-xl border border-dashed border-border-strong bg-surface-muted px-4 py-6 text-fg-muted">
              <Spinner size="md" label="Loading database data" />
              <div>
                <p class="font-medium text-fg">Loading databases...</p>
                <p class="text-sm text-fg-subtle">Please wait while discovery data loads.</p>
              </div>
            </div>
          {:else if !databases.length}
            <EmptyState title="No databases detected" description="The latest snapshot did not find any database instances." />
          {:else}
            <div class="overflow-hidden rounded-xl border border-border bg-surface shadow-sm">
              <table class="min-w-full divide-y divide-border text-left text-sm">
                <thead class="bg-surface-muted text-fg-subtle">
                  <tr>
                    <th class="px-4 py-3 font-medium">Type</th>
                    <th class="px-4 py-3 font-medium">Version</th>
                    <th class="px-4 py-3 font-medium">Port</th>
                    <th class="px-4 py-3 font-medium">Data Directory</th>
                    <th class="px-4 py-3 font-medium">Size</th>
                  </tr>
                </thead>
                <tbody class="divide-y divide-border bg-surface">
                  {#each databases as database}
                    <tr>
                      <td class="px-4 py-3"><Badge variant={databaseVariant(database.type)}>{databaseLabel(database.type)}</Badge></td>
                      <td class="px-4 py-3 text-fg-muted">{database.version}</td>
                      <td class="px-4 py-3 text-fg-muted">{database.port}</td>
                      <td class="px-4 py-3 text-fg-muted">{database.dataDir}</td>
                      <td class="px-4 py-3 text-fg-muted">{formatDbSize(database.sizeMb)}</td>
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
            <h2 class="text-sm font-semibold uppercase tracking-wide text-fg-subtle">Network</h2>
            <p class="mt-1 text-sm text-fg-subtle">Open ports sorted by port number.</p>
          </div>

          {#if loadingSnapshot && !snapshot}
            <div class="flex items-center gap-3 rounded-xl border border-dashed border-border-strong bg-surface-muted px-4 py-6 text-fg-muted">
              <Spinner size="md" label="Loading network data" />
              <div>
                <p class="font-medium text-fg">Loading open ports...</p>
                <p class="text-sm text-fg-subtle">Discovery data is still being fetched.</p>
              </div>
            </div>
          {:else if !sortedPorts.length}
            <EmptyState title="No open ports detected" description="No listening ports were found in the latest snapshot." />
          {:else}
            <div class="overflow-hidden rounded-xl border border-border bg-surface shadow-sm">
              <table class="min-w-full divide-y divide-border text-left text-sm">
                <thead class="bg-surface-muted text-fg-subtle">
                  <tr>
                    <th class="px-4 py-3 font-medium">Port</th>
                    <th class="px-4 py-3 font-medium">Protocol</th>
                    <th class="px-4 py-3 font-medium">Process</th>
                    <th class="px-4 py-3 font-medium">PID</th>
                    <th class="px-4 py-3 font-medium">Bind Address</th>
                  </tr>
                </thead>
                <tbody class="divide-y divide-border bg-surface">
                  {#each sortedPorts as port}
                    <tr>
                      <td class="px-4 py-3 font-medium text-fg">{port.port}</td>
                      <td class="px-4 py-3 text-fg-muted">{port.protocol}</td>
                      <td class="px-4 py-3 text-fg-muted">{port.process}</td>
                      <td class="px-4 py-3 text-fg-muted">{port.pid}</td>
                      <td class="px-4 py-3 text-fg-muted">{port.address}</td>
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
              <h2 class="text-sm font-semibold uppercase tracking-wide text-fg-subtle">Nginx</h2>
              <p class="mt-1 text-sm text-fg-subtle">Virtual hosts and TLS certificate status.</p>
            </div>
            {#if snapshot?.nginx}
              <span class="text-sm text-fg-subtle">Nginx {snapshot.nginx.version || 'unknown'}</span>
            {/if}
          </div>

          {#if loadingSnapshot && !snapshot}
            <div class="flex items-center gap-3 rounded-xl border border-dashed border-border-strong bg-surface-muted px-4 py-6 text-fg-muted">
              <Spinner size="md" label="Loading Nginx data" />
              <div>
                <p class="font-medium text-fg">Loading Nginx snapshot...</p>
                <p class="text-sm text-fg-subtle">Please wait while the discovery scan finishes.</p>
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
                      <p class="text-xs font-semibold uppercase tracking-wide text-fg-subtle">Domain</p>
                      <p class="mt-1 font-medium text-fg">{vhost.serverName}</p>
                    </div>

                    <div>
                      <p class="text-xs font-semibold uppercase tracking-wide text-fg-subtle">Listen</p>
                      <p class="mt-1 text-sm text-fg-muted">{vhost.listen}</p>
                    </div>

                    <div>
                      <p class="text-xs font-semibold uppercase tracking-wide text-fg-subtle">Root</p>
                      <p class="mt-1 text-sm text-fg-muted break-all">{vhost.root || '—'}</p>
                    </div>

                    <div>
                      <p class="text-xs font-semibold uppercase tracking-wide text-fg-subtle">Upstream</p>
                      <p class="mt-1 text-sm text-fg-muted break-all">{vhost.proxyPass || '—'}</p>
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
    <div class="rounded-lg border border-dashed border-border-strong bg-surface px-4 py-8 text-center text-fg-subtle shadow-sm">
      Loading server details...
    </div>
  {:else}
    <div class="rounded-lg border border-error bg-error/10 px-4 py-3 text-sm text-error">
      Server not found.
    </div>
  {/if}
</div>
