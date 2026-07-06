<script lang="ts">
  import { onMount } from 'svelte';
  import { page } from '$app/stores';
  import { goto } from '$app/navigation';
  import { ArrowLeft, ShieldCheck, KeyRound, FileKey, RotateCw, TestTube2, Trash2, Upload, Plus, RefreshCw, ServerCog, Fingerprint, UserCheck, AlertTriangle, Settings2, History, PlugZap, Save } from 'lucide-svelte';
  import { api } from '$lib/api/client';
  import type { Server } from '$lib/stores/servers';

  interface AuthStatus {
    authMethod: string;
    credentialStatus: string;
    hasPassword: boolean;
    hasSSHKey: boolean;
    hasPassphrase: boolean;
    fingerprint: string;
    keyType: string;
  }

  interface CredentialHealthScore {
    score: number;
    grade: string;
    passwordExists: boolean;
    keyInstalled: boolean;
    fingerprintVerified: boolean;
    knownHost: boolean;
    recentSuccess: boolean;
    recentFailure: boolean;
    keyAge: string;
    passphraseEnabled: boolean;
    bastionHealthy: boolean;
    agentHealthy: boolean;
  }

  interface ServerKey {
    id: number;
    serverId: number;
    label: string;
    keyType: string;
    publicKey: string;
    fingerprint: string;
    notes: string;
    enabled: boolean;
    isDefault: boolean;
    priority: number;
    lastUsed: string;
    createdAt: string;
    updatedAt: string;
  }

  interface RetryConfig {
    serverId: number;
    retryCount: number;
    retryDelayMs: number;
    backoffStrategy: string;
    jitterMs: number;
    reconnectPolicy: string;
    authRetryOrder: string[];
  }

  interface AgentConfig {
    useAgent: boolean;
    preferredIdentity: string;
    agentForwarding: boolean;
  }

  interface ActionResponse {
    message?: string;
    status?: string;
  }

  const serverId = parseInt($page.params.id ?? '0', 10);

  let server: Server | null = null;
  let authStatus: AuthStatus | null = null;
  let credentialHealth: CredentialHealthScore | null = null;
  let serverKeys: ServerKey[] = [];
  let retryConfig: RetryConfig | null = null;
  let agentConfig: AgentConfig | null = null;
  let loading = true;
  let loadingAction = '';
  let error = '';
  let message = '';
  let showKeyForm = false;

  let newKeyLabel = '';
  let newKeyType = 'ed25519';
  let newPrivateKey = '';
  let newPassphrase = '';
  let newNotes = '';

  let retryCount = 3;
  let retryDelayMs = 1000;
  let backoffStrategy = 'exponential';
  let jitterMs = 0;
  let reconnectPolicy = 'always';
  let authRetryOrder = 'agent,ed25519,rsa,ecdsa,password';

  let useAgent = false;
  let preferredIdentity = '';
  let agentForwarding = false;

  onMount(loadData);

  async function loadData() {
    loading = true;
    error = '';
    message = '';

    try {
      const [serverData, statusData, healthData, keysData, retryData, agentData] = await Promise.all([
        api.get(`/servers/${serverId}`) as Promise<Server>,
        api.get(`/servers/${serverId}/auth-status`) as Promise<AuthStatus>,
        api.get(`/servers/${serverId}/credential-health`) as Promise<CredentialHealthScore>,
        (api.get(`/servers/${serverId}/keys`) as Promise<ServerKey[]>).catch(() => [] as Array<ServerKey>),
        (api.get(`/servers/${serverId}/retry-config`) as Promise<RetryConfig>).catch(() => null),
        (api.get(`/servers/${serverId}/agent-config`) as Promise<AgentConfig>).catch(() => null)
      ]);

      server = serverData;
      authStatus = statusData;
      credentialHealth = healthData;
      serverKeys = keysData;
      retryConfig = retryData;
      agentConfig = agentData;

      if (retryConfig) {
        retryCount = retryConfig.retryCount;
        retryDelayMs = retryConfig.retryDelayMs;
        backoffStrategy = retryConfig.backoffStrategy;
        jitterMs = retryConfig.jitterMs;
        reconnectPolicy = retryConfig.reconnectPolicy;
        authRetryOrder = retryConfig.authRetryOrder?.join(',') || authRetryOrder;
      }

      if (agentConfig) {
        useAgent = agentConfig.useAgent;
        preferredIdentity = agentConfig.preferredIdentity;
        agentForwarding = agentConfig.agentForwarding;
      }
    } catch (e) {
      error = e instanceof Error ? e.message : 'Failed to load server SSH details';
    } finally {
      loading = false;
    }
  }

  function formatDate(value: string) {
    if (!value) return '—';
    const date = new Date(value);
    return Number.isNaN(date.getTime()) ? value : date.toLocaleString();
  }

  function healthClass(score: number) {
    if (score >= 85) return 'bg-success/15 text-success ring-1 ring-success/30';
    if (score >= 65) return 'bg-warning/15 text-warning ring-1 ring-warning/30';
    return 'bg-error/15 text-error ring-1 ring-error/30';
  }

  function booleanBadge(value: boolean) {
    return value ? 'bg-success/15 text-success ring-1 ring-success/30' : 'bg-surface-muted text-fg-muted ring-1 ring-border';
  }

  async function callAction<T>(method: 'POST' | 'PUT' | 'PATCH' | 'DELETE', path: string, body?: unknown) {
    loadingAction = path;
    error = '';
    message = '';

    try {
      if (method === 'PUT') {
        return body ? await api.put(path, body) as T : await api.put(path) as T;
      }
      if (method === 'DELETE') {
        return await api.delete(path) as T;
      }
      return body ? await api.post(path, body) as T : await api.post(path) as T;
    } catch (e) {
      error = e instanceof Error ? e.message : 'Request failed';
      return null;
    } finally {
      loadingAction = '';
    }
  }

  async function refreshAll() {
    await loadData();
  }

  async function installKey() {
    const result = await callAction('POST', `/servers/${serverId}/install-key`) as ActionResponse | null;
    if (result !== null) {
      const response = result as ActionResponse | undefined;
      message = response?.message || 'Key installation completed.';
      await refreshAll();
    }
  }

  async function verifyKey() {
    const result = await callAction('POST', `/servers/${serverId}/verify-key`) as ActionResponse | null;
    if (result !== null) {
      const response = result as ActionResponse | undefined;
      message = response?.message || 'Key verification completed.';
      await refreshAll();
    }
  }

  async function rotateKey() {
    const result = await callAction('POST', `/servers/${serverId}/rotate-key`, { keyType: 'ed25519' }) as ActionResponse | null;
    if (result !== null) {
      const response = result as ActionResponse | undefined;
      message = response?.message || 'Key rotation completed.';
      await refreshAll();
    }
  }

  async function testAuth() {
    const result = await callAction('POST', `/servers/${serverId}/test-auth`) as ActionResponse | null;
    if (result !== null) {
      const response = result as ActionResponse | undefined;
      message = response?.message || 'Authentication test completed.';
      await refreshAll();
    }
  }

  async function removePassword() {
    if (!confirm('Remove the stored password for this server?')) return;
    const result = await callAction('POST', `/servers/${serverId}/remove-password`) as ActionResponse | null;
    if (result !== null) {
      message = 'Password removed from the server record.';
      await refreshAll();
    }
  }

  async function clearKey() {
    if (!confirm('Clear the stored SSH key and passphrase for this server?')) return;
    const result = await callAction('POST', `/servers/${serverId}/clear-key`) as ActionResponse | null;
    if (result !== null) {
      message = 'SSH key cleared from the server record.';
      await refreshAll();
    }
  }

  async function saveRetryConfig() {
    if (!server) return;
    const result = await callAction('PUT', `/servers/${serverId}/retry-config`, {
      serverId,
      retryCount: Number(retryCount),
      retryDelayMs: Number(retryDelayMs),
      backoffStrategy,
      jitterMs: Number(jitterMs),
      reconnectPolicy,
      authRetryOrder: authRetryOrder.split(',').map((item) => item.trim()).filter(Boolean)
    });

    if (result !== null) {
      message = 'Retry configuration saved.';
      await refreshAll();
    }
  }

  async function saveAgentConfig() {
    const result = await callAction('PUT', `/servers/${serverId}/agent-config`, {
      useAgent,
      preferredIdentity,
      agentForwarding
    });

    if (result !== null) {
      message = 'SSH agent configuration saved.';
      await refreshAll();
    }
  }

  async function addServerKey() {
    if (!newKeyLabel.trim() || !newPrivateKey.trim()) {
      error = 'Label and private key are required to add a server key.';
      return;
    }

    const result = await callAction('POST', `/servers/${serverId}/keys`, {
      label: newKeyLabel.trim(),
      keyType: newKeyType,
      privateKey: newPrivateKey,
      passphrase: newPassphrase,
      notes: newNotes
    });

    if (result !== null) {
      message = 'Server key added.';
      newKeyLabel = '';
      newKeyType = 'ed25519';
      newPrivateKey = '';
      newPassphrase = '';
      newNotes = '';
      showKeyForm = false;
      await refreshAll();
    }
  }

  async function removeServerKey(key: ServerKey) {
    if (!confirm(`Remove key "${key.label}"?`)) return;
    const result = await callAction('DELETE', `/servers/${serverId}/keys/${key.id}`);
    if (result !== null) {
      message = 'Server key removed.';
      await refreshAll();
    }
  }

  async function setDefaultServerKey(key: ServerKey) {
    const result = await callAction('PATCH', `/servers/${serverId}/keys/${key.id}/default`);
    if (result !== null) {
      message = `"${key.label}" is now the default key.`;
      await refreshAll();
    }
  }

  function healthGradeLabel(score: CredentialHealthScore | null) {
    if (!score) return '—';
    return `${score.grade} · ${score.score}/100`;
  }
</script>

<svelte:head>
  <title>Server SSH</title>
</svelte:head>

<div class="p-6">
  <div class="mx-auto flex max-w-7xl flex-col gap-6">
    <div class="rounded-2xl border border-border bg-surface p-6 shadow-sm">
      <a href={server ? `/servers/${serverId}` : '/'} class="inline-flex items-center gap-2 text-sm text-fg-muted transition hover:text-fg">
        <ArrowLeft size={16} /> Back to Server
      </a>

      <div class="mt-4 flex flex-col gap-4 lg:flex-row lg:items-end lg:justify-between">
        <div>
          <div class="inline-flex items-center gap-2 rounded-full bg-accent-subtle px-3 py-1 text-xs font-medium text-accent">
            <ShieldCheck size={14} /> Server SSH
          </div>
          <h1 class="mt-3 text-2xl font-bold tracking-tight text-fg">
            {server?.name || `Server #${serverId}`}
          </h1>
          <p class="mt-2 text-sm text-fg-subtle">
            {server?.host}:{server?.port} · {server?.username}
          </p>
        </div>

        <div class="flex flex-wrap gap-3">
          <button type="button" on:click={refreshAll} class="inline-flex items-center gap-2 rounded-lg border border-border-strong bg-surface px-4 py-2 text-sm font-medium text-fg-muted transition hover:bg-surface-muted">
            <RefreshCw size={16} /> Refresh
          </button>
          <button type="button" on:click={() => goto('/ssh/known-hosts')} class="inline-flex items-center gap-2 rounded-lg border border-border-strong bg-surface px-4 py-2 text-sm font-medium text-fg-muted transition hover:bg-surface-muted">
            <Fingerprint size={16} /> Known Hosts
          </button>
          <button type="button" on:click={() => goto('/ssh/history')} class="inline-flex items-center gap-2 rounded-lg border border-border-strong bg-surface px-4 py-2 text-sm font-medium text-fg-muted transition hover:bg-surface-muted">
            <History size={16} /> History
          </button>
        </div>
      </div>
    </div>

    {#if error}
      <div class="rounded-2xl border border-error bg-error/10 px-4 py-3 text-sm text-error" role="alert">{error}</div>
    {/if}

    {#if message}
      <div class="rounded-2xl border border-success bg-success/10 px-4 py-3 text-sm text-success">{message}</div>
    {/if}

    {#if loading}
      <div class="rounded-2xl border border-dashed border-border-strong bg-surface px-4 py-8 text-center text-fg-subtle">Loading server SSH details...</div>
    {:else if !server}
      <div class="rounded-2xl border border-error bg-error/10 px-4 py-3 text-sm text-error">Server not found.</div>
    {:else}
      <section class="grid gap-4 xl:grid-cols-2">
        <div class="rounded-2xl border border-border bg-surface p-5 shadow-sm">
          <div class="mb-4 flex items-center gap-2">
            <FileKey size={18} class="text-fg-subtle" />
            <div>
              <h2 class="text-sm font-semibold uppercase tracking-wide text-fg-subtle">Auth Status</h2>
              <p class="mt-1 text-sm text-fg-subtle">Current credential state and fingerprint details.</p>
            </div>
          </div>

          <div class="grid gap-3 md:grid-cols-2">
            <div class="rounded-xl border border-border bg-surface-muted p-4">
              <p class="text-xs font-semibold uppercase tracking-wide text-fg-subtle">Method</p>
              <p class="mt-2 text-xl font-bold text-fg">{authStatus?.authMethod || '—'}</p>
              <p class="mt-2 text-sm text-fg-subtle">Credential status: {authStatus?.credentialStatus || 'unknown'}</p>
            </div>
            <div class="rounded-xl border border-border bg-surface-muted p-4">
              <p class="text-xs font-semibold uppercase tracking-wide text-fg-subtle">Fingerprint</p>
              <p class="mt-2 break-all text-sm font-medium text-fg">{authStatus?.fingerprint || server.fingerprint || '—'}</p>
              <p class="mt-2 text-sm text-fg-subtle">Key type: {authStatus?.keyType || server.keyType || '—'}</p>
            </div>
            <div class="rounded-xl border border-border bg-surface-muted p-4">
              <p class="text-xs font-semibold uppercase tracking-wide text-fg-subtle">Credential Flags</p>
              <div class="mt-3 flex flex-wrap gap-2">
                <span class={`rounded-full px-2.5 py-1 text-xs font-medium ${booleanBadge(!!authStatus?.hasPassword)}`}>Password {authStatus?.hasPassword ? 'Yes' : 'No'}</span>
                <span class={`rounded-full px-2.5 py-1 text-xs font-medium ${booleanBadge(!!authStatus?.hasSSHKey)}`}>SSH Key {authStatus?.hasSSHKey ? 'Yes' : 'No'}</span>
                <span class={`rounded-full px-2.5 py-1 text-xs font-medium ${booleanBadge(!!authStatus?.hasPassphrase)}`}>Passphrase {authStatus?.hasPassphrase ? 'Yes' : 'No'}</span>
              </div>
            </div>
            <div class="rounded-xl border border-border bg-surface-muted p-4">
              <p class="text-xs font-semibold uppercase tracking-wide text-fg-subtle">Server Stats</p>
              <p class="mt-2 text-sm text-fg-muted">Successes: {server.successCount}</p>
              <p class="mt-1 text-sm text-fg-muted">Failures: {server.failureCount}</p>
              <p class="mt-1 text-sm text-fg-muted">Last success: {formatDate(server.lastSuccess || '')}</p>
              <p class="mt-1 text-sm text-fg-muted">Last failure: {formatDate(server.lastFailure || '')}</p>
            </div>
          </div>
        </div>

        <div class="rounded-2xl border border-border bg-surface p-5 shadow-sm">
          <div class="mb-4 flex items-center gap-2">
            <UserCheck size={18} class="text-fg-subtle" />
            <div>
              <h2 class="text-sm font-semibold uppercase tracking-wide text-fg-subtle">Credential Health</h2>
              <p class="mt-1 text-sm text-fg-subtle">Calculated score and health signals.</p>
            </div>
          </div>

          <div class="flex items-center justify-between gap-4 rounded-2xl border border-border bg-surface-muted p-4">
            <div>
              <p class="text-sm text-fg-subtle">Health Grade</p>
              <p class="mt-1 text-3xl font-bold text-fg">{healthGradeLabel(credentialHealth)}</p>
            </div>
            <span class={`rounded-full px-3 py-1 text-xs font-medium ${healthClass(credentialHealth?.score || 0)}`}>
              {credentialHealth?.score ?? 0}/100
            </span>
          </div>

          <div class="mt-4 grid gap-3 md:grid-cols-2">
            <div class="rounded-xl border border-border bg-surface-muted p-4 text-sm text-fg-muted">
              <p class="font-medium text-fg">Signals</p>
              <ul class="mt-3 space-y-2 text-sm text-fg-muted">
                <li>Password exists: {credentialHealth?.passwordExists ? 'Yes' : 'No'}</li>
                <li>Key installed: {credentialHealth?.keyInstalled ? 'Yes' : 'No'}</li>
                <li>Fingerprint verified: {credentialHealth?.fingerprintVerified ? 'Yes' : 'No'}</li>
                <li>Known host: {credentialHealth?.knownHost ? 'Yes' : 'No'}</li>
                <li>Recent success: {credentialHealth?.recentSuccess ? 'Yes' : 'No'}</li>
                <li>Recent failure: {credentialHealth?.recentFailure ? 'Yes' : 'No'}</li>
              </ul>
            </div>
            <div class="rounded-xl border border-border bg-surface-muted p-4 text-sm text-fg-muted">
              <p class="font-medium text-fg">Additional Context</p>
              <ul class="mt-3 space-y-2 text-sm text-fg-muted">
                <li>Key age: {credentialHealth?.keyAge || '—'}</li>
                <li>Passphrase enabled: {credentialHealth?.passphraseEnabled ? 'Yes' : 'No'}</li>
                <li>Bastion healthy: {credentialHealth?.bastionHealthy ? 'Yes' : 'No'}</li>
                <li>Agent healthy: {credentialHealth?.agentHealthy ? 'Yes' : 'No'}</li>
              </ul>
            </div>
          </div>
        </div>
      </section>

      <section class="grid gap-4 xl:grid-cols-2">
        <div class="rounded-2xl border border-border bg-surface p-5 shadow-sm">
          <div class="mb-4 flex items-center justify-between gap-3">
            <div>
              <h2 class="text-sm font-semibold uppercase tracking-wide text-fg-subtle">Server Keys</h2>
              <p class="mt-1 text-sm text-fg-subtle">Add, remove, and set the default server key.</p>
            </div>
            <button type="button" on:click={() => (showKeyForm = !showKeyForm)} class="inline-flex items-center gap-2 rounded-lg border border-border-strong bg-surface px-3 py-2 text-sm font-medium text-fg-muted transition hover:bg-surface-muted">
              <Plus size={16} /> {showKeyForm ? 'Hide Form' : 'Add Key'}
            </button>
          </div>

          {#if showKeyForm}
            <div class="mb-5 rounded-2xl border border-border bg-surface-muted p-4">
              <div class="grid gap-4 md:grid-cols-2">
                <div>
                  <label for="server-key-label" class="mb-1 block text-sm font-medium text-fg-muted">Label *</label>
                  <input id="server-key-label" bind:value={newKeyLabel} class="w-full rounded-lg border border-border-strong bg-surface px-3 py-2 text-fg shadow-sm outline-none transition focus:border-accent focus:ring-2 focus:ring-accent/20" placeholder="Production deploy key" />
                </div>
                <div>
                  <label for="server-key-type" class="mb-1 block text-sm font-medium text-fg-muted">Key Type</label>
                  <select id="server-key-type" bind:value={newKeyType} class="w-full rounded-lg border border-border-strong bg-surface px-3 py-2 text-fg shadow-sm outline-none transition focus:border-accent focus:ring-2 focus:ring-accent/20">
                    <option value="ed25519">ed25519</option>
                    <option value="rsa">rsa</option>
                    <option value="ecdsa">ecdsa</option>
                  </select>
                </div>
                <div class="md:col-span-2">
                  <label for="server-private-key" class="mb-1 block text-sm font-medium text-fg-muted">Private Key *</label>
                  <textarea id="server-private-key" bind:value={newPrivateKey} rows="6" class="w-full rounded-lg border border-border-strong bg-surface px-3 py-2 font-mono text-sm text-fg shadow-sm outline-none transition focus:border-accent focus:ring-2 focus:ring-accent/20" placeholder="-----BEGIN OPENSSH PRIVATE KEY-----"></textarea>
                </div>
                <div>
                  <label for="server-key-passphrase" class="mb-1 block text-sm font-medium text-fg-muted">Passphrase</label>
                  <input id="server-key-passphrase" type="password" bind:value={newPassphrase} class="w-full rounded-lg border border-border-strong bg-surface px-3 py-2 text-fg shadow-sm outline-none transition focus:border-accent focus:ring-2 focus:ring-accent/20" placeholder="Optional passphrase" />
                </div>
                <div>
                  <label for="server-key-notes" class="mb-1 block text-sm font-medium text-fg-muted">Notes</label>
                  <input id="server-key-notes" bind:value={newNotes} class="w-full rounded-lg border border-border-strong bg-surface px-3 py-2 text-fg shadow-sm outline-none transition focus:border-accent focus:ring-2 focus:ring-accent/20" placeholder="Who uses this key" />
                </div>
              </div>
              <div class="mt-4 flex flex-wrap gap-3">
                <button type="button" on:click={addServerKey} disabled={loadingAction === `/servers/${serverId}/keys`} class="inline-flex items-center gap-2 rounded-lg bg-accent px-4 py-2 text-sm font-medium text-accent-fg transition hover:bg-accent-hover disabled:cursor-not-allowed disabled:opacity-50">
                  <Upload size={16} /> Add Key
                </button>
                <button type="button" on:click={() => (showKeyForm = false)} class="inline-flex items-center gap-2 rounded-lg border border-border-strong bg-surface px-4 py-2 text-sm font-medium text-fg-muted transition hover:bg-surface-muted">
                  Cancel
                </button>
              </div>
            </div>
          {/if}

          {#if serverKeys.length === 0}
            <div class="rounded-2xl border border-dashed border-border-strong bg-surface-muted px-4 py-8 text-center text-fg-subtle">No server keys stored yet.</div>
          {:else}
            <div class="space-y-3">
              {#each serverKeys as key}
                <div class="rounded-2xl border border-border bg-surface-muted p-4">
                  <div class="flex flex-col gap-3 lg:flex-row lg:items-start lg:justify-between">
                    <div>
                      <div class="flex flex-wrap items-center gap-2">
                        <p class="font-medium text-fg">{key.label}</p>
                        {#if key.isDefault}
                          <span class="rounded-full bg-accent-subtle px-2.5 py-1 text-[11px] font-semibold uppercase tracking-wide text-accent">Default</span>
                        {/if}
                        <span class="rounded-full bg-surface-muted px-2.5 py-1 text-[11px] font-semibold uppercase tracking-wide text-fg-muted">{key.keyType}</span>
                        <span class={`rounded-full px-2.5 py-1 text-[11px] font-semibold uppercase tracking-wide ${key.enabled ? 'bg-success/15 text-success' : 'bg-error/15 text-error'}`}>{key.enabled ? 'Enabled' : 'Disabled'}</span>
                      </div>
                      <p class="mt-2 break-all font-mono text-xs text-fg-subtle">{key.fingerprint || 'No fingerprint stored'}</p>
                      {#if key.notes}
                        <p class="mt-2 text-sm text-fg-muted">{key.notes}</p>
                      {/if}
                      <div class="mt-2 text-xs text-fg-subtle">
                        Priority {key.priority} · Last used {formatDate(key.lastUsed)} · Updated {formatDate(key.updatedAt)}
                      </div>
                    </div>
                    <div class="flex flex-wrap gap-2">
                      <button type="button" on:click={() => setDefaultServerKey(key)} disabled={key.isDefault} class="inline-flex items-center gap-2 rounded-lg border border-border-strong bg-surface px-3 py-2 text-sm font-medium text-fg-muted transition hover:bg-surface-muted disabled:cursor-not-allowed disabled:opacity-50">
                        <KeyRound size={16} /> Set Default
                      </button>
                      <button type="button" on:click={() => removeServerKey(key)} class="inline-flex items-center gap-2 rounded-lg border border-error bg-surface px-3 py-2 text-sm font-medium text-error transition hover:bg-error/10">
                        <Trash2 size={16} /> Remove
                      </button>
                    </div>
                  </div>
                </div>
              {/each}
            </div>
          {/if}
        </div>

        <div class="space-y-4">
          <div class="rounded-2xl border border-border bg-surface p-5 shadow-sm">
            <div class="mb-4 flex items-center gap-2">
              <PlugZap size={18} class="text-fg-subtle" />
              <div>
                <h2 class="text-sm font-semibold uppercase tracking-wide text-fg-subtle">Actions</h2>
                <p class="mt-1 text-sm text-fg-subtle">Run SSH operations against this server.</p>
              </div>
            </div>

            <div class="grid gap-3 sm:grid-cols-2">
              <button type="button" on:click={installKey} class="inline-flex items-center justify-center gap-2 rounded-lg border border-border-strong bg-surface px-4 py-2 text-sm font-medium text-fg-muted transition hover:bg-surface-muted"><Upload size={16} /> Install Key</button>
              <button type="button" on:click={verifyKey} class="inline-flex items-center justify-center gap-2 rounded-lg border border-border-strong bg-surface px-4 py-2 text-sm font-medium text-fg-muted transition hover:bg-surface-muted"><ShieldCheck size={16} /> Verify Key</button>
              <button type="button" on:click={rotateKey} class="inline-flex items-center justify-center gap-2 rounded-lg border border-border-strong bg-surface px-4 py-2 text-sm font-medium text-fg-muted transition hover:bg-surface-muted"><RotateCw size={16} /> Rotate Key</button>
              <button type="button" on:click={testAuth} class="inline-flex items-center justify-center gap-2 rounded-lg border border-border-strong bg-surface px-4 py-2 text-sm font-medium text-fg-muted transition hover:bg-surface-muted"><TestTube2 size={16} /> Test Auth</button>
              <button type="button" on:click={removePassword} class="inline-flex items-center justify-center gap-2 rounded-lg border border-error bg-surface px-4 py-2 text-sm font-medium text-error transition hover:bg-error/10"><Trash2 size={16} /> Remove Password</button>
              <button type="button" on:click={clearKey} class="inline-flex items-center justify-center gap-2 rounded-lg border border-error bg-surface px-4 py-2 text-sm font-medium text-error transition hover:bg-error/10"><FileKey size={16} /> Clear Key</button>
            </div>
          </div>

          <div class="rounded-2xl border border-border bg-surface p-5 shadow-sm">
            <div class="mb-4 flex items-center gap-2">
              <Settings2 size={18} class="text-fg-subtle" />
              <div>
                <h2 class="text-sm font-semibold uppercase tracking-wide text-fg-subtle">Retry Config</h2>
                <p class="mt-1 text-sm text-fg-subtle">Adjust retry behavior and auth fallback order.</p>
              </div>
            </div>

            <div class="grid gap-4 md:grid-cols-2">
              <div>
                <label for="server-retry-count" class="mb-1 block text-sm font-medium text-fg-muted">Retry Count</label>
                <input id="server-retry-count" type="number" bind:value={retryCount} min="0" class="w-full rounded-lg border border-border-strong bg-surface px-3 py-2 text-fg shadow-sm outline-none transition focus:border-accent focus:ring-2 focus:ring-accent/20" />
              </div>
              <div>
                <label for="server-retry-delay" class="mb-1 block text-sm font-medium text-fg-muted">Retry Delay (ms)</label>
                <input id="server-retry-delay" type="number" bind:value={retryDelayMs} min="0" class="w-full rounded-lg border border-border-strong bg-surface px-3 py-2 text-fg shadow-sm outline-none transition focus:border-accent focus:ring-2 focus:ring-accent/20" />
              </div>
              <div>
                <label for="server-backoff-strategy" class="mb-1 block text-sm font-medium text-fg-muted">Backoff Strategy</label>
                <select id="server-backoff-strategy" bind:value={backoffStrategy} class="w-full rounded-lg border border-border-strong bg-surface px-3 py-2 text-fg shadow-sm outline-none transition focus:border-accent focus:ring-2 focus:ring-accent/20">
                  <option value="exponential">Exponential</option>
                  <option value="linear">Linear</option>
                  <option value="fixed">Fixed</option>
                </select>
              </div>
              <div>
                <label for="server-jitter" class="mb-1 block text-sm font-medium text-fg-muted">Jitter (ms)</label>
                <input id="server-jitter" type="number" bind:value={jitterMs} min="0" class="w-full rounded-lg border border-border-strong bg-surface px-3 py-2 text-fg shadow-sm outline-none transition focus:border-accent focus:ring-2 focus:ring-accent/20" />
              </div>
              <div class="md:col-span-2">
                <label for="server-reconnect-policy" class="mb-1 block text-sm font-medium text-fg-muted">Reconnect Policy</label>
                <select id="server-reconnect-policy" bind:value={reconnectPolicy} class="w-full rounded-lg border border-border-strong bg-surface px-3 py-2 text-fg shadow-sm outline-none transition focus:border-accent focus:ring-2 focus:ring-accent/20">
                  <option value="always">Always</option>
                  <option value="on-failure">On failure</option>
                  <option value="never">Never</option>
                </select>
              </div>
              <div class="md:col-span-2">
                <label for="server-auth-retry-order" class="mb-1 block text-sm font-medium text-fg-muted">Auth Retry Order</label>
                <input id="server-auth-retry-order" bind:value={authRetryOrder} class="w-full rounded-lg border border-border-strong bg-surface px-3 py-2 text-fg shadow-sm outline-none transition focus:border-accent focus:ring-2 focus:ring-accent/20" placeholder="agent,ed25519,rsa,password" />
              </div>
            </div>

            <div class="mt-4 flex flex-wrap gap-3">
              <button type="button" on:click={saveRetryConfig} class="inline-flex items-center gap-2 rounded-lg bg-accent px-4 py-2 text-sm font-medium text-accent-fg transition hover:bg-accent-hover">
                <Save size={16} /> Save Retry Config
              </button>
            </div>
          </div>

          <div class="rounded-2xl border border-border bg-surface p-5 shadow-sm">
            <div class="mb-4 flex items-center gap-2">
              <ServerCog size={18} class="text-fg-subtle" />
              <div>
                <h2 class="text-sm font-semibold uppercase tracking-wide text-fg-subtle">SSH Agent Config</h2>
                <p class="mt-1 text-sm text-fg-subtle">Configure use of the local SSH agent for this server.</p>
              </div>
            </div>

            <div class="space-y-4 rounded-2xl border border-border bg-surface-muted p-4">
              <label class="flex items-center gap-2 text-sm font-medium text-fg-muted">
                <input type="checkbox" bind:checked={useAgent} /> Use agent
              </label>
              <div>
                <label for="server-preferred-identity" class="mb-1 block text-sm font-medium text-fg-muted">Preferred Identity</label>
                <input id="server-preferred-identity" bind:value={preferredIdentity} class="w-full rounded-lg border border-border-strong bg-surface px-3 py-2 text-fg shadow-sm outline-none transition focus:border-accent focus:ring-2 focus:ring-accent/20" placeholder="Fingerprint or comment" />
              </div>
              <label class="flex items-center gap-2 text-sm font-medium text-fg-muted">
                <input type="checkbox" bind:checked={agentForwarding} /> Agent forwarding
              </label>
            </div>

            <div class="mt-4 flex flex-wrap gap-3">
              <button type="button" on:click={saveAgentConfig} class="inline-flex items-center gap-2 rounded-lg bg-accent px-4 py-2 text-sm font-medium text-accent-fg transition hover:bg-accent-hover">
                <Save size={16} /> Save Agent Config
              </button>
            </div>
          </div>

          <div class="rounded-2xl border border-border bg-surface p-5 shadow-sm">
            <div class="flex items-start gap-3">
              <AlertTriangle size={18} class="mt-0.5 text-warning" />
              <p class="text-sm text-fg-subtle">
                Buttons such as install, verify, rotate, and auth test call the SSH backend directly. Use them to validate credentials and recovery workflows.
              </p>
            </div>
          </div>
        </div>
      </section>
    {/if}
  </div>
</div>
