<script lang="ts">
  import '../app.css';
  import { goto } from '$app/navigation';
  import { page } from '$app/stores';
  import { onMount } from 'svelte';
  import { authStore, checkStatus } from '$lib/stores/auth';
  import Sidebar from '$lib/components/Sidebar.svelte';
  import TopBar from '$lib/components/TopBar.svelte';
  import Toast from '$lib/components/ui/Toast.svelte';
  import { initTheme } from '$lib/stores/theme';

  let statusChecked = false;

  onMount(async () => {
    initTheme();
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
  <div class="flex h-screen overflow-hidden">
    <Sidebar />
    <div class="flex-1 flex flex-col overflow-hidden">
      <TopBar />
      <main class="flex-1 overflow-auto bg-bg pb-16 md:pb-0">
        <slot />
      </main>
    </div>
  </div>
{/if}

<Toast />