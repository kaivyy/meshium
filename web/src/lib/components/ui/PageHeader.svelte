<script lang="ts">
  import type { Snippet } from 'svelte';
  import { ArrowLeft } from 'lucide-svelte';

  interface Props {
    title: string;
    subtitle?: string;
    backHref?: string;
    actions?: Snippet;
  }

  let { title, subtitle, backHref, actions }: Props = $props();
</script>

<div class="mb-6">
  {#if backHref}
    <a
      href={backHref}
      class="mb-3 inline-flex items-center gap-1.5 text-sm text-slate-500 transition-colors hover:text-slate-900"
    >
      <ArrowLeft size={16} />
      Back
    </a>
  {/if}

  <div class="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
    <div>
      <h1 class="text-xl font-bold text-slate-900">{title}</h1>
      {#if subtitle}
        <p class="mt-1 text-sm text-slate-500">{subtitle}</p>
      {/if}
    </div>

    {#if actions}
      <div class="flex items-center gap-2">
        {@render actions()}
      </div>
    {/if}
  </div>
</div>
