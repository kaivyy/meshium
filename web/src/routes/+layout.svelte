<script lang="ts">
  import '../app.css';
  import { goto } from '$app/navigation';
  import { page } from '$app/stores';
  import { onMount } from 'svelte';
  import { authStore, checkStatus } from '$lib/stores/auth';
  import Sidebar from '$lib/components/Sidebar.svelte';
  import { Menu } from 'lucide-svelte';

  let sidebarOpen = false;

  onMount(() => {
    checkStatus();
  });

  $: {
    const state = $authStore;
    const path = $page.url.pathname;

    if (!state.loading) {
      if (!state.setup && path !== '/setup') {
        goto('/setup');
      } else if (state.setup && path === '/setup') {
        goto(state.locked ? '/login' : '/');
      } else if (state.setup && state.locked && path !== '/login' && path !== '/setup') {
        goto('/login');
      }
    }
  }

  // Pages that should NOT have sidebar (full-screen auth pages)
  $: noSidebar = $page.url.pathname === '/login' || $page.url.pathname === '/setup';
</script>

{#if noSidebar}
  <slot />
{:else}
  <div class="flex h-screen overflow-hidden">
    <Sidebar isOpen={sidebarOpen} onClose={() => sidebarOpen = false} />
    <div class="flex-1 flex flex-col overflow-hidden">
      <!-- Mobile top bar -->
      <div class="md:hidden flex items-center gap-3 bg-white border-b border-slate-200 px-4 py-3 shrink-0">
        <button on:click={() => sidebarOpen = true} class="text-slate-600 hover:text-slate-900">
          <Menu size={22} />
        </button>
        <span class="font-bold text-slate-900">Meshium</span>
      </div>
      <main class="flex-1 overflow-auto bg-slate-50">
        <slot />
      </main>
    </div>
  </div>
{/if}
