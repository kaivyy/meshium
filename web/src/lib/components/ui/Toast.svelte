<script lang="ts">
  import { fade } from 'svelte/transition';
  import { X } from 'lucide-svelte';
  import { dismissToast, toasts } from '$lib/stores/toast';
  import type { Toast as ToastItem } from '$lib/stores/toast';

  const variantClasses: Record<ToastItem['variant'], string> = {
    success: 'bg-surface border border-success/30 text-success',
    error: 'bg-surface border border-error/30 text-error',
    warning: 'bg-surface border border-warning/30 text-warning',
    info: 'bg-surface border border-info/30 text-info',
  };
</script>

<div class="fixed bottom-4 right-4 z-50 flex max-w-sm flex-col gap-2">
  {#each $toasts as toast (toast.id)}
    <div
      class={`flex items-start gap-3 rounded-lg px-4 py-3 text-sm shadow-lg ${variantClasses[toast.variant]}`}
      transition:fade
    >
      <div class="min-w-0 flex-1">{toast.message}</div>
      <button
        type="button"
        class="rounded-lg p-0.5 transition-colors hover:bg-surface-muted"
        aria-label="Dismiss toast"
        onclick={() => dismissToast(toast.id)}
      >
        <X size={16} />
      </button>
    </div>
  {/each}
</div>
