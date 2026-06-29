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

<div class="flex flex-col items-center text-center rounded-2xl bg-white px-6 py-8">
  {#if icon}
    <div class="inline-flex items-center justify-center rounded-full bg-slate-100 p-3 text-slate-500">
      {@render icon()}
    </div>
  {/if}

  <h3 class={`text-sm font-medium text-slate-900 ${icon ? 'mt-4' : ''}`}>{title}</h3>

  {#if description}
    <p class="mt-1 text-sm text-slate-500">{description}</p>
  {/if}

  {#if action}
    <div class="mt-4">
      {@render action()}
    </div>
  {/if}
</div>
