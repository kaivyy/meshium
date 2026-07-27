package migration

import (
	"context"
	"testing"
)

// VPS-B19. The apply stage marked every item in the apply-set with the SAME
// outcome: all applied on success, all failed on error. But a package install
// is not atomic across the set — the live 20.04 → 22.04 run installed ~390
// packages and then aborted, so all 900 were recorded `failed` while 390 were
// genuinely on the target. Rollback then reported "nothing was applied; there
// is nothing to roll back" while the machine had really changed.
//
// reconcileApplied re-reads the target after the applier returns and records
// what is actually there, so the record matches the machine whether the apply
// succeeded, partly succeeded, or failed.

func TestReconcileMarksOnlyItemsThatActuallyLanded(t *testing.T) {
	ssh := newMockSSH()
	ssh.execOutput["cat /etc/os-release"] = "ID=ubuntu\nVERSION_ID=22.04"
	// nginx and curl really installed; redis did not.
	ssh.addOutput("dpkg-query", "ii  nginx\nii  curl\n")

	items := []ItemResult{
		{ItemKey: "package:nginx", Category: "packages"},
		{ItemKey: "package:curl", Category: "packages"},
		{ItemKey: "package:redis", Category: "packages"},
	}
	applySet := map[string]bool{"package:nginx": true, "package:curl": true, "package:redis": true}

	// Even though the apply reported FAILURE, the two that landed must be
	// recorded as applied — that is the state rollback has to undo.
	reconcileApplied(context.Background(), ssh, "packages", items, applySet, 1, "apply failed: dpkg error")

	byKey := map[string]ItemResult{}
	for _, r := range items {
		byKey[r.ItemKey] = r
	}
	if got := byKey["package:nginx"].ExecutionState; got != ExecApplied {
		t.Errorf("nginx is installed on the target but recorded %q", got)
	}
	if got := byKey["package:curl"].ExecutionState; got != ExecApplied {
		t.Errorf("curl is installed on the target but recorded %q", got)
	}
	if got := byKey["package:redis"].ExecutionState; got != ExecFailed {
		t.Errorf("redis is absent from the target but recorded %q", got)
	}
	if byKey["package:redis"].ExecutionNotes == "" {
		t.Error("a failed item must carry the reason")
	}
}

// On a fully successful apply the reconciliation must still be evidence-based:
// an item the probe cannot find is not applied, whatever the applier said.
func TestReconcileDoesNotTrustSuccessBlindly(t *testing.T) {
	ssh := newMockSSH()
	ssh.execOutput["cat /etc/os-release"] = "ID=ubuntu\nVERSION_ID=22.04"
	ssh.addOutput("dpkg-query", "ii  nginx\n")

	items := []ItemResult{
		{ItemKey: "package:nginx", Category: "packages"},
		{ItemKey: "package:ghost", Category: "packages"},
	}
	applySet := map[string]bool{"package:nginx": true, "package:ghost": true}

	reconcileApplied(context.Background(), ssh, "packages", items, applySet, 1, "")

	byKey := map[string]ItemResult{}
	for _, r := range items {
		byKey[r.ItemKey] = r
	}
	if byKey["package:ghost"].ExecutionState == ExecApplied {
		t.Error("an item the target does not have was recorded as applied on a 'successful' apply")
	}
}

// Items outside the apply-set (keep_target/skip/blocked) must not be touched:
// they were never applied and reconciliation is not an excuse to relabel them.
func TestReconcileLeavesNonApplySetItemsAlone(t *testing.T) {
	ssh := newMockSSH()
	ssh.execOutput["cat /etc/os-release"] = "ID=ubuntu\nVERSION_ID=22.04"
	ssh.addOutput("dpkg-query", "ii  nginx\n")

	items := []ItemResult{{ItemKey: "package:nginx", Category: "packages", ExecutionState: ExecSkipped}}
	reconcileApplied(context.Background(), ssh, "packages", items, map[string]bool{}, 1, "")

	if items[0].ExecutionState != ExecSkipped {
		t.Errorf("a skipped item was relabelled %q by reconciliation", items[0].ExecutionState)
	}
}

// A category with no probe (database) cannot be reconciled from evidence, so
// it must fall back to the applier's own verdict rather than inventing one.
func TestReconcileFallsBackWhenCategoryHasNoProbe(t *testing.T) {
	ssh := newMockSSH()
	items := []ItemResult{{ItemKey: "database:app", Category: "database"}}
	applySet := map[string]bool{"database:app": true}

	reconcileApplied(context.Background(), ssh, "database", items, applySet, 1, "")
	if items[0].ExecutionState != ExecApplied {
		t.Errorf("unprobeable category on a successful apply = %q, want applied", items[0].ExecutionState)
	}

	items2 := []ItemResult{{ItemKey: "database:app", Category: "database"}}
	reconcileApplied(context.Background(), ssh, "database", items2, applySet, 1, "restore failed")
	if items2[0].ExecutionState != ExecFailed {
		t.Errorf("unprobeable category on a failed apply = %q, want failed", items2[0].ExecutionState)
	}
}

// Reconciliation asks "did it land", verification asks "is it correct". For
// configs those differ: right after upload there is no recorded expected hash,
// so the verify probe reports every file unverified. Using it for
// reconciliation marked all 2051 successfully uploaded config files `failed` —
// the record contradicting the machine in the opposite direction to the bug
// reconciliation exists to fix.
func TestReconcileConfigsUsesPresenceNotHash(t *testing.T) {
	ssh := newMockSSH()
	// The file is on the target; no expected hash is recorded yet.
	ssh.addOutput("ls -d", "/etc/app.conf\n")

	items := []ItemResult{{ItemKey: "config:/etc/app.conf", Category: "configs"}}
	applySet := map[string]bool{"config:/etc/app.conf": true}

	reconcileApplied(context.Background(), ssh, "configs", items, applySet, 1, "")

	if items[0].ExecutionState != ExecApplied {
		t.Errorf("an uploaded config was recorded %q (%s); it is on the target",
			items[0].ExecutionState, items[0].ExecutionNotes)
	}
}

// A config that genuinely did not land must still be recorded failed.
func TestReconcileConfigsDetectsMissingFile(t *testing.T) {
	ssh := newMockSSH()
	ssh.addOutput("ls -d", "/etc/present.conf\n")

	items := []ItemResult{
		{ItemKey: "config:/etc/present.conf", Category: "configs"},
		{ItemKey: "config:/etc/absent.conf", Category: "configs"},
	}
	applySet := map[string]bool{"config:/etc/present.conf": true, "config:/etc/absent.conf": true}

	reconcileApplied(context.Background(), ssh, "configs", items, applySet, 1, "")

	if items[0].ExecutionState != ExecApplied {
		t.Errorf("present config recorded %q", items[0].ExecutionState)
	}
	if items[1].ExecutionState != ExecFailed {
		t.Errorf("absent config recorded %q, want failed", items[1].ExecutionState)
	}
}
