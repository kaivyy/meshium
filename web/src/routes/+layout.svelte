<script lang="ts">
  import '../app.css';
  import { goto } from '$app/navigation';
  import { page } from '$app/stores';
  import { onMount } from 'svelte';
  import { authStore, checkStatus, lock } from '$lib/stores/auth';
  import Sidebar from '$lib/components/Sidebar.svelte';

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
  <div class="flex h-screen">
    <Sidebar />
    <main class="flex-1 overflow-auto bg-slate-50">
      <slot />
    </main>
  </div>
{/if}
