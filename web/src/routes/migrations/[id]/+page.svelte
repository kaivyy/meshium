<script lang="ts">
  import { onMount } from 'svelte';
  import { page } from '$app/stores';
  import { goto } from '$app/navigation';

  const migrationId = $page.params.id;

  onMount(() => {
    goto(`/migrations/${migrationId}/pipeline`, { replaceState: true });
  });
</script>

<div class="flex items-center justify-center h-64 text-fg-subtle">
  <svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="animate-spin mr-3"><path d="M21 12a9 9 0 1 1-6.219-8.56"/></svg>
  Redirecting to migration wizard...
</div>
