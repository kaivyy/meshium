<script lang="ts">
  import { ShieldAlert, ShieldCheck, AlertTriangle, Eye, Undo2 } from 'lucide-svelte';
  import type { RiskReport, StrategySelection } from '$lib/api/pipeline';

  interface Props {
    currentState: string;
    healthScore: number;
    replicationLag: number;
    rollbackAvailable: boolean;
    riskReport?: RiskReport | null;
    strategy?: StrategySelection | null;
    autoCutoverConfigured?: boolean;
    observationRemaining?: number;
    runbookHref?: string;
  }

  let {
    currentState,
    healthScore,
    replicationLag,
    rollbackAvailable,
    riskReport = null,
    strategy = null,
    autoCutoverConfigured = false,
    observationRemaining = 0,
    runbookHref = '#'
  }: Props = $props();

  type Kind = 'awaiting_cutover' | 'observing' | 'needs_manual_intervention' | 'rollback_degraded' | 'none';

  const kind = $derived(((): Kind => {
    const s = currentState.toLowerCase();
    if (s.includes('awaiting_cutover') || s.includes('awaiting cutover')) return 'awaiting_cutover';
    if (s.includes('observation') || s.includes('observing')) return 'observing';
    if (s.includes('needs_manual_intervention')) return 'needs_manual_intervention';
    if (s.includes('rollback_degraded')) return 'rollback_degraded';
    return 'none';
  })());

  const healthyNow = $derived(healthScore >= 80 && replicationLag <= 5);
</script>

{#if kind !== 'none'}
  <div
    class="rounded-xl border p-4 sm:p-5 mb-4 {kind === 'needs_manual_intervention' || kind === 'rollback_degraded'
      ? 'border-error/30 bg-error/10'
      : 'border-info/30 bg-info/10'}"
    role="alert"
    aria-live="polite"
  >
    {#if kind === 'awaiting_cutover'}
      <div class="flex items-start gap-3">
        <ShieldCheck size={22} class="text-info shrink-0 mt-0.5" />
        <div class="min-w-0">
          <h2 class="text-base font-semibold text-fg">Awaiting cutover</h2>
          <p class="text-sm text-fg-muted mt-1">
            {#if autoCutoverConfigured}
              Automatic (fenced) cutover is configured. Confirm the switch below — the server will move traffic and
              verify ownership by read-after-write before committing.
            {:else}
              Manual cutover required. Move traffic to the target (DNS / reverse proxy / load balancer) yourself, then
              confirm here to commit. The server will not perform the switch for you.
            {/if}
          </p>
          <ul class="mt-2 space-y-1 text-xs text-fg-muted">
            <li>• Source frozen / fenced: verify the fence is held before confirming.</li>
            <li>• Replication lag ≤ 5s and target healthy: current {replicationLag}s, health {healthScore}.</li>
            <li>• Confirm traffic ownership (read-after-write) — do not assume success from provider status alone.</li>
          </ul>
          {#if !(healthScore >= 80 && replicationLag <= 5)}
            <p class="mt-2 text-xs text-warning">Not ready to cut over yet: lag/health outside the safe window.</p>
          {/if}
        </div>
      </div>

    {:else if kind === 'observing'}
      <div class="flex items-start gap-3">
        <Eye size={22} class="text-info shrink-0 mt-0.5" />
        <div class="min-w-0">
          <h2 class="text-base font-semibold text-fg">Observing (post-cutover watch window)</h2>
          <p class="text-sm text-fg-muted mt-1">
            Target is now primary. Watching target health and traffic-ownership verification before the migration is
            declared complete. {#if observationRemaining > 0}≈ {observationRemaining}s remaining.{/if}
          </p>
          <ul class="mt-2 space-y-1 text-xs text-fg-muted">
            <li>• Target health: {healthScore} — {healthyNow ? 'healthy' : 'degraded, keep watching'}.</li>
            <li>• Rollback policy: rollback is only safe if the target has not yet received writes you must keep.</li>
            <li>• Do not assume rollback is safe after target writes — verify before any rollback action.</li>
          </ul>
        </div>
      </div>

    {:else if kind === 'needs_manual_intervention'}
      <div class="flex items-start gap-3">
        <ShieldAlert size={22} class="text-error shrink-0 mt-0.5" />
        <div class="min-w-0">
          <h2 class="text-base font-semibold text-error">Needs manual intervention</h2>
          <p class="text-sm text-fg-muted mt-1">
            The pipeline could not continue safely (unsafe or ambiguous replication topology, or a failed cutover step).
            Do not retry blindly — review the target manually first.
          </p>
          <ul class="mt-2 space-y-1 text-xs text-fg-muted">
            <li>• Known: backend persisted the failure evidence in the audit trail (see Logs &amp; Audit).</li>
            <li>• Unknown: the exact root cause — inspect target state and the failed step's evidence before acting.</li>
            <li>• Forbidden assumptions: do not assume the source is still the writer, or that the target is safe to promote, until verified.</li>
            <li>• Recommended next step: open the runbook, then either retry the failed step or perform a manual cutover.</li>
          </ul>
          <div class="mt-3 flex flex-wrap gap-2">
            <a href={runbookHref} class="inline-flex items-center gap-1.5 px-3 py-1.5 bg-surface-muted hover:bg-surface rounded-lg text-xs font-medium transition-colors">
              Open runbook
            </a>
            {#if rollbackAvailable}
              <span class="inline-flex items-center gap-1.5 px-3 py-1.5 bg-error/15 text-error rounded-lg text-xs font-medium">
                <Undo2 size={13} /> Rollback available
              </span>
            {/if}
          </div>
        </div>
      </div>

    {:else if kind === 'rollback_degraded'}
      <div class="flex items-start gap-3">
        <AlertTriangle size={22} class="text-error shrink-0 mt-0.5" />
        <div class="min-w-0">
          <h2 class="text-base font-semibold text-error">Rollback degraded</h2>
          <p class="text-sm text-fg-muted mt-1">
            Rollback finished with one or more step failures. The target may be partially rolled back — verify manually
            before trusting either system.
          </p>
          <ul class="mt-2 space-y-1 text-xs text-fg-muted">
            <li>• Succeeded sub-steps rolled back cleanly; failed sub-steps are listed in the audit trail.</li>
            <li>• Source/target role is now uncertain — confirm who is the current writer before any further action.</li>
            <li>• Forbidden assumptions: do not assume the system is fully reverted, and do not auto-rollback again after target writes.</li>
            <li>• Next steps: open the runbook, inspect the failed sub-steps, and reconcile manually.</li>
          </ul>
          <div class="mt-3">
            <a href={runbookHref} class="inline-flex items-center gap-1.5 px-3 py-1.5 bg-surface-muted hover:bg-surface rounded-lg text-xs font-medium transition-colors">
              Open runbook
            </a>
          </div>
        </div>
      </div>
    {/if}
  </div>
{/if}
