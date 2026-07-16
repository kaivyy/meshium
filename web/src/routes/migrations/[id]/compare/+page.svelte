<script lang="ts">
  import { onMount } from 'svelte';
  import { page } from '$app/state';
  import { ArrowLeft, ArrowRightLeft, RefreshCw, ShieldAlert, ShieldCheck, CircleSlash, Eye } from 'lucide-svelte';
  import {
    migrationApi,
    type ParityResult,
    type ParityItem,
    type ParityStatus,
    type ApplyLevel,
    type ParityAction,
    type SelectionDecision,
    type ParitySummary,
    type BulkPolicy,
  } from '$lib/api/migrations';
  import { Badge, EmptyState, Spinner } from '$lib/components/ui';
  import { toast } from '$lib/stores/toast';

  const migrationId = $derived(page.params.id);

  let result = $state<ParityResult | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let selected: ParityItem | null = $state(null);
  let selections = $state<Record<string, ParityAction>>({});
  let summary = $state<ParitySummary | null>(null);
  let saving = $state(false);

  // Per-item action: explicit selection wins; otherwise the engine's suggested.
  function actionFor(it: ParityItem | null): ParityAction {
    if (!it) return 'skip';
    return selections[it.itemKey] ?? it.suggested;
  }

  const actionOptions: { value: ParityAction; label: string }[] = [
    { value: 'apply_from_source', label: 'Apply from source' },
    { value: 'keep_target', label: 'Keep target' },
    { value: 'skip', label: 'Skip' },
    { value: 'review_manual', label: 'Review manual' },
  ];

  async function persistSelection(it: ParityItem | null, action: ParityAction) {
    if (!it) return;
    saving = true;
    try {
      await migrationApi.putSelection(Number(migrationId), it.itemKey, it.category, action);
      selections = { ...selections, [it.itemKey]: action };
      toast.success(`Saved: ${action}`);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : 'Failed to save selection');
    } finally {
      saving = false;
    }
  }

  // 6B7: destructive confirmation. Choosing keep_target/skip on an item that is
  // NOT already identical to the target means discarding a real source state or
  // overwriting target state — require an explicit operator confirm.
  let pendingConfirm = $state<{ it: ParityItem; action: ParityAction } | null>(null);

  function requestAction(it: ParityItem | null, action: ParityAction) {
    if (!it) return;
    const destructive = (action === 'keep_target' || action === 'skip') &&
      it.status !== 'same' && it.status !== 'accepted_target';
    if (destructive) {
      pendingConfirm = { it, action };
      return;
    }
    persistSelection(it, action);
  }

  function confirmDestructive() {
    if (!pendingConfirm) return;
    const { it, action } = pendingConfirm;
    pendingConfirm = null;
    persistSelection(it, action);
  }

  async function loadSummary() {
    try {
      summary = await migrationApi.paritySummary(Number(migrationId));
    } catch {
      summary = null;
    }
  }

  // Group items by category for the matrix (FE re-groups; backend flattens).
  let grouped = $derived.by(() => {
    const map = new Map<string, ParityItem[]>();
    for (const it of result?.items ?? []) {
      const arr = map.get(it.category) ?? [];
      arr.push(it);
      map.set(it.category, arr);
    }
    return Array.from(map.entries());
  });

  const statusLabel: Record<ParityStatus, string> = {
    same: 'Same',
    missing_on_target: 'Missing on target',
    different: 'Different',
    stale: 'Stale',
    unsupported: 'Unsupported',
    selected_for_apply: 'Selected',
    accepted_target: 'Keep target',
    skipped_by_user: 'Skipped',
    manual_required: 'Manual required',
    applied: 'Applied',
    verified: 'Verified',
    unresolved: 'Unresolved',
  };

  const levelLabel: Record<number, string> = {
    1: 'Safe',
    2: 'Warning',
    3: 'Guarded',
    4: 'Manual',
  };

  function statusTone(s: ParityStatus): 'success' | 'warning' | 'error' | 'info' | 'neutral' {
    if (s === 'same' || s === 'verified' || s === 'accepted_target') return 'success';
    if (s === 'missing_on_target' || s === 'different') return 'warning';
    if (s === 'unresolved' || s === 'manual_required') return 'error';
    if (s === 'skipped_by_user' || s === 'stale' || s === 'unsupported') return 'neutral';
    return 'info';
  }

  function levelTone(l: ApplyLevel): 'success' | 'warning' | 'error' | 'info' {
    if (l <= 1) return 'success';
    if (l === 2) return 'warning';
    if (l === 3) return 'info';
    return 'error';
  }

  function depTone(kind: string): 'error' | 'warning' | 'info' {
    if (kind === 'hard') return 'error';
    if (kind === 'recommended') return 'warning';
    return 'info';
  }

  let bulking = $state(false);

  const bulkPolicies: { policy: BulkPolicy; label: string; hint: string }[] = [
    { policy: 'apply_safe', label: 'Auto-apply safe', hint: 'Apply level ≤ Warn with satisfied hard deps' },
    { policy: 'accept_risky_unchanged', label: 'Accept unchanged risky', hint: 'Keep target where guarded items already match' },
  ];

  async function runBulk(policy: BulkPolicy) {
    bulking = true;
    try {
      const res = await migrationApi.bulkApply(Number(migrationId), policy);
      toast.success(`Bulk ${policy}: ${res.applied} applied, ${res.accepted} accepted, ${res.manual} left manual`);
      await load();
    } catch (e) {
      toast.error(e instanceof Error ? e.message : 'Bulk apply failed');
    } finally {
      bulking = false;
    }
  }

  async function load() {
    loading = true;
    error = null;
    try {
      const [res, sel] = await Promise.all([
        migrationApi.parity(Number(migrationId)),
        migrationApi.getSelections(Number(migrationId)).catch(() => []),
      ]);
      result = res;
      const map: Record<string, ParityAction> = {};
      for (const d of sel as SelectionDecision[]) map[d.itemKey] = d.action;
      selections = map;
      await loadSummary();
    } catch (e) {
      error = e instanceof Error ? e.message : 'Failed to load parity';
      toast.error(error);
    } finally {
      loading = false;
    }
  }

  onMount(load);
</script>

<div class="max-w-5xl mx-auto p-4 sm:p-6">
  <div class="flex items-center justify-between mb-4">
    <div class="flex items-center gap-3">
      <a href="/migrations/{migrationId}" class="text-fg-subtle hover:text-fg">
        <ArrowLeft size={18} />
      </a>
      <div>
        <h1 class="text-xl font-bold flex items-center gap-2">
          <ArrowRightLeft class="inline" size={18} /> Compare &amp; Select
        </h1>
        <p class="text-sm text-fg-subtle">
          Per-item source vs live target parity for migration #{migrationId}.
        </p>
      </div>
    </div>
    <button
      on:click={load}
      disabled={loading}
      class="flex items-center gap-2 px-3 py-2 bg-surface-muted hover:bg-surface rounded-lg text-sm transition-colors disabled:opacity-50"
    >
      <RefreshCw size={16} class={loading ? 'animate-spin' : ''} /> Refresh
    </button>
  </div>

  {#if !loading && result && result.items.length}
    <div class="mb-4 flex flex-wrap items-center gap-2">
      <span class="text-xs text-fg-subtle">Bulk automation:</span>
      {#each bulkPolicies as b}
        <button
          title={b.hint}
          on:click={() => runBulk(b.policy)}
          disabled={bulking}
          class="px-3 py-1.5 rounded-lg border border-border bg-surface-muted hover:bg-surface text-sm transition-colors disabled:opacity-50"
        >
          {b.label}
        </button>
      {/each}
    </div>
  {/if}

  {#if loading}
    <div class="flex justify-center py-16"><Spinner label="Loading parity" /></div>
  {:else if error}
    <div class="rounded-xl border border-error/40 bg-error/10 p-6 text-center text-danger">{error}</div>
  {:else if !result || !result.items.length}
    <EmptyState
      title="Nothing to compare"
      description="No source collect steps were found for this migration. Re-plan with the categories you want to compare."
    />
  {:else}
    <div class="mb-4 flex flex-wrap gap-3 text-sm">
      <span class="px-2 py-1 rounded bg-surface-muted">
        {result.items.length} items
      </span>
      <span class="px-2 py-1 rounded bg-surface-muted">
        freshness: <strong>{result.freshness}</strong> (live target collect)
      </span>
      <span class="px-2 py-1 rounded bg-surface-muted">
        computed {new Date(result.computedAt).toLocaleString()}
      </span>
    </div>

    {#each grouped as [category, items] (category)}
      <div class="mb-4 overflow-hidden rounded-xl border border-border">
        <div class="px-4 py-3 bg-surface-muted border-b border-border font-semibold capitalize flex items-center justify-between">
          <span>{category}</span>
          <span class="text-xs font-normal text-fg-subtle">{items.length} items</span>
        </div>
        <div class="divide-y divide-border">
          {#each items as it (it.itemKey)}
            <button
              class="w-full text-left px-4 py-3 hover:bg-surface-muted/40 transition-colors flex items-start gap-3"
              on:click={() => (selected = it)}
            >
              <div class="flex-1 min-w-0">
                <div class="font-mono text-sm truncate">{it.itemKey}</div>
                {#if it.warnings?.length}
                  <div class="text-xs text-warning mt-1 flex items-center gap-1">
                    <ShieldAlert size={12} /> {it.warnings[0]}
                  </div>
                {/if}
              </div>
              <div class="flex flex-col items-end gap-1 shrink-0">
                <Badge variant={statusTone(it.status)}>{statusLabel[it.status] ?? it.status}</Badge>
                <Badge variant={levelTone(it.applyLevel)}>{levelLabel[it.applyLevel]}</Badge>
                {#if selections[it.itemKey]}
                  <Badge variant="info">→ {selections[it.itemKey]}</Badge>
                {/if}
              </div>
            </button>
          {/each}
        </div>
      </div>
    {/each}

    {#if summary}
      <div class="mb-4 rounded-xl border border-border-strong bg-surface p-4 sm:p-5">
        <h2 class="text-sm font-semibold uppercase tracking-wide text-fg-subtle mb-3">Verification summary (post-apply)</h2>
        <div class="grid grid-cols-1 sm:grid-cols-3 gap-3">
          {#each [['Infra parity', summary.infraScore], ['Runtime readiness', summary.runtimeScore], ['App health', summary.appHealthScore]] as [label, score]}
            <div class="rounded-lg bg-surface-muted p-3">
              <div class="text-xs text-fg-subtle mb-1">{label}</div>
              {#if typeof score === 'number'}
                <div class="text-2xl font-bold {score >= 100 ? 'text-success' : score >= 50 ? 'text-warning' : 'text-error'}">{score.toFixed(0)}%</div>
              {:else}
                <div class="text-2xl font-bold text-fg-subtle">{score}%</div>
              {/if}
            </div>
          {/each}
        </div>
        <div class="flex flex-wrap gap-3 mt-3 text-sm">
          <span class="px-2 py-1 rounded bg-surface-muted">passed: <strong>{summary.passed}</strong></span>
          <span class="px-2 py-1 rounded bg-surface-muted">failed: <strong>{summary.failed}</strong></span>
          <span class="px-2 py-1 rounded bg-surface-muted">manual gaps: <strong>{summary.manualGaps}</strong></span>
          <span class="px-2 py-1 rounded bg-surface-muted">unresolved drift: <strong>{summary.unresolvedDrift}</strong></span>
        </div>
        {#if summary.manualGaps > 0 || summary.unresolvedDrift > 0}
          <p class="text-xs text-warning mt-3 flex items-center gap-1">
            <ShieldAlert size={12} /> Not a bare "completed" — manual follow-up or unresolved drift remains.
          </p>
        {/if}
      </div>
    {/if}
  {/if}
</div>

{#if selected}
  <div
    class="fixed inset-0 z-40 bg-black/40 flex items-center justify-center p-4"
    role="dialog"
    aria-modal="true"
    on:click={() => (selected = null)}
  >
    <div
      class="w-full max-w-lg rounded-xl border border-border-strong bg-surface p-6 shadow-2xl"
      on:click|stopPropagation
    >
      <div class="flex items-start justify-between mb-4">
        <div>
          <div class="text-xs text-fg-subtle uppercase tracking-wide">Parity item</div>
          <div class="font-mono text-sm break-all">{selected.itemKey}</div>
        </div>
        <button class="text-fg-subtle hover:text-fg" on:click={() => (selected = null)} aria-label="Close">✕</button>
      </div>

      <div class="grid grid-cols-2 gap-3 mb-4">
        <div class="rounded-lg bg-surface-muted p-3">
          <div class="text-xs text-fg-subtle mb-1">Status</div>
          <Badge variant={statusTone(selected.status)}>{statusLabel[selected.status] ?? selected.status}</Badge>
        </div>
        <div class="rounded-lg bg-surface-muted p-3">
          <div class="text-xs text-fg-subtle mb-1">Suggested action</div>
          <Badge variant="info">{selected.suggested}</Badge>
        </div>
        <div class="rounded-lg bg-surface-muted p-3">
          <div class="text-xs text-fg-subtle mb-1">Apply level</div>
          <Badge variant={levelTone(selected.applyLevel)}>{levelLabel[selected.applyLevel]}</Badge>
        </div>
        <div class="rounded-lg bg-surface-muted p-3">
          <div class="text-xs text-fg-subtle mb-1">Freshness</div>
          <span class="text-sm">{selected.freshness ?? 'fresh'}</span>
        </div>
      </div>

      <div class="mb-4">
        <div class="text-xs text-fg-subtle mb-2">Decision (persists; drives apply)</div>
        <div class="flex flex-wrap gap-2">
          {#each actionOptions as opt}
            <button
              class="px-3 py-1.5 rounded-lg border text-sm transition-colors disabled:opacity-50 {actionFor(selected) === opt.value ? 'bg-accent text-accent-fg border-accent' : 'bg-surface-muted border-border hover:bg-surface'}"
              disabled={saving}
              on:click={() => requestAction(selected, opt.value)}
            >
              {opt.label}
            </button>
          {/each}
        </div>
        {#if actionFor(selected) !== selected.suggested}
          <p class="text-xs text-fg-subtle mt-2">Overrides suggested <code>{selected.suggested}</code>.</p>
        {/if}
      </div>

      <div class="mb-4">
        <div class="text-xs text-fg-subtle mb-1">Source value</div>
        <div class="font-mono text-sm break-all bg-surface-muted rounded p-2">{selected.sourceValue || '—'}</div>
      </div>
      <div class="mb-4">
        <div class="text-xs text-fg-subtle mb-1">Target value</div>
        <div class="font-mono text-sm break-all bg-surface-muted rounded p-2">{selected.targetValue || '—'}</div>
      </div>

      {#if selected.warnings?.length}
        <div class="mb-4 rounded-lg border border-warning/40 bg-warning/10 p-3 text-sm">
          <div class="flex items-center gap-1 text-warning mb-1"><ShieldAlert size={14} /> Warnings</div>
          <ul class="list-disc pl-5 space-y-1">
            {#each selected.warnings as w}
              <li>{w}</li>
            {/each}
          </ul>
        </div>
      {/if}

      {#if selected.deps?.length}
        <div>
          <div class="text-xs text-fg-subtle mb-1">Dependencies</div>
          <ul class="space-y-2">
            {#each selected.deps as d}
              <li class="flex items-start gap-2 text-sm">
                <span class="mt-0.5">
                  {#if d.kind === 'hard' && !d.satisfied}
                    <CircleSlash size={14} class="text-danger" />
                  {:else if d.satisfied}
                    <ShieldCheck size={14} class="text-success" />
                  {:else}
                    <Eye size={14} class="text-warning" />
                  {/if}
                </span>
                <span class="flex-1">
                  <Badge variant={depTone(d.kind)}>{d.kind}</Badge>
                  <span class="ml-2 font-mono text-xs">{d.itemKey}</span>
                  <div class="text-fg-subtle text-xs">{d.note}</div>
                </span>
              </li>
            {/each}
          </ul>
        </div>
      {/if}
    </div>
  </div>
{/if}

{#if pendingConfirm}
  <div
    class="fixed inset-0 z-50 bg-black/50 flex items-center justify-center p-4"
    role="alertdialog"
    aria-modal="true"
    on:click={() => (pendingConfirm = null)}
  >
    <div
      class="w-full max-w-md rounded-xl border border-error/40 bg-surface p-6 shadow-2xl"
      on:click|stopPropagation
    >
      <div class="flex items-center gap-2 mb-3 text-danger">
        <ShieldAlert size={18} />
        <h2 class="text-base font-semibold">Confirm {pendingConfirm.action}</h2>
      </div>
      <p class="text-sm text-fg-subtle mb-2">
        The item <code class="font-mono text-fg">{pendingConfirm.it.itemKey}</code> is
        <strong class="text-warning">{pendingConfirm.it.status}</strong> on the target.
      </p>
      <p class="text-sm text-fg-subtle mb-4">
        {#if pendingConfirm.action === 'keep_target'}
          Keeping the target discards the differing source state — it will not be applied.
        {:else}
          Skipping removes this item from the migration; the source state will not be transferred.
        {/if}
      </p>
      <div class="flex justify-end gap-2">
        <button
          class="px-3 py-1.5 rounded-lg bg-surface-muted hover:bg-surface text-sm"
          on:click={() => (pendingConfirm = null)}
        >
          Cancel
        </button>
        <button
          class="px-3 py-1.5 rounded-lg bg-error text-error-fg hover:opacity-90 text-sm font-medium disabled:opacity-50"
          disabled={saving}
          on:click={confirmDestructive}
        >
          Confirm {pendingConfirm.action}
        </button>
      </div>
    </div>
  </div>
{/if}
