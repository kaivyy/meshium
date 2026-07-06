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
    <PageHeader {title} {subtitle} actions={action} />

    <Card padding="lg">
      <div class="flex flex-col items-center justify-center py-12 text-center">
        <div class="inline-flex items-center justify-center rounded-2xl bg-surface-muted p-4 text-fg-subtle">
          <icon size={32}></icon>
        </div>
        <h3 class="mt-4 text-base font-semibold text-fg">{title} — Coming Soon</h3>
        <p class="mt-1 max-w-md text-sm text-fg-muted">
          This module is part of the Meshium roadmap. The backend API is ready —
          the UI is under active development.
        </p>

        {#if features.length > 0}
          <div class="mt-6 w-full max-w-md">
            <p class="mb-2 text-xs font-semibold uppercase tracking-wider text-fg-subtle">Planned Features</p>
            <ul class="space-y-1.5">
              {#each features as feature}
                <li class="flex items-center gap-2 text-sm text-fg-muted">
                  <span class="inline-block h-1.5 w-1.5 rounded-full bg-accent"></span>
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
