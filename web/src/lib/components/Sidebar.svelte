<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { page } from '$app/stores';
  import { lock } from '$lib/stores/auth';
  import { jobsApi } from '$lib/api/jobs';
  import { themeStore, cycleTheme } from '$lib/stores/theme';
  import {
    LayoutDashboard, Server, Search, ArrowRightLeft, Briefcase,
    Bot, Cpu, ScrollText, Clock, Shield, Download, GitBranch,
    Container, FolderTree, Terminal, Activity, Bell, Settings, LogOut,
    ChevronLeft, ChevronRight, ClipboardList, GitCompare,
    Grid, X, Sun, Moon, Monitor
  } from 'lucide-svelte';

  let collapsed = $state(false);
  let drawerOpen = $state(false);

  function toggleSidebar() {
    collapsed = !collapsed;
    try { localStorage.setItem('meshium-sidebar', collapsed ? '1' : '0'); } catch { /* ignore */ }
  }

  function toggleDrawer() {
    drawerOpen = !drawerOpen;
  }

  function closeDrawer() {
    drawerOpen = false;
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
        { href: '/drift', label: 'Drift', icon: GitBranch },
      ]
    },
    {
      label: 'Tools',
      items: [
        { href: '/docker', label: 'Docker', icon: Container },
        { href: '/files', label: 'Files', icon: FolderTree },
        { href: '/terminal', label: 'Terminal', icon: Terminal },
        { href: '/services', label: 'Services', icon: Settings },
        { href: '/processes', label: 'Processes', icon: Cpu },
      ]
    },
    {
      label: 'System',
      items: [
        { href: '/logs', label: 'Logs', icon: ScrollText },
        { href: '/cron', label: 'Cron', icon: Clock },
        { href: '/firewall', label: 'Firewall', icon: Shield },
        { href: '/updates', label: 'Updates', icon: Download },
      ]
    },
    {
      label: 'Insights',
      items: [
        { href: '/monitoring', label: 'Monitoring', icon: Activity },
        { href: '/assistant', label: 'Assistant', icon: Bot },
        { href: '/alerts', label: 'Alerts', icon: Bell },
      ]
    },
  ];

  // Quick-access items shown directly on the bottom bar (left + right of center button)
  const quickItems = [
    { href: '/', label: 'Home', icon: LayoutDashboard },
    { href: '/servers', label: 'Servers', icon: Server },
    { href: '/migrations', label: 'Migrate', icon: ArrowRightLeft },
    { href: '/jobs', label: 'Jobs', icon: Briefcase },
  ];

  let activeJobs = $state(0);
  let pollInterval: ReturnType<typeof setInterval> | null = null;

  async function fetchActiveJobCount() {
    try {
      const res = await jobsApi.list({ status: 'running', limit: 1 });
      activeJobs = res.length;
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

  // Close drawer on route change
  $effect(() => {
    // Reading the pathname registers it as a dependency so this effect
    // re-runs on navigation and closes the drawer.
    void $page.url.pathname;
    drawerOpen = false;
  });
</script>

<!-- Desktop sidebar -->
<aside
  class="hidden md:flex flex-col bg-surface border-r border-border shrink-0 transition-all duration-200 {collapsed ? 'w-16' : 'w-60'}"
>
  <!-- Logo -->
  <div class="flex items-center justify-between p-4 border-b border-border">
    {#if !collapsed}
      <a href="/" class="text-lg font-bold flex items-center gap-2 text-fg">
        <span class="w-8 h-8 bg-accent rounded-lg flex items-center justify-center text-accent-fg text-sm font-bold shrink-0">M</span>
        Meshium
      </a>
    {:else}
      <a href="/" class="w-8 h-8 bg-accent rounded-lg flex items-center justify-center text-accent-fg text-sm font-bold mx-auto">
        M
      </a>
    {/if}
    <button
      onclick={toggleSidebar}
      class="p-1.5 rounded-lg text-fg-subtle hover:text-fg hover:bg-surface-muted transition focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent focus-visible:ring-offset-2"
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
        <div class="px-3 pt-4 pb-1 text-[11px] font-semibold uppercase tracking-wider text-fg-subtle">
          {group.label}
        </div>
      {:else}
        <div class="my-3 border-t border-border"></div>
      {/if}
      {#each group.items as item}
        <a
          href={item.href}
          title={collapsed ? item.label : ''}
          aria-current={isActive(item.href) ? 'page' : undefined}
          class="flex items-center gap-3 px-3 py-2 rounded-lg text-sm transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent
            {isActive(item.href)
              ? 'bg-accent-subtle text-accent font-medium'
              : 'text-fg-muted hover:bg-surface-muted'}
            {collapsed ? 'justify-center' : ''}"
        >
          <item.icon size={18} class="shrink-0" />
          {#if !collapsed}
            <span>{item.label}</span>
            {#if item.href === '/jobs' && activeJobs > 0}
              <span class="ml-auto inline-flex h-5 min-w-5 items-center justify-center rounded-full bg-accent px-1.5 text-[10px] font-semibold leading-none text-accent-fg">
                {activeJobs}
              </span>
            {/if}
          {/if}
          {#if collapsed && item.href === '/jobs' && activeJobs > 0}
            <span class="absolute right-1 top-1 inline-flex h-2 w-2 rounded-full bg-accent"></span>
          {/if}
        </a>
      {/each}
    {/each}
  </nav>

  <!-- Bottom -->
  <div class="p-2 border-t border-border">
    <a
      href="/settings"
      title={collapsed ? 'Settings' : ''}
      class="flex items-center gap-3 px-3 py-2 rounded-lg text-sm text-fg-muted hover:bg-surface-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent {collapsed ? 'justify-center' : ''}"
    >
      <Settings size={18} class="shrink-0" />
      {#if !collapsed}<span>Settings</span>{/if}
    </a>
    <button
      onclick={() => lock()}
      title={collapsed ? 'Lock' : ''}
      class="w-full flex items-center gap-3 px-3 py-2 rounded-lg text-sm text-fg-muted hover:bg-surface-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent {collapsed ? 'justify-center' : ''}"
    >
      <LogOut size={18} class="shrink-0" />
      {#if !collapsed}<span>Lock</span>{/if}
    </button>
  </div>
</aside>

<!-- Mobile bottom navbar with center drawer button -->
<nav
  class="md:hidden fixed bottom-0 inset-x-0 z-40 bg-surface border-t border-border flex items-stretch px-1 pt-1 pb-[max(0.375rem,env(safe-area-inset-bottom))]"
>
  <!-- Left: 2 quick items -->
  {#each quickItems.slice(0, 2) as item}
    <a
      href={item.href}
      aria-current={isActive(item.href) ? 'page' : undefined}
      class="flex flex-1 basis-0 flex-col items-center justify-center gap-1 rounded-lg py-1.5 text-[10px] font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent
        {isActive(item.href) ? 'text-accent' : 'text-fg-subtle'}"
    >
      <span class="relative flex h-6 w-6 items-center justify-center">
        <item.icon size={20} class="shrink-0" />
        {#if item.href === '/jobs' && activeJobs > 0}
          <span class="absolute -right-2 -top-1.5 inline-flex h-4 min-w-4 items-center justify-center rounded-full bg-accent px-1 text-[9px] font-semibold leading-none text-accent-fg">
            {activeJobs}
          </span>
        {/if}
      </span>
      <span class="leading-none">{item.label}</span>
    </a>
  {/each}

  <!-- Center: Drawer toggle button -->
  <div class="flex flex-1 basis-0 items-start justify-center">
    <button
      type="button"
      onclick={toggleDrawer}
      class="flex h-12 w-12 -translate-y-3 items-center justify-center rounded-full bg-accent text-accent-fg shadow-lg shadow-accent/30 transition-transform active:scale-95 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent focus-visible:ring-offset-2 focus-visible:ring-offset-surface"
      aria-label={drawerOpen ? 'Close menu' : 'Open menu'}
      aria-expanded={drawerOpen}
    >
      {#if drawerOpen}
        <X size={22} class="shrink-0" />
      {:else}
        <Grid size={22} class="shrink-0" />
      {/if}
    </button>
  </div>

  <!-- Right: 2 quick items -->
  {#each quickItems.slice(2) as item}
    <a
      href={item.href}
      aria-current={isActive(item.href) ? 'page' : undefined}
      class="flex flex-1 basis-0 flex-col items-center justify-center gap-1 rounded-lg py-1.5 text-[10px] font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent
        {isActive(item.href) ? 'text-accent' : 'text-fg-subtle'}"
    >
      <span class="relative flex h-6 w-6 items-center justify-center">
        <item.icon size={20} class="shrink-0" />
        {#if item.href === '/jobs' && activeJobs > 0}
          <span class="absolute -right-2 -top-1.5 inline-flex h-4 min-w-4 items-center justify-center rounded-full bg-accent px-1 text-[9px] font-semibold leading-none text-accent-fg">
            {activeJobs}
          </span>
        {/if}
      </span>
      <span class="leading-none">{item.label}</span>
    </a>
  {/each}
</nav>

<!-- Mobile drawer overlay -->
{#if drawerOpen}
  <div
    class="md:hidden fixed inset-0 z-30 bg-black/30 backdrop-blur-sm transition-opacity"
    onclick={closeDrawer}
    onkeydown={(event) => {
      if (event.key === 'Enter' || event.key === ' ') closeDrawer();
    }}
    role="button"
    tabindex="0"
    aria-label="Close menu"
  ></div>
{/if}

<!-- Mobile drawer panel (slides up from bottom) -->
<div
  class="md:hidden fixed bottom-0 inset-x-0 z-50 transition-transform duration-300 ease-out
    {drawerOpen ? 'translate-y-0' : 'translate-y-full'}"
>
  <div class="bg-surface rounded-t-2xl shadow-2xl border-t border-border pb-[max(1rem,env(safe-area-inset-bottom))]">
    <!-- Handle bar -->
    <div class="flex justify-center pt-2 pb-1">
      <div class="h-1 w-10 rounded-full bg-border-strong"></div>
    </div>

    <!-- Header -->
    <div class="flex items-center justify-between px-5 py-3 border-b border-border">
      <div class="flex items-center gap-2">
        <span class="w-7 h-7 bg-accent rounded-lg flex items-center justify-center text-accent-fg text-xs font-bold">M</span>
        <span class="text-sm font-semibold text-fg">Menu</span>
      </div>
      <button
        onclick={closeDrawer}
        class="p-1.5 rounded-lg text-fg-subtle hover:text-fg hover:bg-surface-muted transition"
        aria-label="Close menu"
      >
        <X size={18} />
      </button>
    </div>

    <!-- Navigation grid -->
    <div class="px-4 py-3 max-h-[60vh] overflow-y-auto">
      {#each navGroups as group}
        <div class="px-1 pt-3 pb-1 text-[11px] font-semibold uppercase tracking-wider text-fg-subtle">
          {group.label}
        </div>
        <div class="grid grid-cols-4 gap-2">
          {#each group.items as item}
            <a
              href={item.href}
              onclick={closeDrawer}
              aria-current={isActive(item.href) ? 'page' : undefined}
              class="flex flex-col items-center gap-1.5 rounded-xl p-3 transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent
                {isActive(item.href)
                  ? 'bg-accent-subtle text-accent'
                  : 'text-fg-muted hover:bg-surface-muted'}"
            >
              <div class="relative">
                <item.icon size={22} />
                {#if item.href === '/jobs' && activeJobs > 0}
                  <span class="absolute -right-2 -top-2 inline-flex h-4 min-w-4 items-center justify-center rounded-full bg-accent px-1 text-[9px] font-semibold leading-none text-accent-fg">
                    {activeJobs}
                  </span>
                {/if}
              </div>
              <span class="text-[10px] font-medium text-center leading-tight">{item.label}</span>
            </a>
          {/each}
        </div>
      {/each}

      <!-- Settings & Lock -->
      <div class="px-1 pt-3 pb-1 text-[11px] font-semibold uppercase tracking-wider text-fg-subtle">
        System
      </div>
      <div class="grid grid-cols-4 gap-2">
        <a
          href="/settings"
          onclick={closeDrawer}
          class="flex flex-col items-center gap-1.5 rounded-xl p-3 transition-colors
            {isActive('/settings') ? 'bg-accent-subtle text-accent' : 'text-fg-muted hover:bg-surface-muted'}"
        >
          <Settings size={22} />
          <span class="text-[10px] font-medium">Settings</span>
        </a>
        <button
          onclick={() => { closeDrawer(); lock(); }}
          class="flex flex-col items-center gap-1.5 rounded-xl p-3 transition-colors text-fg-muted hover:bg-surface-muted"
        >
          <LogOut size={22} />
          <span class="text-[10px] font-medium">Lock</span>
        </button>
        <button
          onclick={() => cycleTheme($themeStore)}
          class="flex flex-col items-center gap-1.5 rounded-xl p-3 transition-colors text-fg-muted hover:bg-surface-muted"
        >
          {#if $themeStore === 'light'}<Sun size={22} />{:else if $themeStore === 'dark'}<Moon size={22} />{:else}<Monitor size={22} />{/if}
          <span class="text-[10px] font-medium">Theme</span>
        </button>
      </div>
    </div>
  </div>
</div>
