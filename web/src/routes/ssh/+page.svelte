<script lang="ts">
  import { onMount } from 'svelte';
  import { ShieldCheck, Link2, Settings, Siren, Clock3, KeyRound, AlertTriangle, Server, Users, Activity, Gauge, Fingerprint, Network, BadgeAlert } from 'lucide-svelte';
  import { api } from '$lib/api/client';

  interface AuthDashboard {
    passwordServers: number;
    keyServers: number;
    agentServers: number;
    bastionServers: number;
    fingerprintChanged: number;
    unknownHosts: number;
    credentialWarnings: number;
    expiredKeys: number;
    authFailures: number;
    authSuccessRate: number;
    connectionSuccessRate: number;
    averageLatency: number;
    medianLatency: number;
    p95Latency: number;
    p99Latency: number;
  }

  interface SSHAgentIdentity {
    fingerprintSha256: string;
    type: string;
    comment: string;
  }

  interface SSHAgentStatus {
    available: boolean;
    socketPath: string;
    identities: SSHAgentIdentity[];
  }

  interface HostKeyChange {
    id: number;
    host: string;
    port: number;
    oldFingerprint: string;
    newFingerprint: string;
    riskLevel: string;
    actionTaken: string;
    serverId: number;
    timestamp: string;
  }

  const quickLinks = [
    { href: '/settings', label: 'SSH Settings', description: 'Manage the app key pair and lock state.', icon: Settings },
    { href: '/ssh/known-hosts', label: 'Known Hosts', description: 'Review verified, unknown, and changed host keys.', icon: Fingerprint },
    { href: '/ssh/history', label: 'Diagnostics', description: 'Inspect authentication history and failure patterns.', icon: Activity }
  ];

  let dashboard: AuthDashboard | null = null;
  let agent: SSHAgentStatus | null = null;
  let hostKeyChanges: HostKeyChange[] = [];
  let loading = true;
  let error = '';

  onMount(loadDashboard);

  async function loadDashboard() {
    loading = true;
    error = '';

    try {
      const [dashboardData, agentData, changesData] = await Promise.all([
        api.get('/dashboard') as Promise<AuthDashboard>,
        (api.get('/ssh-agent/status') as Promise<SSHAgentStatus>).catch(() => null),
        (api.get('/host-key-changes?limit=8') as Promise<HostKeyChange[]>).catch(() => [] as Array<HostKeyChange>)
      ]);

      dashboard = dashboardData;
      agent = agentData;
      hostKeyChanges = changesData;
    } catch (e) {
      error = e instanceof Error ? e.message : 'Failed to load SSH dashboard';
    } finally {
      loading = false;
    }
  }

  function formatPercent(value: number) {
    return `${(value * 100).toFixed(1)}%`;
  }

  function formatLatency(value: number) {
    return `${value.toFixed(1)} ms`;
  }

  function formatTimestamp(value: string) {
    if (!value) return '—';
    const date = new Date(value);
    return Number.isNaN(date.getTime()) ? value : date.toLocaleString();
  }

  function badgeForLevel(level: string) {
    const normalized = level.toLowerCase();
    if (normalized === 'high' || normalized === 'critical') {
      return 'bg-rose-100 text-rose-700 ring-1 ring-rose-200';
    }
    if (normalized === 'medium' || normalized === 'warn' || normalized === 'warning') {
      return 'bg-amber-100 text-amber-700 ring-1 ring-amber-200';
    }
    return 'bg-emerald-100 text-emerald-700 ring-1 ring-emerald-200';
  }

  function agentStatusLabel(status: SSHAgentStatus | null) {
    if (!status) return 'Unknown';
    if (status.available) return 'Available';
    return status.socketPath ? 'Socket not responding' : 'Unavailable';
  }

  function agentStatusClass(status: SSHAgentStatus | null) {
    if (!status) return 'bg-slate-100 text-slate-600 ring-1 ring-slate-200';
    return status.available
      ? 'bg-emerald-100 text-emerald-700 ring-1 ring-emerald-200'
      : 'bg-rose-100 text-rose-700 ring-1 ring-rose-200';
  }
</script>

<svelte:head>
  <title>SSH Dashboard</title>
</svelte:head>

<div class="p-6">
  <div class="mx-auto flex max-w-7xl flex-col gap-6">
    <div class="flex flex-col gap-4 rounded-2xl border border-slate-200 bg-white p-6 shadow-sm lg:flex-row lg:items-end lg:justify-between">
      <div>
        <div class="inline-flex items-center gap-2 rounded-full bg-blue-50 px-3 py-1 text-xs font-medium text-blue-700">
          <ShieldCheck size={14} /> Enterprise SSH
        </div>
        <h1 class="mt-3 text-2xl font-bold tracking-tight text-slate-900">Authentication Dashboard</h1>
        <p class="mt-2 max-w-3xl text-sm text-slate-500">
          Monitor credential health, authentication outcomes, known-host drift, and connection latency across your fleet.
        </p>
      </div>

      <div class="flex flex-wrap gap-3">
        {#each quickLinks as link}
          <a
            href={link.href}
            class="inline-flex items-center gap-2 rounded-lg border border-slate-300 bg-white px-4 py-2 text-sm font-medium text-slate-700 transition hover:bg-slate-50"
          >
            <link.icon size={16} />
            {link.label}
          </a>
        {/each}
      </div>
    </div>

    {#if loading}
      <div class="rounded-2xl border border-dashed border-slate-300 bg-white px-4 py-8 text-center text-slate-500">
        Loading SSH dashboard...
      </div>
    {:else if error}
      <div class="rounded-2xl border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700" role="alert">
        {error}
      </div>
    {:else if dashboard}
      <section class="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
        <div class="rounded-2xl border border-slate-200 bg-white p-5 shadow-sm">
          <div class="flex items-center justify-between gap-3">
            <div>
              <p class="text-xs font-semibold uppercase tracking-wide text-slate-500">Auth Success Rate</p>
              <p class="mt-2 text-3xl font-bold text-slate-900">{formatPercent(dashboard.authSuccessRate)}</p>
            </div>
            <div class="rounded-2xl bg-emerald-50 p-3 text-emerald-600"><Gauge size={20} /></div>
          </div>
          <p class="mt-3 text-sm text-slate-500">Connection success rate: {formatPercent(dashboard.connectionSuccessRate)}</p>
        </div>

        <div class="rounded-2xl border border-slate-200 bg-white p-5 shadow-sm">
          <div class="flex items-center justify-between gap-3">
            <div>
              <p class="text-xs font-semibold uppercase tracking-wide text-slate-500">Auth Failures</p>
              <p class="mt-2 text-3xl font-bold text-slate-900">{dashboard.authFailures}</p>
            </div>
            <div class="rounded-2xl bg-rose-50 p-3 text-rose-600"><Siren size={20} /></div>
          </div>
          <p class="mt-3 text-sm text-slate-500">Credential warnings: {dashboard.credentialWarnings} · Expired keys: {dashboard.expiredKeys}</p>
        </div>

        <div class="rounded-2xl border border-slate-200 bg-white p-5 shadow-sm">
          <div class="flex items-center justify-between gap-3">
            <div>
              <p class="text-xs font-semibold uppercase tracking-wide text-slate-500">Known Host Drift</p>
              <p class="mt-2 text-3xl font-bold text-slate-900">{dashboard.fingerprintChanged}</p>
            </div>
            <div class="rounded-2xl bg-amber-50 p-3 text-amber-600"><Fingerprint size={20} /></div>
          </div>
          <p class="mt-3 text-sm text-slate-500">Unknown hosts: {dashboard.unknownHosts}</p>
        </div>

        <div class="rounded-2xl border border-slate-200 bg-white p-5 shadow-sm">
          <div class="flex items-center justify-between gap-3">
            <div>
              <p class="text-xs font-semibold uppercase tracking-wide text-slate-500">Latency P95</p>
              <p class="mt-2 text-3xl font-bold text-slate-900">{formatLatency(dashboard.p95Latency)}</p>
            </div>
            <div class="rounded-2xl bg-blue-50 p-3 text-blue-600"><Clock3 size={20} /></div>
          </div>
          <p class="mt-3 text-sm text-slate-500">Median: {formatLatency(dashboard.medianLatency)} · P99: {formatLatency(dashboard.p99Latency)}</p>
        </div>
      </section>

      <section class="grid gap-4 lg:grid-cols-3">
        <div class="rounded-2xl border border-slate-200 bg-white p-5 shadow-sm lg:col-span-2">
          <div class="mb-4 flex items-center justify-between gap-3">
            <div>
              <h2 class="text-sm font-semibold uppercase tracking-wide text-slate-500">Authentication Mix</h2>
              <p class="mt-1 text-sm text-slate-500">Servers by authentication strategy and bastion coverage.</p>
            </div>
            <div class="inline-flex items-center gap-2 rounded-full bg-slate-100 px-3 py-1 text-xs font-medium text-slate-600">
              <Users size={14} /> Fleet
            </div>
          </div>

          <div class="grid gap-3 md:grid-cols-2 xl:grid-cols-4">
            <div class="rounded-xl border border-slate-200 bg-slate-50 p-4">
              <p class="text-xs font-semibold uppercase tracking-wide text-slate-500">Password</p>
              <p class="mt-2 text-2xl font-bold text-slate-900">{dashboard.passwordServers}</p>
            </div>
            <div class="rounded-xl border border-slate-200 bg-slate-50 p-4">
              <p class="text-xs font-semibold uppercase tracking-wide text-slate-500">Key</p>
              <p class="mt-2 text-2xl font-bold text-slate-900">{dashboard.keyServers}</p>
            </div>
            <div class="rounded-xl border border-slate-200 bg-slate-50 p-4">
              <p class="text-xs font-semibold uppercase tracking-wide text-slate-500">Agent</p>
              <p class="mt-2 text-2xl font-bold text-slate-900">{dashboard.agentServers}</p>
            </div>
            <div class="rounded-xl border border-slate-200 bg-slate-50 p-4">
              <p class="text-xs font-semibold uppercase tracking-wide text-slate-500">Bastion</p>
              <p class="mt-2 text-2xl font-bold text-slate-900">{dashboard.bastionServers}</p>
            </div>
          </div>
        </div>

        <div class="rounded-2xl border border-slate-200 bg-white p-5 shadow-sm">
          <div class="mb-4 flex items-center gap-2">
            <Network size={18} class="text-slate-500" />
            <div>
              <h2 class="text-sm font-semibold uppercase tracking-wide text-slate-500">SSH Agent Status</h2>
              <p class="mt-1 text-sm text-slate-500">Local agent state and loaded identities.</p>
            </div>
          </div>

          <div class="flex items-center justify-between gap-3 rounded-xl border border-slate-200 bg-slate-50 p-4">
            <div>
              <p class="text-sm font-medium text-slate-900">{agentStatusLabel(agent)}</p>
              <p class="mt-1 text-xs text-slate-500">Socket: {agent?.socketPath || '—'}</p>
            </div>
            <span class={`rounded-full px-3 py-1 text-xs font-medium ${agentStatusClass(agent)}`}>
              {agentStatusLabel(agent)}
            </span>
          </div>

          <div class="mt-4 space-y-3">
            <div class="rounded-xl border border-slate-200 bg-slate-50 p-3 text-sm text-slate-700">
              <span class="font-medium text-slate-900">Identities loaded:</span> {agent?.identities.length || 0}
            </div>
            {#if agent?.identities?.length}
              {#each agent.identities.slice(0, 3) as identity}
                <div class="rounded-xl border border-slate-200 bg-white p-3 text-sm text-slate-700">
                  <p class="font-medium text-slate-900">{identity.type}</p>
                  <p class="mt-1 break-all text-xs text-slate-500">{identity.fingerprintSha256}</p>
                  {#if identity.comment}
                    <p class="mt-1 text-xs text-slate-500">{identity.comment}</p>
                  {/if}
                </div>
              {/each}
            {:else}
              <p class="text-sm text-slate-500">No identities are currently loaded into the agent.</p>
            {/if}
          </div>
        </div>
      </section>

      <section class="grid gap-4 xl:grid-cols-2">
        <div class="rounded-2xl border border-slate-200 bg-white p-5 shadow-sm">
          <div class="mb-4 flex items-center gap-2">
            <AlertTriangle size={18} class="text-slate-500" />
            <div>
              <h2 class="text-sm font-semibold uppercase tracking-wide text-slate-500">Latency Stats</h2>
              <p class="mt-1 text-sm text-slate-500">Performance distribution for successful connections.</p>
            </div>
          </div>

          <div class="grid gap-3 md:grid-cols-2">
            <div class="rounded-xl border border-slate-200 bg-slate-50 p-4">
              <p class="text-xs font-semibold uppercase tracking-wide text-slate-500">Average</p>
              <p class="mt-2 text-xl font-bold text-slate-900">{formatLatency(dashboard.averageLatency)}</p>
            </div>
            <div class="rounded-xl border border-slate-200 bg-slate-50 p-4">
              <p class="text-xs font-semibold uppercase tracking-wide text-slate-500">Median</p>
              <p class="mt-2 text-xl font-bold text-slate-900">{formatLatency(dashboard.medianLatency)}</p>
            </div>
            <div class="rounded-xl border border-slate-200 bg-slate-50 p-4">
              <p class="text-xs font-semibold uppercase tracking-wide text-slate-500">P95</p>
              <p class="mt-2 text-xl font-bold text-slate-900">{formatLatency(dashboard.p95Latency)}</p>
            </div>
            <div class="rounded-xl border border-slate-200 bg-slate-50 p-4">
              <p class="text-xs font-semibold uppercase tracking-wide text-slate-500">P99</p>
              <p class="mt-2 text-xl font-bold text-slate-900">{formatLatency(dashboard.p99Latency)}</p>
            </div>
          </div>
        </div>

        <div class="rounded-2xl border border-slate-200 bg-white p-5 shadow-sm">
          <div class="mb-4 flex items-center gap-2">
            <Link2 size={18} class="text-slate-500" />
            <div>
              <h2 class="text-sm font-semibold uppercase tracking-wide text-slate-500">Quick Links</h2>
              <p class="mt-1 text-sm text-slate-500">Jump to the SSH workflows you use most often.</p>
            </div>
          </div>

          <div class="space-y-3">
            {#each quickLinks as link}
              <a href={link.href} class="block rounded-xl border border-slate-200 bg-slate-50 p-4 transition hover:border-blue-200 hover:bg-blue-50/50">
                <div class="flex items-center gap-3">
                  <span class="rounded-xl bg-white p-2 text-slate-600 shadow-sm">
                    <link.icon size={18} />
                  </span>
                  <div>
                    <p class="font-medium text-slate-900">{link.label}</p>
                    <p class="mt-1 text-sm text-slate-500">{link.description}</p>
                  </div>
                </div>
              </a>
            {/each}
          </div>
        </div>
      </section>

      <section class="rounded-2xl border border-slate-200 bg-white p-5 shadow-sm">
        <div class="mb-4 flex items-center gap-2">
          <BadgeAlert size={18} class="text-slate-500" />
          <div>
            <h2 class="text-sm font-semibold uppercase tracking-wide text-slate-500">Recent Fingerprint Changes</h2>
            <p class="mt-1 text-sm text-slate-500">Recent host key changes recorded by the backend.</p>
          </div>
        </div>

        {#if hostKeyChanges.length === 0}
          <div class="rounded-xl border border-dashed border-slate-300 bg-slate-50 px-4 py-8 text-center text-slate-500">
            No recent host key changes.
          </div>
        {:else}
          <div class="overflow-x-auto rounded-2xl border border-slate-200">
            <table class="min-w-full divide-y divide-slate-200">
              <thead class="bg-slate-50">
                <tr>
                  <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500">Host</th>
                  <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500">Risk</th>
                  <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500">Action</th>
                  <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500">Updated</th>
                </tr>
              </thead>
              <tbody class="divide-y divide-slate-100 bg-white">
                {#each hostKeyChanges as change}
                  <tr>
                    <td class="px-4 py-4 align-top text-sm text-slate-700">
                      <div class="font-medium text-slate-900">{change.host}</div>
                      <div class="mt-1 text-xs text-slate-500">Port {change.port}</div>
                      <div class="mt-2 break-all text-xs text-slate-400">{change.newFingerprint || change.oldFingerprint || '—'}</div>
                    </td>
                    <td class="px-4 py-4 align-top">
                      <span class={`inline-flex rounded-full px-2.5 py-1 text-xs font-medium ${badgeForLevel(change.riskLevel)}`}>
                        {change.riskLevel || 'unknown'}
                      </span>
                    </td>
                    <td class="px-4 py-4 align-top text-sm text-slate-700">{change.actionTaken || '—'}</td>
                    <td class="px-4 py-4 align-top text-sm text-slate-500">{formatTimestamp(change.timestamp)}</td>
                  </tr>
                {/each}
              </tbody>
            </table>
          </div>
        {/if}
      </section>
    {/if}
  </div>
</div>
