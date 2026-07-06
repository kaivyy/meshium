<script lang="ts">
  type ProgressVariant = 'default' | 'success' | 'warning' | 'error';

  interface ProgressBarProps {
    value: number;
    label?: string;
    sublabel?: string;
    variant?: ProgressVariant;
    animated?: boolean;
  }

  let { value, label, sublabel, variant = 'default', animated = false }: ProgressBarProps = $props();

  const variantClasses: Record<ProgressVariant, string> = {
    default: 'bg-accent',
    success: 'bg-success',
    warning: 'bg-warning',
    error: 'bg-error',
  };

  const percentage = $derived.by(() => Math.max(0, Math.min(100, value)));
</script>

<div class="w-full">
  {#if label}
    <div class="mb-1 flex justify-between text-sm text-fg-muted">
      <span>{label}</span>
      <span>{percentage}%</span>
    </div>
  {/if}

  <div class="w-full overflow-hidden rounded-full bg-surface-muted h-2" role="progressbar" aria-valuemin="0" aria-valuemax="100" aria-valuenow={percentage} aria-label={label || 'Progress'}>
    <div
      class={`h-2 rounded-full transition-all duration-300 ${variantClasses[variant]} ${animated ? 'animate-pulse' : ''}`}
      style={`width: ${percentage}%`}
    ></div>
  </div>

  {#if sublabel}
    <p class="mt-1 text-xs text-fg-subtle">{sublabel}</p>
  {/if}
</div>
