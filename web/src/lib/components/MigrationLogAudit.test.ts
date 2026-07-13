import { describe, it, expect } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import MigrationLogAudit from './MigrationLogAudit.svelte';
import type { AuditEntry, MigrationEvent } from '$lib/api/pipeline';

const audit: AuditEntry[] = [
  { id: 1, migrationId: 1, eventType: 'traffic_switch', createdAt: '2026-07-13T10:00:00Z', eventData: 'REDISCLI_AUTH=leakme redis-cli -psecret', fenceStatus: 'held', trafficVerifySummary: 'read-after-write verified' },
];
const events: MigrationEvent[] = [
  { id: 1, migrationId: 1, sequence: 1, timestamp: '2026-07-13T10:01:00Z', level: 'critical', stage: 'cutover', type: 'x', source: 'pipeline', message: 'failed to verify ownership' },
];

describe('MigrationLogAudit', () => {
  it('redacts secrets in rendered text (never shows the raw secret)', () => {
    render(MigrationLogAudit, { props: { auditTrail: audit, events } });
    expect(screen.queryByText(/leakme/i)).toBeNull();
    expect(screen.getByText(/REDISCLI_AUTH=••••••/i)).toBeTruthy();
    expect(screen.getByText(/read-after-write verified/i)).toBeTruthy();
  });

  it('filters by search query', async () => {
    render(MigrationLogAudit, { props: { auditTrail: audit, events } });
    const input = screen.getByLabelText(/Search audit and events/i);
    await fireEvent.input(input, { target: { value: 'ownership' } });
    expect(screen.getByText(/failed to verify ownership/i)).toBeTruthy();
    expect(screen.queryByText(/REDISCLI_AUTH/i)).toBeNull();
  });

  it('filters by level', async () => {
    render(MigrationLogAudit, { props: { auditTrail: audit, events } });
    const select = screen.getByLabelText(/Filter by level/i);
    await fireEvent.change(select, { target: { value: 'info' } });
    // audit row is 'info' after redaction mapping; event is 'critical' → hidden
    expect(screen.getByText(/REDISCLI_AUTH=••••••/i)).toBeTruthy();
    expect(screen.queryByText(/failed to verify ownership/i)).toBeNull();
  });
});
