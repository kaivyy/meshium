package migration

import (
	"context"
	"encoding/json"
	"testing"
)

// Regression: a migration left in StatusPlanned whose collect steps don't cover
// every requested category must surface an in-memory error via buildSession, so
// the pipeline wizard's incompletePlan guard offers "Recreate Migration Plan"
// instead of dead-ending on empty Dry Run / Provision / Execute steps. This
// reproduces migration 18: the plan WebSocket disconnected mid-collection
// before the interrupted-marking fix, leaving a stale row with only some
// categories collected. The error is set in-memory only — not persisted.
func TestBuildSessionFlagsPartialCollection(t *testing.T) {
	repo := newTestRepo(t)
	db := repo.(*sqliteRepo).db

	// Two servers to satisfy FK constraints.
	for _, h := range []string{"source", "target"} {
		_, _ = db.Exec(`INSERT INTO servers (name, host, port, username, password, ssh_key, passphrase, tags, environment, region, icon, color, favorite, bastion_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			h, h, 22, "user", "", "", "", "[]", "", "", "", "", 0, 0)
	}

	// Plan two categories but collect only one (packages). configs is missing —
	// the partial state the old early-return bug left behind.
	id, err := repo.CreateMigration(1, 2, []string{"packages", "configs"})
	if err != nil {
		t.Fatalf("CreateMigration: %v", err)
	}
	data, _ := json.Marshal(CategoryData{Type: "packages", Data: []byte("{}")})
	if _, err := repo.CreateStep(id, "packages", "collect", string(data)); err != nil {
		t.Fatalf("CreateStep: %v", err)
	}

	handler := NewPipelineHandler(nil, repo.(PipelineRepo), repo.(Repo))
	session, err := handler.buildSession(context.Background(), id)
	if err != nil {
		t.Fatalf("buildSession: %v", err)
	}
	if session.Migration == nil {
		t.Fatal("nil migration in session")
	}
	if session.Migration.Error == "" {
		t.Fatal("expected in-memory error flagging incomplete collection, got empty")
	}

	// Must be in-memory only: the DB row stays clean (no persisted error) so a
	// legitimate re-collection isn't blocked by a stale error.
	persisted, _ := repo.GetMigration(id)
	if persisted.Error != "" {
		t.Fatalf("error must not persist, got %q", persisted.Error)
	}
}

// A fully-collected planned migration must NOT be flagged incomplete.
func TestBuildSessionCompleteCollectionNotFlagged(t *testing.T) {
	repo := newTestRepo(t)
	db := repo.(*sqliteRepo).db
	for _, h := range []string{"source", "target"} {
		_, _ = db.Exec(`INSERT INTO servers (name, host, port, username, password, ssh_key, passphrase, tags, environment, region, icon, color, favorite, bastion_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			h, h, 22, "user", "", "", "", "[]", "", "", "", "", 0, 0)
	}
	id, _ := repo.CreateMigration(1, 2, []string{"packages", "configs"})
	for _, cat := range []string{"packages", "configs"} {
		data, _ := json.Marshal(CategoryData{Type: cat, Data: []byte("{}")})
		repo.CreateStep(id, cat, "collect", string(data))
	}

	handler := NewPipelineHandler(nil, repo.(PipelineRepo), repo.(Repo))
	session, err := handler.buildSession(context.Background(), id)
	if err != nil {
		t.Fatalf("buildSession: %v", err)
	}
	if session.Migration.Error != "" {
		t.Fatalf("fully-collected plan must not be flagged, got %q", session.Migration.Error)
	}
}
