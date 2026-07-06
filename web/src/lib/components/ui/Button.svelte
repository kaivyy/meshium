<script lang="ts">
  import type { Snippet } from 'svelte';
  import Spinner from './Spinner.svelte';

  type Variant = 'primary' | 'secondary' | 'danger' | 'ghost';
  type Size = 'sm' | 'md' | 'lg';
  type ButtonType = 'button' | 'submit' | 'reset';

  interface Props {
    variant?: Variant;
    size?: Size;
    loading?: boolean;
    disabled?: boolean;
    type?: ButtonType;
    children?: Snippet;
    icon?: Snippet;
    onclick?: (event: MouseEvent) => void;
  }

  let {
    variant = 'primary',
    size = 'md',
    loading = false,
    disabled = false,
    type = 'button',
    children,
    icon,
    onclick
  }: Props = $props();

  const variantClass = $derived(
    variant === 'primary'
      ? 'bg-accent text-accent-fg hover:bg-accent-hover focus-visible:ring-accent'
      : variant === 'secondary'
        ? 'border border-border-strong bg-surface text-fg-muted hover:bg-surface-muted focus-visible:ring-accent'
        : variant === 'danger'
          ? 'bg-error text-white hover:opacity-90 focus-visible:ring-error'
          : 'text-fg-muted hover:bg-surface-muted focus-visible:ring-accent'
  );

  const sizeClass = $derived(
    size === 'sm'
      ? 'px-3 py-1.5 text-sm'
      : size === 'lg'
        ? 'px-5 py-2.5 text-base'
        : 'px-4 py-2 text-sm'
  );

  const spinnerSize = $derived(size === 'lg' ? 'md' : 'sm');
  const isDisabled = $derived(disabled || loading);
</script>

<button
  type={type}
  disabled={isDisabled}
  aria-disabled={isDisabled}
  aria-busy={loading}
  onclick={onclick}
  class={`rounded-lg font-medium transition-colors inline-flex items-center justify-center gap-2 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-offset-2 ${sizeClass} ${variantClass} ${isDisabled ? 'opacity-50 cursor-not-allowed' : ''}`}
>
  {#if loading}
    <Spinner size={spinnerSize} label="Loading" />
  {:else if icon}
    {@render icon()}
  {/if}

  {#if children}
    {@render children()}
  {/if}
</button>
