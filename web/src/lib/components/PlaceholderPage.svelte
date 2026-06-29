<script lang="ts">
  import type { Snippet } from 'svelte';
  import type { Component } from 'svelte';
  import { PageHeader, Card } from '$lib/components/ui';

  interface Props {
    title: string;
    subtitle: string;
    icon: Component;
    features?: string[];
    action?: Snippet;
  }

  let { title, subtitle, icon, features = [], action }: Props = $props();
</script>

<div class="p-4 sm:p-6">
  <div class="max-w-7xl mx-auto">
    <PageHeader {title} {subtitle}>
      {#if action}
        {#snippet actions()}
          {@render action()}
        {/snippet}
      {/if}
    </PageHeader>

    <Card padding="lg">
      <div class="flex flex-col items-center justify-center py-12 text-center">
        <div class="inline-flex items-center justify-center rounded-2xl bg-slate-100 p-4 text-slate-400">
          <icon size={32}></icon>
        </div>
        <h3 class="mt-4 text-base font-semibold text-slate-900">{title} — Coming Soon</h3>
        <p class="mt-1 max-w-md text-sm text-slate-500">
          This module is part of the Meshium roadmap. The backend API is ready —
          the UI is under active development.
        </p>

        {#if features.length > 0}
          <div class="mt-6 w-full max-w-md">
            <p class="mb-2 text-xs font-semibold uppercase tracking-wider text-slate-400">Planned Features</p>
            <ul class="space-y-1.5">
              {#each features as feature}
                <li class="flex items-center gap-2 text-sm text-slate-600">
                  <span class="inline-block h-1.5 w-1.5 rounded-full bg-blue-500"></span>
                  {feature}
                </li>
              {/each}
            </ul>
          </div>
        {/if}
      </div>
    </Card>
  </div>
</div>
