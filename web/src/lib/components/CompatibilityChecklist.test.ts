import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/svelte';
import CompatibilityChecklist from './CompatibilityChecklist.svelte';
import type { CompatibilityCheckResult } from '$lib/api/pipeline';

const mk = (over: Partial<CompatibilityCheckResult>): CompatibilityCheckResult => ({
  checkName: 'x',
  severity: 'info',
  passed: true,
  message: 'ok',
  ...over,
});

describe('CompatibilityChecklist', () => {
  it('shows can-proceed when there are only passed checks', () => {
    render(CompatibilityChecklist, { results: [mk({ passed: true, severity: 'info', checkName: 'disk', message: 'enough space' })] });
    expect(screen.getByText(/No blocking issues/i)).toBeTruthy();
    expect(screen.queryByRole('heading', { name: /Blocking/i })).toBeNull();
  });

  it('flags a critical failure as blocking and refuses to proceed', () => {
    render(CompatibilityChecklist, {
      results: [
        mk({ checkName: 'engine', severity: 'critical', passed: false, message: 'unsupported engine' }),
        mk({ checkName: 'disk', severity: 'info', passed: true, message: 'ok' }),
      ],
    });
    expect(screen.getByText(/1 blocking issue/i)).toBeTruthy();
    // The blocking summary must explain, not smooth over.
    expect(screen.getByText(/Resolve these before the migration can proceed/i)).toBeTruthy();
  });

  it('routes a warning-level failure to "review required", not blocking', () => {
    render(CompatibilityChecklist, {
      results: [mk({ checkName: 'ssl', severity: 'warning', passed: false, message: 'self-signed cert' })],
    });
    expect(screen.getByText(/Review required/i)).toBeTruthy();
    expect(screen.queryByRole('heading', { name: /Blocking/i })).toBeNull();
  });

  it('renders the check name + message text (never color-only)', () => {
    render(CompatibilityChecklist, {
      results: [mk({ checkName: 'topology', severity: 'high', passed: false, message: 'ambiguous topology' })],
    });
    expect(screen.getByText('topology')).toBeTruthy();
    expect(screen.getByText(/ambiguous topology/i)).toBeTruthy();
  });
});
