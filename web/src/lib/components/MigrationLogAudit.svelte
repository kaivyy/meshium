<script lang="ts">
  import { Search, ShieldCheck, Download } from 'lucide-svelte';
  import type { AuditEntry, MigrationEvent } from '$lib/api/pipeline';
  import { redactForDisplay } from '$lib/redact';

  interface Props {
    auditTrail?: AuditEntry[];
    events?: MigrationEvent[];
    onExport?: () => void;
  }
  let { auditTrail = [], events = [], onExport }: Props = $props();

  let query = $state('');
  let levelFilter = $state<'all' | 'critical' | 'error' | 'warning' | 'info'>('all');

  type Row = {
    ts: string;
    kind: 'audit' | 'event';
    tag: string;
    level: string;
    text: string;
  };

  const rows = $derived<Row[]>(
    [
      ...auditTrail.map((a) => ({
        ts: a.createdAt,
        kind: 'audit' as const,
        tag: a.eventType,
        level: a.result === 'failed' || a.result === 'rejected' ? 'error' : a.result === 'degraded' ? 'warning' : 'info',
        text: [a.eventData, a.fenceStatus ? `fence=${a.fenceStatus}` : '', a.trafficVerifySummary, a.topologySummary]
          .filter(Boolean)
          .join(' · ') || a.eventType,
      })),
      ...events.map((e) => ({
        ts: e.timestamp,
        kind: 'event' as const,
        tag: e.stage,
        level: e.level,
        text: e.message + (e.details ? ` — ${e.details}` : ''),
      })),
    ]
      .sort((a, b) => b.ts.localeCompare(a.ts))
      .map((r) => ({ ...r, text: redactForDisplay(r.text) }))
  );

  const filtered = $derived(
    rows.filter((r) => {
      if (levelFilter !== 'all' && r.level !== levelFilter) return false;
      if (query && !`${r.tag} ${r.text}`.toLowerCase().includes(query.toLowerCase())) return false;
      return true;
    })
  );

  const levelClass = (lvl: string) =>
    lvl === 'critical' || lvl === 'error'
      ? 'bg-error/15 text-error border-error/30'
      : lvl === 'warning'
        ? 'bg-warning/15 text-warning border-warning/30'
        : 'bg-info/15 text-info border-info/30';
</script>

<section aria-label="Audit trail and event log" class="space-y-3">
  <div class="flex flex-wrap items-center gap-2">
    <div class="relative flex-1 min-w-[180px]">
      <Search size={14} class="absolute left-2.5 top-1/2 -translate-y-1/2 text-fg-subtle" />
      <input
        bind:value={query}
        placeholder="Search messages, correlation id, stage…"
        class="w-full pl-8 pr-3 py-1.5 text-sm bg-surface-muted border border-border rounded-lg"
        aria-label="Search audit and events"
      />
    </div>
    <select bind:value={levelFilter} class="text-sm bg-surface-muted border border-border rounded-lg px-2 py-1.5" aria-label="Filter by level">
      <option value="all">All levels</option>
      <option value="critical">Critical</option>
      <option value="error">Error</option>
      <option value="warning">Warning</option>
      <option value="info">Info</option>
    </select>
    {#if onExport}
      <button onclick={onExport} class="flex items-center gap-1.5 px-3 py-1.5 bg-surface-muted hover:bg-surface rounded-lg text-xs font-medium transition-colors">
        <Download size={13} /> Export
      </button>
    {/if}
  </div>

  <div class="flex items-center gap-1.5 text-xs text-fg-subtle">
    <ShieldCheck size={13} class="text-success" />
    All rendered text is redacted client-side — secrets (passwords, tokens, connection-string creds) are masked before display.
  </div>

  <div class="border border-border rounded-lg divide-y divide-border max-h-80 overflow-y-auto">
    {#if filtered.length === 0}
      <p class="text-center py-6 text-fg-subtle text-sm">No matching entries.</p>
    {:else}
      {#each filtered as row (row.ts + row.kind + row.tag + row.text)}
        <div class="flex items-start gap-2 p-2 text-xs">
          <span class="text-fg-subtle shrink-0 w-16 font-mono">{new Date(row.ts).toLocaleTimeString()}</span>
          <span class="px-1.5 py-0.5 rounded shrink-0 {levelClass(row.level)}">{row.level}</span>
          <span class="text-fg-muted shrink-0 max-w-[8rem] truncate" title={row.tag}>{row.tag}</span>
          <span class="text-fg-muted break-all">{row.text}</span>
        </div>
      {/each}
    {/if}
  </div>
</section>
