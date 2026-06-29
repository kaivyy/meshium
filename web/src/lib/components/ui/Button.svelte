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
  }

  let {
    variant = 'primary',
    size = 'md',
    loading = false,
    disabled = false,
    type = 'button',
    children,
    icon
  }: Props = $props();

  const variantClass = $derived(
    variant === 'primary'
      ? 'bg-blue-600 text-white hover:bg-blue-700'
      : variant === 'secondary'
        ? 'border border-slate-300 bg-white text-slate-700 hover:bg-slate-50'
        : variant === 'danger'
          ? 'bg-red-600 text-white hover:bg-red-700'
          : 'text-slate-600 hover:bg-slate-100'
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
  class={`rounded-lg font-medium transition-colors inline-flex items-center justify-center gap-2 ${sizeClass} ${variantClass} ${isDisabled ? 'opacity-50 cursor-not-allowed' : ''}`}
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
