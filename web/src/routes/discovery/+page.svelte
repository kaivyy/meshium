<script lang="ts">
  import { onMount } from 'svelte';
  import {
    Server as ServerIcon, Search, RefreshCw, Filter, ChevronDown, ChevronUp,
    Cpu, HardDrive, MemoryStick, Globe,
    Clock, AlertCircle
  } from 'lucide-svelte';
  import { api } from '$lib/api/client';
  import { discoveryApi, type ServerSnapshot, type CollectorError } from '$lib/api/discovery';
  import { snapshotsStore, loadSnapshots, invalidateSnapshot, hasSnapshot as hasSnap } from '$lib/stores/snapshots';
  import { type Server } from '$lib/stores/servers';
  import { Badge, Card, DropdownMenu, EmptyState, PageHeader, Skeleton, Spinner } from '$lib/components/ui';
  import { formatRelativeTime } from '$lib/utils/format';
  import { toast } from '$lib/stores/toast';

  let servers = $state([] as Server[]);
  let snapshots = $derived.by(() => $snapshotsStore);
  let loading = $state(true);
  let searchQuery = $state('');
  let showFilters = $state(false);

  let filterEnvironment = $state<string>('all');
  let filterProvider = $state<string>('all');
  let filterOS = $state<string>('all');
  let filterDocker = $state<string>('all');
  let filterDatabase = $state<string>('all');

  const environments = $derived.by(() => {
    const envs = new Set();
    servers.forEach(s => { if (s.environment) envs.add(s.environment); });
    return ['all', ...Array.from(envs).sort()];
  });

  const providers = $derived.by(() => {
    const provs = new Set();
    Object.values(snapshots).forEach(snap => { if (snap) provs.add(detectProvider(snap)); });
    return ['all', ...Array.from(provs).sort()];
  });

  const osFamilies = $derived.by(() => {
    const families = new Set();
    Object.values(snapshots).forEach(snap => {
      if (snap?.os?.distro) families.add(getOSFamily(snap.os.distro));
    });
    return ['all', ...Array.from(families).sort()];
  });

  const filteredServers = $derived.by(() => {
    let result = servers;
    if (searchQuery) {
      const q = searchQuery.toLowerCase();
      result = result.filter(s => {
        const snap = snapshots[s.id];
        return s.name.toLowerCase().includes(q) ||
          s.host.toLowerCase().includes(q) ||
          snap?.os?.hostname?.toLowerCase().includes(q);
      });
    }
    if (filterEnvironment !== 'all') {
      result = result.filter(s => s.environment === filterEnvironment);
    }
    if (filterProvider !== 'all') {
      result = result.filter(s => snapshots[s.id] && detectProvider(snapshots[s.id]!) === filterProvider);
    }
    if (filterOS !== 'all') {
      result = result.filter(s => {
        const snap = snapshots[s.id];
        return snap?.os?.distro && getOSFamily(snap.os.distro) === filterOS;
      });
    }
    if (filterDocker !== 'all') {
      result = result.filter(s => {
        const snap = snapshots[s.id];
        const hasDocker = snap?.docker?.version !== undefined;
        return filterDocker === 'yes' ? hasDocker : !hasDocker;
      });
    }
    if (filterDatabase !== 'all') {
      result = result.filter(s => {
        const snap = snapshots[s.id];
        const hasDb = (snap?.databases?.length ?? 0) > 0;
        return filterDatabase === 'yes' ? hasDb : !hasDb;
      });
    }
    return result;
  });

  onMount(async () => {
    await loadServers();
  });

  async function loadServers() {
    loading = true;
    try {
      const data = await api.get('/servers') as Server[];
      servers = data;
      await loadSnapshots(data.map(s => s.id));
    } catch {
      toast.error('Failed to load servers');
    } finally {
      loading = false;
    }
  }

  async function triggerDiscovery(serverId: number) {
    try {
      await discoveryApi.triggerDiscovery(serverId);
      toast.success('Discovery started - check Jobs for progress');
      invalidateSnapshot(serverId);
    } catch {
      toast.error('Failed to start discovery');
    }
  }

  function detectProvider(snap: ServerSnapshot): string {
    const virt = snap?.os?.virtualization?.toLowerCase() || '';
    if (virt.includes('docker')) return 'Docker';
    if (virt.includes('kvm')) return 'KVM';
    if (virt.includes('vmware')) return 'VMware';
    if (virt.includes('xen')) return 'Xen';
    return 'Unknown';
  }

  function getOSFamily(distro: string): string {
    const d = distro.toLowerCase();
    if (d.includes('ubuntu') || d.includes('debian')) return 'Debian';
    if (d.includes('centos') || d.includes('rhel') || d.includes('red hat') || d.includes('rocky') || d.includes('alma')) return 'RHEL';
    if (d.includes('fedora')) return 'Fedora';
    if (d.includes('arch')) return 'Arch';
    if (d.includes('alpine')) return 'Alpine';
    return 'Other';
  }

  function getDatabases(snap: ServerSnapshot | null | undefined): string[] {
    return snap?.databases?.map(db => db.type) ?? [];
  }

  function getCollectionErrors(snap: ServerSnapshot | null | undefined): CollectorError[] {
    return snap?.collectionErrors ?? [];
  }

  function exportSnapshot(serverId: number) {
    const snap = snapshots[serverId];
    if (!snap) return;
    const blob = new Blob([JSON.stringify(snap, null, 2)], { type: 'application/json' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = `snapshot-${serverId}.json`;
    a.click();
    URL.revokeObjectURL(url);
  }

  function viewRawSnapshot(serverId: number) {
    const snap = snapshots[serverId];
    if (!snap) return;
    const blob = new Blob([JSON.stringify(snap, null, 2)], { type: 'application/json' });
    const url = URL.createObjectURL(blob);
    window.open(url, '_blank');
  }

  function getSnapshotAge(snap: ServerSnapshot | null | undefined): string {
    if (!snap?.capturedAt) return 'Never';
    return formatRelativeTime(snap.capturedAt);
  }

  function hasSnapshot(serverId: number): boolean {
    return hasSnap(serverId);
  }
</script>

<svelte:head><title>Discovery - Meshium</title></svelte:head>

<div class="p-4 sm:p-6 max-w-7xl mx-auto">
  <PageHeader title="Discovery" subtitle="Infrastructure inventory from discovery scans.">
    {#snippet actions()}
      <button type="button" onclick={loadServers} disabled={loading} class="inline-flex items-center gap-2 rounded-lg border border-slate-300 bg-white px-4 py-2 text-sm font-medium text-slate-700 hover:bg-slate-50 disabled:opacity-60">
        {#if loading}<Spinner size="sm" label="Refreshing" />{:else}<RefreshCw size={16} />{/if}
        Refresh
      </button>
    {/snippet}
  </PageHeader>

  <div class="mb-6">
    <Card>
    <div class="flex flex-col gap-4">
      <div class="flex items-center gap-3">
        <div class="relative flex-1">
          <Search size={16} class="absolute left-3 top-1/2 -translate-y-1/2 text-slate-400" />
          <input type="text" bind:value={searchQuery} placeholder="Search servers..." class="w-full rounded-lg border border-slate-300 bg-white py-2 pl-10 pr-4 text-sm outline-none focus:border-blue-500" />
        </div>
        <button type="button" onclick={() => showFilters = !showFilters} class="inline-flex items-center gap-2 rounded-lg border border-slate-300 bg-white px-4 py-2 text-sm font-medium text-slate-700 hover:bg-slate-50">
          <Filter size={16} />Filters{#if showFilters}<ChevronUp size={16} />{:else}<ChevronDown size={16} />{/if}
        </button>
      </div>
      {#if showFilters}
        <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-5">
          <label class="block">
            <span class="mb-1 block text-xs font-semibold uppercase tracking-wide text-slate-500">Environment</span>
            <select bind:value={filterEnvironment} class="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-sm outline-none focus:border-blue-500">
              {#each environments as env}<option value={env}>{env === 'all' ? 'All' : env}</option>{/each}
            </select>
          </label>
          <label class="block">
            <span class="mb-1 block text-xs font-semibold uppercase tracking-wide text-slate-500">Provider</span>
            <select bind:value={filterProvider} class="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-sm outline-none focus:border-blue-500">
              {#each providers as prov}<option value={prov}>{prov === 'all' ? 'All' : prov}</option>{/each}
            </select>
          </label>
          <label class="block">
            <span class="mb-1 block text-xs font-semibold uppercase tracking-wide text-slate-500">OS Family</span>
            <select bind:value={filterOS} class="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-sm outline-none focus:border-blue-500">
              {#each osFamilies as os}<option value={os}>{os === 'all' ? 'All' : os}</option>{/each}
            </select>
          </label>
          <label class="block">
            <span class="mb-1 block text-xs font-semibold uppercase tracking-wide text-slate-500">Docker</span>
            <select bind:value={filterDocker} class="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-sm outline-none focus:border-blue-500">
              <option value="all">All</option><option value="yes">Has Docker</option><option value="no">No Docker</option>
            </select>
          </label>
          <label class="block">
            <span class="mb-1 block text-xs font-semibold uppercase tracking-wide text-slate-500">Database</span>
            <select bind:value={filterDatabase} class="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-sm outline-none focus:border-blue-500">
              <option value="all">All</option><option value="yes">Has Database</option><option value="no">No Database</option>
            </select>
          </label>
        </div>
      {/if}
    </div>
    </Card>
  </div>

  <div class="mb-4 flex items-center justify-between text-sm text-slate-500">
    <span>{filteredServers.length} server{filteredServers.length !== 1 ? 's' : ''}</span>
    <span>{Object.keys(snapshots).length} with snapshots</span>
  </div>

  {#if loading && servers.length === 0}
    <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
      {#each Array(6) as _}
        <Card><div class="space-y-3"><Skeleton width="60%" /><Skeleton width="40%" /><div class="grid grid-cols-2 gap-2 pt-2"><Skeleton height="3rem" rounded /><Skeleton height="3rem" rounded /></div></div></Card>
      {/each}
    </div>
  {:else if filteredServers.length === 0}
    <EmptyState title={servers.length === 0 ? "No servers yet" : "No matching servers"} description={servers.length === 0 ? "Add servers to discover their infrastructure." : "Try adjusting your filters."} icon={emptyIcon} action={servers.length === 0 ? addAction : undefined} />
  {:else}
    <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
      {#each filteredServers as server (server.id)}
        {@const snap = snapshots[server.id]}
        {@const isLoading = snap === undefined}
        {@const errors = getCollectionErrors(snap)}
        <Card padding="lg" hoverable>
          <div class="mb-3 flex items-start justify-between gap-2">
            <div class="min-w-0 flex-1">
              <div class="flex items-center gap-2">
                <h3 class="truncate text-sm font-semibold text-slate-900">{server.name}</h3>
                {#if server.environment}<Badge variant="info" size="sm">{server.environment}</Badge>{/if}
              </div>
              <p class="mt-0.5 truncate text-xs text-slate-500">{server.host}:{server.port}</p>
            </div>
            <DropdownMenu items={[
              { label: 'Open server', href: `/servers/${server.id}` },
              { label: 'Re-scan', onclick: () => triggerDiscovery(server.id) },
              ...(hasSnapshot(server.id) ? [
                { label: 'View raw snapshot', onclick: () => viewRawSnapshot(server.id) },
                { label: 'Export snapshot', onclick: () => exportSnapshot(server.id) },
              ] : []),
            ]} label="Actions" />
          </div>
          {#if isLoading}
            <div class="flex items-center justify-center py-6 text-slate-400"><Spinner size="md" label="Loading" /></div>
          {:else if !hasSnapshot(server.id)}
            <div class="rounded-lg border border-dashed border-slate-200 bg-slate-50 p-4 text-center">
              <ServerIcon size={24} class="mx-auto text-slate-300" />
              <p class="mt-2 text-sm text-slate-500">Not discovered yet</p>
              <button type="button" onclick={() => triggerDiscovery(server.id)} class="mt-3 inline-flex items-center gap-1.5 rounded-lg bg-blue-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-blue-700">
                <RefreshCw size={12} />Discover
              </button>
            </div>
          {:else}
            <div class="space-y-3">
              <div class="flex items-center gap-2 text-sm">
                <Globe size={14} class="shrink-0 text-slate-400" />
                <span class="truncate font-medium text-slate-700">{snap!.os?.hostname || 'Unknown'}</span>
              </div>
              <div class="text-xs text-slate-500">
                <span class="font-medium">{snap!.os?.distro || 'Unknown'}</span>
                <span class="mx-1">·</span>
                <span>{snap!.os?.kernel || ''}</span>
                <span class="mx-1">·</span>
                <span>{snap!.os?.architecture || ''}</span>
              </div>
              <div class="grid grid-cols-3 gap-2">
                <div class="rounded-lg bg-slate-50 p-2 text-center">
                  <Cpu size={14} class="mx-auto text-slate-400" />
                  <p class="mt-1 text-xs font-medium text-slate-700">{snap!.hardware?.cpuCores || 0}</p>
                  <p class="text-[10px] text-slate-400">Cores</p>
                </div>
                <div class="rounded-lg bg-slate-50 p-2 text-center">
                  <MemoryStick size={14} class="mx-auto text-slate-400" />
                  <p class="mt-1 text-xs font-medium text-slate-700">{snap!.hardware?.ramTotalMb ? Math.round(snap!.hardware.ramTotalMb / 1024) : 0}</p>
                  <p class="text-[10px] text-slate-400">GB RAM</p>
                </div>
                <div class="rounded-lg bg-slate-50 p-2 text-center">
                  <HardDrive size={14} class="mx-auto text-slate-400" />
                  <p class="mt-1 text-xs font-medium text-slate-700">{Math.round(snap!.hardware?.diskTotalGb || 0)}</p>
                  <p class="text-[10px] text-slate-400">GB Disk</p>
                </div>
              </div>
              <div class="flex flex-wrap gap-1.5">
                {#if snap!.docker?.version}<Badge variant="info" size="sm">Docker {snap!.docker.version}</Badge>{/if}
                {#each getDatabases(snap) as dbType}<Badge variant="warning" size="sm">{dbType}</Badge>{/each}
                {#if snap!.nginx?.version}<Badge variant="neutral" size="sm">Nginx</Badge>{/if}
                {#if snap!.services && snap!.services.length > 0}<Badge variant="neutral" size="sm">{snap!.services.length} services</Badge>{/if}
              </div>
              {#if errors.length > 0}
                <div class="flex items-center gap-1.5 text-xs text-yellow-600"><AlertCircle size={12} /><span>{errors.length} error{errors.length !== 1 ? 's' : ''}</span></div>
              {/if}
              <div class="flex items-center justify-between text-xs text-slate-400">
                <div class="flex items-center gap-1"><Clock size={12} /><span>Scanned {getSnapshotAge(snap)}</span></div>
                <a href="/servers/{server.id}" class="text-blue-600 hover:underline">View →</a>
              </div>
            </div>
          {/if}
        </Card>
      {/each}
    </div>
  {/if}
</div>

{#snippet emptyIcon()}<ServerIcon size={22} />{/snippet}
{#snippet addAction()}<a href="/servers/new" class="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700">Add Server</a>{/snippet}
