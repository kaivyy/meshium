package migration

// RecoveryGuidance is the operator-facing playbook returned by the recovery
// API for a migration's current state. It is derived purely from the honest
// fail-closed semantics already encoded in the state machine and cutover
// orchestrator — it never claims an automatic recovery that does not exist.
type RecoveryGuidance struct {
	State           string   `json:"state"`
	Summary         string   `json:"summary"`
	OperatorActions []string `json:"operatorActions"`
	// SafeToResume reports whether Resume/Retry is a supported transition from
	// this state. When false the only safe paths are rollback or manual
	// intervention — never a silent auto-forward.
	SafeToResume bool `json:"safeToResume"`
}

// recoveryGuidance maps a migration state to concrete operator guidance.
// States absent from the map (the normal progression states) return a generic
// "in-progress, observe" guidance so the API always answers.
func recoveryGuidance(state MigrationState) RecoveryGuidance {
	switch state {
	case StateFailed:
		return RecoveryGuidance{
			State:   "failed",
			Summary: "A stage failed and the pipeline cannot proceed. Inspect the diagnostic bundle and audit trail for the root cause before any action.",
			OperatorActions: []string{
				"GET /api/pipeline/diagnostics/{id} and review the failing stage's error + audit entry.",
				"If the failure is transient (network blip, source briefly unreachable), POST /api/pipeline/{id}/retry to resume from the last checkpoint.",
				"If the failure is permanent, POST /api/pipeline/{id}/rollback to restore source/target to pre-migration state.",
			},
			SafeToResume: true,
		}
	case StateInterrupted:
		return RecoveryGuidance{
			State:   "interrupted",
			Summary: "The migration was interrupted (crash or disconnect) but is resumable from the last completed stage checkpoint.",
			OperatorActions: []string{
				"POST /api/pipeline/{id}/resume to continue from the last checkpoint.",
				"If resume repeatedly fails, treat as failed: review diagnostics then retry or roll back.",
			},
			SafeToResume: true,
		}
	case StatePaused:
		return RecoveryGuidance{
			State:   "paused",
			Summary: "Paused by an operator. No work is in flight; safe to resume.",
			OperatorActions: []string{
				"POST /api/pipeline/{id}/resume to continue.",
			},
			SafeToResume: true,
		}
	case StateAwaitingCutover:
		return RecoveryGuidance{
			State:   "awaiting_cutover",
			Summary: "Pipeline stopped after a manual_required traffic switch. Only an explicit operator commit may finish it; it survives restart.",
			OperatorActions: []string{
				"Confirm traffic is actually flowing to the target (health checks / DNS propagation).",
				"POST /api/pipeline/{id}/commit to finalize, or /rollback to revert if verification failed.",
			},
			SafeToResume: false,
		}
	case StateNeedsManualIntervention:
		return RecoveryGuidance{
			State:   "needs_manual_intervention",
			Summary: "Fail-closed: ambiguous topology, unsafe rollback, checkpoint-write failure, or a rejected commit. No automatic forward/rollback transition is legal from here.",
			OperatorActions: []string{
				"GET /api/pipeline/diagnostics/{id} and read the failing gate's reason (fence status, topology summary, traffic-verify summary).",
				"Resolve the underlying condition (e.g. re-establish the fence lease, confirm target is a standby before re-cutover).",
				"POST /api/pipeline/{id}/rollback if the migration must be abandoned; do not retry until the gate reason is cleared.",
			},
			SafeToResume: false,
		}
	case StateRolledBack, StateRollbackDegraded:
		return RecoveryGuidance{
			State:   state.String(),
			Summary: "Rollback completed. Source and target are restored to their pre-migration state; the migration is finished.",
			OperatorActions: []string{
				"Review the diagnostic bundle to confirm source/target integrity, then plan a corrected re-run if desired.",
			},
			SafeToResume: false,
		}
	case StateCancelled:
		return RecoveryGuidance{
			State:          "cancelled",
			Summary:        "Cancelled by an operator. The migration is finished and not resumable.",
			OperatorActions: []string{"Plan a fresh migration if needed."},
			SafeToResume:   false,
		}
	case StateCommitted:
		return RecoveryGuidance{
			State:          "committed",
			Summary:        "All stages completed; traffic is on the target. Migration is done.",
			OperatorActions: []string{"No action required."},
			SafeToResume:   false,
		}
	default:
		return RecoveryGuidance{
			State:   state.String(),
			Summary: "Migration is progressing through its pipeline stages. Observe via the WebSocket stream.",
			OperatorActions: []string{
				"Watch the live WebSocket stream for stage progress and errors.",
				"Use pause/cancel if you need to halt.",
			},
			SafeToResume: false,
		}
	}
}
