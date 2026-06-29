<script lang="ts">
  import { fade } from 'svelte/transition';
  import { X } from 'lucide-svelte';
  import { dismissToast, toasts } from '$lib/stores/toast';
  import type { Toast as ToastItem } from '$lib/stores/toast';

  const variantClasses: Record<ToastItem['variant'], string> = {
    success: 'bg-green-50 border border-green-200 text-green-800',
    error: 'bg-red-50 border border-red-200 text-red-800',
    warning: 'bg-yellow-50 border border-yellow-200 text-yellow-800',
    info: 'bg-blue-50 border border-blue-200 text-blue-800',
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
        class="rounded-lg p-0.5 transition-colors hover:bg-black/5"
        aria-label="Dismiss toast"
        onclick={() => dismissToast(toast.id)}
      >
        <X size={16} />
      </button>
    </div>
  {/each}
</div>
