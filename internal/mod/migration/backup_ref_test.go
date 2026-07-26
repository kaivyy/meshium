package migration

import (
	"testing"
)

// migration_item_results has carried a backup_ref column since Phase 6C, but
// nothing ever wrote to it. Rollback therefore had no per-item evidence and
// had to fall back to "roll back every category that contains at least one
// applied item" — which is why the VPS-A audit classified rollback as
// limited/unsafe for mixed-category migrations.
//
// preparationStage creates one backup row per category and knows its id;
// initialSyncStage creates the item rows. Linking the two is what turns
// "some backup exists somewhere" into "THIS item is restorable from THIS
// backup".
func TestBackupRefFormatIsStableAndParsable(t *testing.T) {
	ref := backupRefFor(42)
	if ref != "backup:42" {
		t.Fatalf("backupRefFor(42) = %q, want backup:42", ref)
	}
	id, ok := parseBackupRef(ref)
	if !ok || id != 42 {
		t.Errorf("parseBackupRef(%q) = %d,%v; want 42,true", ref, id, ok)
	}
}

func TestParseBackupRefRejectsGarbage(t *testing.T) {
	for _, bad := range []string{"", "backup:", "backup:abc", "42", "restore:42"} {
		if _, ok := parseBackupRef(bad); ok {
			t.Errorf("parseBackupRef(%q) accepted a malformed ref", bad)
		}
	}
}

// An item whose category was backed up must carry that backup's ref, so
// rollback can prove per item that a restore point exists.
func TestItemsCarryTheirCategoryBackupRef(t *testing.T) {
	refs := map[string]string{
		"configs":  backupRefFor(7),
		"packages": backupRefFor(8),
	}

	items := []ItemResult{
		{ItemKey: "config:/etc/a.conf", Category: "configs"},
		{ItemKey: "package:nginx", Category: "packages"},
		{ItemKey: "database:app", Category: "database"}, // no backup taken
	}

	for i := range items {
		applyBackupRef(&items[i], refs)
	}

	if got := items[0].BackupRef; got != "backup:7" {
		t.Errorf("config item ref = %q, want backup:7", got)
	}
	if got := items[1].BackupRef; got != "backup:8" {
		t.Errorf("package item ref = %q, want backup:8", got)
	}
	if got := items[2].BackupRef; got != "" {
		t.Errorf("item with no category backup must carry no ref, got %q — a false restore point is worse than none", got)
	}
}

// Rollback feasibility is a property of the item, and must be answerable
// without re-reading the backup blob.
func TestItemRestorableReportsPerItemEvidence(t *testing.T) {
	restorable := ItemResult{ItemKey: "config:/etc/a.conf", ExecutionState: ExecApplied, BackupRef: "backup:7"}
	noBackup := ItemResult{ItemKey: "config:/etc/b.conf", ExecutionState: ExecApplied}
	notApplied := ItemResult{ItemKey: "config:/etc/c.conf", ExecutionState: ExecSkipped, BackupRef: "backup:7"}

	if !ItemRestorable(restorable) {
		t.Error("applied item with a backup ref should be restorable")
	}
	if ItemRestorable(noBackup) {
		t.Error("applied item with no backup ref must not claim restorability")
	}
	if ItemRestorable(notApplied) {
		t.Error("an item that was never applied has nothing to roll back")
	}
}
