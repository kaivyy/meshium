<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import { page } from '$app/stores';
  import { ArrowLeft, Clock, Cpu, HardDrive, MemoryStick, Network, Play } from 'lucide-svelte';
  import { api } from '$lib/api/client';
  import { wsConnect, type WSMessage } from '$lib/api/websocket';
  import type { Server, ServerInfo } from '$lib/stores/servers';

  let server: Server | null = null;
  let info: ServerInfo | null = null;
  let loading = true;
  let loadingInfo = false;
  let connecting = false;
  let wsSteps: { step: string; status: string; value?: string; error?: string }[] = [];
  let ws: WebSocket | null = null;

  const serverId = parseInt($page.params.id, 10);

  onMount(async () => {
    try {
      server = await api.get<Server>(`/servers/${serverId}`);
      await loadInfo();
    } catch {
      // handled in template
    } finally {
      loading = false;
    }
  });

  onDestroy(() => {
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

  function handleConnect() {
    connecting = true;
    wsSteps = [];
    ws?.close();

    ws = wsConnect(
      serverId,
      (msg: WSMessage) => {
        if (msg.step === 'done' || msg.status === 'complete') {
          connecting = false;
          loadInfo();
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
        connecting = false;
      },
      () => {
        connecting = false;
      }
    );
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
      {#if wsSteps.length > 0}
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
    {/if}
  </main>
</div>
