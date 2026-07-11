package migration

import (
	"context"
	"testing"
)

// seedServers inserts two servers to satisfy the migrations FK constraints.
func seedServers(t *testing.T, repo JobRepository) {
	t.Helper()
	db := repo.(*sqliteRepo).db
	for _, h := range []string{"source", "target"} {
		_, err := db.Exec(`INSERT INTO servers (name, host, port, username, password, ssh_key, passphrase, tags, environment, region, icon, color, favorite, bastion_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			h, h, 22, "user", "", "", "", "[]", "", "", "", "", 0, 0)
		if err != nil {
			t.Fatalf("seed server %s: %v", h, err)
		}
	}
}

// P0-3: UpdateStageCheckpoint (previously zero callers) is now wired and
// persists checkpoint_data into the stage row. Create a stage, write a
// checkpoint, and verify it round-trips through GetStages.
func TestStageCheckpointPersists(t *testing.T) {
	repo := newTestRepo(t)
	prepo := repo.(PipelineRepo)
	seedServers(t, repo)
	ctx := context.Background()

	migID, err := repo.CreateMigration(1, 2, []string{"packages"})
	if err != nil {
		t.Fatalf("create migration: %v", err)
	}
	stageID, err := prepo.CreateStage(ctx, migID, "discovery", 0)
	if err != nil {
		t.Fatalf("create stage: %v", err)
	}
	ckpt := stageCheckpointJSON("discovery", 1, 14)
	if err := prepo.UpdateStageCheckpoint(ctx, stageID, ckpt); err != nil {
		t.Fatalf("update checkpoint: %v", err)
	}
	stages, err := prepo.GetStages(migID)
	if err != nil {
		t.Fatalf("get stages: %v", err)
	}
	if len(stages) != 1 {
		t.Fatalf("want 1 stage, got %d", len(stages))
	}
	if stages[0].CheckpointData != ckpt {
		t.Fatalf("checkpoint round-trip: got %q want %q", stages[0].CheckpointData, ckpt)
	}
}

// P0-3: a cancelled context fails the checkpoint write closed — it returns
// ctx.Err() rather than silently dropping the checkpoint and advancing.
func TestStageCheckpointWriteFailureFailsClosed(t *testing.T) {
	repo := newTestRepo(t)
	prepo := repo.(PipelineRepo)
	seedServers(t, repo)
	ctx := context.Background()

	migID, err := repo.CreateMigration(1, 2, []string{"packages"})
	if err != nil {
		t.Fatalf("create migration: %v", err)
	}
	stageID, err := prepo.CreateStage(ctx, migID, "discovery", 0)
	if err != nil {
		t.Fatalf("create stage: %v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := prepo.UpdateStageCheckpoint(cancelled, stageID, "{}"); err == nil {
		t.Fatal("a cancelled context must surface an error from UpdateStageCheckpoint (fail closed)")
	}
}

// P0-3: stageCheckpointJSON produces valid JSON with the stage name + completed state.
func TestStageCheckpointJSONShape(t *testing.T) {
	s := stageCheckpointJSON("initial_sync", 2, 14)
	if !containsStr(s, `"state":"completed"`) || !containsStr(s, `"stage":"initial_sync"`) || !containsStr(s, `"attempt":2`) {
		t.Fatalf("unexpected checkpoint JSON: %s", s)
	}
}
