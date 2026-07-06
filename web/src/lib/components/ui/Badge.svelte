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
      ? 'bg-success/15 text-success border border-success/30'
      : variant === 'warning'
        ? 'bg-warning/15 text-warning border border-warning/30'
        : variant === 'error'
          ? 'bg-error/15 text-error border border-error/30'
          : variant === 'info'
            ? 'bg-info/15 text-info border border-info/30'
            : 'bg-surface-muted text-fg-muted border border-border'
  );

  const sizeClass = $derived(size === 'sm' ? 'px-2 py-0.5' : 'px-2.5 py-1');
</script>

<span class={`rounded-full font-medium inline-flex items-center text-xs ${sizeClass} ${variantClass}`}>
  {#if children}
    {@render children()}
  {/if}
</span>
