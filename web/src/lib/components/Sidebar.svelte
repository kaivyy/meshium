<script lang="ts">
  import { page } from '$app/stores';
  import { lock } from '$lib/stores/auth';
  import { Server, ArrowRightLeft, Settings, LogOut } from 'lucide-svelte';

  const navItems = [
    { href: '/', label: 'Servers', icon: Server },
    { href: '/migrations', label: 'Migrations', icon: ArrowRightLeft },
  ];

  function isActive(href: string): boolean {
    if (href === '/') return $page.url.pathname === '/';
    return $page.url.pathname.startsWith(href);
  }
</script>

<!-- Desktop sidebar -->
<aside class="hidden md:flex w-60 bg-white border-r border-slate-200 flex-col shrink-0">
  <div class="p-4 border-b border-slate-200">
    <a href="/" class="text-lg font-bold flex items-center gap-2 text-slate-900">
      <span class="w-8 h-8 bg-blue-600 rounded-lg flex items-center justify-center text-white text-sm font-bold">M</span>
      Meshium
    </a>
  </div>

  <nav class="flex-1 p-2">
    {#each navItems as item}
      <a
        href={item.href}
        class="flex items-center gap-3 px-3 py-2 rounded-lg text-sm transition-colors
          {isActive(item.href)
            ? 'bg-blue-50 text-blue-700 font-medium'
            : 'text-slate-600 hover:bg-slate-50'}"
      >
        <item.icon size={18} />
        {item.label}
      </a>
    {/each}
  </nav>

  <div class="p-2 border-t border-slate-200">
    <a href="/settings" class="flex items-center gap-3 px-3 py-2 rounded-lg text-sm text-slate-600 hover:bg-slate-50">
      <Settings size={18} />
      Settings
    </a>
    <button
      on:click={() => lock()}
      class="w-full flex items-center gap-3 px-3 py-2 rounded-lg text-sm text-slate-600 hover:bg-slate-50"
    >
      <LogOut size={18} />
      Lock
    </button>
  </div>
</aside>

<!-- Mobile bottom navbar -->
<nav class="md:hidden fixed bottom-0 inset-x-0 z-40 bg-white border-t border-slate-200 flex items-center justify-around px-2 py-1.5 pb-[max(0.375rem,env(safe-area-inset-bottom))]">
  {#each navItems as item}
    <a
      href={item.href}
      class="flex flex-col items-center gap-0.5 px-3 py-1 rounded-lg text-xs transition-colors
        {isActive(item.href) ? 'text-blue-600 font-medium' : 'text-slate-500'}"
    >
      <item.icon size={20} />
      {item.label}
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
  <button
    on:click={() => lock()}
    class="flex flex-col items-center gap-0.5 px-3 py-1 rounded-lg text-xs text-slate-500"
  >
    <LogOut size={20} />
    Lock
  </button>
</nav>
