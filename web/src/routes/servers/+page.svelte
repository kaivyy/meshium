<script lang="ts">
  import { onMount } from 'svelte';
  import { Star, Plus, Search, Server } from 'lucide-svelte';
  import { lock } from '$lib/stores/auth';
  import { serverStore, fetchServers, toggleFavorite } from '$lib/stores/servers';

  let searchQuery = '';
  let environmentFilter = '';
  let regionFilter = '';

  onMount(() => {
    fetchServers();
  });

  function handleSearch() {
    fetchServers({
      q: searchQuery || undefined,
      environment: environmentFilter || undefined,
      region: regionFilter || undefined
    });
  }

  function envColor(env: string): string {
    switch (env) {
      case 'production':
        return 'bg-red-100 text-red-700';
      case 'staging':
        return 'bg-yellow-100 text-yellow-700';
      case 'development':
        return 'bg-green-100 text-green-700';
      default:
        return 'bg-gray-100 text-gray-700';
    }
  }
</script>

<div class="min-h-screen bg-slate-50">
  <header class="flex items-center justify-between border-b border-slate-200 bg-white px-6 py-4">
    <h1 class="text-xl font-bold tracking-tight text-slate-900">Meshium</h1>
    <button
      type="button"
      on:click={() => lock()}
      class="text-sm font-medium text-slate-600 transition hover:text-slate-900"
    >
      Lock
    </button>
  </header>

  <main class="mx-auto w-full max-w-6xl p-6">
    <div class="mb-6 flex items-center justify-between gap-4">
      <h2 class="text-lg font-semibold text-slate-900">Servers</h2>
      <a
        href="/servers/new"
        class="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white transition hover:bg-blue-700"
      >
        <Plus size={18} />
        Add Server
      </a>
    </div>

    <div class="mb-6 flex flex-col gap-4 md:flex-row">
      <div class="relative flex-1">
        <Search size={18} class="absolute left-3 top-1/2 -translate-y-1/2 text-slate-400" />
        <input
          type="text"
          bind:value={searchQuery}
          on:input={handleSearch}
          placeholder="Search servers..."
          class="w-full rounded-lg border border-slate-300 bg-white py-2 pl-10 pr-4 text-slate-900 shadow-sm outline-none transition focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20"
        />
      </div>

      <select
        bind:value={environmentFilter}
        on:change={handleSearch}
        class="rounded-lg border border-slate-300 bg-white px-3 py-2 text-slate-900 shadow-sm outline-none transition focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20"
      >
        <option value="">All Environments</option>
        <option value="production">Production</option>
        <option value="staging">Staging</option>
        <option value="development">Development</option>
      </select>

      <select
        bind:value={regionFilter}
        on:change={handleSearch}
        class="rounded-lg border border-slate-300 bg-white px-3 py-2 text-slate-900 shadow-sm outline-none transition focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20"
      >
        <option value="">All Regions</option>
        <option value="indonesia">Indonesia</option>
        <option value="singapore">Singapore</option>
        <option value="japan">Japan</option>
      </select>
    </div>

    {#if $serverStore.loading}
      <p class="text-slate-500">Loading...</p>
    {:else if $serverStore.servers.length === 0}
      <p class="rounded-lg border border-dashed border-slate-300 bg-white px-4 py-8 text-center text-slate-500">
        No servers found. Add one to get started.
      </p>
    {:else}
      <div class="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
        {#each $serverStore.servers as server}
          <a
            href={`/servers/${server.id}`}
            class="block rounded-xl border border-slate-200 bg-white p-4 transition hover:shadow-sm"
          >
            <div class="mb-2 flex items-start justify-between gap-3">
              <div class="flex items-center gap-2">
                <Server size={18} class="text-slate-400" />
                <span class="font-medium text-slate-900">{server.name}</span>
              </div>
              <button
                type="button"
                on:click|preventDefault={() => toggleFavorite(server.id)}
                class="text-slate-300 transition hover:text-yellow-400"
                aria-label={server.favorite ? 'Remove favorite' : 'Add favorite'}
              >
                <Star
                  size={18}
                  fill={server.favorite ? 'currentColor' : 'none'}
                  class={server.favorite ? 'text-yellow-400' : ''}
                />
              </button>
            </div>

            <p class="mb-2 text-sm text-slate-500">{server.host}:{server.port}</p>

            <div class="flex flex-wrap gap-2">
              {#if server.environment}
                <span class={`rounded-full px-2 py-0.5 text-xs font-medium ${envColor(server.environment)}`}>
                  {server.environment}
                </span>
              {/if}

              {#if server.region}
                <span class="rounded-full bg-blue-100 px-2 py-0.5 text-xs font-medium text-blue-700">
                  {server.region}
                </span>
              {/if}
            </div>
          </a>
        {/each}
      </div>
    {/if}
  </main>
</div>
