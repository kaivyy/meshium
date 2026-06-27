<script lang="ts">
  import { goto } from '$app/navigation';
  import { authStore } from '$lib/stores/auth';

  $: {
    const state = $authStore;

    if (!state.loading) {
      if (!state.setup) {
        goto('/setup');
      } else if (state.locked) {
        goto('/login');
      } else {
        goto('/servers');
      }
    }
  }
</script>
