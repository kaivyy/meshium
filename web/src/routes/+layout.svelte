<script lang="ts">
  import '../app.css';
  import { goto } from '$app/navigation';
  import { page } from '$app/stores';
  import { onMount } from 'svelte';
  import { authStore, checkStatus } from '$lib/stores/auth';

  onMount(() => {
    checkStatus();
  });

  $: {
    const state = $authStore;

    if (!state.loading) {
      const path = $page.url.pathname;

      if (!state.setup && path !== '/setup') {
        goto('/setup');
      } else if (state.setup && state.locked && path !== '/login' && path !== '/setup') {
        goto('/login');
      }
    }
  }
</script>

<slot />
