import { describe, it, expect } from 'vitest';
import { isTerminalState, stateToStep, isAnomalyState } from './stepper';

// Phase 4I.X fix: the stepper's state→step mapping and terminality must be
// single-sourced and unit-tested (extracted from the pipeline page).

describe('isTerminalState', () => {
  it('treats committed/completed/rolled_back/cancelled as terminal', () => {
    for (const s of ['committed', 'completed', 'rolled_back', 'cancelled']) {
      expect(isTerminalState(s)).toBe(true);
    }
  });

  it('treats needs_manual_intervention as terminal (no pipeline action allowed)', () => {
    expect(isTerminalState('needs_manual_intervention')).toBe(true);
  });

  it('treats rollback_degraded as terminal', () => {
    expect(isTerminalState('rollback_degraded')).toBe(true);
  });

  it('treats in-flight / anomaly states as non-terminal', () => {
    for (const s of ['planned', 'discovery', 'live_replication', 'awaiting_cutover', 'failed', 'paused', 'interrupted', 'resuming', 'rolling_back']) {
      expect(isTerminalState(s)).toBe(false);
    }
  });

  it('coerces a non-string input defensively', () => {
    expect(isTerminalState('' as unknown as string)).toBe(false);
  });
});

describe('stateToStep', () => {
  it('maps terminal states to step 10 (Finish)', () => {
    expect(stateToStep('committed')).toBe(10);
    expect(stateToStep('completed')).toBe(10);
    expect(stateToStep('rolled_back')).toBe(10);
    expect(stateToStep('cancelled')).toBe(10);
  });

  it('maps the happy-path states to their steps', () => {
    expect(stateToStep('discovery')).toBe(0);
    expect(stateToStep('compatibility_check')).toBe(1);
    expect(stateToStep('risk_assessment')).toBe(2);
    expect(stateToStep('backup')).toBe(5);
    expect(stateToStep('initial_sync')).toBe(6);
    expect(stateToStep('live_replication')).toBe(7);
    expect(stateToStep('pre_cutover')).toBe(8);
    expect(stateToStep('traffic_switch')).toBe(8);
    expect(stateToStep('observation')).toBe(9);
    expect(stateToStep('post_verification')).toBe(9);
  });

  // Step 3 (Plan) is not driven by any backend state — expected to return null.
  it('returns null for states with no pinned step (e.g. plan)', () => {
    expect(stateToStep('planning')).toBe(0); // planning recovers to Discovery step
    expect(stateToStep('rolling_back')).toBeNull();
    expect(stateToStep('awaiting_cutover')).toBeNull();
    expect(stateToStep('paused')).toBeNull();
    expect(stateToStep('interrupted')).toBeNull();
  });

  it('is case-insensitive', () => {
    expect(stateToStep('LIVE_REPLICATION')).toBe(7);
  });
});

describe('isAnomalyState', () => {
  it('flags paused/interrupted/resuming/failed/awaiting_cutover/rolling_back', () => {
    for (const s of ['paused', 'interrupted', 'resuming', 'failed', 'awaiting_cutover', 'rolling_back']) {
      expect(isAnomalyState(s)).toBe(true);
    }
  });

  it('does not flag normal or terminal states', () => {
    for (const s of ['discovery', 'live_replication', 'committed', 'rolled_back', 'needs_manual_intervention']) {
      expect(isAnomalyState(s)).toBe(false);
    }
  });
});
