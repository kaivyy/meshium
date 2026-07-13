<script lang="ts">
  import { CheckCircle2, XCircle, AlertTriangle, ShieldCheck } from 'lucide-svelte';
  import type { CompatibilityCheckResult } from '$lib/api/pipeline';

  interface Props {
    results: CompatibilityCheckResult[];
  }
  let { results }: Props = $props();

  // A result blocks the migration when it failed at critical/high severity.
  // Info/warning failures are "review required", never silently passed.
  function isBlocking(r: CompatibilityCheckResult): boolean {
    return !r.passed && (r.severity === 'critical' || r.severity === 'high');
  }

  const blockers = $derived(results.filter(isBlocking));
  const reviews = $derived(results.filter((r) => !r.passed && !isBlocking(r)));
  const passed = $derived(results.filter((r) => r.passed));
  const canProceed = $derived(blockers.length === 0);

  const sevClass = (sev: string) =>
    sev === 'critical' || sev === 'high'
      ? 'bg-error/15 text-error border-error/30'
      : sev === 'warning'
        ? 'bg-warning/15 text-warning border-warning/30'
        : 'bg-info/15 text-info border-info/30';

  const sevLabel = (sev: string) => (sev === 'critical' ? 'Critical' : sev === 'high' ? 'High' : sev === 'warning' ? 'Warning' : 'Info');
</script>

<section aria-label="Compatibility check results" class="space-y-3">
  <!-- Summary: honest gating, never optimistic -->
  <div
    class="flex items-center gap-3 p-3 rounded-lg border {canProceed
      ? 'border-success/30 bg-success/10'
      : 'border-error/30 bg-error/10'}"
    role="status"
  >
    {#if canProceed}
      <ShieldCheck size={20} class="text-success shrink-0" />
      <div class="text-sm">
        <span class="font-medium text-success">No blocking issues.</span>
        <span class="text-fg-muted">
          {reviews.length > 0 ? `${reviews.length} item(s) to review. ` : ''}You may proceed to the next step.
        </span>
      </div>
    {:else}
      <XCircle size={20} class="text-error shrink-0" />
      <div class="text-sm">
        <span class="font-medium text-error">{blockers.length} blocking issue(s).</span>
        <span class="text-fg-muted">Resolve these before the migration can proceed.</span>
      </div>
    {/if}
  </div>

  {#if blockers.length > 0}
    <div class="space-y-2">
      <h4 class="text-xs font-semibold text-error uppercase tracking-wide">Blocking</h4>
      {#each blockers as r (r.checkName)}
        <div class="p-3 rounded-lg border border-error/30 bg-error/10 flex items-start gap-3">
          <XCircle size={18} class="text-error shrink-0 mt-0.5" />
          <div class="min-w-0">
            <div class="flex items-center gap-2 mb-0.5">
              <span class="px-2 py-0.5 rounded text-xs font-medium border {sevClass(r.severity)}">{sevLabel(r.severity)}</span>
              <span class="font-medium text-sm text-fg">{r.checkName}</span>
            </div>
            <p class="text-sm text-fg-muted break-words">{r.message}</p>
          </div>
        </div>
      {/each}
    </div>
  {/if}

  {#if reviews.length > 0}
    <div class="space-y-2">
      <h4 class="text-xs font-semibold text-warning uppercase tracking-wide">Review required</h4>
      {#each reviews as r (r.checkName)}
        <div class="p-3 rounded-lg border border-warning/30 bg-warning/10 flex items-start gap-3">
          <AlertTriangle size={18} class="text-warning shrink-0 mt-0.5" />
          <div class="min-w-0">
            <div class="flex items-center gap-2 mb-0.5">
              <span class="px-2 py-0.5 rounded text-xs font-medium border {sevClass(r.severity)}">{sevLabel(r.severity)}</span>
              <span class="font-medium text-sm text-fg">{r.checkName}</span>
            </div>
            <p class="text-sm text-fg-muted break-words">{r.message}</p>
          </div>
        </div>
      {/each}
    </div>
  {/if}

  {#if passed.length > 0}
    <details class="rounded-lg border border-border bg-surface-muted">
      <summary class="cursor-pointer p-3 text-sm text-fg-muted flex items-center gap-2">
        <CheckCircle2 size={16} class="text-success" />
        {passed.length} passed check{passed.length !== 1 ? 's' : ''}
      </summary>
      <div class="px-3 pb-3 space-y-1.5">
        {#each passed as r (r.checkName)}
          <div class="flex items-start gap-2 text-sm">
            <CheckCircle2 size={16} class="text-success shrink-0 mt-0.5" />
            <div class="min-w-0">
              <span class="font-medium text-fg">{r.checkName}</span>
              <p class="text-xs text-fg-subtle break-words">{r.message}</p>
            </div>
          </div>
        {/each}
      </div>
    </details>
  {/if}
</section>
