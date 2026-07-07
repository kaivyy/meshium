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
      return 'bg-error/15 text-error ring-1 ring-error';
    }
    if (normalized === 'medium' || normalized === 'warn' || normalized === 'warning') {
      return 'bg-warning/15 text-warning ring-1 ring-warning';
    }
    return 'bg-success/15 text-success ring-1 ring-success';
  }

  function agentStatusLabel(status: SSHAgentStatus | null) {
    if (!status) return 'Unknown';
    if (status.available) return 'Available';
    return status.socketPath ? 'Socket not responding' : 'Unavailable';
  }

  function agentStatusClass(status: SSHAgentStatus | null) {
    if (!status) return 'bg-surface-muted text-fg-muted ring-1 ring-border';
    return status.available
      ? 'bg-success/15 text-success ring-1 ring-success'
      : 'bg-error/15 text-error ring-1 ring-error';
  }
</script>

<svelte:head>
  <title>SSH Dashboard</title>
</svelte:head>

<div class="p-6">
  <div class="mx-auto flex max-w-7xl flex-col gap-6">
    <div class="flex flex-col gap-4 rounded-2xl border border-border bg-surface p-6 shadow-sm lg:flex-row lg:items-end lg:justify-between">
      <div>
        <div class="inline-flex items-center gap-2 rounded-full bg-accent-subtle px-3 py-1 text-xs font-medium text-accent">
          <ShieldCheck size={14} /> Enterprise SSH
        </div>
        <h1 class="mt-3 text-2xl font-bold tracking-tight text-fg">Authentication Dashboard</h1>
        <p class="mt-2 max-w-3xl text-sm text-fg-subtle">
          Monitor credential health, authentication outcomes, known-host drift, and connection latency across your fleet.
        </p>
      </div>

      <div class="flex flex-wrap gap-3">
        {#each quickLinks as link}
          <a
            href={link.href}
            class="inline-flex items-center gap-2 rounded-lg border border-border-strong bg-surface px-4 py-2 text-sm font-medium text-fg-muted transition hover:bg-surface-muted"
          >
            <link.icon size={16} />
            {link.label}
          </a>
        {/each}
      </div>
    </div>

    {#if loading}
      <div class="rounded-2xl border border-dashed border-border-strong bg-surface px-4 py-8 text-center text-fg-subtle">
        Loading SSH dashboard...
      </div>
    {:else if error}
      <div class="rounded-2xl border border-error bg-error/10 px-4 py-3 text-sm text-error" role="alert">
        {error}
      </div>
    {:else if dashboard}
      <section class="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
        <div class="rounded-2xl border border-border bg-surface p-5 shadow-sm">
          <div class="flex items-center justify-between gap-3">
            <div>
              <p class="text-xs font-semibold uppercase tracking-wide text-fg-subtle">Auth Success Rate</p>
              <p class="mt-2 text-3xl font-bold text-fg">{formatPercent(dashboard.authSuccessRate)}</p>
            </div>
            <div class="rounded-2xl bg-success/10 p-3 text-success"><Gauge size={20} /></div>
          </div>
          <p class="mt-3 text-sm text-fg-subtle">Connection success rate: {formatPercent(dashboard.connectionSuccessRate)}</p>
        </div>

        <div class="rounded-2xl border border-border bg-surface p-5 shadow-sm">
          <div class="flex items-center justify-between gap-3">
            <div>
              <p class="text-xs font-semibold uppercase tracking-wide text-fg-subtle">Auth Failures</p>
              <p class="mt-2 text-3xl font-bold text-fg">{dashboard.authFailures}</p>
            </div>
            <div class="rounded-2xl bg-error/10 p-3 text-error"><Siren size={20} /></div>
          </div>
          <p class="mt-3 text-sm text-fg-subtle">Credential warnings: {dashboard.credentialWarnings} · Expired keys: {dashboard.expiredKeys}</p>
        </div>

        <div class="rounded-2xl border border-border bg-surface p-5 shadow-sm">
          <div class="flex items-center justify-between gap-3">
            <div>
              <p class="text-xs font-semibold uppercase tracking-wide text-fg-subtle">Known Host Drift</p>
              <p class="mt-2 text-3xl font-bold text-fg">{dashboard.fingerprintChanged}</p>
            </div>
            <div class="rounded-2xl bg-warning/10 p-3 text-warning"><Fingerprint size={20} /></div>
          </div>
          <p class="mt-3 text-sm text-fg-subtle">Unknown hosts: {dashboard.unknownHosts}</p>
        </div>

        <div class="rounded-2xl border border-border bg-surface p-5 shadow-sm">
          <div class="flex items-center justify-between gap-3">
            <div>
              <p class="text-xs font-semibold uppercase tracking-wide text-fg-subtle">Latency P95</p>
              <p class="mt-2 text-3xl font-bold text-fg">{formatLatency(dashboard.p95Latency)}</p>
            </div>
            <div class="rounded-2xl bg-accent-subtle p-3 text-accent"><Clock3 size={20} /></div>
          </div>
          <p class="mt-3 text-sm text-fg-subtle">Median: {formatLatency(dashboard.medianLatency)} · P99: {formatLatency(dashboard.p99Latency)}</p>
        </div>
      </section>

      <section class="grid gap-4 lg:grid-cols-3">
        <div class="rounded-2xl border border-border bg-surface p-5 shadow-sm lg:col-span-2">
          <div class="mb-4 flex items-center justify-between gap-3">
            <div>
              <h2 class="text-sm font-semibold uppercase tracking-wide text-fg-subtle">Authentication Mix</h2>
              <p class="mt-1 text-sm text-fg-subtle">Servers by authentication strategy and bastion coverage.</p>
            </div>
            <div class="inline-flex items-center gap-2 rounded-full bg-surface-muted px-3 py-1 text-xs font-medium text-fg-muted">
              <Users size={14} /> Fleet
            </div>
          </div>

          <div class="grid gap-3 md:grid-cols-2 xl:grid-cols-4">
            <div class="rounded-xl border border-border bg-surface-muted p-4">
              <p class="text-xs font-semibold uppercase tracking-wide text-fg-subtle">Password</p>
              <p class="mt-2 text-2xl font-bold text-fg">{dashboard.passwordServers}</p>
            </div>
            <div class="rounded-xl border border-border bg-surface-muted p-4">
              <p class="text-xs font-semibold uppercase tracking-wide text-fg-subtle">Key</p>
              <p class="mt-2 text-2xl font-bold text-fg">{dashboard.keyServers}</p>
            </div>
            <div class="rounded-xl border border-border bg-surface-muted p-4">
              <p class="text-xs font-semibold uppercase tracking-wide text-fg-subtle">Agent</p>
              <p class="mt-2 text-2xl font-bold text-fg">{dashboard.agentServers}</p>
            </div>
            <div class="rounded-xl border border-border bg-surface-muted p-4">
              <p class="text-xs font-semibold uppercase tracking-wide text-fg-subtle">Bastion</p>
              <p class="mt-2 text-2xl font-bold text-fg">{dashboard.bastionServers}</p>
            </div>
          </div>
        </div>

        <div class="rounded-2xl border border-border bg-surface p-5 shadow-sm">
          <div class="mb-4 flex items-center gap-2">
            <Network size={18} class="text-fg-subtle" />
            <div>
              <h2 class="text-sm font-semibold uppercase tracking-wide text-fg-subtle">SSH Agent Status</h2>
              <p class="mt-1 text-sm text-fg-subtle">Local agent state and loaded identities.</p>
            </div>
          </div>

          <div class="flex items-center justify-between gap-3 rounded-xl border border-border bg-surface-muted p-4">
            <div>
              <p class="text-sm font-medium text-fg">{agentStatusLabel(agent)}</p>
              <p class="mt-1 text-xs text-fg-subtle">Socket: {agent?.socketPath || '—'}</p>
            </div>
            <span class={`rounded-full px-3 py-1 text-xs font-medium ${agentStatusClass(agent)}`}>
              {agentStatusLabel(agent)}
            </span>
          </div>

          <div class="mt-4 space-y-3">
            <div class="rounded-xl border border-border bg-surface-muted p-3 text-sm text-fg-muted">
              <span class="font-medium text-fg">Identities loaded:</span> {agent?.identities.length || 0}
            </div>
            {#if agent?.identities?.length}
              {#each agent.identities.slice(0, 3) as identity}
                <div class="rounded-xl border border-border bg-surface p-3 text-sm text-fg-muted">
                  <p class="font-medium text-fg">{identity.type}</p>
                  <p class="mt-1 break-all text-xs text-fg-subtle">{identity.fingerprintSha256}</p>
                  {#if identity.comment}
                    <p class="mt-1 text-xs text-fg-subtle">{identity.comment}</p>
                  {/if}
                </div>
              {/each}
            {:else}
              <p class="text-sm text-fg-subtle">No identities are currently loaded into the agent.</p>
            {/if}
          </div>
        </div>
      </section>

      <section class="grid gap-4 xl:grid-cols-2">
        <div class="rounded-2xl border border-border bg-surface p-5 shadow-sm">
          <div class="mb-4 flex items-center gap-2">
            <AlertTriangle size={18} class="text-fg-subtle" />
            <div>
              <h2 class="text-sm font-semibold uppercase tracking-wide text-fg-subtle">Latency Stats</h2>
              <p class="mt-1 text-sm text-fg-subtle">Performance distribution for successful connections.</p>
            </div>
          </div>

          <div class="grid gap-3 md:grid-cols-2">
            <div class="rounded-xl border border-border bg-surface-muted p-4">
              <p class="text-xs font-semibold uppercase tracking-wide text-fg-subtle">Average</p>
              <p class="mt-2 text-xl font-bold text-fg">{formatLatency(dashboard.averageLatency)}</p>
            </div>
            <div class="rounded-xl border border-border bg-surface-muted p-4">
              <p class="text-xs font-semibold uppercase tracking-wide text-fg-subtle">Median</p>
              <p class="mt-2 text-xl font-bold text-fg">{formatLatency(dashboard.medianLatency)}</p>
            </div>
            <div class="rounded-xl border border-border bg-surface-muted p-4">
              <p class="text-xs font-semibold uppercase tracking-wide text-fg-subtle">P95</p>
              <p class="mt-2 text-xl font-bold text-fg">{formatLatency(dashboard.p95Latency)}</p>
            </div>
            <div class="rounded-xl border border-border bg-surface-muted p-4">
              <p class="text-xs font-semibold uppercase tracking-wide text-fg-subtle">P99</p>
              <p class="mt-2 text-xl font-bold text-fg">{formatLatency(dashboard.p99Latency)}</p>
            </div>
          </div>
        </div>

        <div class="rounded-2xl border border-border bg-surface p-5 shadow-sm">
          <div class="mb-4 flex items-center gap-2">
            <Link2 size={18} class="text-fg-subtle" />
            <div>
              <h2 class="text-sm font-semibold uppercase tracking-wide text-fg-subtle">Quick Links</h2>
              <p class="mt-1 text-sm text-fg-subtle">Jump to the SSH workflows you use most often.</p>
            </div>
          </div>

          <div class="space-y-3">
            {#each quickLinks as link}
              <a href={link.href} class="block rounded-xl border border-border bg-surface-muted p-4 transition hover:border-accent hover:bg-accent-subtle/50">
                <div class="flex items-center gap-3">
                  <span class="rounded-xl bg-surface p-2 text-fg-muted shadow-sm">
                    <link.icon size={18} />
                  </span>
                  <div>
                    <p class="font-medium text-fg">{link.label}</p>
                    <p class="mt-1 text-sm text-fg-subtle">{link.description}</p>
                  </div>
                </div>
              </a>
            {/each}
          </div>
        </div>
      </section>

      <section class="rounded-2xl border border-border bg-surface p-5 shadow-sm">
        <div class="mb-4 flex items-center gap-2">
          <BadgeAlert size={18} class="text-fg-subtle" />
          <div>
            <h2 class="text-sm font-semibold uppercase tracking-wide text-fg-subtle">Recent Fingerprint Changes</h2>
            <p class="mt-1 text-sm text-fg-subtle">Recent host key changes recorded by the backend.</p>
          </div>
        </div>

        {#if hostKeyChanges.length === 0}
          <div class="rounded-xl border border-dashed border-border-strong bg-surface-muted px-4 py-8 text-center text-fg-subtle">
            No recent host key changes.
          </div>
        {:else}
          <div class="hidden overflow-x-auto rounded-2xl border border-border md:block">
            <table class="min-w-full divide-y divide-border">
              <thead class="bg-surface-muted">
                <tr>
                  <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-fg-subtle">Host</th>
                  <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-fg-subtle">Risk</th>
                  <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-fg-subtle">Action</th>
                  <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-fg-subtle">Updated</th>
                </tr>
              </thead>
              <tbody class="divide-y divide-border bg-surface">
                {#each hostKeyChanges as change}
                  <tr>
                    <td class="px-4 py-4 align-top text-sm text-fg-muted">
                      <div class="font-medium text-fg">{change.host}</div>
                      <div class="mt-1 text-xs text-fg-subtle">Port {change.port}</div>
                      <div class="mt-2 break-all text-xs text-fg-subtle">{change.newFingerprint || change.oldFingerprint || '—'}</div>
                    </td>
                    <td class="px-4 py-4 align-top">
                      <span class={`inline-flex rounded-full px-2.5 py-1 text-xs font-medium ${badgeForLevel(change.riskLevel)}`}>
                        {change.riskLevel || 'unknown'}
                      </span>
                    </td>
                    <td class="px-4 py-4 align-top text-sm text-fg-muted">{change.actionTaken || '—'}</td>
                    <td class="px-4 py-4 align-top text-sm text-fg-subtle">{formatTimestamp(change.timestamp)}</td>
                  </tr>
                {/each}
              </tbody>
            </table>
          </div>

          <!-- Mobile: stacked cards -->
          <div class="mt-4 space-y-3 md:hidden">
            {#each hostKeyChanges as change}
              <div class="rounded-xl border border-border bg-surface p-4">
                <div class="flex items-start justify-between gap-2">
                  <div class="min-w-0">
                    <div class="break-words font-medium text-fg">{change.host}</div>
                    <div class="mt-1 text-xs text-fg-subtle">Port {change.port}</div>
                  </div>
                  <span class={`inline-flex shrink-0 rounded-full px-2.5 py-1 text-xs font-medium ${badgeForLevel(change.riskLevel)}`}>
                    {change.riskLevel || 'unknown'}
                  </span>
                </div>
                <dl class="mt-3 grid grid-cols-2 gap-x-4 gap-y-2 text-sm">
                  <div class="col-span-2">
                    <dt class="text-xs uppercase tracking-wide text-fg-subtle">Fingerprint</dt>
                    <dd class="break-all text-xs text-fg-muted">{change.newFingerprint || change.oldFingerprint || '—'}</dd>
                  </div>
                  <div>
                    <dt class="text-xs uppercase tracking-wide text-fg-subtle">Action</dt>
                    <dd class="break-words text-fg-muted">{change.actionTaken || '—'}</dd>
                  </div>
                  <div>
                    <dt class="text-xs uppercase tracking-wide text-fg-subtle">Updated</dt>
                    <dd class="break-words text-fg-subtle">{formatTimestamp(change.timestamp)}</dd>
                  </div>
                </dl>
              </div>
            {/each}
          </div>
        {/if}
      </section>
    {/if}
  </div>
</div>
