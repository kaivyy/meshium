<script lang="ts">
  import { onMount } from 'svelte';
  import { AlertTriangle, Loader2, Plus, RefreshCw, Server, Shield, ShieldOff, Trash2 } from 'lucide-svelte';
  import { firewallApi, type FirewallRule, type FirewallRuleRequest, type FirewallRules } from '$lib/api/firewall';
  import { Badge, Card, EmptyState, Modal, PageHeader, Skeleton, Spinner } from '$lib/components/ui';
  import { fetchServers, serverStore } from '$lib/stores/servers';
  import { toast } from '$lib/stores/toast';

  type ConfirmAction =
    | { kind: 'delete'; rule: FirewallRule }
    | { kind: 'disable' }
    | null;

  let selectedServerId = $state<number | null>(null);
  let firewall = $state<FirewallRules | null>(null);
  let loading = $state(false);
  let saving = $state(false);
  let error = $state<string | null>(null);
  let addModalOpen = $state(false);
  let confirmAction = $state<ConfirmAction>(null);
  let form = $state<FirewallRuleRequest>(defaultRuleRequest());
  let loadSeq = 0;

  const servers = $derived($serverStore.servers);
  const selectedServer = $derived(servers.find((server) => server.id === selectedServerId) ?? null);
  const canEditRules = $derived(firewall?.status.type === 'ufw' || firewall?.status.type === 'iptables');
  const isFirewalld = $derived(firewall?.status.type === 'firewalld');
  const totalRules = $derived((firewall?.rules.length ?? 0) + (firewall?.ipv6?.length ?? 0));
  const ipv6Rules = $derived(firewall?.ipv6 ?? []);
  const allowCount = $derived(countRules(firewall, ['ALLOW']));
  const blockCount = $derived(countRules(firewall, ['DENY', 'DROP', 'REJECT']));

  function defaultRuleRequest(): FirewallRuleRequest {
    return {
      action: 'ALLOW',
      protocol: 'tcp',
      port: '22',
      source: 'any',
      direction: 'IN'
    };
  }

  function countRules(data: FirewallRules | null, actions: string[]): number {
    const rules = [...(data?.rules ?? []), ...(data?.ipv6 ?? [])];
    return rules.filter((rule) => actions.includes(String(rule.action).toUpperCase())).length;
  }

  function normalizeServerSelection(value: string): number | null {
    if (!value) return null;
    const parsed = Number(value);
    return Number.isNaN(parsed) ? null : parsed;
  }

  function badgeVariantForStatus(active: boolean, type: string): 'success' | 'warning' | 'info' | 'neutral' {
    if (type === 'none') return 'neutral';
    return active ? 'success' : 'warning';
  }

  function badgeVariantForAction(action: string): 'success' | 'warning' | 'error' | 'info' | 'neutral' {
    switch (String(action).toUpperCase()) {
      case 'ALLOW':
        return 'success';
      case 'DENY':
      case 'DROP':
        return 'error';
      case 'REJECT':
        return 'warning';
      default:
        return 'neutral';
    }
  }

  function selectedRuleTitle(rule: FirewallRule): string {
    return `${rule.action} ${rule.port}`;
  }

  function ruleSupportsEditing(): boolean {
    return Boolean(selectedServer && canEditRules);
  }

  function resetForm(): void {
    form = defaultRuleRequest();
  }

  function openAddModal(): void {
    if (!ruleSupportsEditing()) return;
    resetForm();
    addModalOpen = true;
  }

  function closeAddModal(): void {
    if (saving) return;
    addModalOpen = false;
  }

  function openDeleteConfirm(rule: FirewallRule): void {
    if (!ruleSupportsEditing()) return;
    confirmAction = { kind: 'delete', rule };
  }

  function openDisableConfirm(): void {
    if (!selectedServer || !firewall?.status.active) return;
    confirmAction = { kind: 'disable' };
  }

  function closeConfirmModal(): void {
    if (saving) return;
    confirmAction = null;
  }

  async function loadFirewall(serverId: number): Promise<void> {
    const seq = ++loadSeq;
    loading = true;
    error = null;

    try {
      const data = await firewallApi.get(serverId);
      if (seq !== loadSeq) return;
      firewall = data;
    } catch (err) {
      if (seq !== loadSeq) return;
      firewall = null;
      error = err instanceof Error ? err.message : 'Failed to load firewall data';
      toast.error(error);
    } finally {
      if (seq === loadSeq) loading = false;
    }
  }

  async function refreshFirewall(): Promise<void> {
    if (!selectedServerId) return;
    await loadFirewall(selectedServerId);
  }

  async function submitRule(): Promise<void> {
    if (!selectedServerId) return;

    saving = true;
    try {
      await firewallApi.addRule(selectedServerId, form);
      toast.success('Firewall rule added');
      addModalOpen = false;
      resetForm();
      await loadFirewall(selectedServerId);
    } catch (err) {
      toast.error(err instanceof Error ? err.message : 'Failed to add firewall rule');
    } finally {
      saving = false;
    }
  }

  async function deleteSelectedRule(): Promise<void> {
    if (!selectedServerId || confirmAction?.kind !== 'delete') return;

    saving = true;
    try {
      await firewallApi.deleteRule(selectedServerId, confirmAction.rule.id);
      toast.success('Firewall rule deleted');
      confirmAction = null;
      await loadFirewall(selectedServerId);
    } catch (err) {
      toast.error(err instanceof Error ? err.message : 'Failed to delete firewall rule');
    } finally {
      saving = false;
    }
  }

  async function enableFirewall(): Promise<void> {
    if (!selectedServerId) return;

    saving = true;
    try {
      await firewallApi.enable(selectedServerId);
      toast.success('Firewall enabled');
      await loadFirewall(selectedServerId);
    } catch (err) {
      toast.error(err instanceof Error ? err.message : 'Failed to enable firewall');
    } finally {
      saving = false;
    }
  }

  async function disableFirewall(): Promise<void> {
    if (!selectedServerId) return;

    saving = true;
    try {
      await firewallApi.disable(selectedServerId);
      toast.success('Firewall disabled');
      confirmAction = null;
      await loadFirewall(selectedServerId);
    } catch (err) {
      toast.error(err instanceof Error ? err.message : 'Failed to disable firewall');
    } finally {
      saving = false;
    }
  }

  function toggleFirewall(): void {
    if (!selectedServerId || !firewall) return;
    if (firewall.status.active) {
      openDisableConfirm();
      return;
    }
    void enableFirewall();
  }

  function selectServer(value: string): void {
    selectedServerId = normalizeServerSelection(value);
  }

  onMount(async () => {
    if ($serverStore.servers.length === 0) {
      await fetchServers();
    }
  });

  $effect(() => {
    if (servers.length === 0) {
      selectedServerId = null;
      firewall = null;
      return;
    }

    if (selectedServerId === null || !servers.some((server) => server.id === selectedServerId)) {
      selectedServerId = servers[0].id;
    }
  });

  $effect(() => {
    if (selectedServerId !== null) {
      void loadFirewall(selectedServerId);
    }
  });
</script>

<svelte:head>
  <title>Firewall Manager</title>
</svelte:head>

<div class="mx-auto max-w-7xl p-4 sm:p-6 lg:p-8">
  <PageHeader title="Firewall Manager" subtitle="View and manage firewall rules on remote servers." backHref="/servers">
    {#snippet actions()}
      <button
        type="button"
        class="inline-flex items-center gap-2 rounded-lg border border-border-strong bg-surface px-3 py-2 text-sm font-medium text-fg-muted transition-colors hover:bg-surface-muted disabled:cursor-not-allowed disabled:opacity-50"
        onclick={refreshFirewall}
        disabled={loading || selectedServerId === null}
      >
        <RefreshCw size={16} class={loading ? 'animate-spin' : ''} />
        Refresh
      </button>
      <button
        type="button"
        class="inline-flex items-center gap-2 rounded-lg bg-accent px-3 py-2 text-sm font-medium text-accent-fg transition-colors hover:bg-accent-hover disabled:cursor-not-allowed disabled:opacity-50"
        onclick={openAddModal}
        disabled={!ruleSupportsEditing()}
      >
        <Plus size={16} />
        Add rule
      </button>
    {/snippet}
  </PageHeader>

  {#if $serverStore.loading}
    <Card padding="lg">
      <div class="flex items-center justify-center gap-3 py-12 text-fg-subtle">
        <Spinner size="md" label="Loading servers" />
        <span>Loading server list...</span>
      </div>
    </Card>
  {:else if servers.length === 0}
    {#snippet serversEmptyIcon()}
      <Server size={28} />
    {/snippet}

    {#snippet serversEmptyAction()}
      <a
        href="/servers"
        class="inline-flex items-center gap-2 rounded-lg bg-accent px-4 py-2 text-sm font-medium text-accent-fg transition-colors hover:bg-accent-hover"
      >
        Go to servers
      </a>
    {/snippet}

    <EmptyState
      title="No servers available"
      description="Add a server first, then return here to manage its firewall."
      icon={serversEmptyIcon}
      action={serversEmptyAction}
    />
  {:else}
    <div class="grid gap-4 xl:grid-cols-[320px_minmax(0,1fr)]">
      <Card padding="lg">
        <div class="space-y-4">
          <div>
            <h2 class="text-base font-semibold text-fg">Server</h2>
            <p class="mt-1 text-sm text-fg-subtle">Select the server you want to inspect.</p>
          </div>

          <div class="space-y-2">
            <label for="firewall-server" class="text-sm font-medium text-fg">Remote server</label>
            <select
              id="firewall-server"
              class="w-full rounded-lg border border-border-strong bg-surface px-3 py-2.5 text-sm text-fg shadow-sm outline-none transition focus:border-accent focus:ring-2 focus:ring-accent/20"
              value={selectedServerId ?? ''}
              onchange={(event) => selectServer((event.currentTarget as HTMLSelectElement).value)}
            >
              {#each servers as server}
                <option value={server.id}>{server.name} · {server.host}</option>
              {/each}
            </select>
          </div>

          {#if selectedServer}
            <div class="rounded-xl border border-border bg-surface-muted p-4 text-sm text-fg-muted">
              <div class="flex items-center gap-2 font-medium text-fg">
                <Shield size={16} />
                {selectedServer.name}
              </div>
              <div class="mt-2 space-y-1 text-xs text-fg-subtle">
                <div>{selectedServer.username}@{selectedServer.host}:{selectedServer.port ?? 22}</div>
                <div>Firewall type: {firewall?.status.type ?? 'loading...'}</div>
              </div>
            </div>
          {/if}

          {#if isFirewalld}
            <div class="rounded-xl border border-warning/20 bg-warning/10 p-4 text-sm text-warning">
              <div class="flex items-start gap-2">
                <AlertTriangle size={16} class="mt-0.5 shrink-0" />
                <div>
                  <div class="font-medium">firewalld detected</div>
                  <p class="mt-1 text-xs leading-5 text-warning">
                    The current UI can display the firewall state, but rule editing is limited for firewalld.
                  </p>
                </div>
              </div>
            </div>
          {/if}

          <button
            type="button"
            class={`inline-flex w-full items-center justify-center gap-2 rounded-lg px-3 py-2.5 text-sm font-medium text-accent-fg transition-colors disabled:cursor-not-allowed disabled:opacity-50 ${firewall?.status.active ? 'bg-error hover:bg-error/90' : 'bg-success hover:bg-success/90'}`}
            onclick={toggleFirewall}
            disabled={!firewall || selectedServerId === null || firewall.status.type === 'none'}
          >
            {#if firewall?.status.active}
              <ShieldOff size={16} />
              Disable firewall
            {:else}
              <Shield size={16} />
              Enable firewall
            {/if}
          </button>
        </div>
      </Card>

      <div class="space-y-4">
        {#if loading && !firewall}
          <div class="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
            {#each Array(4) as _}
              <Card padding="md">
                <Skeleton width="55%" height="0.9rem" />
                <Skeleton width="80%" height="1.6rem" class="mt-3" />
              </Card>
            {/each}
          </div>
        {:else if error}
          <Card padding="lg">
            <div class="flex items-start gap-3 text-error">
              <AlertTriangle size={18} class="mt-0.5 shrink-0" />
              <div>
                <div class="font-semibold">Unable to load firewall</div>
                <p class="mt-1 text-sm text-error">{error}</p>
              </div>
            </div>
          </Card>
        {:else if firewall}
          <div class="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
            <Card padding="md">
              <div class="text-xs font-medium uppercase tracking-wide text-fg-subtle">Firewall type</div>
              <div class="mt-2 text-lg font-semibold text-fg">{firewall.status.type}</div>
              <Badge variant={badgeVariantForStatus(firewall.status.active, firewall.status.type)}>
                {firewall.status.active ? 'Active' : 'Inactive'}
              </Badge>
            </Card>

            <Card padding="md">
              <div class="text-xs font-medium uppercase tracking-wide text-fg-subtle">Default policy</div>
              <div class="mt-2 text-lg font-semibold text-fg">{firewall.status.default || '—'}</div>
              <div class="mt-1 text-sm text-fg-subtle">Current remote policy</div>
            </Card>

            <Card padding="md">
              <div class="text-xs font-medium uppercase tracking-wide text-fg-subtle">Rules</div>
              <div class="mt-2 text-lg font-semibold text-fg">{totalRules}</div>
              <div class="mt-1 text-sm text-fg-subtle">IPv4 and IPv6 combined</div>
            </Card>

            <Card padding="md">
              <div class="text-xs font-medium uppercase tracking-wide text-fg-subtle">Action mix</div>
              <div class="mt-2 flex gap-2">
                <Badge variant="success">Allow {allowCount}</Badge>
                <Badge variant="error">Block {blockCount}</Badge>
              </div>
              <div class="mt-1 text-sm text-fg-subtle">Counted from parsed rules</div>
            </Card>
          </div>

          <Card padding="lg">
            <div class="flex items-center justify-between gap-3">
              <div>
                <h3 class="text-base font-semibold text-fg">Rules</h3>
                <p class="mt-1 text-sm text-fg-subtle">Manage parsed firewall rules from the remote server.</p>
              </div>
              <Badge variant="info">IPv6: {ipv6Rules.length}</Badge>
            </div>

            {#if firewall.rules.length === 0}
              <div class="mt-5 rounded-xl border border-border px-4 py-8 text-center text-sm text-fg-subtle">
                No IPv4 rules found.
              </div>
            {:else}
              <!-- Mobile: stacked cards -->
              <div class="mt-5 space-y-3 md:hidden">
                {#each firewall.rules as rule}
                  <div class="rounded-xl border border-border bg-surface p-4">
                    <div class="flex items-center justify-between gap-2">
                      <Badge variant={badgeVariantForAction(rule.action)}>{rule.action}</Badge>
                      <span class="font-medium text-fg">{rule.port}</span>
                    </div>
                    <dl class="mt-3 grid grid-cols-2 gap-x-4 gap-y-2 text-sm">
                      <div>
                        <dt class="text-xs uppercase tracking-wide text-fg-subtle">Protocol</dt>
                        <dd class="text-fg-muted">{rule.protocol}</dd>
                      </div>
                      <div>
                        <dt class="text-xs uppercase tracking-wide text-fg-subtle">Direction</dt>
                        <dd class="text-fg-muted">{rule.direction}</dd>
                      </div>
                      <div class="col-span-2">
                        <dt class="text-xs uppercase tracking-wide text-fg-subtle">Source</dt>
                        <dd class="break-all text-fg-muted">{rule.source || 'any'}</dd>
                      </div>
                    </dl>
                    {#if canEditRules}
                      <button
                        type="button"
                        class="mt-3 inline-flex w-full items-center justify-center gap-1.5 rounded-lg border border-border-strong bg-surface px-3 py-2 text-xs font-medium text-fg-muted transition-colors hover:bg-surface-muted"
                        onclick={() => openDeleteConfirm(rule)}
                      >
                        <Trash2 size={14} />
                        Delete
                      </button>
                    {/if}
                  </div>
                {/each}
              </div>

              <!-- Desktop: table -->
              <div class="mt-5 hidden overflow-x-auto rounded-xl border border-border md:block">
                <table class="min-w-full divide-y divide-border text-sm">
                  <thead class="bg-surface-muted text-fg-muted">
                    <tr>
                      <th class="px-4 py-3 text-left font-medium">Action</th>
                      <th class="px-4 py-3 text-left font-medium">Port</th>
                      <th class="px-4 py-3 text-left font-medium">Protocol</th>
                      <th class="px-4 py-3 text-left font-medium">Source</th>
                      <th class="px-4 py-3 text-left font-medium">Direction</th>
                      <th class="px-4 py-3 text-left font-medium">ID</th>
                      <th class="px-4 py-3 text-right font-medium">Actions</th>
                    </tr>
                  </thead>
                  <tbody class="divide-y divide-border bg-surface">
                    {#each firewall.rules as rule}
                      <tr>
                        <td class="px-4 py-3">
                          <Badge variant={badgeVariantForAction(rule.action)}>{rule.action}</Badge>
                        </td>
                        <td class="px-4 py-3 font-medium text-fg">{rule.port}</td>
                        <td class="px-4 py-3 text-fg-muted">{rule.protocol}</td>
                        <td class="px-4 py-3 text-fg-muted">{rule.source || 'any'}</td>
                        <td class="px-4 py-3 text-fg-muted">{rule.direction}</td>
                        <td class="px-4 py-3 text-xs text-fg-subtle">{rule.id}</td>
                        <td class="px-4 py-3 text-right">
                          {#if canEditRules}
                            <button
                              type="button"
                              class="inline-flex items-center gap-1.5 rounded-lg border border-border-strong bg-surface px-3 py-1.5 text-xs font-medium text-fg-muted transition-colors hover:bg-surface-muted"
                              onclick={() => openDeleteConfirm(rule)}
                            >
                              <Trash2 size={14} />
                              Delete
                            </button>
                          {:else}
                            <span class="text-xs text-fg-subtle">Read only</span>
                          {/if}
                        </td>
                      </tr>
                    {/each}
                  </tbody>
                </table>
              </div>
            {/if}
          </Card>

          {#if ipv6Rules.length > 0}
            <Card padding="lg">
              <div class="flex items-center justify-between gap-3">
                <div>
                  <h3 class="text-base font-semibold text-fg">IPv6 rules</h3>
                  <p class="mt-1 text-sm text-fg-subtle">Additional parsed rules detected for IPv6.</p>
                </div>
                <Badge variant="neutral">{ipv6Rules.length} rules</Badge>
              </div>

              <!-- Mobile: stacked cards -->
              <div class="mt-5 space-y-3 md:hidden">
                {#each ipv6Rules as rule}
                  <div class="rounded-xl border border-border bg-surface p-4">
                    <div class="flex items-center justify-between gap-2">
                      <Badge variant={badgeVariantForAction(rule.action)}>{rule.action}</Badge>
                      <span class="font-medium text-fg">{rule.port}</span>
                    </div>
                    <dl class="mt-3 grid grid-cols-2 gap-x-4 gap-y-2 text-sm">
                      <div>
                        <dt class="text-xs uppercase tracking-wide text-fg-subtle">Protocol</dt>
                        <dd class="text-fg-muted">{rule.protocol}</dd>
                      </div>
                      <div>
                        <dt class="text-xs uppercase tracking-wide text-fg-subtle">Direction</dt>
                        <dd class="text-fg-muted">{rule.direction}</dd>
                      </div>
                      <div class="col-span-2">
                        <dt class="text-xs uppercase tracking-wide text-fg-subtle">Source</dt>
                        <dd class="break-all text-fg-muted">{rule.source || 'any'}</dd>
                      </div>
                    </dl>
                  </div>
                {/each}
              </div>

              <!-- Desktop: table -->
              <div class="mt-5 hidden overflow-x-auto rounded-xl border border-border md:block">
                <table class="min-w-full divide-y divide-border text-sm">
                  <thead class="bg-surface-muted text-fg-muted">
                    <tr>
                      <th class="px-4 py-3 text-left font-medium">Action</th>
                      <th class="px-4 py-3 text-left font-medium">Port</th>
                      <th class="px-4 py-3 text-left font-medium">Protocol</th>
                      <th class="px-4 py-3 text-left font-medium">Source</th>
                      <th class="px-4 py-3 text-left font-medium">Direction</th>
                      <th class="px-4 py-3 text-left font-medium">ID</th>
                    </tr>
                  </thead>
                  <tbody class="divide-y divide-border bg-surface">
                    {#each ipv6Rules as rule}
                      <tr>
                        <td class="px-4 py-3">
                          <Badge variant={badgeVariantForAction(rule.action)}>{rule.action}</Badge>
                        </td>
                        <td class="px-4 py-3 font-medium text-fg">{rule.port}</td>
                        <td class="px-4 py-3 text-fg-muted">{rule.protocol}</td>
                        <td class="px-4 py-3 text-fg-muted">{rule.source || 'any'}</td>
                        <td class="px-4 py-3 text-fg-muted">{rule.direction}</td>
                        <td class="px-4 py-3 text-xs text-fg-subtle">{rule.id}</td>
                      </tr>
                    {/each}
                  </tbody>
                </table>
              </div>
            </Card>
          {/if}
        {:else}
          <Card padding="lg">
            <div class="flex items-center justify-center gap-3 py-12 text-fg-subtle">
              <Spinner size="md" label="Loading firewall" />
              <span>Loading firewall status...</span>
            </div>
          </Card>
        {/if}
      </div>
    </div>
  {/if}
</div>

<Modal
  open={addModalOpen}
  title="Add firewall rule"
  size="lg"
  onClose={closeAddModal}
  children={addModalBody}
  footer={addModalFooter}
/>

<Modal
  open={confirmAction !== null}
  title={confirmAction?.kind === 'delete' ? 'Delete firewall rule' : 'Disable firewall'}
  size="md"
  onClose={closeConfirmModal}
  children={confirmModalBody}
  footer={confirmModalFooter}
/>

{#snippet addModalBody()}
  <div class="space-y-4">
    <p class="text-sm text-fg-subtle">Create a new rule for the selected server.</p>

    <div class="grid gap-4 sm:grid-cols-2">
      <label class="space-y-1 text-sm font-medium text-fg">
        <span>Action</span>
        <select bind:value={form.action} class="w-full rounded-lg border border-border-strong bg-surface px-3 py-2 text-sm text-fg">
          <option value="ALLOW">ALLOW</option>
          <option value="DENY">DENY</option>
          <option value="REJECT">REJECT</option>
          <option value="DROP">DROP</option>
        </select>
      </label>

      <label class="space-y-1 text-sm font-medium text-fg">
        <span>Protocol</span>
        <select bind:value={form.protocol} class="w-full rounded-lg border border-border-strong bg-surface px-3 py-2 text-sm text-fg">
          <option value="tcp">tcp</option>
          <option value="udp">udp</option>
          <option value="both">both</option>
        </select>
      </label>

      <label class="space-y-1 text-sm font-medium text-fg">
        <span>Port</span>
        <input bind:value={form.port} class="w-full rounded-lg border border-border-strong bg-surface px-3 py-2 text-sm text-fg" placeholder="22 or 8000:8100" />
      </label>

      <label class="space-y-1 text-sm font-medium text-fg">
        <span>Direction</span>
        <select bind:value={form.direction} class="w-full rounded-lg border border-border-strong bg-surface px-3 py-2 text-sm text-fg">
          <option value="IN">IN</option>
          <option value="OUT">OUT</option>
          <option value="FORWARD">FORWARD</option>
        </select>
      </label>
    </div>

    <label class="block space-y-1 text-sm font-medium text-fg">
      <span>Source</span>
      <input
        bind:value={form.source}
        class="w-full rounded-lg border border-border-strong bg-surface px-3 py-2 text-sm text-fg"
        placeholder="any, 10.0.0.0/8, 192.168.1.10"
      />
    </label>
  </div>
{/snippet}

{#snippet addModalFooter()}
  <div class="flex items-center justify-end gap-2">
    <button
      type="button"
      class="rounded-lg border border-border-strong bg-surface px-4 py-2 text-sm font-medium text-fg-muted transition-colors hover:bg-surface-muted disabled:cursor-not-allowed disabled:opacity-50"
      onclick={closeAddModal}
      disabled={saving}
    >
      Cancel
    </button>
    <button
      type="button"
      class="inline-flex items-center gap-2 rounded-lg bg-accent px-4 py-2 text-sm font-medium text-accent-fg transition-colors hover:bg-accent-hover disabled:cursor-not-allowed disabled:opacity-50"
      onclick={submitRule}
      disabled={saving}
    >
      {#if saving}
        <Loader2 size={16} class="animate-spin" />
      {/if}
      Add rule
    </button>
  </div>
{/snippet}

{#snippet confirmModalBody()}
  <div class="space-y-4">
    {#if confirmAction?.kind === 'delete' && confirmAction.rule}
      <div class="rounded-lg border border-error/20 bg-error/10 p-4 text-sm text-error">
        This will remove the rule <span class="font-semibold">{selectedRuleTitle(confirmAction.rule)}</span>.
      </div>
      <div class="text-sm text-fg-muted">
        <div><span class="font-medium text-fg">Source:</span> {confirmAction.rule.source || 'any'}</div>
        <div><span class="font-medium text-fg">Direction:</span> {confirmAction.rule.direction}</div>
        <div><span class="font-medium text-fg">ID:</span> {confirmAction.rule.id}</div>
      </div>
    {:else if confirmAction?.kind === 'disable'}
      <div class="rounded-lg border border-warning/20 bg-warning/10 p-4 text-sm text-warning">
        Disabling the firewall will remove the active protection on this server.
      </div>
    {/if}
  </div>
{/snippet}

{#snippet confirmModalFooter()}
  <div class="flex items-center justify-end gap-2">
    <button
      type="button"
      class="rounded-lg border border-border-strong bg-surface px-4 py-2 text-sm font-medium text-fg-muted transition-colors hover:bg-surface-muted disabled:cursor-not-allowed disabled:opacity-50"
      onclick={closeConfirmModal}
      disabled={saving}
    >
      Cancel
    </button>
    <button
      type="button"
      class={`inline-flex items-center gap-2 rounded-lg px-4 py-2 text-sm font-medium text-accent-fg transition-colors disabled:cursor-not-allowed disabled:opacity-50 ${
        confirmAction?.kind === 'delete' ? 'bg-error hover:bg-error/90' : 'bg-warning hover:bg-warning/90'
      }`}
      onclick={confirmAction?.kind === 'delete' ? deleteSelectedRule : disableFirewall}
      disabled={saving || confirmAction === null}
    >
      {#if saving}
        <Loader2 size={16} class="animate-spin" />
      {/if}
      {confirmAction?.kind === 'delete' ? 'Delete rule' : 'Disable firewall'}
    </button>
  </div>
{/snippet}
