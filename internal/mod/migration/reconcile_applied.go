package migration

import (
	"context"
	"time"
)

// reconcileApplied records what is ACTUALLY on the target after an apply,
// rather than what the applier reported.
//
// The apply stage used to stamp one outcome across the whole apply-set: all
// applied on success, all failed on error. A package install is not atomic
// across the set, so that was wrong in both directions. The live 20.04 → 22.04
// run installed ~390 packages and then aborted; all 900 were recorded failed,
// and rollback duly announced "nothing was applied; there is nothing to roll
// back" while the machine had really changed by 390 packages. The record has
// to match the machine, or every downstream decision — rollback scope,
// verification, the final status — is reasoning about a fiction.
//
// It reuses the same probes as verification (probeItems), so "did it land" is
// answered by one implementation rather than two that can drift apart.
// Categories with no probe fall back to the applier's verdict: inventing
// evidence would be worse than admitting there is none.
func reconcileApplied(ctx context.Context, ssh SSHExecuter, category string,
	items []ItemResult, applySet map[string]bool, stepID int, applyErr string) {

	// Only the apply-set is in question. keep_target / skip / blocked items
	// were never applied and must keep the state applyItemPlan gave them.
	inQuestion := make([]ItemResult, 0, len(applySet))
	for _, r := range items {
		if applySet[r.ItemKey] {
			inQuestion = append(inQuestion, r)
		}
	}
	if len(inQuestion) == 0 {
		return
	}

	verdicts := probeItemsPresence(ctx, ssh, category, inQuestion)
	now := time.Now().UTC().Format(time.RFC3339)

	for i := range items {
		if !applySet[items[i].ItemKey] {
			continue
		}
		v, probed := verdicts[items[i].ItemKey]
		switch {
		case !probed:
			// No evidence available for this category — trust the applier.
			if applyErr != "" {
				items[i].ExecutionState = ExecFailed
				items[i].ExecutionNotes = "apply failed: " + applyErr
			} else {
				items[i].ExecutionState = ExecApplied
				items[i].LastExecutionAt = now
				items[i].StepRefs = []int{stepID}
			}
		case v.OK:
			// Present on the target — applied, regardless of what the batch
			// as a whole reported.
			items[i].ExecutionState = ExecApplied
			items[i].LastExecutionAt = now
			items[i].StepRefs = []int{stepID}
			items[i].ExecutionNotes = ""
		default:
			items[i].ExecutionState = ExecFailed
			note := v.Detail
			if note == "" {
				note = "not present on the target after apply"
			}
			if applyErr != "" {
				note += " (apply reported: " + applyErr + ")"
			}
			items[i].ExecutionNotes = note
		}
	}
}
