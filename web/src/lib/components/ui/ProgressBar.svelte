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
    default: 'bg-blue-600',
    success: 'bg-green-500',
    warning: 'bg-yellow-500',
    error: 'bg-red-500',
  };

  const percentage = $derived.by(() => Math.max(0, Math.min(100, value)));
</script>

<div class="w-full">
  {#if label}
    <div class="mb-1 flex justify-between text-sm text-slate-600">
      <span>{label}</span>
      <span>{percentage}%</span>
    </div>
  {/if}

  <div class="w-full overflow-hidden rounded-full bg-slate-200 h-2">
    <div
      class={`h-2 rounded-full transition-all duration-300 ${variantClasses[variant]} ${animated ? 'animate-pulse' : ''}`}
      style={`width: ${percentage}%`}
      aria-valuemin="0"
      aria-valuemax="100"
      aria-valuenow={percentage}
      role="progressbar"
    ></div>
  </div>

  {#if sublabel}
    <p class="mt-1 text-xs text-slate-400">{sublabel}</p>
  {/if}
</div>
