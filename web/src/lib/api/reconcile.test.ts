import { describe, it, expect } from 'vitest';
import { decideReconcile } from './migrations';

// phase-4g1: the FE must NEVER guess success. Completion is decided by this
// pure function against backend data. These tests pin the truthfulness rules:
//   - a recoverable/terminal migration exists  => completed (navigate to it)
//   - a recorded failure                       => failed (honest, never silent reset)
//   - nothing found / reconcile error          => unknown (ambiguity shown, not fake success)

describe('decideReconcile (plan completion truthfulness)', () => {
  it('resolves a planned migration to completed', () => {
    expect(decideReconcile([{ id: 7, status: 'planned' }])).toEqual({ kind: 'completed', id: 7, migrationStatus: 'planned' });
  });

  it('resolves running/interrupted/completed/committed to completed', () => {
    for (const status of ['running', 'interrupted', 'completed', 'committed']) {
      const o = decideReconcile([{ id: 3, status }]);
      expect(o.kind).toBe('completed');
      if (o.kind === 'completed') expect(o.id).toBe(3);
    }
  });

  // Test B — lost terminal event: the WS frame never arrived, but the backend
  // persisted the plan. Reconcile must still confirm completion.
  it('confirms completion even when the WS terminal frame was lost (refresh/reconnect)', () => {
    expect(decideReconcile([{ id: 12, status: 'planned' }]).kind).toBe('completed');
  });

  // Truthfulness rule: a genuine backend failure is shown as failed, NOT reset
  // to Step 1 and NOT as a silent success.
  it('maps a backend failure to the failed outcome (honest, no fake success)', () => {
    expect(decideReconcile([{ id: 5, status: 'failed' }])).toEqual({ kind: 'failed', id: 5, migrationStatus: 'failed' });
  });

  // Test E — reconcile endpoint errors / network blip. Ambiguity must be shown
  // as unknown, never success.
  it('surfaces unknown when the reconcile call itself errored', () => {
    expect(decideReconcile([{ id: 1, status: 'planned' }], true)).toEqual({ kind: 'unknown' });
  });

  // Test D / refresh-after-backend-completed: no row yet (still in flight, not
  // persisted) => unknown, prompting the user to check history or retry.
  it('returns unknown when no migration exists for the operation id', () => {
    expect(decideReconcile([])).toEqual({ kind: 'unknown' });
    expect(decideReconcile(null)).toEqual({ kind: 'unknown' });
  });

  // Unknown backend status is treated as unknown (ambiguity shown), not success.
  it('treats an unrecognized status as unknown rather than guessing', () => {
    expect(decideReconcile([{ id: 9, status: 'frobnicated' }])).toEqual({ kind: 'unknown' });
  });
});
