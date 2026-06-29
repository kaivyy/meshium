<script lang="ts">
  import { page } from '$app/stores';
  import { Search, Wifi, Bell } from 'lucide-svelte';
  import { authStore } from '$lib/stores/auth';

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

<header class="flex items-center justify-between gap-4 border-b border-slate-200 bg-white px-4 py-2.5 shrink-0">
  <!-- Page title -->
  <div class="flex items-center gap-3">
    <h2 class="text-sm font-semibold text-slate-900">{pageTitle}</h2>
  </div>

  <!-- Right side -->
  <div class="flex items-center gap-3">
    <!-- Global search (placeholder) -->
    <div class="hidden sm:flex items-center gap-2 rounded-lg border border-slate-200 bg-slate-50 px-3 py-1.5 text-sm text-slate-400 cursor-pointer hover:bg-slate-100 transition">
      <Search size={14} />
      <span>Search...</span>
      <kbd class="ml-4 hidden lg:inline-flex h-5 items-center rounded border border-slate-300 bg-white px-1.5 text-[10px] font-medium text-slate-500">⌘K</kbd>
    </div>

    <!-- Connection status -->
    <div class="flex items-center gap-1.5 text-xs text-green-600" title="Connected">
      <Wifi size={14} />
      <span class="hidden sm:inline">Connected</span>
    </div>

    <!-- Alerts bell -->
    <a href="/alerts" class="relative p-1.5 rounded-lg text-slate-400 hover:text-slate-700 hover:bg-slate-100 transition" aria-label="Alerts">
      <Bell size={16} />
    </a>
  </div>
</header>
