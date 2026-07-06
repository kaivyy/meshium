<script lang="ts">
  import type { Snippet } from 'svelte';

  interface Props {
    title: string;
    description?: string;
    icon?: Snippet;
    action?: Snippet;
  }

  let { title, description, icon, action }: Props = $props();
</script>

<div class="flex flex-col items-center text-center rounded-2xl bg-surface px-6 py-10" role="status">
  {#if icon}
    <div class="inline-flex items-center justify-center rounded-full bg-surface-muted p-4 text-fg-subtle" aria-hidden="true">
      {@render icon()}
    </div>
  {/if}

  <h3 class={`text-base font-semibold text-fg ${icon ? 'mt-4' : ''}`}>{title}</h3>

  {#if description}
    <p class="mt-1.5 text-sm text-fg-muted max-w-sm">{description}</p>
  {/if}

  {#if action}
    <div class="mt-5">
      {@render action()}
    </div>
  {/if}
</div>
