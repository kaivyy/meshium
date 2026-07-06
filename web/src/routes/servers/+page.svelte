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
          class="inline-flex items-center justify-center gap-2 rounded-lg bg-accent px-4 py-2 text-sm font-medium text-accent-fg transition hover:bg-accent-hover"
        >
          <Plus size={18} />
          Add Server
        </a>
      {/snippet}
    </PageHeader>

    <!-- Search + Favorites filter -->
    <div class="mb-6 flex flex-col gap-3 sm:flex-row sm:items-center">
      <div class="relative flex-1">
        <Search size={18} class="absolute left-3 top-1/2 -translate-y-1/2 text-fg-subtle" />
        <input
          type="text"
          value={$serverStore.searchQuery}
          oninput={handleSearch}
          placeholder="Search servers..."
          class="w-full rounded-lg border border-border-strong bg-surface py-2 pl-10 pr-4 text-fg shadow-sm outline-none transition focus:border-accent focus:ring-2 focus:ring-accent/20"
        />
      </div>

      <button
        type="button"
        onclick={() => setFilterFavorites(!$serverStore.filterFavorites)}
        aria-pressed={$serverStore.filterFavorites}
        class={`inline-flex items-center justify-center gap-2 rounded-lg border px-4 py-2 text-sm font-medium shadow-sm transition focus:outline-none focus:ring-2 focus:ring-accent/20 ${
          $serverStore.filterFavorites
            ? 'border-warning bg-warning/10 text-warning hover:bg-warning/20'
            : 'border-border-strong bg-surface text-fg-muted hover:bg-surface-muted'
        }`}
      >
        <Star
          size={18}
          fill={$serverStore.filterFavorites ? 'currentColor' : 'none'}
          class={$serverStore.filterFavorites ? 'text-warning' : 'text-fg-subtle'}
        />
        Favorites only
      </button>
    </div>

    <!-- Content -->
    {#if $serverStore.loading}
      <!-- Skeleton loading -->
      <div class="hidden md:block overflow-hidden rounded-2xl border border-border bg-surface shadow-sm">
        <table class="min-w-full divide-y divide-border">
          <thead class="bg-surface-muted">
            <tr>
              <th class="w-16 px-4 py-3"></th>
              <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-fg-subtle">Server</th>
              <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-fg-subtle">Host</th>
              <th class="hidden md:table-cell px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-fg-subtle">User</th>
              <th class="hidden lg:table-cell px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-fg-subtle">Labels</th>
              <th class="w-10 px-4 py-3"></th>
            </tr>
          </thead>
          <tbody class="divide-y divide-border bg-surface">
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
          <div class="rounded-xl border border-border bg-surface p-4 shadow-sm">
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
      <div class="rounded-lg border border-error bg-error/10 px-4 py-3 text-sm text-error">
        Failed to load servers: {$serverStore.error}
      </div>
    {:else if $serverStore.filteredServers.length === 0}
      <!-- Actionable empty state -->
      <div class="flex flex-col items-center text-center rounded-2xl border border-dashed border-border-strong bg-surface px-6 py-12">
        <div class="inline-flex items-center justify-center rounded-full bg-surface-muted p-3 text-fg-subtle">
          <Server size={24} />
        </div>
        <h3 class="mt-4 text-sm font-medium text-fg">No servers yet</h3>
        <p class="mt-1 text-sm text-fg-subtle">Add your first server to get started with Meshium.</p>
        <a
          href="/servers/new"
          class="mt-4 inline-flex items-center justify-center gap-2 rounded-lg bg-accent px-4 py-2 text-sm font-medium text-accent-fg transition hover:bg-accent-hover"
        >
          <Plus size={18} />
          Add Server
        </a>
      </div>
    {:else}
      <!-- Desktop: table -->
      <div class="hidden md:block overflow-x-auto rounded-2xl border border-border bg-surface shadow-sm">
        <table class="min-w-full divide-y divide-border">
          <thead class="bg-surface-muted">
            <tr>
              <th scope="col" class="w-16 px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-fg-subtle">
                Fav
              </th>
              <th scope="col" class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-fg-subtle">
                Server
              </th>
              <th scope="col" class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-fg-subtle">
                Host
              </th>
              <th scope="col" class="hidden lg:table-cell px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-fg-subtle">
                User
              </th>
              <th scope="col" class="hidden lg:table-cell px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-fg-subtle">
                Labels
              </th>
              <th scope="col" class="w-10 px-4 py-3"></th>
            </tr>
          </thead>
          <tbody class="divide-y divide-border bg-surface">
            {#each $serverStore.filteredServers as server}
              <tr class="hover:bg-surface-muted">
                <td class="whitespace-nowrap px-4 py-4 align-top">
                  <button
                    type="button"
                    onclick={() => toggleFavorite(server.id)}
                    aria-label={server.favorite ? `Remove ${server.name} from favorites` : `Add ${server.name} to favorites`}
                    class="inline-flex rounded-full p-1 text-fg-subtle transition hover:text-warning"
                  >
                    <Star
                      size={18}
                      fill={server.favorite ? 'currentColor' : 'none'}
                      class={server.favorite ? 'text-warning' : ''}
                    />
                  </button>
                </td>
                <td class="px-4 py-4 align-top">
                  <div class="flex items-start gap-3">
                    <div class="mt-0.5 rounded-full bg-surface-muted p-2 text-fg-subtle">
                      <Server size={16} />
                    </div>
                    <div>
                      <a href={`/servers/${server.id}`} class="font-medium text-fg transition hover:text-accent">
                        {server.name}
                      </a>
                      {#if server.description}
                        <div class="mt-1 max-w-md text-sm text-fg-subtle">{server.description}</div>
                      {/if}
                      <div class="mt-2 flex flex-wrap gap-3 text-xs font-medium">
                        <a href={`/servers/${server.id}`} class="text-accent transition hover:text-accent">View</a>
                        <a href={`/servers/${server.id}/edit`} class="text-fg-subtle transition hover:text-fg">Edit</a>
                      </div>
                    </div>
                  </div>
                </td>
                <td class="whitespace-nowrap px-4 py-4 align-top text-sm text-fg-muted">
                  {server.host}:{server.port}
                </td>
                <td class="hidden lg:table-cell whitespace-nowrap px-4 py-4 align-top text-sm text-fg-muted">
                  {server.username}
                </td>
                <td class="hidden lg:table-cell px-4 py-4 align-top">
                  <div class="flex flex-wrap gap-2">
                    {#if server.environment}
                      <span class="rounded-full bg-accent-subtle px-2 py-0.5 text-xs font-medium text-accent">{server.environment}</span>
                    {/if}
                    {#if server.region}
                      <span class="rounded-full bg-surface-muted px-2 py-0.5 text-xs font-medium text-fg-muted">{server.region}</span>
                    {/if}
                    {#each server.tags as tag}
                      <span class="rounded-full bg-surface-muted px-2 py-0.5 text-xs font-medium text-fg-muted">{tag}</span>
                    {/each}
                    {#if !server.environment && !server.region && server.tags.length === 0}
                      <span class="text-sm text-fg-subtle">—</span>
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
          <div class="rounded-xl border border-border bg-surface p-4 shadow-sm">
            <div class="flex items-start justify-between gap-2">
              <div class="flex items-start gap-3 min-w-0">
                <button
                  type="button"
                  onclick={() => toggleFavorite(server.id)}
                  class="mt-0.5 shrink-0 text-fg-subtle transition hover:text-warning"
                >
                  <Star
                    size={18}
                    fill={server.favorite ? 'currentColor' : 'none'}
                    class={server.favorite ? 'text-warning' : ''}
                  />
                </button>
                <div class="min-w-0">
                  <a href={`/servers/${server.id}`} class="font-medium text-fg hover:text-accent block truncate">
                    {server.name}
                  </a>
                  <p class="text-sm text-fg-subtle mt-0.5 truncate">{server.host}:{server.port} · {server.username}</p>
                </div>
              </div>
              <div class="rounded-full bg-surface-muted p-2 text-fg-subtle shrink-0">
                <Server size={16} />
              </div>
            </div>

            {#if server.description}
              <p class="mt-2 text-sm text-fg-subtle line-clamp-2">{server.description}</p>
            {/if}

            {#if server.environment || server.region || server.tags.length > 0}
              <div class="mt-3 flex flex-wrap gap-2">
                {#if server.environment}
                  <span class="rounded-full bg-accent-subtle px-2 py-0.5 text-xs font-medium text-accent">{server.environment}</span>
                {/if}
                {#if server.region}
                  <span class="rounded-full bg-surface-muted px-2 py-0.5 text-xs font-medium text-fg-muted">{server.region}</span>
                {/if}
                {#each server.tags as tag}
                  <span class="rounded-full bg-surface-muted px-2 py-0.5 text-xs font-medium text-fg-muted">{tag}</span>
                {/each}
              </div>
            {/if}

            <div class="mt-3 flex items-center gap-3 text-xs font-medium">
              <a href={`/servers/${server.id}`} class="text-accent hover:text-accent">View</a>
              <a href={`/servers/${server.id}/edit`} class="text-fg-subtle hover:text-fg">Edit</a>
              <a href={`/migrations/new?source=${server.id}`} class="text-fg-subtle hover:text-fg">Migrate</a>
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
