<script lang="ts">
  import { CheckCircle2, ShieldCheck, Server, GitBranch, Activity, AlertTriangle } from 'lucide-svelte';
  import type { SyncSession, ReplicationStatus, AuditEntry } from '$lib/api/pipeline';

  interface Props {
    syncSessions?: SyncSession[];
    replicationStatus?: ReplicationStatus[];
    auditTrail?: AuditEntry[];
  }
  let { syncSessions = [], replicationStatus = [], auditTrail = [] }: Props = $props();

  function bytes(n: number): string {
    if (!n) return '0 B';
    const u = ['B', 'KB', 'MB', 'GB', 'TB'];
    const i = Math.min(u.length - 1, Math.floor(Math.log(n) / Math.log(1024)));
    return `${(n / Math.pow(1024, i)).toFixed(i === 0 ? 0 : 1)} ${u[i]}`;
  }

  function fmtSpeed(n: number): string {
    return n > 0 ? `${bytes(n)}/s` : '—';
  }

  function pct(done: number, total: number): number {
    if (!total) return 0;
    return Math.min(100, Math.round((done / total) * 100));
  }

  // Best-effort ownership/fence evidence from the audit trail's latest
  // cutover/traffic events. The backend produces these; we reflect, never invent.
  const lastTrafficVerify = $derived(
    [...auditTrail].reverse().find((a) => a.trafficVerifySummary)?.trafficVerifySummary ?? null
  );
  const lastFence = $derived([...auditTrail].reverse().find((a) => a.fenceStatus) ?? null);
  const lastTopology = $derived(
    [...auditTrail].reverse().find((a) => a.topologySummary)?.topologySummary ?? null
  );

  function statusClass(s: string): string {
    const v = s.toLowerCase();
    if (v === 'completed' || v === 'active' || v === 'synced') return 'text-success';
    if (v === 'failed' || v === 'error' || v === 'stalled') return 'text-error';
    if (v === 'running' || v === 'syncing') return 'text-info';
    return 'text-fg-muted';
  }
</script>

<section aria-label="Migration evidence" class="space-y-4">
  <!-- Transfer / sync evidence -->
  <div class="rounded-lg border border-border bg-surface p-3">
    <h4 class="text-sm font-semibold text-fg flex items-center gap-2 mb-2">
      <Activity size={16} class="text-accent" /> Data transfer
    </h4>
    {#if syncSessions.length === 0}
      <p class="text-xs text-fg-subtle">No transfer sessions recorded yet.</p>
    {:else}
      <div class="space-y-3">
        {#each syncSessions as s (s.id)}
          <div class="text-sm">
            <div class="flex items-center justify-between text-xs text-fg-subtle mb-1">
              <span class="font-medium text-fg">{s.syncType}</span>
              <span class={statusClass(s.status)}>{s.status}</span>
            </div>
            <div class="w-full h-1.5 bg-surface-muted rounded-full overflow-hidden mb-1">
              <div class="h-full bg-accent rounded-full" style="width: {pct(s.bytesTransferred, s.bytesTotal)}%"></div>
            </div>
            <div class="flex flex-wrap gap-x-4 gap-y-0.5 text-xs text-fg-muted">
              <span>{bytes(s.bytesTransferred)} / {bytes(s.bytesTotal)}</span>
              <span>{s.filesTransferred}/{s.filesTotal} files</span>
              <span>Speed: {fmtSpeed(s.speedBytesSec)}</span>
              <span class={s.checksumVerified ? 'text-success' : 'text-warning'}>
                {s.checksumVerified ? 'checksum verified' : 'checksum unverified'}
              </span>
            </div>
            {#if s.error}
              <p class="text-xs text-error mt-1">{s.error}</p>
            {/if}
          </div>
        {/each}
      </div>
    {/if}
  </div>

  <!-- Replication evidence -->
  <div class="rounded-lg border border-border bg-surface p-3">
    <h4 class="text-sm font-semibold text-fg flex items-center gap-2 mb-2">
      <GitBranch size={16} class="text-accent" /> Replication
    </h4>
    {#if replicationStatus.length === 0}
      <p class="text-xs text-fg-subtle">No replication status reported yet.</p>
    {:else}
      <div class="space-y-2">
        {#each replicationStatus as r (r.id)}
          <div class="text-sm flex items-center justify-between gap-2">
            <span class="font-medium text-fg flex items-center gap-1.5">
              <Server size={13} class="text-fg-muted" />{r.databaseType}{r.databaseName ? ` · ${r.databaseName}` : ''}
            </span>
            <span class="text-xs {statusClass(r.status)}">{r.status}</span>
          </div>
          <div class="flex flex-wrap gap-x-4 gap-y-0.5 text-xs text-fg-muted">
            <span>{r.sourceHost || '?'} → {r.targetHost || '?'}</span>
            <span>mode: {r.replicationMode}</span>
            <span>lag: {r.replicationLag}s</span>
          </div>
          {#if r.lastError}
            <p class="text-xs text-error">{r.lastError}</p>
          {/if}
        {/each}
      </div>
    {/if}
  </div>

  <!-- Fence / ownership / topology evidence (from audit trail) -->
  <div class="rounded-lg border border-border bg-surface p-3">
    <h4 class="text-sm font-semibold text-fg flex items-center gap-2 mb-2">
      <ShieldCheck size={16} class="text-accent" /> Cutover safety evidence
    </h4>
    <div class="space-y-2 text-sm">
      <div class="flex items-start gap-2">
        <CheckCircle2 size={15} class="text-fg-muted mt-0.5 shrink-0" />
        <div class="min-w-0">
          <span class="text-xs font-medium text-fg-subtle uppercase">Traffic ownership proof</span>
          <p class="text-xs text-fg-muted break-words">{lastTrafficVerify ?? 'Not recorded yet — appears after a cutover switch.'}</p>
        </div>
      </div>
      <div class="flex items-start gap-2">
        <CheckCircle2 size={15} class="text-fg-muted mt-0.5 shrink-0" />
        <div class="min-w-0">
          <span class="text-xs font-medium text-fg-subtle uppercase">Fence status</span>
          <p class="text-xs text-fg-muted break-words">
            {lastFence ? `${lastFence.fenceStatus}${lastFence.fenceGeneration ? ` (gen ${lastFence.fenceGeneration})` : ''}` : 'Not recorded yet.'}
          </p>
        </div>
      </div>
      <div class="flex items-start gap-2">
        <CheckCircle2 size={15} class="text-fg-muted mt-0.5 shrink-0" />
        <div class="min-w-0">
          <span class="text-xs font-medium text-fg-subtle uppercase">Topology summary</span>
          <p class="text-xs text-fg-muted break-words">{lastTopology ?? 'Not recorded yet.'}</p>
        </div>
      </div>
    </div>
  </div>
</section>
