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

// ParityItem is one comparable, selectable, verifiable unit. ItemKey follows
// the deterministic contract in parity spec §B.2 (category-namespaced, stable
// across compare/apply/rollback/verify).
type ParityItem struct {
	Category    string          `json:"category"`
	ItemKey     string          `json:"itemKey"`
	SourceValue string          `json:"sourceValue"`
	TargetValue string          `json:"targetValue"`
	Status      ParityStatus    `json:"status"`
	Suggested   ParityAction    `json:"suggested"`  // default action from the recommendation rule engine
	ApplyLevel  ApplyLevel      `json:"applyLevel"` // 1..4
	Deps        []DependencyRef `json:"deps,omitempty"`
	Warnings    []string        `json:"warnings,omitempty"`
	Freshness   string          `json:"freshness,omitempty"` // fresh | stale
	VerifyState string          `json:"verifyState,omitempty"` // infra_only | runtime_only | verified | unresolved | manual_required | skipped
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
// "app healthy" (app health) — the kancasoft lesson.
type ParitySummary struct {
	InfraScore      float64 `json:"infraScore"`      // layer 1: files/pkgs/units/images/volumes/DB/cert exist
	RuntimeScore    float64 `json:"runtimeScore"`    // layer 2: active/listening/Up/ready
	AppHealthScore  float64 `json:"appHealthScore"`  // layer 3: /health 200 or manual acknowledgment
	ManualGaps      int     `json:"manualGaps"`      // skip/review_manual/L4 unhandled
	UnresolvedDrift int     `json:"unresolvedDrift"` // applied but verify failed / no probe
	Passed          int     `json:"passed"`
	Failed          int     `json:"failed"`
	MigrationID     int     `json:"migrationId"`
	ComputedAt      string  `json:"computedAt"`
}

// SelectionDecision is the persisted operator decision for one parity item
// (parity spec §I.3). action is one of the ParityAction values.
type SelectionDecision struct {
	MigrationID int    `json:"migrationId"`
	ItemKey     string `json:"itemKey"`
	Category    string `json:"category"`
	Action      string `json:"action"`
	UpdatedAt   string `json:"updatedAt,omitempty"`
}
