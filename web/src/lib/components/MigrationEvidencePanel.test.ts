import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/svelte';
import MigrationEvidencePanel from './MigrationEvidencePanel.svelte';
import type { SyncSession, ReplicationStatus, AuditEntry } from '$lib/api/pipeline';

describe('MigrationEvidencePanel', () => {
  it('shows empty states when no evidence exists (honest, not fake)', () => {
    render(MigrationEvidencePanel, { syncSessions: [], replicationStatus: [], auditTrail: [] });
    expect(screen.getByText(/No transfer sessions recorded yet/i)).toBeTruthy();
    expect(screen.getByText(/No replication status reported yet/i)).toBeTruthy();
    // Fence/ownership not invented: explicitly "Not recorded yet".
    expect(screen.getAllByText(/Not recorded yet/i).length).toBeGreaterThan(0);
  });

  it('renders transfer progress, speed, and checksum status', () => {
    const syncs: SyncSession[] = [
      {
        id: 1, migrationId: 1, syncType: 'rsync', bytesTransferred: 512, bytesTotal: 1024,
        filesTransferred: 1, filesTotal: 2, speedBytesSec: 1048576, checksumVerified: true, status: 'running'
      },
    ];
    render(MigrationEvidencePanel, { syncSessions: syncs, replicationStatus: [], auditTrail: [] });
    expect(screen.getByText(/512 B \/ 1\.0 KB/i)).toBeTruthy();
    expect(screen.getByText(/1\.0 MB\/s/i)).toBeTruthy();
    expect(screen.getByText(/checksum verified/i)).toBeTruthy();
  });

  it('renders replication hosts/mode/lag and fence evidence from the audit trail', () => {
    const repl: ReplicationStatus[] = [
      { id: 1, migrationId: 1, databaseType: 'postgres', sourceHost: 's1', targetHost: 't1', replicationMode: 'logical', replicationLag: 2, status: 'active' },
    ];
    const audit: AuditEntry[] = [
      { id: 1, migrationId: 1, eventType: 'traffic_switch', createdAt: 'x', fenceStatus: 'held', fenceGeneration: 3, trafficVerifySummary: 'read-after-write verified', topologySummary: 'docker pair' },
    ];
    render(MigrationEvidencePanel, { syncSessions: [], replicationStatus: repl, auditTrail: audit });
    expect(screen.getByText(/s1 → t1/i)).toBeTruthy();
    expect(screen.getByText(/mode: logical/i)).toBeTruthy();
    expect(screen.getByText(/read-after-write verified/i)).toBeTruthy();
    expect(screen.getByText(/held \(gen 3\)/i)).toBeTruthy();
    expect(screen.getByText(/docker pair/i)).toBeTruthy();
  });
});
