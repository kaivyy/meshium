<script lang="ts">
  import { onMount } from 'svelte';
  import { page } from '$app/stores';
  import { lock } from '$lib/stores/auth';
  import { Server, ArrowRightLeft, Settings, LogOut, X } from 'lucide-svelte';

  export let isOpen = false;
  export let onClose = () => {};

  // Disable transitions on initial render to prevent flash
  let mounted = false;
  onMount(() => { mounted = true; });

  const navItems = [
    { href: '/', label: 'Servers', icon: Server },
    { href: '/migrations', label: 'Migrations', icon: ArrowRightLeft },
  ];

  function handleNav() {
    onClose();
  }
</script>

<!-- Mobile overlay -->
{#if isOpen}
  <div
    class="fixed inset-0 bg-black/40 z-30 md:hidden"
    on:click={onClose}
    role="button"
    tabindex="0"
    on:keydown={(e) => e.key === 'Escape' && onClose()}
  ></div>
{/if}

<aside
  class="w-60 bg-white border-r border-slate-200 flex flex-col shrink-0 z-40
    fixed md:static inset-y-0 left-0
    {mounted ? 'transition-transform duration-200' : ''}
    {isOpen ? 'translate-x-0' : '-translate-x-full'}
    md:translate-x-0"
>
  <div class="p-4 border-b border-slate-200 flex items-center justify-between">
    <a href="/" class="text-lg font-bold flex items-center gap-2 text-slate-900" on:click={handleNav}>
      <span class="w-8 h-8 bg-blue-600 rounded-lg flex items-center justify-center text-white text-sm font-bold">M</span>
      Meshium
    </a>
    <button on:click={onClose} class="md:hidden text-slate-500 hover:text-slate-700">
      <X size={20} />
    </button>
  </div>

  <nav class="flex-1 p-2">
    {#each navItems as item}
      <a
        href={item.href}
        on:click={handleNav}
        class="flex items-center gap-3 px-3 py-2 rounded-lg text-sm transition-colors
          {($page.url.pathname === '/' && item.href === '/') || ($page.url.pathname.startsWith(item.href) && item.href !== '/')
            ? 'bg-blue-50 text-blue-700 font-medium'
            : 'text-slate-600 hover:bg-slate-50'}"
      >
        <item.icon size={18} />
        {item.label}
      </a>
    {/each}
  </nav>

  <div class="p-2 border-t border-slate-200">
    <a href="/settings" on:click={handleNav} class="flex items-center gap-3 px-3 py-2 rounded-lg text-sm text-slate-600 hover:bg-slate-50">
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
