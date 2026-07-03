<script lang="ts">
  import type { Snippet } from 'svelte';

  type Padding = 'none' | 'sm' | 'md' | 'lg';

  interface Props {
    padding?: Padding;
    hoverable?: boolean;
    children?: Snippet;
  }

  let { padding = 'md', hoverable = false, children }: Props = $props();

  const paddingClass = $derived(
    padding === 'none' ? '' : padding === 'sm' ? 'p-3' : padding === 'lg' ? 'p-6' : 'p-4 sm:p-5'
  );

  const interactiveClass = $derived(
    hoverable ? 'hover:border-slate-300 hover:shadow-md transition-all cursor-pointer' : ''
  );
</script>

<div class={`rounded-xl border border-slate-200 bg-white shadow-sm transition-shadow ${paddingClass} ${interactiveClass}`}>
  {#if children}
    {@render children()}
  {/if}
</div>
