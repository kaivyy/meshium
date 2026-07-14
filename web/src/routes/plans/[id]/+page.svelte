<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { ArrowRightLeft } from 'lucide-svelte';

  // Phase 4H: plan detail (/plans/:id) is deprecated. Migration detail lives at
  // /migrations/:id/pipeline. Redirect to the migrations list.
  onMount(() => {
    goto('/migrations', { replaceState: true });
  });
</script>

<svelte:head>
  <title>Plans deprecated</title>
</svelte:head>

<div class="flex flex-col items-center justify-center gap-4 p-10 text-center">
  <ArrowRightLeft size={28} class="text-fg-subtle" aria-hidden="true" />
  <p class="text-sm text-fg-subtle">
    The planner (/plans) has been removed. Find your migrations at
    <a href="/migrations" class="text-accent underline">/migrations</a>.
  </p>
</div>
