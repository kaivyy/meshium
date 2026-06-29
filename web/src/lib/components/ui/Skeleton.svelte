<script lang="ts">
  interface Props {
    width?: string;
    height?: string;
    rounded?: boolean;
    class?: string;
  }

  let { width = '100%', height = '1rem', rounded = false, class: className = '' }: Props = $props();

  const radius = $derived(rounded ? 'rounded-full' : 'rounded-lg');
</script>

<div
  class={`animate-pulse bg-slate-200 ${radius} ${className}`}
  style={`width: ${width}; height: ${height};`}
  aria-hidden="true"
></div>
