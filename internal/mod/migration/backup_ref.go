package migration

import (
	"fmt"
	"strconv"
	"strings"
)

// backupRefPrefix namespaces the restore-point reference stored on
// migration_item_results.backup_ref, so the column can later carry other kinds
// of restore evidence (snapshots, dump paths) without ambiguity.
const backupRefPrefix = "backup:"

// backupRefFor renders the reference to a migration_backups row.
func backupRefFor(backupID int) string {
	return backupRefPrefix + strconv.Itoa(backupID)
}

// parseBackupRef extracts the migration_backups id from a reference.
// A malformed or empty ref returns ok=false — callers must treat that as
// "no restore point", never as a default id.
func parseBackupRef(ref string) (int, bool) {
	if !strings.HasPrefix(ref, backupRefPrefix) {
		return 0, false
	}
	id, err := strconv.Atoi(strings.TrimPrefix(ref, backupRefPrefix))
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// applyBackupRef stamps an item with the restore point captured for its
// category. Items in a category with no backup are left blank: recording a
// restore point that does not exist is worse than recording none, because
// rollback would then believe it can undo the change.
func applyBackupRef(r *ItemResult, refsByCategory map[string]string) {
	if r == nil {
		return
	}
	if ref, ok := refsByCategory[r.Category]; ok && ref != "" {
		r.BackupRef = ref
	}
}

// ItemRestorable reports whether this specific item has both a change to undo
// and a restore point to undo it from.
//
// This is the per-item evidence the VPS-A audit found missing: without it,
// rollback could only reason at category granularity ("some item in this
// category was applied, so restore the whole category"), which is why
// mixed-category rollback was classified limited.
func ItemRestorable(r ItemResult) bool {
	if r.ExecutionState != ExecApplied {
		return false
	}
	_, ok := parseBackupRef(r.BackupRef)
	return ok
}

// RollbackScope summarises what a rollback can and cannot undo, so the
// operator sees the real boundary before triggering it.
type RollbackScope struct {
	Restorable   []string // applied items with a restore point
	NoRestore    []string // applied items WITHOUT a restore point
	NotApplied   int      // items that were never applied (nothing to undo)
	ByCategory   map[string]int
	FullyCovered bool // every applied item has a restore point
}

// DescribeRollbackScope computes the rollback boundary from per-item evidence.
func DescribeRollbackScope(items []ItemResult) RollbackScope {
	scope := RollbackScope{ByCategory: map[string]int{}}
	for _, r := range items {
		if r.ExecutionState != ExecApplied {
			scope.NotApplied++
			continue
		}
		if ItemRestorable(r) {
			scope.Restorable = append(scope.Restorable, r.ItemKey)
			scope.ByCategory[r.Category]++
		} else {
			scope.NoRestore = append(scope.NoRestore, r.ItemKey)
		}
	}
	scope.FullyCovered = len(scope.NoRestore) == 0
	return scope
}

// Summary renders the scope for an operator-facing message.
func (s RollbackScope) Summary() string {
	if len(s.Restorable) == 0 && len(s.NoRestore) == 0 {
		return "nothing was applied; there is nothing to roll back"
	}
	if s.FullyCovered {
		return fmt.Sprintf("%d applied item(s) can be restored from their backups", len(s.Restorable))
	}
	return fmt.Sprintf("%d of %d applied item(s) can be restored; %d have no restore point and will NOT be undone",
		len(s.Restorable), len(s.Restorable)+len(s.NoRestore), len(s.NoRestore))
}

// applyBackupRefsTo stamps the category's restore point onto every item that
// actually ended up applied. Only applied items get one: a ref on an item that
// was never written would tell rollback there is something to undo.
func applyBackupRefsTo(items []ItemResult, applySet map[string]bool, refs map[string]string) {
	for i := range items {
		if !applySet[items[i].ItemKey] {
			continue
		}
		if items[i].ExecutionState != ExecApplied {
			continue
		}
		applyBackupRef(&items[i], refs)
	}
}
