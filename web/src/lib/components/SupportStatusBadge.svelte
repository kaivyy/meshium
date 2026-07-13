<script lang="ts">
  import { CheckCircle2, Wrench, AlertTriangle, XCircle, Clock, HelpCircle } from 'lucide-svelte';
  import type { SupportLevel } from '$lib/support-status';
  import { supportClasses } from '$lib/support-status';

  interface Props {
    level: SupportLevel;
    label?: string;
    size?: 'sm' | 'md';
    // Optional click/slot anchor; default slot renders extra text (e.g. reason).
    children?: import('svelte').Snippet;
  }

  let { level, label, size = 'md', children }: Props = $props();

  const icon = $derived(
    level === 'automatic' ? CheckCircle2
    : level === 'manual' ? Wrench
    : level === 'degraded' ? AlertTriangle
    : level === 'blocked' ? XCircle
    : level === 'deferred' ? Clock
    : HelpCircle
  );

  const text = $derived(
    label ??
    (level === 'automatic' ? 'Automatic'
    : level === 'manual' ? 'Manual only'
    : level === 'degraded' ? 'Degraded'
    : level === 'blocked' ? 'Blocked'
    : level === 'deferred' ? 'Deferred'
    : level === 'staging-only' ? 'Staging only'
    : 'Unknown')
  );

  const sizeClass = $derived(size === 'sm' ? 'px-2 py-0.5 text-[11px]' : 'px-2.5 py-1 text-xs');
</script>

<span
  class={`inline-flex items-center gap-1.5 rounded-full font-medium border ${sizeClass} ${supportClasses(level)}`}
  role="status"
  aria-label={`Support status: ${text}`}
>
  {#if icon}
    {@const Icon = icon}
    <Icon size={size === 'sm' ? 12 : 14} aria-hidden="true" />
  {/if}
  <span>{text}</span>
  {#if children}{@render children()}{/if}
</span>
