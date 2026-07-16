package migration

// Phase 6B (selective apply + verification) type definitions.
//
// This file contains ONLY the data contracts. The engines that produce and
// consume these types (ComputeParity, selection persistence, the deepened
// healthVerificationStage) are implemented in later sub-phases (6B2+). Keeping
// the contracts here, frozen and compile-clean, is the "contract hardening"
// step (6B1) so downstream phases have stable types to build against.
//
// Non-negotiables carried from the audit:
//   - Compare target side is always a LIVE collect (DiffService), never the
//     onboarding snapshot as source of truth (reuse.go refuses all categories).
//   - Secrets/cert/DNS/app-payload appear ONLY as manual_required placeholder
//     items; their values are never stored here.

// ParityStatus is the lifecycle status of one compare item. The set is the
// union of pre-apply (compare) and post-apply (verify) states so a single item
// can transition across the whole flow without losing history.
type ParityStatus string

const (
	// Compare-phase states.
	ParitySame             ParityStatus = "same"               // source == target
	ParityMissingOnTarget  ParityStatus = "missing_on_target"  // exists on source, absent on target
	ParityDifferent        ParityStatus = "different"          // present both sides, values differ
	ParityStale            ParityStatus = "stale"              // compare result older than freshness window
	ParityUnsupported      ParityStatus = "unsupported"        // cannot be compared/applied by Meshium yet

	// Selection-phase states (set by operator decision, persisted in
	// migration_selections — 6B2).
	ParitySelectedForApply ParityStatus = "selected_for_apply"
	ParityAcceptedTarget   ParityStatus = "accepted_target"    // keep_target: target is canonical, not drift
	ParitySkippedByUser    ParityStatus = "skipped_by_user"    // skip: unresolved-by-choice
	ParityManualRequired   ParityStatus = "manual_required"    // L4 / review_manual

	// Apply/verify-phase states.
	ParityApplied    ParityStatus = "applied"    // apply_from_source executed
	ParityVerified   ParityStatus = "verified"  // all reachable verify layers passed or acknowledged
	ParityUnresolved ParityStatus = "unresolved" // apply done but verify failed / no probe available
)

// ParityAction is the operator's decision for one item.
type ParityAction string

const (
	ActionApplyFromSource ParityAction = "apply_from_source"
	ActionKeepTarget     ParityAction = "keep_target"
	ActionSkip           ParityAction = "skip"
	ActionReviewManual   ParityAction = "review_manual"
)

// ApplyLevel classifies how safe an item is to automate (parity spec §B.2/§K).
//
//	1 safe-auto   2 auto-with-warning   3 guarded/manual-confirm   4 manual/out-of-scope
type ApplyLevel int

const (
	ApplyLevelSafe    ApplyLevel = 1
	ApplyLevelWarn    ApplyLevel = 2
	ApplyLevelGuarded ApplyLevel = 3
	ApplyLevelManual  ApplyLevel = 4
)

// DependencyKind classifies a dependency's relationship to its parent item.
type DependencyKind string

const (
	// DepHard blocks apply if unsatisfied (e.g. Node runtime for a PM2 service).
	DepHard DependencyKind = "hard"
	// DepRecommended should be present but does not block (e.g. upstream app
	// healthy for an nginx vhost).
	DepRecommended DependencyKind = "recommended"
	// DepVerifyPost is checked AFTER apply (e.g. port listening, cert valid).
	DepVerifyPost DependencyKind = "verify_post"
)

// DependencyRef records one dependency of a parity item. Satisfied is computed
// against the target inventory at compare time; it is advisory in the UI (hard
// deps disable the apply toggle when unsatisfied).
type DependencyRef struct {
	ItemKey    string         `json:"itemKey"`    // references another ParityItem.ItemKey
	Kind       DependencyKind `json:"kind"`
	Note       string         `json:"note"`
	Satisfied bool           `json:"satisfied"`
}

// --- Phase 6C-BE: four independent state dimensions (contract §F). ---
// These MUST stay separate. Applied != verified; keep_target != equality;
// skip = unresolved; manual_required never auto-completes.

// ExecutionState is the pipeline-layer outcome for one item (contract §F.3).
type ExecutionState string

const (
	ExecNotApplicable   ExecutionState = "not_applicable" // not in apply scope (review_manual / n/a)
	ExecPending         ExecutionState = "pending"         // queued, not yet run
	ExecBlocked         ExecutionState = "blocked"         // hard dep unsatisfied — never applied
	ExecSkipped         ExecutionState = "skipped"         // keep_target or skip decision
	ExecApplied         ExecutionState = "applied"         // apply_from_source executed OK
	ExecFailed          ExecutionState = "failed"          // apply attempted, errored
	ExecRolledBack      ExecutionState = "rolled_back"     // applied then rolled back
	ExecPartiallyApplied ExecutionState = "partially_applied"
)

// VerificationState is the post-apply probe outcome (contract §F.4).
type VerificationState string

const (
	VerifyNotVerified VerificationState = "not_verified" // never probed
	VerifyInfra       VerificationState = "infra_verified"
	VerifyRuntime     VerificationState = "runtime_verified"
	VerifyApp         VerificationState = "app_verified"
	VerifyPartial     VerificationState = "partial_verified"
	VerifyFailed      VerificationState = "verify_failed"
	VerifyUnresolved  VerificationState = "unresolved" // probed, inconclusive / no probe
)

// DecisionState is the operator-layer choice (contract §F.2). Derived from
// migration_selections when present, else undecided.
type DecisionState string

const (
	DecisionUndecided   DecisionState = "undecided"
	DecisionApply       DecisionState = "apply_from_source"
	DecisionKeepTarget  DecisionState = "keep_target"
	DecisionSkip        DecisionState = "skip"
	DecisionReviewManual DecisionState = "review_manual"
)

// ItemResult is the per-item execution + verification evidence row persisted in
// migration_item_results (contract §F.3/§F.4). Honest rule: Applied != Verified
// are tracked as separate fields; an item with no verify probe stays
// verify_unresolved / not_verified and is never reported green.
type ItemResult struct {
	MigrationID       int              `json:"migrationId"`
	ItemKey           string           `json:"itemKey"`
	Category          string           `json:"category"`
	ExecutionState    ExecutionState   `json:"executionState"`
	LastExecutionAt   string           `json:"lastExecutionAt,omitempty"`
	StepRefs          []int            `json:"stepRefs"`
	ExecutionNotes    string           `json:"executionNotes,omitempty"`
	VerificationState VerificationState `json:"verificationState"`
	VerificationLevel string           `json:"verificationLevel,omitempty"` // infra | runtime | app
	VerifyEvidence    string           `json:"verifyEvidence,omitempty"`
	VerifyNotes       string           `json:"verifyNotes,omitempty"`
	BackupRef         string           `json:"backupRef,omitempty"`
	CreatedAt         string           `json:"createdAt,omitempty"`
	UpdatedAt         string           `json:"updatedAt,omitempty"`
}

// SelectionHistory is one append-only audit row in migration_selection_history
// (contract §F.2). Never updated — only inserted.
type SelectionHistory struct {
	MigrationID int    `json:"migrationId"`
	ItemKey     string `json:"itemKey"`
	FromAction  string `json:"fromAction"`
	ToAction    string `json:"toAction"`
	Reason      string `json:"reason"`
	Actor       string `json:"actor"`
	CreatedAt   string `json:"createdAt,omitempty"`
}

// ParityItem is one comparable, selectable, verifiable unit. ItemKey follows
// the deterministic contract in parity spec §B.2 (category-namespaced, stable
// across compare/apply/rollback/verify).
type ParityItem struct {
	Category    string          `json:"category"`
	ItemKey     string          `json:"itemKey"`
	SourceValue string          `json:"sourceValue"`
	TargetValue string          `json:"targetValue"`
	// ObservedState — what the comparison found (contract §F.1). Kept as
	// `Status` for FE compat; do NOT overload it with execution/verification.
	Status      ParityStatus    `json:"status"`
	// DecisionState — operator choice (contract §F.2). Default derived = undecided.
	DecisionState DecisionState  `json:"decisionState"`
	Suggested   ParityAction    `json:"suggested"`  // default action from the recommendation rule engine
	ApplyLevel  ApplyLevel      `json:"applyLevel"` // 1..4
	Deps        []DependencyRef `json:"deps,omitempty"`
	// HardBlocked is true when an item has any unsatisfied hard dependency.
	// Such items MUST be ExecBlocked at apply time and never applied.
	HardBlocked bool            `json:"hardBlocked"`
	Warnings    []string        `json:"warnings,omitempty"`
	Freshness   string          `json:"freshness,omitempty"` // fresh | stale
	// ExecutionState / VerificationState — populated after apply/verify when a
	// migration_item_results row exists; best-effort from step status for legacy.
	ExecutionState    string `json:"executionState,omitempty"`
	LastExecutionAt  string `json:"lastExecutionAt,omitempty"`
	VerificationState string `json:"verificationState,omitempty"`
	VerificationLevel string `json:"verificationLevel,omitempty"` // infra | runtime | app
	VerifyEvidence    string `json:"verifyEvidence,omitempty"`
	// Decision metadata (contract §I.3).
	DecisionReason    string `json:"decisionReason,omitempty"`
	RiskAcknowledged bool   `json:"riskAcknowledged"`
	ManualFollowup   string `json:"manualFollowup,omitempty"`
}

// ParityResult is the full compare output for one migration. Items are
// flattened across categories; the FE groups them back by Category for the
// matrix. Source side derives from migration_steps.data (plan-time collect);
// target side derives from a live DiffService collect.
type ParityResult struct {
	MigrationID int          `json:"migrationId"`
	Items       []ParityItem `json:"items"`
	Freshness   string       `json:"freshness"` // fresh | stale (whole-result, per spec §I.7)
	ComputedAt  string       `json:"computedAt"`
}

// ParitySummary is the post-apply verification report (parity spec §H.5/§I.6).
// Three independent scores keep "file copied" (infra) from being confused with
// "app healthy" (app health) — the kancasoft lesson. Phase 6C-BE adds five
// axes (observedParity / decisionCoverage / executionCompletion /
// verificationConfidence / manualDeferredBurden) plus honest counters so the FE
// can never confuse "selected" with "applied" with "verified" (contract §N).
type ParitySummary struct {
	MigrationID int `json:"migrationId"`
	// Legacy layer scores (kept for FE compat).
	InfraScore     float64 `json:"infraScore"`     // layer 1: files/pkgs/units/images/volumes/DB/cert exist
	RuntimeScore   float64 `json:"runtimeScore"`   // layer 2: active/listening/Up/ready
	AppHealthScore float64 `json:"appHealthScore"` // layer 3: /health 200 or manual acknowledgment
	// Phase 6C-BE five axes (0..1).
	ObservedParity        float64 `json:"observedParity"`        // share of items that are `same`
	DecisionCoverage      float64 `json:"decisionCoverage"`      // share of items with a decision
	ExecutionCompletion   float64 `json:"executionCompletion"`   // share of decided items actually executed
	VerificationConfidence float64 `json:"verificationConfidence"` // share of applied items verified
	ManualDeferredBurden float64 `json:"manualDeferredBurden"`  // share of items deferred to manual
	// Honest counters.
	AcceptedDrift  int `json:"acceptedDrift"`  // keep_target count
	UnresolvedDrift int `json:"unresolvedDrift"` // applied but verify failed / no probe
	ManualGaps     int `json:"manualGaps"`     // skip/review_manual/L4 unhandled
	Failed         int `json:"failed"`        // execution failed
	Passed         int `json:"passed"`        // applied + verified
	ComputedAt     string `json:"computedAt"`
}

// SelectionDecision is the persisted operator decision for one parity item
// (parity spec §I.3). action is one of the ParityAction values.
type SelectionDecision struct {
	MigrationID        int    `json:"migrationId"`
	ItemKey            string `json:"itemKey"`
	Category           string `json:"category"`
	Action             string `json:"action"`
	DecisionReason     string `json:"decisionReason,omitempty"`     // why this choice
	RiskAcknowledged   bool   `json:"riskAcknowledged"`            // operator accepted risk
	ManualFollowup     string `json:"manualFollowup,omitempty"`    // free-text follow-up
	UpdatedAt          string `json:"updatedAt,omitempty"`
}
