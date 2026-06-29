<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import { page } from '$app/stores';
  import { ArrowLeft, Clock, Cpu, HardDrive, MemoryStick, Network, Play, KeyRound, Shield, Activity, Settings } from 'lucide-svelte';
  import { api } from '$lib/api/client';
  import { wsConnect, type WSMessage } from '$lib/api/websocket';
  import type { Server, ServerInfo } from '$lib/stores/servers';

  let server: Server | null = null;
  let info: ServerInfo | null = null;
  let loading = true;
  let loadingInfo = false;
  let connecting = false;
  let wsError = '';
  let wsSteps: { step: string; status: string; value?: string; error?: string }[] = [];
  let ws: WebSocket | null = null;
  let connectionId = 0;
  let activeTab = 'overview';

  // SSH enterprise data
  let authStatus: any = null;
  let credentialHealth: any = null;
  let connectionMetrics: any = null;
  let loadingSSH = false;

  const serverId = parseInt($page.params.id, 10);

  onMount(async () => {
    try {
      server = await api.get<Server>(`/servers/${serverId}`);
      await loadInfo();
      await loadSSHData();
    } catch {
      // handled in template
    } finally {
      loading = false;
    }
  });

  onDestroy(() => {
    connectionId += 1;
    ws?.close();
  });

  async function loadInfo() {
    loadingInfo = true;

    try {
      info = await api.get<ServerInfo>(`/servers/${serverId}/info`);
    } catch {
      info = null;
    } finally {
      loadingInfo = false;
    }
  }

  async function loadSSHData() {
    loadingSSH = true;
    try {
      const [auth, health, metrics] = await Promise.allSettled([
        api.get(`/servers/${serverId}/auth-status`),
        api.get(`/servers/${serverId}/credential-health`),
        api.get(`/servers/${serverId}/connection-metrics`),
      ]);
      if (auth.status === 'fulfilled') authStatus = auth.value;
      if (health.status === 'fulfilled') credentialHealth = health.value;
      if (metrics.status === 'fulfilled') connectionMetrics = metrics.value;
    } catch {
      // ignore
    } finally {
      loadingSSH = false;
    }
  }

  function handleConnect() {
    const currentConnection = ++connectionId;
    connecting = true;
    wsError = '';
    wsSteps = [];
    ws?.close();

    ws = wsConnect(
      serverId,
      (msg: WSMessage) => {
        if (currentConnection !== connectionId) return;

        if (msg.status === 'error' || msg.error) {
          wsError = msg.error || `Connection test failed during ${msg.step}`;
        }

        if (msg.step === 'done' || msg.status === 'complete') {
          connecting = false;
          loadInfo();
          loadSSHData();
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

  function getHealthGradeColor(grade: string): string {
    switch (grade) {
      case 'A+': return 'bg-emerald-100 text-emerald-700';
      case 'A': return 'bg-green-100 text-green-700';
      case 'B': return 'bg-blue-100 text-blue-700';
      case 'C': return 'bg-yellow-100 text-yellow-700';
      case 'D': return 'bg-orange-100 text-orange-700';
      default: return 'bg-red-100 text-red-700';
    }
  }

  function getCredentialStatusColor(status: string): string {
    switch (status) {
      case 'valid': return 'bg-emerald-100 text-emerald-700';
      case 'invalid': return 'bg-red-100 text-red-700';
      case 'expired': return 'bg-orange-100 text-orange-700';
      case 'locked': return 'bg-yellow-100 text-yellow-700';
      default: return 'bg-slate-100 text-slate-600';
    }
  }
</script>

<div class="min-h-screen bg-slate-50">
  <header class="border-b border-slate-200 bg-white px-6 py-4">
    <a href="/" class="mb-3 inline-flex items-center gap-2 text-sm text-slate-600 transition hover:text-slate-900">
      <ArrowLeft size={16} /> Back to Servers
    </a>

    {#if server}
      <div class="flex flex-col gap-4 lg:flex-row lg:items-center lg:justify-between">
        <div>
          <div class="flex items-center gap-3">
            <h1 class="text-xl font-bold tracking-tight text-slate-900">{server.name}</h1>
            <span class="rounded-full bg-slate-100 px-2.5 py-1 text-xs font-medium text-slate-600">
              {server.environment || 'no environment'}
            </span>
          </div>
          <p class="mt-1 text-sm text-slate-500">
            {server.host}:{server.port} · {server.username}
          </p>
          {#if server.description}
            <p class="mt-2 max-w-3xl text-sm text-slate-600">{server.description}</p>
          {/if}
        </div>

        <div class="flex flex-wrap items-center gap-3">
          <a
            href={`/servers/${server.id}/edit`}
            class="inline-flex items-center justify-center rounded-lg border border-slate-300 bg-white px-4 py-2 text-sm font-medium text-slate-700 transition hover:bg-slate-50"
          >
            Edit Server
          </a>
          <a
            href={`/servers/${server.id}/ssh`}
            class="inline-flex items-center justify-center gap-2 rounded-lg border border-slate-300 bg-white px-4 py-2 text-sm font-medium text-slate-700 transition hover:bg-slate-50"
          >
            <KeyRound size={16} />
            SSH Settings
          </a>
          <button
            type="button"
            on:click={handleConnect}
            disabled={connecting}
            class="inline-flex items-center justify-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white transition hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-50"
          >
            <Play size={18} />
            {connecting ? 'Connecting...' : 'Test Connection'}
          </button>
        </div>
      </div>
    {/if}
  </header>

  <main class="mx-auto w-full max-w-6xl p-6">
    {#if loading}
      <div class="rounded-lg border border-dashed border-slate-300 bg-white px-4 py-8 text-center text-slate-500">
        Loading server details...
      </div>
    {:else if !server}
      <div class="rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">
        Server not found.
      </div>
    {:else}
      {#if wsSteps.length > 0 || wsError}
        <section class="mb-6 rounded-2xl border border-slate-200 bg-white p-5 shadow-sm">
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
                    <span class="text-slate-400">→ {step.value}</span>
                  {/if}
                  {#if step.error}
                    <span class="text-rose-600">→ {step.error}</span>
                  {/if}
                </div>
              {/each}
            </div>
          {/if}
        </section>
      {/if}

      <section class="rounded-2xl border border-slate-200 bg-white p-5 shadow-sm">
        <div class="mb-4 flex items-center justify-between gap-4">
          <div>
            <h2 class="text-sm font-semibold uppercase tracking-wide text-slate-500">System Information</h2>
            <p class="mt-1 text-sm text-slate-500">Cached data from the latest successful connection test.</p>
          </div>
          {#if loadingInfo}
            <span class="text-sm text-slate-500">Refreshing...</span>
          {/if}
        </div>

        {#if info}
          <div class="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
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
          <div class="rounded-lg border border-dashed border-slate-300 bg-slate-50 px-4 py-8 text-center text-slate-500">
            {#if loadingInfo}
              Loading system information...
            {:else}
              No cached system information yet. Run a connection test to collect it.
            {/if}
          </div>
        {/if}
      </section>

      <!-- SSH Enterprise Section -->
      <section class="mt-6 rounded-2xl border border-slate-200 bg-white p-5 shadow-sm">
        <div class="mb-4 flex items-center justify-between gap-4">
          <div>
            <h2 class="text-sm font-semibold uppercase tracking-wide text-slate-500">SSH Authentication</h2>
            <p class="mt-1 text-sm text-slate-500">Authentication status, credential health, and connection metrics.</p>
          </div>
          <a href={`/servers/${server.id}/ssh`} class="text-sm text-blue-600 hover:text-blue-700 font-medium">
            Manage SSH →
          </a>
        </div>

        {#if loadingSSH}
          <div class="rounded-lg border border-dashed border-slate-300 bg-slate-50 px-4 py-8 text-center text-slate-500">
            Loading SSH data...
          </div>
        {:else}
          <div class="grid gap-4 md:grid-cols-3">
            <!-- Auth Status -->
            <div class="rounded-xl border border-slate-200 bg-slate-50 p-4">
              <div class="mb-2 flex items-center gap-2 text-slate-500">
                <Shield size={18} />
                <span class="text-sm font-semibold text-slate-900">Auth Status</span>
              </div>
              {#if authStatus}
                <div class="space-y-1 text-sm">
                  <div class="flex items-center justify-between">
                    <span class="text-slate-500">Method</span>
                    <span class="font-medium text-slate-700">{authStatus.authMethod || 'password'}</span>
                  </div>
                  <div class="flex items-center justify-between">
                    <span class="text-slate-500">Status</span>
                    <span class={`rounded-full px-2 py-0.5 text-xs font-medium ${getCredentialStatusColor(authStatus.credentialStatus || 'unknown')}`}>
                      {authStatus.credentialStatus || 'unknown'}
                    </span>
                  </div>
                  <div class="flex items-center justify-between">
                    <span class="text-slate-500">Password</span>
                    <span class={authStatus.hasPassword ? 'text-emerald-600' : 'text-slate-400'}>
                      {authStatus.hasPassword ? '✓ Set' : '✗ Not set'}
                    </span>
                  </div>
                  <div class="flex items-center justify-between">
                    <span class="text-slate-500">SSH Key</span>
                    <span class={authStatus.hasSSHKey ? 'text-emerald-600' : 'text-slate-400'}>
                      {authStatus.hasSSHKey ? '✓ Set' : '✗ Not set'}
                    </span>
                  </div>
                  {#if authStatus.fingerprint}
                    <div class="flex items-center justify-between">
                      <span class="text-slate-500">Fingerprint</span>
                      <span class="font-mono text-xs text-slate-600 truncate max-w-[150px]">{authStatus.fingerprint}</span>
                    </div>
                  {/if}
                  {#if authStatus.keyType}
                    <div class="flex items-center justify-between">
                      <span class="text-slate-500">Key Type</span>
                      <span class="font-medium text-slate-700 uppercase">{authStatus.keyType}</span>
                    </div>
                  {/if}
                </div>
              {:else}
                <p class="text-sm text-slate-400">No auth data available</p>
              {/if}
            </div>

            <!-- Credential Health -->
            <div class="rounded-xl border border-slate-200 bg-slate-50 p-4">
              <div class="mb-2 flex items-center gap-2 text-slate-500">
                <Activity size={18} />
                <span class="text-sm font-semibold text-slate-900">Credential Health</span>
              </div>
              {#if credentialHealth}
                <div class="flex items-center gap-3 mb-3">
                  <span class={`rounded-lg px-3 py-1 text-lg font-bold ${getHealthGradeColor(credentialHealth.grade)}`}>
                    {credentialHealth.grade}
                  </span>
                  <span class="text-2xl font-bold text-slate-700">{credentialHealth.score}<span class="text-sm text-slate-400">/100</span></span>
                </div>
                <div class="space-y-1 text-xs">
                  <div class="flex items-center justify-between">
                    <span class="text-slate-500">Key Installed</span>
                    <span class={credentialHealth.keyInstalled ? 'text-emerald-600' : 'text-slate-400'}>{credentialHealth.keyInstalled ? '✓' : '✗'}</span>
                  </div>
                  <div class="flex items-center justify-between">
                    <span class="text-slate-500">Fingerprint Verified</span>
                    <span class={credentialHealth.fingerprintVerified ? 'text-emerald-600' : 'text-slate-400'}>{credentialHealth.fingerprintVerified ? '✓' : '✗'}</span>
                  </div>
                  <div class="flex items-center justify-between">
                    <span class="text-slate-500">Recent Success</span>
                    <span class={credentialHealth.recentSuccess ? 'text-emerald-600' : 'text-slate-400'}>{credentialHealth.recentSuccess ? '✓' : '✗'}</span>
                  </div>
                  <div class="flex items-center justify-between">
                    <span class="text-slate-500">Passphrase</span>
                    <span class={credentialHealth.passphraseEnabled ? 'text-emerald-600' : 'text-slate-400'}>{credentialHealth.passphraseEnabled ? '✓' : '✗'}</span>
                  </div>
                </div>
              {:else}
                <p class="text-sm text-slate-400">No health data available</p>
              {/if}
            </div>

            <!-- Connection Metrics -->
            <div class="rounded-xl border border-slate-200 bg-slate-50 p-4">
              <div class="mb-2 flex items-center gap-2 text-slate-500">
                <Activity size={18} />
                <span class="text-sm font-semibold text-slate-900">Connection Metrics</span>
              </div>
              {#if connectionMetrics}
                <div class="space-y-1 text-sm">
                  <div class="flex items-center justify-between">
                    <span class="text-slate-500">Total Attempts</span>
                    <span class="font-medium text-slate-700">{connectionMetrics.totalAttempts || 0}</span>
                  </div>
                  <div class="flex items-center justify-between">
                    <span class="text-slate-500">Success Rate</span>
                    <span class="font-medium text-emerald-600">{(connectionMetrics.successRate * 100).toFixed(1)}%</span>
                  </div>
                  <div class="flex items-center justify-between">
                    <span class="text-slate-500">Failures</span>
                    <span class="font-medium text-red-600">{connectionMetrics.failureCount || 0}</span>
                  </div>
                  {#if connectionMetrics.lastSuccess}
                    <div class="flex items-center justify-between">
                      <span class="text-slate-500">Last Success</span>
                      <span class="text-xs text-slate-600">{connectionMetrics.lastSuccess}</span>
                    </div>
                  {/if}
                  {#if connectionMetrics.lastFailure}
                    <div class="flex items-center justify-between">
                      <span class="text-slate-500">Last Failure</span>
                      <span class="text-xs text-slate-600">{connectionMetrics.lastFailure}</span>
                    </div>
                  {/if}
                </div>
              {:else}
                <p class="text-sm text-slate-400">No metrics available</p>
              {/if}
            </div>
          </div>
        {/if}
      </section>
    {/if}
  </main>
</div>
