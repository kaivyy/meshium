<script lang="ts">
  import { onMount } from 'svelte';
  import { Plus, Search, Server, Star, MoreVertical, Eye, Pencil, Plug, ArrowRightLeft, GitCompare } from 'lucide-svelte';
  import {
    fetchServers,
    serverStore,
    setFilterFavorites,
    setSearchQuery,
    toggleFavorite
  } from '$lib/stores/servers';
  import { toast } from '$lib/stores/toast';
  import { PageHeader, Skeleton, DropdownMenu, type DropdownMenuItem } from '$lib/components/ui';
  import type { Snippet } from 'svelte';

  let menuOpenId = $state<number | null>(null);

  onMount(() => {
    fetchServers();
  });

  function handleSearch(event: Event) {
    setSearchQuery((event.currentTarget as HTMLInputElement).value);
  }

  function buildMenuItems(serverId: number): DropdownMenuItem[] {
    return [
      { label: 'View details', href: `/servers/${serverId}`, icon: viewIcon },
      { label: 'Edit', href: `/servers/${serverId}/edit`, icon: editIcon },
      { label: 'Test connection', onclick: () => toast.info('Testing connection...'), icon: plugIcon },
      { label: '', divider: true },
      { label: 'Migrate FROM this server', href: `/migrations/new?source=${serverId}`, icon: migrateFromIcon },
      { label: 'Migrate TO this server', href: `/migrations/new?target=${serverId}`, icon: migrateToIcon },
      { label: 'Compare with...', href: `/servers/compare?source=${serverId}`, icon: compareIcon },
    ];
  }

  function buildMobileMenuItems(serverId: number): DropdownMenuItem[] {
    return [
      { label: 'View details', href: `/servers/${serverId}` },
      { label: 'Edit', href: `/servers/${serverId}/edit` },
      { label: '', divider: true },
      { label: 'Migrate FROM', href: `/migrations/new?source=${serverId}` },
      { label: 'Migrate TO', href: `/migrations/new?target=${serverId}` },
      { label: 'Compare with...', href: `/servers/compare?source=${serverId}` },
    ];
  }
</script>

{#snippet viewIcon()}<Eye size={14} />{/snippet}
{#snippet editIcon()}<Pencil size={14} />{/snippet}
{#snippet plugIcon()}<Plug size={14} />{/snippet}
{#snippet migrateFromIcon()}<ArrowRightLeft size={14} />{/snippet}
{#snippet migrateToIcon()}<ArrowRightLeft size={14} class="rotate-180" />{/snippet}
{#snippet compareIcon()}<GitCompare size={14} />{/snippet}

<div class="p-4 sm:p-6">
  <div class="max-w-7xl mx-auto">
    <PageHeader title="Servers" subtitle="Search, favorite, and manage your saved servers.">
      {#snippet actions()}
        <a
          href="/servers/new"
          class="inline-flex items-center justify-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white transition hover:bg-blue-700"
        >
          <Plus size={18} />
          Add Server
        </a>
      {/snippet}
    </PageHeader>

    <!-- Search + Favorites filter -->
    <div class="mb-6 flex flex-col gap-3 sm:flex-row sm:items-center">
      <div class="relative flex-1">
        <Search size={18} class="absolute left-3 top-1/2 -translate-y-1/2 text-slate-400" />
        <input
          type="text"
          value={$serverStore.searchQuery}
          oninput={handleSearch}
          placeholder="Search servers..."
          class="w-full rounded-lg border border-slate-300 bg-white py-2 pl-10 pr-4 text-slate-900 shadow-sm outline-none transition focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20"
        />
      </div>

      <button
        type="button"
        onclick={() => setFilterFavorites(!$serverStore.filterFavorites)}
        aria-pressed={$serverStore.filterFavorites}
        class={`inline-flex items-center justify-center gap-2 rounded-lg border px-4 py-2 text-sm font-medium shadow-sm transition focus:outline-none focus:ring-2 focus:ring-blue-500/20 ${
          $serverStore.filterFavorites
            ? 'border-yellow-300 bg-yellow-50 text-yellow-700 hover:bg-yellow-100'
            : 'border-slate-300 bg-white text-slate-700 hover:bg-slate-50'
        }`}
      >
        <Star
          size={18}
          fill={$serverStore.filterFavorites ? 'currentColor' : 'none'}
          class={$serverStore.filterFavorites ? 'text-yellow-500' : 'text-slate-400'}
        />
        Favorites only
      </button>
    </div>

    <!-- Content -->
    {#if $serverStore.loading}
      <!-- Skeleton loading -->
      <div class="hidden md:block overflow-hidden rounded-2xl border border-slate-200 bg-white shadow-sm">
        <table class="min-w-full divide-y divide-slate-200">
          <thead class="bg-slate-50">
            <tr>
              <th class="w-16 px-4 py-3"></th>
              <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500">Server</th>
              <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500">Host</th>
              <th class="hidden md:table-cell px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500">User</th>
              <th class="hidden lg:table-cell px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500">Labels</th>
              <th class="w-10 px-4 py-3"></th>
            </tr>
          </thead>
          <tbody class="divide-y divide-slate-100 bg-white">
            {#each Array(3) as _}
              <tr>
                <td class="px-4 py-4"><Skeleton width="18px" height="18px" rounded /></td>
                <td class="px-4 py-4">
                  <div class="flex items-center gap-3">
                    <Skeleton width="32px" height="32px" rounded />
                    <div class="space-y-1.5">
                      <Skeleton width="120px" height="14px" />
                      <Skeleton width="80px" height="12px" />
                    </div>
                  </div>
                </td>
                <td class="px-4 py-4"><Skeleton width="140px" height="14px" /></td>
                <td class="hidden md:table-cell px-4 py-4"><Skeleton width="60px" height="14px" /></td>
                <td class="hidden lg:table-cell px-4 py-4"><Skeleton width="100px" height="20px" rounded /></td>
                <td class="px-4 py-4"></td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>

      <!-- Mobile skeleton -->
      <div class="md:hidden space-y-3">
        {#each Array(3) as _}
          <div class="rounded-xl border border-slate-200 bg-white p-4 shadow-sm">
            <div class="flex items-center gap-3">
              <Skeleton width="18px" height="18px" rounded />
              <div class="flex-1 space-y-1.5">
                <Skeleton width="140px" height="16px" />
                <Skeleton width="120px" height="12px" />
              </div>
              <Skeleton width="32px" height="32px" rounded />
            </div>
            <div class="mt-3 flex gap-2">
              <Skeleton width="60px" height="20px" rounded />
              <Skeleton width="50px" height="20px" rounded />
            </div>
          </div>
        {/each}
      </div>
    {:else if $serverStore.error}
      <div class="rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">
        Failed to load servers: {$serverStore.error}
      </div>
    {:else if $serverStore.filteredServers.length === 0}
      <!-- Actionable empty state -->
      <div class="flex flex-col items-center text-center rounded-2xl border border-dashed border-slate-300 bg-white px-6 py-12">
        <div class="inline-flex items-center justify-center rounded-full bg-slate-100 p-3 text-slate-400">
          <Server size={24} />
        </div>
        <h3 class="mt-4 text-sm font-medium text-slate-900">No servers yet</h3>
        <p class="mt-1 text-sm text-slate-500">Add your first server to get started with Meshium.</p>
        <a
          href="/servers/new"
          class="mt-4 inline-flex items-center justify-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white transition hover:bg-blue-700"
        >
          <Plus size={18} />
          Add Server
        </a>
      </div>
    {:else}
      <!-- Desktop: table -->
      <div class="hidden md:block overflow-x-auto rounded-2xl border border-slate-200 bg-white shadow-sm">
        <table class="min-w-full divide-y divide-slate-200">
          <thead class="bg-slate-50">
            <tr>
              <th scope="col" class="w-16 px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500">
                Fav
              </th>
              <th scope="col" class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500">
                Server
              </th>
              <th scope="col" class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500">
                Host
              </th>
              <th scope="col" class="hidden lg:table-cell px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500">
                User
              </th>
              <th scope="col" class="hidden lg:table-cell px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500">
                Labels
              </th>
              <th scope="col" class="w-10 px-4 py-3"></th>
            </tr>
          </thead>
          <tbody class="divide-y divide-slate-100 bg-white">
            {#each $serverStore.filteredServers as server}
              <tr class="hover:bg-slate-50">
                <td class="whitespace-nowrap px-4 py-4 align-top">
                  <button
                    type="button"
                    onclick={() => toggleFavorite(server.id)}
                    aria-label={server.favorite ? `Remove ${server.name} from favorites` : `Add ${server.name} to favorites`}
                    class="inline-flex rounded-full p-1 text-slate-300 transition hover:text-yellow-500"
                  >
                    <Star
                      size={18}
                      fill={server.favorite ? 'currentColor' : 'none'}
                      class={server.favorite ? 'text-yellow-500' : ''}
                    />
                  </button>
                </td>
                <td class="px-4 py-4 align-top">
                  <div class="flex items-start gap-3">
                    <div class="mt-0.5 rounded-full bg-slate-100 p-2 text-slate-500">
                      <Server size={16} />
                    </div>
                    <div>
                      <a href={`/servers/${server.id}`} class="font-medium text-slate-900 transition hover:text-blue-600">
                        {server.name}
                      </a>
                      {#if server.description}
                        <div class="mt-1 max-w-md text-sm text-slate-500">{server.description}</div>
                      {/if}
                      <div class="mt-2 flex flex-wrap gap-3 text-xs font-medium">
                        <a href={`/servers/${server.id}`} class="text-blue-600 transition hover:text-blue-700">View</a>
                        <a href={`/servers/${server.id}/edit`} class="text-slate-500 transition hover:text-slate-900">Edit</a>
                      </div>
                    </div>
                  </div>
                </td>
                <td class="whitespace-nowrap px-4 py-4 align-top text-sm text-slate-700">
                  {server.host}:{server.port}
                </td>
                <td class="hidden lg:table-cell whitespace-nowrap px-4 py-4 align-top text-sm text-slate-700">
                  {server.username}
                </td>
                <td class="hidden lg:table-cell px-4 py-4 align-top">
                  <div class="flex flex-wrap gap-2">
                    {#if server.environment}
                      <span class="rounded-full bg-blue-50 px-2 py-0.5 text-xs font-medium text-blue-600">{server.environment}</span>
                    {/if}
                    {#if server.region}
                      <span class="rounded-full bg-slate-100 px-2 py-0.5 text-xs font-medium text-slate-600">{server.region}</span>
                    {/if}
                    {#each server.tags as tag}
                      <span class="rounded-full bg-slate-100 px-2 py-0.5 text-xs font-medium text-slate-600">{tag}</span>
                    {/each}
                    {#if !server.environment && !server.region && server.tags.length === 0}
                      <span class="text-sm text-slate-300">—</span>
                    {/if}
                  </div>
                </td>
                <td class="px-4 py-4 align-top">
                  <DropdownMenu items={buildMenuItems(server.id)} />
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>

      <!-- Mobile: cards -->
      <div class="md:hidden space-y-3">
        {#each $serverStore.filteredServers as server}
          <div class="rounded-xl border border-slate-200 bg-white p-4 shadow-sm">
            <div class="flex items-start justify-between gap-2">
              <div class="flex items-start gap-3 min-w-0">
                <button
                  type="button"
                  onclick={() => toggleFavorite(server.id)}
                  class="mt-0.5 shrink-0 text-slate-300 transition hover:text-yellow-500"
                >
                  <Star
                    size={18}
                    fill={server.favorite ? 'currentColor' : 'none'}
                    class={server.favorite ? 'text-yellow-500' : ''}
                  />
                </button>
                <div class="min-w-0">
                  <a href={`/servers/${server.id}`} class="font-medium text-slate-900 hover:text-blue-600 block truncate">
                    {server.name}
                  </a>
                  <p class="text-sm text-slate-500 mt-0.5 truncate">{server.host}:{server.port} · {server.username}</p>
                </div>
              </div>
              <div class="rounded-full bg-slate-100 p-2 text-slate-500 shrink-0">
                <Server size={16} />
              </div>
            </div>

            {#if server.description}
              <p class="mt-2 text-sm text-slate-500 line-clamp-2">{server.description}</p>
            {/if}

            {#if server.environment || server.region || server.tags.length > 0}
              <div class="mt-3 flex flex-wrap gap-2">
                {#if server.environment}
                  <span class="rounded-full bg-blue-50 px-2 py-0.5 text-xs font-medium text-blue-600">{server.environment}</span>
                {/if}
                {#if server.region}
                  <span class="rounded-full bg-slate-100 px-2 py-0.5 text-xs font-medium text-slate-600">{server.region}</span>
                {/if}
                {#each server.tags as tag}
                  <span class="rounded-full bg-slate-100 px-2 py-0.5 text-xs font-medium text-slate-600">{tag}</span>
                {/each}
              </div>
            {/if}

            <div class="mt-3 flex items-center gap-3 text-xs font-medium">
              <a href={`/servers/${server.id}`} class="text-blue-600 hover:text-blue-700">View</a>
              <a href={`/servers/${server.id}/edit`} class="text-slate-500 hover:text-slate-900">Edit</a>
              <a href={`/migrations/new?source=${server.id}`} class="text-slate-500 hover:text-slate-900">Migrate</a>
              <div class="ml-auto">
                <DropdownMenu items={buildMobileMenuItems(server.id)} />
              </div>
            </div>
          </div>
        {/each}
      </div>
    {/if}
  </div>
</div>
