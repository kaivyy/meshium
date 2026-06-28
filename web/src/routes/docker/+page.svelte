<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import {
    Container, Search, RefreshCw, Filter, ChevronDown, ChevronUp,
    Server as ServerIcon, Box, Layers
  } from 'lucide-svelte';
  import { api } from '$lib/api/client';
  import { type ServerSnapshot, type ContainerInfo, type ImageInfo } from '$lib/api/discovery';
  import { loadSnapshots, snapshotsStore } from '$lib/stores/snapshots';
  import { type Server } from '$lib/stores/servers';
  import { Badge, Card, EmptyState, PageHeader, Skeleton, Spinner } from '$lib/components/ui';
  import { toast } from '$lib/stores/toast';

  let servers = $state([] as Server[]);
  let loading = $state(true);
  let searchQuery = $state('');
  let showFilters = $state(false);
  let filterServer = $state('all');
  let filterState = $state('all');
  let activeTab = $state<'containers' | 'images'>('containers');

  interface AggregatedContainer {
    container: ContainerInfo;
    serverId: number;
    serverName: string;
  }

  interface AggregatedImage {
    image: ImageInfo;
    serverId: number;
    serverName: string;
  }

  const allContainers = $derived.by(() => {
    const result: AggregatedContainer[] = [];
    servers.forEach(s => {
      const snap: ServerSnapshot | null | undefined = $snapshotsStore[s.id];
      if (snap?.docker?.containers) {
        snap.docker.containers.forEach(c => {
          result.push({ container: c, serverId: s.id, serverName: s.name });
        });
      }
    });
    return result;
  });

  const allImages = $derived.by(() => {
    const result: AggregatedImage[] = [];
    servers.forEach(s => {
      const snap: ServerSnapshot | null | undefined = $snapshotsStore[s.id];
      if (snap?.docker?.images) {
        snap.docker.images.forEach(img => {
          result.push({ image: img, serverId: s.id, serverName: s.name });
        });
      }
    });
    return result;
  });

  const serverOptions = $derived.by(() => {
    const svrs = new Set();
    allContainers.forEach(c => svrs.add(c.serverName));
    return ['all', ...Array.from(svrs).sort()];
  });

  const filteredContainers = $derived.by(() => {
    let result = allContainers;
    if (searchQuery) {
      const q = searchQuery.toLowerCase();
      result = result.filter(c =>
        c.container.name.toLowerCase().includes(q) ||
        c.container.image.toLowerCase().includes(q) ||
        c.serverName.toLowerCase().includes(q)
      );
    }
    if (filterServer !== 'all') {
      result = result.filter(c => c.serverName === filterServer);
    }
    if (filterState !== 'all') {
      result = result.filter(c => c.container.state.toLowerCase() === filterState);
    }
    return result;
  });

  const filteredImages = $derived.by(() => {
    let result = allImages;
    if (searchQuery) {
      const q = searchQuery.toLowerCase();
      result = result.filter(i =>
        i.image.repository.toLowerCase().includes(q) ||
        i.image.tag.toLowerCase().includes(q) ||
        i.serverName.toLowerCase().includes(q)
      );
    }
    if (filterServer !== 'all') {
      result = result.filter(i => i.serverName === filterServer);
    }
    return result;
  });

  const containerStats = $derived.by(() => {
    const stats = { running: 0, exited: 0, total: allContainers.length };
    allContainers.forEach(c => {
      if (c.container.state === 'running') stats.running++;
      else if (c.container.state === 'exited') stats.exited++;
    });
    return stats;
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

  function containerStateVariant(state: string): 'success' | 'warning' | 'error' | 'neutral' {
    const s = state.toLowerCase();
    if (s === 'running') return 'success';
    if (s === 'exited' || s === 'dead' || s === 'created') return 'neutral';
    if (s === 'error' || s === 'restarting') return 'error';
    return 'neutral';
  }

  function formatPorts(ports: { hostPort: number; containerPort: number; protocol: string }[]): string {
    if (!ports || ports.length === 0) return '—';
    return ports.map(p => `${p.hostPort}:${p.containerPort}/${p.protocol}`).join(', ');
  }

  function formatUptime(status: string): string {
    const match = status.match(/^Up\s+(.*)$/i);
    return match?.[1] ?? status;
  }
</script>

<svelte:head><title>Docker - Meshium</title></svelte:head>

<div class="p-4 sm:p-6 max-w-7xl mx-auto">
  <PageHeader title="Docker" subtitle="Docker containers and images across all servers.">
    {#snippet actions()}
      <button type="button" onclick={loadServers} disabled={loading} class="inline-flex items-center gap-2 rounded-lg border border-slate-300 bg-white px-4 py-2 text-sm font-medium text-slate-700 hover:bg-slate-50 disabled:opacity-60">
        {#if loading}<Spinner size="sm" label="Refreshing" />{:else}<RefreshCw size={16} />{/if}
        Refresh
      </button>
    {/snippet}
  </PageHeader>

  <!-- Stats cards -->
  <div class="mb-6 grid gap-4 sm:grid-cols-3">
    <Card padding="lg">
      <div class="flex items-center gap-3">
        <div class="flex h-10 w-10 items-center justify-center rounded-lg bg-blue-50 text-blue-600"><Container size={20} /></div>
        <div>
          <p class="text-2xl font-bold text-slate-900">{containerStats.total}</p>
          <p class="text-xs text-slate-500">Total Containers</p>
        </div>
      </div>
    </Card>
    <Card padding="lg">
      <div class="flex items-center gap-3">
        <div class="flex h-10 w-10 items-center justify-center rounded-lg bg-green-50 text-green-600"><Box size={20} /></div>
        <div>
          <p class="text-2xl font-bold text-slate-900">{containerStats.running}</p>
          <p class="text-xs text-slate-500">Running</p>
        </div>
      </div>
    </Card>
    <Card padding="lg">
      <div class="flex items-center gap-3">
        <div class="flex h-10 w-10 items-center justify-center rounded-lg bg-purple-50 text-purple-600"><Layers size={20} /></div>
        <div>
          <p class="text-2xl font-bold text-slate-900">{allImages.length}</p>
          <p class="text-xs text-slate-500">Images</p>
        </div>
      </div>
    </Card>
  </div>

  <!-- Tabs -->
  <div class="mb-6 border-b border-slate-200">
    <div class="-mb-px flex gap-2">
      <button type="button" onclick={() => activeTab = 'containers'} class={`rounded-t-lg border px-4 py-2 text-sm font-medium transition ${activeTab === 'containers' ? 'border-slate-200 border-b-white bg-white text-slate-900' : 'border-transparent text-slate-500 hover:bg-slate-50'}`}>
        Containers ({filteredContainers.length})
      </button>
      <button type="button" onclick={() => activeTab = 'images'} class={`rounded-t-lg border px-4 py-2 text-sm font-medium transition ${activeTab === 'images' ? 'border-slate-200 border-b-white bg-white text-slate-900' : 'border-transparent text-slate-500 hover:bg-slate-50'}`}>
        Images ({filteredImages.length})
      </button>
    </div>
  </div>

  <!-- Search and Filters -->
  <Card class="mb-6">
    <div class="flex flex-col gap-4">
      <div class="flex items-center gap-3">
        <div class="relative flex-1">
          <Search size={16} class="absolute left-3 top-1/2 -translate-y-1/2 text-slate-400" />
          <input type="text" bind:value={searchQuery} placeholder="Search containers, images, servers..." class="w-full rounded-lg border border-slate-300 bg-white py-2 pl-10 pr-4 text-sm outline-none focus:border-blue-500" />
        </div>
        <button type="button" onclick={() => showFilters = !showFilters} class="inline-flex items-center gap-2 rounded-lg border border-slate-300 bg-white px-4 py-2 text-sm font-medium text-slate-700 hover:bg-slate-50">
          <Filter size={16} />Filters{#if showFilters}<ChevronUp size={16} />{:else}<ChevronDown size={16} />{/if}
        </button>
      </div>
      {#if showFilters}
        <div class="grid gap-4 sm:grid-cols-2">
          <label class="block">
            <span class="mb-1 block text-xs font-semibold uppercase tracking-wide text-slate-500">Server</span>
            <select bind:value={filterServer} class="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-sm outline-none focus:border-blue-500">
              {#each serverOptions as srv}<option value={srv}>{srv === 'all' ? 'All servers' : srv}</option>{/each}
            </select>
          </label>
          {#if activeTab === 'containers'}
            <label class="block">
              <span class="mb-1 block text-xs font-semibold uppercase tracking-wide text-slate-500">State</span>
              <select bind:value={filterState} class="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-sm outline-none focus:border-blue-500">
                <option value="all">All states</option>
                <option value="running">Running</option>
                <option value="exited">Exited</option>
                <option value="created">Created</option>
                <option value="dead">Dead</option>
              </select>
            </label>
          {/if}
        </div>
      {/if}
    </div>
  </Card>

  <!-- Content -->
  {#if loading && servers.length === 0}
    <div class="space-y-3">
      {#each Array(5) as _}
        <Card><div class="flex items-center gap-4"><Skeleton width="200px" /><Skeleton width="100px" /><Skeleton width="150px" /><Skeleton width="100px" /></div></Card>
      {/each}
    </div>
  {:else if activeTab === 'containers'}
    {#if filteredContainers.length === 0}
      <EmptyState title="No containers found" description={allContainers.length === 0 ? "No Docker containers detected across your servers." : "Try adjusting your filters."} icon={emptyIcon} />
    {:else}
      <div class="overflow-hidden rounded-2xl border border-slate-200 bg-white shadow-sm">
        <table class="min-w-full divide-y divide-slate-200">
          <thead class="bg-slate-50">
            <tr>
              <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500">Name</th>
              <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500">Image</th>
              <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500">State</th>
              <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500">Ports</th>
              <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500">Uptime</th>
              <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500">Server</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-slate-100">
            {#each filteredContainers as item (item.container.name + item.serverId)}
              <tr class="hover:bg-slate-50 cursor-pointer" onclick={() => goto(`/servers/${item.serverId}`)}>
                <td class="px-4 py-3 font-medium text-slate-900">{item.container.name}</td>
                <td class="px-4 py-3 text-slate-600">{item.container.image}</td>
                <td class="px-4 py-3"><Badge variant={containerStateVariant(item.container.state)}>{item.container.state}</Badge></td>
                <td class="px-4 py-3 text-slate-600 text-xs">{formatPorts(item.container.ports)}</td>
                <td class="px-4 py-3 text-slate-600 text-xs">{formatUptime(item.container.status)}</td>
                <td class="px-4 py-3 text-slate-600">{item.serverName}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  {:else}
    {#if filteredImages.length === 0}
      <EmptyState title="No images found" description={allImages.length === 0 ? "No Docker images detected across your servers." : "Try adjusting your filters."} icon={emptyIcon} />
    {:else}
      <div class="overflow-hidden rounded-2xl border border-slate-200 bg-white shadow-sm">
        <table class="min-w-full divide-y divide-slate-200">
          <thead class="bg-slate-50">
            <tr>
              <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500">Repository</th>
              <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500">Tag</th>
              <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500">Size</th>
              <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500">Server</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-slate-100">
            {#each filteredImages as item (item.image.id + item.serverId)}
              <tr class="hover:bg-slate-50 cursor-pointer" onclick={() => goto(`/servers/${item.serverId}`)}>
                <td class="px-4 py-3 font-medium text-slate-900">{item.image.repository}</td>
                <td class="px-4 py-3 text-slate-600">{item.image.tag}</td>
                <td class="px-4 py-3 text-slate-600">{item.image.size}</td>
                <td class="px-4 py-3 text-slate-600">{item.serverName}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  {/if}
</div>

{#snippet emptyIcon()}<Container size={22} />{/snippet}
