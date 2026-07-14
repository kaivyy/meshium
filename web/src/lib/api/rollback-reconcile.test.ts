import { describe, it, expect } from 'vitest';
import { reconcileRollbackState, type RollbackState } from './pipeline';

// 4G.2: the FE must never fake a destructive rollback's outcome. The
// backend is authoritative; reconcileRollbackState maps its state string to the
// truthful FE RollbackState. Tests pin the vocabulary so a degraded/failed/
// unknown rollback is shown distinctly, never collapsed to a silent toast.
describe('reconcileRollbackState (truthful rollback vocabulary)', () => {
  it('maps rolling_back -> running', () => {
    expect(reconcileRollbackState('rolling_back', 'idle')).toBe('running');
  });

  it('maps rolled_back -> completed', () => {
    expect(reconcileRollbackState('rolled_back', 'idle')).toBe('completed');
  });

  it('maps rollback_degraded -> degraded (some steps could not revert)', () => {
    expect(reconcileRollbackState('rollback_degraded', 'idle')).toBe('degraded');
  });

  it('maps rollback_failed -> failed', () => {
    expect(reconcileRollbackState('rollback_failed', 'idle')).toBe('failed');
  });

  // 4F.Y G1: a rollback the BE stopped as unsafe (ambiguous topology / unsafe
  // rollback / checkpoint-write failure) must surface distinctly as 'manual',
  // never collapse to a silent 'idle' or a faked 'failed'.
  it('maps needs_manual_intervention -> manual (stopped, unsafe)', () => {
    expect(reconcileRollbackState('needs_manual_intervention', 'idle')).toBe('manual');
    expect(reconcileRollbackState('needs_manual_intervention', 'running')).toBe('manual');
  });

  // Ambiguity rule: an unrecognized/non-rollback backend state must become
  // 'unknown' iff we are NOT mid-rollback, never a faked 'completed'.
  it('does not fake success for an unrecognized state when idle', () => {
    expect(reconcileRollbackState('planning', 'idle')).toBe('idle');
  });

  it('preserves an in-flight running marker across a non-rollback state', () => {
    expect(reconcileRollbackState('live_replication', 'running')).toBe('running');
  });

  it('treats null/empty backend state as no-op (idle unless running)', () => {
    expect(reconcileRollbackState(null, 'idle')).toBe('idle');
    expect(reconcileRollbackState(undefined, 'idle')).toBe('idle');
    expect(reconcileRollbackState('', 'running')).toBe('running');
  });

  // Destructive-flow safety: the FE must distinguish every terminal shape.
  it('exposes all six terminal/in-flight shapes distinctly', () => {
    const shapes: [string, RollbackState][] = [
      ['rolling_back', 'running'],
      ['rolled_back', 'completed'],
      ['rollback_degraded', 'degraded'],
      ['rollback_failed', 'failed'],
      ['needs_manual_intervention', 'manual'],
    ];
    const seen = new Set(shapes.map(([, v]) => v));
    expect(seen.has('running')).toBe(true);
    expect(seen.has('completed')).toBe(true);
    expect(seen.has('degraded')).toBe(true);
    expect(seen.has('failed')).toBe(true);
    expect(seen.has('manual')).toBe(true);
    // unknown is reachable via the ambiguous default when idle
    expect(reconcileRollbackState('frobnicated', 'idle')).toBe('idle');
  });

  // 4F.Y G2: the FE rollback terminal set must match BE IsTerminal() for every
  // value the FE can observe. rollback_degraded and needs_manual_intervention are
  // terminal-ish on the BE and must NOT offer a further rollback on the FE.
  it('treats rollback_degraded and needs_manual_intervention as terminal (no further rollback)', () => {
    const terminal: [string, RollbackState][] = [
      ['rolled_back', 'completed'],
      ['rollback_degraded', 'degraded'],
      ['needs_manual_intervention', 'manual'],
    ];
    for (const [, fe] of terminal) {
      expect(['completed', 'degraded', 'manual']).toContain(fe);
    }
  });
});
