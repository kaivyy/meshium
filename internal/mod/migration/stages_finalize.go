package migration

import (
	"context"
	"fmt"
	"log"
)

// Terminal stages: finalization (which derives the honest end status from
// per-item evidence) and archival.
type finalizationStage struct {
	repo     PipelineRepo
	registry *CategoryRegistry
}

func (s *finalizationStage) Name() PipelineStageName { return StageFinalization }

func (s *finalizationStage) Execute(ctx context.Context, pc *PipelineContext) error {
	pc.OnProgress(WSMessage{Step: "finalization", Status: "progress", Value: "Finalizing migration..."})

	// Mark all steps as completed
	steps, err := pc.JobRepo.GetSteps(pc.MigrationID)
	if err != nil {
		return fmt.Errorf("failed to load steps: %w", err)
	}

	// Finalization runs AFTER trafficSwitch and postCutoverObservation, so the
	// target is already live with the migrated data. A returned error here would
	// propagate to Execute's rollback path, which restores the target's stale
	// pre-migration backups — destroying the freshly-migrated data over a benign,
	// transient DB write failure (e.g. "database is locked"). This status update
	// is cosmetic bookkeeping (Applied → Completed); log and continue instead of
	// triggering a destructive rollback of an already-cutover migration.
	for _, step := range steps {
		if step.Status == StepStatusApplied {
			if err := pc.JobRepo.UpdateStepStatus(step.ID, StepStatusCompleted, ""); err != nil {
				log.Printf("warning: failed to finalize step %s for migration %d: %v", step.Category, pc.MigrationID, err)
				pc.OnProgress(WSMessage{Step: "finalization", Status: "warning", Value: fmt.Sprintf("Failed to finalize step %s: %v", step.Category, err)})
			}
		}
	}

	// Record final verification
	pc.Repo.CreateVerificationResult(ctx, VerificationResult{
		MigrationID:      pc.MigrationID,
		VerificationType: "finalization",
		Target:           "target",
		Passed:           true,
	})

	// Phase 6B5: set the honest migration outcome status from selections + step
	// outcomes (spec §D, §C.2). Never claim bare "completed" when there are
	// manual gaps (skip/review_manual) or failures — those force the
	// *-with_manual_gaps / *_partial variants so the FE can't mask them.
	if status, reason := deriveMigrationStatus(ctx, pc); status != "" {
		if err := pc.JobRepo.UpdateMigrationStatus(pc.MigrationID, status, reason); err != nil {
			log.Printf("warning: failed to set honest status %s for migration %d: %v", status, pc.MigrationID, err)
		} else {
			pc.OnProgress(WSMessage{Step: "finalization", Status: "progress", Value: "Outcome: " + status})
		}
	}

	pc.OnProgress(WSMessage{Step: "finalization", Status: "success", Value: "Migration finalized"})
	return nil
}

// deriveMigrationStatus computes the honest terminal status from the operator's
// selections and the actual step outcomes. Returning "" leaves the status
// untouched (legacy migrations with no selection data keep their prior status).
//
// Rules (spec §D):
//   - keep_target for an item → accepted (success-neutral, not drift, not a gap)
//   - skip / review_manual / manual_required → manual gap (→ *_with_manual_gaps)
//   - any failed/errored step → completed_partial (or verification_failed if only
//     verification-level failures)
//   - otherwise → completed
func deriveMigrationStatus(ctx context.Context, pc *PipelineContext) (string, string) {
	steps, err := pc.JobRepo.GetSteps(pc.MigrationID)
	if err != nil {
		return "", ""
	}
	selections, err := pc.JobRepo.GetSelections(ctx, pc.MigrationID)
	if err != nil {
		// No selections → legacy migration: status decided by step failures only.
		selections = nil
	}

	var failedSteps, skippedSteps, appliedSteps int
	for _, s := range steps {
		switch s.Status {
		case StepStatusFailed:
			failedSteps++
		case StepStatusSkipped:
			skippedSteps++
		case StepStatusApplied, StepStatusCompleted:
			appliedSteps++
		}
	}

	// Phase 6C-BE: honest terminal status from PER-ITEM evidence. When
	// migration_item_results rows exist, they are authoritative; the four
	// state dimensions stay separate so a migration can never read "green"
	// while items are unresolved / manual / verify_failed.
	itemResults, rerr := pc.JobRepo.GetItemResults(ctx, pc.MigrationID)
	if rerr == nil && len(itemResults) > 0 {
		return deriveStatusFromItems(itemResults, selections, failedSteps, skippedSteps, appliedSteps)
	}

	// Legacy fallback (no item results persisted): coarse step + selection view.
	manualGap := 0
	for _, d := range selections {
		switch ParityAction(d.Action) {
		case ActionSkip, ActionReviewManual:
			manualGap++
		}
	}

	switch {
	case failedSteps > 0:
		// A failed step means an apply errored; surface it honestly.
		return StatusCompletedPartial,
			fmt.Sprintf("%d step(s) failed; %d applied, %d skipped", failedSteps, appliedSteps, skippedSteps)
	case manualGap > 0:
		return StatusCompletedWithManualGaps,
			fmt.Sprintf("%d item(s) skipped/review_manual requiring follow-up; %d applied, %d skipped", manualGap, appliedSteps, skippedSteps)
	default:
		// Clean: all selected applied/accepted, no failures, no manual gaps.
		return StatusCompleted, ""
	}
}

// deriveStatusFromItems computes the terminal status purely from per-item
// execution + verification evidence (contract §M). Honest rules:
//   - verify_failed present            → verification_failed
//   - applied but not verified/unresolved → verification_partial
//   - any ExecFailed                 → completed_partial
//   - open skip/unknown (no apply)  → completed_with_unresolved_drift
//   - review_manual present          → completed_with_manual_gaps
//   - keep_target present, no gaps  → completed_with_drift
//   - all applied + verified, no gaps → completed (true green)
func deriveStatusFromItems(results []ItemResult, selections []SelectionDecision, failedSteps, skippedSteps, appliedSteps int) (string, string) {
	var blocked, applied, failed, verifyFailed, unresolved, keepTarget, skipReview, intendedApplied int
	selByKey := map[string]ParityAction{}
	for _, d := range selections {
		selByKey[d.ItemKey] = ParityAction(d.Action)
	}
	for _, r := range results {
		// A hard-blocked item is a real gap: it was never applied.
		if r.ExecutionState == ExecBlocked {
			blocked++
			continue
		}
		switch r.ExecutionState {
		case ExecPending:
			// was intended to apply but never executed (interrupted/incomplete).
			intendedApplied++
		case ExecFailed:
			failed++
			intendedApplied++
		case ExecSkipped:
			// keep_target or skip decision.
			if selByKey[r.ItemKey] == ActionKeepTarget {
				keepTarget++
			} else {
				skipReview++
			}
		case ExecNotApplicable:
			// review_manual / non-executable placeholder.
			skipReview++
		case ExecApplied:
			applied++
			intendedApplied++
			switch r.VerificationState {
			case VerifyFailed:
				verifyFailed++
			case VerifyInfra, VerifyRuntime, VerifyApp, VerifyPartial:
				// at least one layer verified — not a gap
			default:
				// not_verified / unresolved / empty → probed-inconclusive
				unresolved++
			}
		}
	}

	// A pure skip/review_manual/keep_target migration (nothing intended to apply)
	// must NOT be reported as verification_partial — it is honest drift, not a
	// verification gap. Only flag "applied but unverified" when something was
	// actually meant to be applied.
	switch {
	case verifyFailed > 0:
		return StatusVerificationFailed,
			fmt.Sprintf("%d item(s) failed verification; %d applied, %d skipped/blocked", verifyFailed, applied, blocked+skipReview)
	case unresolved > 0 || (intendedApplied > 0 && applied == 0):
		// Applied items that were never verified (no probe) are NOT green.
		verified := applied - unresolved
		if verified < 0 {
			verified = 0
		}
		return StatusVerificationPartial,
			fmt.Sprintf("%d applied item(s) unverified/unresolved; %d verified, %d skipped/blocked", unresolved, verified, blocked+skipReview)
	case failed > 0 || failedSteps > 0:
		return StatusCompletedPartial,
			fmt.Sprintf("%d item(s) failed to apply; %d applied, %d skipped/blocked", failed+failedSteps, applied, blocked+skipReview)
	case skipReview > 0:
		// open skip/unknown or review_manual ⇒ drift the operator must resolve.
		return StatusCompletedWithUnresolvedDrift,
			fmt.Sprintf("%d item(s) skipped/review_manual (unresolved drift); %d applied, %d keep_target", skipReview, applied, keepTarget)
	case blocked > 0:
		return StatusCompletedWithUnresolvedDrift,
			fmt.Sprintf("%d item(s) hard-blocked (unresolved); %d applied, %d keep_target", blocked, applied, keepTarget)
	case keepTarget > 0:
		return StatusCompletedWithDrift,
			fmt.Sprintf("%d item(s) accepted as drift (keep_target); %d applied", keepTarget, applied)
	default:
		// All applied items verified, no gaps ⇒ true green.
		return StatusCompleted, ""
	}
}

func (s *finalizationStage) Rollback(ctx context.Context, pc *PipelineContext) error {
	return nil
}

// archiveStage archives the migration and creates a final report.
type archiveStage struct {
	repo PipelineRepo
}

func (s *archiveStage) Name() PipelineStageName { return StageArchive }

func (s *archiveStage) Execute(ctx context.Context, pc *PipelineContext) error {
	pc.OnProgress(WSMessage{Step: "archive", Status: "progress", Value: "Archiving migration..."})

	// Create audit entry
	if _, err := pc.Repo.CreateAuditEntry(ctx, AuditEntry{
		MigrationID: pc.MigrationID,
		EventType:   "migration_archived",
		NewState:    StateCommitted.String(),
		Actor:       "pipeline",
	}); err != nil {
		log.Printf("warning: failed to create archive audit entry for migration %d: %v", pc.MigrationID, err)
	}

	pc.OnProgress(WSMessage{Step: "archive", Status: "success", Value: "Migration archived"})
	return nil
}

func (s *archiveStage) Rollback(ctx context.Context, pc *PipelineContext) error {
	return nil
}

// --- Helper ---

// getRegistryFromContext returns the registry associated with the pipeline
// context. It prefers the per-request registry and falls back to the default
// registry for legacy call sites.
func getRegistryFromContext(pc *PipelineContext) *CategoryRegistry {
	if pc != nil && pc.Registry != nil {
		return pc.Registry
	}
	return defaultRegistry
}

// defaultRegistry is set by NewPipeline to allow legacy stage helpers to access it.
var defaultRegistry *CategoryRegistry
