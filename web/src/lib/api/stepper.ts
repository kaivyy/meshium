// Pure, unit-tested helpers for mapping a backend MigrationState string to the
// pipeline stepper. Extracted from the pipeline page (Phase 4I.X fix) so the
// state→step and terminality logic is single-sourced and testable without
// rendering the Svelte component.

// isTerminalState mirrors backend IsTerminal() (state.go). needs_manual_intervention
// is terminal: no automatic exit and no pipeline action (retry/resume/cancel/rollback)
// is permitted from it.
export function isTerminalState(state: string): boolean {
  return [
    'completed',
    'committed',
    'rolled_back',
    'rollback_degraded',
    'needs_manual_intervention',
    'cancelled',
  ].includes(String(state ?? '').toLowerCase());
}

// stateToStep maps a backend state string to the 0-based wizard step. Returns
// null when the state does not pin a specific step (e.g. anomaly states that
// the caller renders via a badge instead). Terminal states map to step 10.
//
// Note: step 3 (Plan) is intentionally absent — it is not driven by any backend
// state; the page only reaches it via the operator's Next action.
export function stateToStep(state: string): number | null {
  const st = String(state ?? '').toLowerCase();
  if (['completed', 'committed', 'rolled_back', 'cancelled'].includes(st)) return 10;
  if (['observation', 'post_verification'].includes(st)) return 9;
  if (['traffic_switch', 'pre_cutover'].includes(st)) return 8;
  if (['live_replication', 'verification'].includes(st)) return 7;
  if (['initial_sync', 'install_dependencies', 'provision_target'].includes(st)) return 6;
  if (st === 'backup') return 5;
  if (st === 'risk_assessment') return 2;
  if (st === 'compatibility_check') return 1;
  if (['discovery', 'planning', 'created'].includes(st)) return 0;
  return null;
}

// ANOMALY_STATES are states that must be shown with a distinct badge/status,
// not as a normal "running" step.
export const ANOMALY_STATES = [
  'awaiting_cutover',
  'failed',
  'paused',
  'interrupted',
  'resuming',
  'rolling_back',
] as const;

export function isAnomalyState(state: string): boolean {
  return (ANOMALY_STATES as readonly string[]).includes(String(state ?? '').toLowerCase());
}
