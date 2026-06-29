<script lang="ts">
  type Size = 'sm' | 'md' | 'lg';

  interface Props {
    size?: Size;
    label: string;
  }

  let { size = 'md', label }: Props = $props();

  const sizeClass = $derived(
    size === 'sm' ? 'w-4 h-4' : size === 'lg' ? 'w-8 h-8' : 'w-6 h-6'
  );
</script>

<svg
  class={`shrink-0 ${sizeClass} border-2 border-current border-t-transparent rounded-full animate-spin text-slate-400`}
  viewBox="0 0 24 24"
  fill="none"
  xmlns="http://www.w3.org/2000/svg"
  role="img"
  aria-label={label}
  focusable="false"
></svg>
