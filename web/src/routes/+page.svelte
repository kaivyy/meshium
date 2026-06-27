<script lang="ts">
  import { onMount } from 'svelte';
  import { Plus, Search, Server, Star } from 'lucide-svelte';
  import {
    fetchServers,
    serverStore,
    setFilterFavorites,
    setSearchQuery,
    toggleFavorite
  } from '$lib/stores/servers';

  onMount(() => {
    fetchServers();
  });

  function handleSearch(event: Event) {
    setSearchQuery((event.currentTarget as HTMLInputElement).value);
  }
</script>

<div class="p-6">
  <div class="mb-6 flex flex-col gap-4 lg:flex-row lg:items-center lg:justify-between max-w-7xl mx-auto">
    <div>
      <h2 class="text-lg font-semibold text-slate-900">Servers</h2>
      <p class="mt-1 text-sm text-slate-500">Search, favorite, and manage your saved servers.</p>
    </div>

    <a
      href="/servers/new"
      class="inline-flex items-center justify-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white transition hover:bg-blue-700"
    >
      <Plus size={18} />
      Add Server
    </a>
  </div>

  <div class="mb-6 flex flex-col gap-3 md:flex-row md:items-center max-w-7xl mx-auto">
    <div class="relative flex-1">
      <Search size={18} class="absolute left-3 top-1/2 -translate-y-1/2 text-slate-400" />
      <input
        type="text"
        value={$serverStore.searchQuery}
        on:input={handleSearch}
        placeholder="Search servers..."
        class="w-full rounded-lg border border-slate-300 bg-white py-2 pl-10 pr-4 text-slate-900 shadow-sm outline-none transition focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20"
      />
    </div>

    <button
      type="button"
      on:click={() => setFilterFavorites(!$serverStore.filterFavorites)}
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

  <div class="max-w-7xl mx-auto">
    {#if $serverStore.loading}
      <div class="rounded-lg border border-dashed border-slate-300 bg-white px-4 py-8 text-center text-slate-500">
        Loading servers...
      </div>
    {:else if $serverStore.error}
      <div class="rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">
        Failed to load servers: {$serverStore.error}
      </div>
    {:else if $serverStore.filteredServers.length === 0}
      <p class="rounded-lg border border-dashed border-slate-300 bg-white px-4 py-8 text-center text-slate-500">
        {#if $serverStore.searchQuery || $serverStore.filterFavorites}
          No servers match your current filters.
        {:else}
          No servers found. Add one to get started.
        {/if}
      </p>
    {:else}
      <div class="overflow-x-auto rounded-2xl border border-slate-200 bg-white shadow-sm">
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
              <th scope="col" class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500">
                User
              </th>
              <th scope="col" class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500">
                Environment
              </th>
              <th scope="col" class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500">
                Region
              </th>
              <th scope="col" class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500">
                Tags
              </th>
            </tr>
          </thead>
          <tbody class="divide-y divide-slate-100 bg-white">
            {#each $serverStore.filteredServers as server}
              <tr class="hover:bg-slate-50">
                <td class="whitespace-nowrap px-4 py-4 align-top">
                  <button
                    type="button"
                    on:click={() => toggleFavorite(server.id)}
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
                <td class="whitespace-nowrap px-4 py-4 align-top text-sm text-slate-700">
                  {server.username}
                </td>
                <td class="whitespace-nowrap px-4 py-4 align-top text-sm text-slate-700">
                  {server.environment || '—'}
                </td>
                <td class="whitespace-nowrap px-4 py-4 align-top text-sm text-slate-700">
                  {server.region || '—'}
                </td>
                <td class="px-4 py-4 align-top">
                  <div class="flex flex-wrap gap-2">
                    {#if server.tags.length}
                      {#each server.tags as tag}
                        <span class="rounded-full bg-slate-100 px-2 py-1 text-xs font-medium text-slate-600">
                          {tag}
                        </span>
                      {/each}
                    {:else}
                      <span class="text-sm text-slate-400">—</span>
                    {/if}
                  </div>
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  </div>
</div>
