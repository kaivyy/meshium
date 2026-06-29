<script lang="ts">
  import type { Snippet } from 'svelte';

  type Variant = 'success' | 'warning' | 'error' | 'info' | 'neutral';
  type Size = 'sm' | 'md';

  interface Props {
    variant?: Variant;
    size?: Size;
    children?: Snippet;
  }

  let { variant = 'neutral', size = 'md', children }: Props = $props();

  const variantClass = $derived(
    variant === 'success'
      ? 'bg-green-100 text-green-700'
      : variant === 'warning'
        ? 'bg-yellow-100 text-yellow-700'
        : variant === 'error'
          ? 'bg-red-100 text-red-700'
          : variant === 'info'
            ? 'bg-blue-100 text-blue-700'
            : 'bg-slate-100 text-slate-700'
  );

  const sizeClass = $derived(size === 'sm' ? 'px-2 py-0.5' : 'px-2.5 py-1');
</script>

<span class={`rounded-full font-medium inline-flex items-center text-xs ${sizeClass} ${variantClass}`}>
  {#if children}
    {@render children()}
  {/if}
</span>
