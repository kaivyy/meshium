import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/svelte';
import MigrationLogAudit from './MigrationLogAudit.svelte';
import type { AuditEntry } from '$lib/api/pipeline';

function big(n: number): { auditTrail: AuditEntry[]; events: [] } {
  const auditTrail: AuditEntry[] = Array.from({ length: n }, (_, i) => ({
    id: i, migrationId: 1, eventType: 'step', createdAt: `2026-07-13T10:00:${String(i).padStart(2, '0')}Z`,
  }));
  return { auditTrail, events: [] };
}

describe('MigrationLogAudit performance + a11y (4G 3H)', () => {
  it('caps rendered rows and shows a refine hint instead of dumping everything', () => {
    const { auditTrail } = big(500);
    render(MigrationLogAudit, { props: { auditTrail, events: [] } });
    // Only the capped window is in the DOM; the hint tells the operator to search.
    expect(screen.getByText(/Showing first 200 of 500 entries/i)).toBeTruthy();
    const rows = document.querySelectorAll('[aria-label="Audit trail and event log"] .divide-y > div');
    expect(rows.length).toBe(200);
  });

  it('exposes an accessible region label', () => {
    render(MigrationLogAudit, { props: { auditTrail: [], events: [] } });
    expect(screen.getByRole('region', { name: /Audit trail and event log/i })).toBeTruthy();
  });
});
