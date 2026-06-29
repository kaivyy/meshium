<script lang="ts">
  import '../app.css';
  import { goto } from '$app/navigation';
  import { page } from '$app/stores';
  import { onMount } from 'svelte';
  import { authStore, checkStatus } from '$lib/stores/auth';
  import Sidebar from '$lib/components/Sidebar.svelte';

  let statusChecked = false;

  onMount(async () => {
    await checkStatus();
    statusChecked = true;
  });

  // Only redirect after the auth status check has completed
  $: if (statusChecked && !$authStore.loading) {
    const path = $page.url.pathname;

    if (!$authStore.setup && path !== '/setup') {
      goto('/setup');
    } else if ($authStore.setup && path === '/setup') {
      goto($authStore.locked ? '/login' : '/');
    } else if ($authStore.setup && $authStore.locked && path !== '/login' && path !== '/setup') {
      goto('/login');
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
