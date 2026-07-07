<script lang="ts">
  import { page } from '$app/stores';
  import { Search, Wifi, Bell } from 'lucide-svelte';
  import { authStore } from '$lib/stores/auth';
  import ThemeToggle from '$lib/components/ThemeToggle.svelte';

  // Derive page title from path
  $: path = $page.url.pathname;
  $: pageTitle = (() => {
    const parts = path.split('/').filter(Boolean);
    if (parts.length === 0) return 'Dashboard';
    const map: Record<string, string> = {
      servers: 'Servers',
      discovery: 'Discovery',
      migrations: 'Migration',
      jobs: 'Jobs',
      docker: 'Docker',
      files: 'Files',
      terminal: 'Terminal',
      monitoring: 'Monitoring',
      alerts: 'Alerts',
      settings: 'Settings',
      plans: 'Plans',
    };
    return map[parts[0]] ?? parts[0].charAt(0).toUpperCase() + parts[0].slice(1);
  })();
</script>

<header class="flex h-14 md:h-16 items-center justify-between gap-4 border-b border-border bg-surface px-4 shrink-0">
  <!-- Page title -->
  <div class="flex items-center gap-3">
    <h2 class="text-sm font-semibold text-fg">{pageTitle}</h2>
  </div>

  <!-- Right side -->
  <div class="flex items-center gap-2">
    <!-- Global search (placeholder) -->
    <div class="hidden sm:flex items-center gap-2 rounded-lg border border-border bg-surface-muted px-3 py-1.5 text-sm text-fg-subtle cursor-pointer hover:bg-surface-muted transition focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent" role="search" aria-label="Search">
      <Search size={14} aria-hidden="true" />
      <span>Search...</span>
      <kbd class="ml-4 hidden lg:inline-flex h-5 items-center rounded border border-border-strong bg-surface px-1.5 text-[10px] font-medium text-fg-subtle" aria-hidden="true">⌘K</kbd>
    </div>

    <!-- Connection status -->
    <div class="flex h-9 items-center gap-1.5 px-1.5 text-xs text-success" title="Connected" aria-label="Connection status: Connected">
      <Wifi size={16} aria-hidden="true" />
      <span class="hidden sm:inline">Connected</span>
    </div>

    <!-- Theme toggle -->
    <ThemeToggle />

    <!-- Alerts bell -->
    <a href="/alerts" class="relative flex h-9 w-9 items-center justify-center rounded-lg text-fg-subtle hover:text-fg hover:bg-surface-muted transition focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent" aria-label="View alerts">
      <Bell size={16} aria-hidden="true" />
    </a>
  </div>
</header>
