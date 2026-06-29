<script lang="ts">
  import type { Snippet } from 'svelte';
  import { X } from 'lucide-svelte';

  type ModalSize = 'sm' | 'md' | 'lg';

  interface ModalProps {
    open?: boolean;
    title: string;
    size?: ModalSize;
    onClose?: () => void;
    children?: Snippet;
    footer?: Snippet;
  }

  let { open = false, title, size = 'md', onClose, children, footer }: ModalProps = $props();

  const sizeClasses: Record<ModalSize, string> = {
    sm: 'max-w-md',
    md: 'max-w-lg',
    lg: 'max-w-2xl',
  };

  function closeModal(): void {
    onClose?.();
  }

  $effect(() => {
    if (!open) return;

    const handleKeydown = (event: KeyboardEvent): void => {
      if (event.key === 'Escape') {
        closeModal();
      }
    };

    window.addEventListener('keydown', handleKeydown);
    return () => window.removeEventListener('keydown', handleKeydown);
  });
</script>

{#if open}
  <div class="fixed inset-0 z-50 flex items-center justify-center p-4">
    <button
      type="button"
      class="absolute inset-0 bg-black/50"
      aria-label="Close modal"
      onclick={closeModal}
    ></button>

    <div
      class={`relative z-10 w-full ${sizeClasses[size]} overflow-hidden rounded-2xl bg-white shadow-xl`}
      role="dialog"
      aria-modal="true"
      aria-labelledby="modal-title"
      tabindex="-1"
    >
      <div class="flex items-start justify-between border-b border-slate-200 px-6 py-4">
        <h2 id="modal-title" class="text-lg font-semibold text-slate-900">{title}</h2>
        <button
          type="button"
          class="rounded-lg p-1 text-slate-500 transition-colors hover:bg-slate-100 hover:text-slate-700"
          aria-label="Close modal"
          onclick={closeModal}
        >
          <X size={18} />
        </button>
      </div>

      <div class="px-6 py-4">
        {#if children}
          {@render children()}
        {/if}
      </div>

      {#if footer}
        <div class="border-t border-slate-200 px-6 py-4">
          {@render footer()}
        </div>
      {/if}
    </div>
  </div>
{/if}
