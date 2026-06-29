<script lang="ts">
  import type { Snippet } from 'svelte';
  import { MoreVertical } from 'lucide-svelte';

  export interface DropdownMenuItem {
    label: string;
    href?: string;
    onclick?: () => void;
    icon?: Snippet;
    danger?: boolean;
    divider?: boolean;
  }

  interface Props {
    items: DropdownMenuItem[];
    label?: string;
    align?: 'left' | 'right';
    trigger?: Snippet;
  }

  let { items, label = 'Open menu', align = 'right', trigger }: Props = $props();

  let open = $state(false);
  let menuEl: HTMLDivElement | null = null;

  function toggle() {
    open = !open;
  }

  function handleClickOutside(event: MouseEvent) {
    if (menuEl && !menuEl.contains(event.target as Node)) {
      open = false;
    }
  }

  function handleKeydown(event: KeyboardEvent) {
    if (event.key === 'Escape') {
      open = false;
    }
  }

  $effect(() => {
    if (open) {
      document.addEventListener('click', handleClickOutside);
      document.addEventListener('keydown', handleKeydown);
    }

    return () => {
      document.removeEventListener('click', handleClickOutside);
      document.removeEventListener('keydown', handleKeydown);
    };
  });
</script>

<div class="relative" bind:this={menuEl}>
  <button
    type="button"
    onclick={toggle}
    aria-label={label}
    aria-expanded={open}
    class="inline-flex items-center justify-center rounded-lg p-1.5 text-slate-400 transition-colors hover:bg-slate-100 hover:text-slate-700"
  >
    {#if trigger}
      {@render trigger()}
    {:else}
      <MoreVertical size={18} />
    {/if}
  </button>

  {#if open}
    <div
      class={`absolute z-50 mt-1 min-w-48 rounded-lg border border-slate-200 bg-white py-1 shadow-lg ${align === 'right' ? 'right-0' : 'left-0'}`}
      role="menu"
    >
      {#each items as item, i}
        {#if item.divider}
          <div class="my-1 border-t border-slate-100"></div>
        {:else if item.href}
          <a
            href={item.href}
            role="menuitem"
            class={`flex items-center gap-2.5 px-4 py-2 text-sm transition-colors hover:bg-slate-50 ${item.danger ? 'text-red-600 hover:bg-red-50' : 'text-slate-700'}`}
            onclick={() => { open = false; }}
          >
            {#if item.icon}
              <span class="shrink-0 text-slate-400">
                {@render item.icon()}
              </span>
            {/if}
            {item.label}
          </a>
        {:else}
          <button
            type="button"
            role="menuitem"
            class={`flex w-full items-center gap-2.5 px-4 py-2 text-left text-sm transition-colors hover:bg-slate-50 ${item.danger ? 'text-red-600 hover:bg-red-50' : 'text-slate-700'}`}
            onclick={() => { open = false; item.onclick?.(); }}
          >
            {#if item.icon}
              <span class="shrink-0 text-slate-400">
                {@render item.icon()}
              </span>
            {/if}
            {item.label}
          </button>
        {/if}
      {/each}
    </div>
  {/if}
</div>
