<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { ArrowRightLeft } from 'lucide-svelte';

  // Phase 4H: /plans is deprecated. The canonical migration path is the wizard
  // at /migrations/new (live collect) → /migrations/:id/pipeline. Redirect.
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
    The planner (/plans) has been removed. Use the migration wizard at
    <a href="/migrations/new" class="text-accent underline">/migrations/new</a>.
  </p>
</div>
