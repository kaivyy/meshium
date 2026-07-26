package migration

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// Rollback drops every database on the target that is absent from the backup's
// ExistingDbs set. Backup filled that set only when Detect succeeded AND
// ListDatabases returned no error, but every failure path returned an EMPTY map
// with a NIL error — recorded as a successful backup.
//
// So a transient auth failure or a missed Detect at backup time turned rollback
// into "drop every database on the target", including databases that predate
// the migration entirely. This is the largest blast radius in the package: it
// destroys data the tool never touched.
//
// A backup that could not enumerate the target must be distinguishable from one
// that enumerated it and found nothing.
func TestDatabaseBackupMarksEnumerationFailure(t *testing.T) {
	t.Run("engine not detected", func(t *testing.T) {
		ssh := newMockSSH() // no pgrep stub -> Detect false
		a := &DatabaseApplier{}
		a.SetConfig(&DatabaseConfig{Engine: "postgres", Username: "u", Password: "p", Host: "h", Port: 5432})

		backup, err := a.Backup(context.Background(), ssh)
		if err != nil {
			t.Fatalf("Backup: %v", err)
		}
		var b DatabaseBackup
		if err := json.Unmarshal(backup.Data, &b); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if b.Enumerated {
			t.Fatal("backup claims it enumerated the target, but the engine was never detected")
		}
	})

	t.Run("list fails", func(t *testing.T) {
		ssh := newMockSSH()
		ssh.addOutput("pgrep -x postgres", "yes")
		// psql exits non-zero (bad credentials, server down) with no rows.
		ssh.addOutput("psql", "")
		ssh.execExit["psql"] = 2

		a := &DatabaseApplier{}
		a.SetConfig(&DatabaseConfig{Engine: "postgres", Username: "u", Password: "p", Host: "h", Port: 5432})

		backup, err := a.Backup(context.Background(), ssh)
		if err != nil {
			t.Fatalf("Backup: %v", err)
		}
		var b DatabaseBackup
		if err := json.Unmarshal(backup.Data, &b); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if b.Enumerated {
			t.Fatal("backup claims it enumerated the target, but ListDatabases failed")
		}
	})
}

// With a non-enumerated backup, Rollback must drop nothing — the safe action
// when you cannot tell which databases you created is to touch none of them.
func TestDatabaseRollbackRefusesToDropWithoutEnumeratedBackup(t *testing.T) {
	ssh := newMockSSH()
	ssh.addOutput("pgrep -x postgres", "yes")
	// The target currently hosts a database that predates the migration.
	ssh.addOutput("psql", "customer_prod\t1024\n")

	raw, _ := json.Marshal(DatabaseBackup{ExistingDbs: map[string]bool{}, Enumerated: false})

	a := &DatabaseApplier{}
	a.SetConfig(&DatabaseConfig{Engine: "postgres", Username: "u", Password: "p", Host: "h", Port: 5432})

	err := a.Rollback(context.Background(), ssh, BackupData{Type: "database", Data: raw})
	if err == nil {
		t.Fatal("Rollback silently succeeded on a backup that never enumerated the target")
	}
	if !strings.Contains(err.Error(), "refusing to drop") {
		t.Errorf("unexpected error: %v", err)
	}

	for _, cmd := range ssh.commands {
		if strings.Contains(strings.ToUpper(cmd), "DROP DATABASE") {
			t.Fatalf("rollback dropped a database from a backup that never enumerated the target: %q", cmd)
		}
	}
}

// The normal path must still work: an enumerated backup drops only what the
// migration added.
func TestDatabaseRollbackDropsOnlyNewDatabases(t *testing.T) {
	ssh := newMockSSH()
	ssh.addOutput("pgrep -x postgres", "yes")
	ssh.addOutput("psql", "customer_prod\t1024\nmigrated_db\t2048\n")

	raw, _ := json.Marshal(DatabaseBackup{
		ExistingDbs: map[string]bool{"customer_prod": true},
		Enumerated:  true,
	})

	a := &DatabaseApplier{}
	a.SetConfig(&DatabaseConfig{Engine: "postgres", Username: "u", Password: "p", Host: "h", Port: 5432})

	if err := a.Rollback(context.Background(), ssh, BackupData{Type: "database", Data: raw}); err != nil {
		t.Fatalf("Rollback: %v", err)
	}

	var dropped []string
	for _, cmd := range ssh.commands {
		if strings.Contains(strings.ToUpper(cmd), "DROP DATABASE") {
			dropped = append(dropped, cmd)
		}
	}
	if len(dropped) != 1 {
		t.Fatalf("expected exactly one drop, got %d: %v", len(dropped), dropped)
	}
	if !strings.Contains(dropped[0], "migrated_db") {
		t.Errorf("dropped the wrong database: %q", dropped[0])
	}
	if strings.Contains(dropped[0], "customer_prod") {
		t.Errorf("dropped a pre-existing database: %q", dropped[0])
	}
}
