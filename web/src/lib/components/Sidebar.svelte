<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { page } from '$app/stores';
  import { lock } from '$lib/stores/auth';
  import { api } from '$lib/api/client';
  import {
    LayoutDashboard, Server, Search, ArrowRightLeft, Briefcase,
    Container, FolderTree, Terminal, Activity, Bell, Settings, LogOut,
    ChevronLeft, ChevronRight, ClipboardList, GitCompare
  } from 'lucide-svelte';

  let collapsed = $state(false);

  function toggleSidebar() {
    collapsed = !collapsed;
    try { localStorage.setItem('meshium-sidebar', collapsed ? '1' : '0'); } catch { /* ignore */ }
  }

  onMount(() => {
    try {
      collapsed = localStorage.getItem('meshium-sidebar') === '1';
    } catch { /* ignore */ }
  });

  const navGroups = [
    {
      label: 'Overview',
      items: [
        { href: '/', label: 'Dashboard', icon: LayoutDashboard },
        { href: '/servers', label: 'Servers', icon: Server },
        { href: '/discovery', label: 'Discovery', icon: Search },
      ]
    },
    {
      label: 'Operations',
      items: [
        { href: '/plans', label: 'Plans', icon: ClipboardList },
        { href: '/migrations', label: 'Migration', icon: ArrowRightLeft },
        { href: '/jobs', label: 'Jobs', icon: Briefcase },
      ]
    },
    {
      label: 'Compare',
      items: [
        { href: '/servers/compare', label: 'Compare', icon: GitCompare },
      ]
    },
    {
      label: 'Tools',
      items: [
        { href: '/docker', label: 'Docker', icon: Container },
        { href: '/files', label: 'Files', icon: FolderTree },
        { href: '/terminal', label: 'Terminal', icon: Terminal },
      ]
    },
    {
      label: 'Insights',
      items: [
        { href: '/monitoring', label: 'Monitoring', icon: Activity },
        { href: '/alerts', label: 'Alerts', icon: Bell },
      ]
    },
  ];

  // Flat list for mobile nav (max 5 items)
  const mobileNavItems = [
    { href: '/', label: 'Home', icon: LayoutDashboard },
    { href: '/servers', label: 'Servers', icon: Server },
    { href: '/migrations', label: 'Migrate', icon: ArrowRightLeft },
    { href: '/jobs', label: 'Jobs', icon: Briefcase },
  ];

  let activeJobs = $state(0);
  let pollInterval: ReturnType<typeof setInterval> | null = null;

  async function fetchActiveJobCount() {
    try {
      const res = await api.get<{ total: number }>('/jobs?status=running&limit=1');
      activeJobs = res?.total ?? 0;
    } catch {
      activeJobs = 0;
    }
  }

  onMount(() => {
    void fetchActiveJobCount();
    pollInterval = setInterval(() => void fetchActiveJobCount(), 10000);
  });

  onDestroy(() => {
    if (pollInterval) clearInterval(pollInterval);
  });

  function isActive(href: string): boolean {
    if (href === '/') return $page.url.pathname === '/';
    return $page.url.pathname.startsWith(href);
  }
</script>

<!-- Desktop sidebar -->
<aside
  class="hidden md:flex flex-col bg-white border-r border-slate-200 shrink-0 transition-all duration-200 {collapsed ? 'w-16' : 'w-60'}"
>
  <!-- Logo -->
  <div class="flex items-center justify-between p-4 border-b border-slate-200">
    {#if !collapsed}
      <a href="/" class="text-lg font-bold flex items-center gap-2 text-slate-900">
        <span class="w-8 h-8 bg-blue-600 rounded-lg flex items-center justify-center text-white text-sm font-bold shrink-0">M</span>
        Meshium
      </a>
    {:else}
      <a href="/" class="w-8 h-8 bg-blue-600 rounded-lg flex items-center justify-center text-white text-sm font-bold mx-auto">
        M
      </a>
    {/if}
    <button
      onclick={toggleSidebar}
      class="p-1 rounded text-slate-400 hover:text-slate-700 hover:bg-slate-100 transition"
      aria-label={collapsed ? 'Expand sidebar' : 'Collapse sidebar'}
    >
      {#if collapsed}
        <ChevronRight size={16} />
      {:else}
        <ChevronLeft size={16} />
      {/if}
    </button>
  </div>

  <!-- Nav -->
  <nav class="flex-1 overflow-y-auto p-2">
    {#each navGroups as group}
      {#if !collapsed}
        <div class="px-3 pt-4 pb-1 text-[11px] font-semibold uppercase tracking-wider text-slate-400">
          {group.label}
        </div>
      {:else}
        <div class="my-3 border-t border-slate-100"></div>
      {/if}
      {#each group.items as item}
        <a
          href={item.href}
          title={collapsed ? item.label : ''}
          class="flex items-center gap-3 px-3 py-2 rounded-lg text-sm transition-colors
            {isActive(item.href)
              ? 'bg-blue-50 text-blue-700 font-medium'
              : 'text-slate-600 hover:bg-slate-50'}
            {collapsed ? 'justify-center' : ''}"
        >
          <item.icon size={18} class="shrink-0" />
          {#if !collapsed}
            <span>{item.label}</span>
            {#if item.href === '/jobs' && activeJobs > 0}
              <span class="ml-auto inline-flex h-5 min-w-5 items-center justify-center rounded-full bg-blue-600 px-1.5 text-[10px] font-semibold leading-none text-white">
                {activeJobs}
              </span>
            {/if}
          {/if}
          {#if collapsed && item.href === '/jobs' && activeJobs > 0}
            <span class="absolute right-1 top-1 inline-flex h-2 w-2 rounded-full bg-blue-600"></span>
          {/if}
        </a>
      {/each}
    {/each}
  </nav>

  <!-- Bottom -->
  <div class="p-2 border-t border-slate-200">
    <a
      href="/settings"
      title={collapsed ? 'Settings' : ''}
      class="flex items-center gap-3 px-3 py-2 rounded-lg text-sm text-slate-600 hover:bg-slate-50 {collapsed ? 'justify-center' : ''}"
    >
      <Settings size={18} class="shrink-0" />
      {#if !collapsed}<span>Settings</span>{/if}
    </a>
    <button
      onclick={() => lock()}
      title={collapsed ? 'Lock' : ''}
      class="w-full flex items-center gap-3 px-3 py-2 rounded-lg text-sm text-slate-600 hover:bg-slate-50 {collapsed ? 'justify-center' : ''}"
    >
      <LogOut size={18} class="shrink-0" />
      {#if !collapsed}<span>Lock</span>{/if}
    </button>
  </div>
</aside>

<!-- Mobile bottom navbar -->
<nav class="md:hidden fixed bottom-0 inset-x-0 z-40 bg-white border-t border-slate-200 flex items-center justify-around px-2 py-1.5 pb-[max(0.375rem,env(safe-area-inset-bottom))]">
  {#each mobileNavItems as item}
    <a
      href={item.href}
      class="flex flex-col items-center gap-0.5 px-3 py-1 rounded-lg text-xs transition-colors
        {isActive(item.href) ? 'text-blue-600 font-medium' : 'text-slate-500'}"
    >
      <div class="relative">
        <item.icon size={20} />
        {#if item.href === '/jobs' && activeJobs > 0}
          <span class="absolute -right-2 -top-2 inline-flex h-4 min-w-4 items-center justify-center rounded-full bg-blue-600 px-1 text-[9px] font-semibold leading-none text-white">
            {activeJobs}
          </span>
        {/if}
      </div>
      <span>{item.label}</span>
    </a>
  {/each}
  <a
    href="/settings"
    class="flex flex-col items-center gap-0.5 px-3 py-1 rounded-lg text-xs transition-colors
      {isActive('/settings') ? 'text-blue-600 font-medium' : 'text-slate-500'}"
  >
    <Settings size={20} />
    Settings
  </a>
</nav>
