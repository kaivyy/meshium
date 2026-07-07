<script lang="ts">
  import { Sun, Moon, Monitor } from 'lucide-svelte';
  import { themeStore, cycleTheme } from '$lib/stores/theme';

  const labels = { light: 'Light theme', dark: 'Dark theme', system: 'System theme' };
</script>

<button
  type="button"
  onclick={() => cycleTheme($themeStore)}
  class="flex h-9 w-9 items-center justify-center rounded-lg text-fg-subtle hover:text-fg hover:bg-surface-muted transition focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent focus-visible:ring-offset-2"
  aria-label={`Theme: ${labels[$themeStore]}. Click to change.`}
  title={labels[$themeStore]}
>
  {#if $themeStore === 'light'}
    <Sun size={16} aria-hidden="true" />
  {:else if $themeStore === 'dark'}
    <Moon size={16} aria-hidden="true" />
  {:else}
    <Monitor size={16} aria-hidden="true" />
  {/if}
</button>
