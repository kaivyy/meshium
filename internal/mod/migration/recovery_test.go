package migration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"meshium/internal/mod/server"
)

type testRollbackApplier struct {
	name        string
	rollbackErr error
	order       *[]string
}

func (a *testRollbackApplier) Backup(ctx context.Context, ssh SSHExecuter) (BackupData, error) {
	return BackupData{Type: a.name, Data: json.RawMessage(`{}`)}, nil
}

func (a *testRollbackApplier) Apply(ctx context.Context, ssh SSHExecuter, data CategoryData, onProgress StepCallback) error {
	return nil
}

func (a *testRollbackApplier) Rollback(ctx context.Context, ssh SSHExecuter, backup BackupData) error {
	if a.order != nil {
		*a.order = append(*a.order, a.name)
	}
	return a.rollbackErr
}

func newInterruptedMigrationTestSetup(t *testing.T, categories []string, rollbackErrs map[string]error) (*RecoveryManager, JobRepository, int, *[]string) {
	t.Helper()

	db, _ := newTestDB(t)
	repo := NewRepo(db).(JobRepository)

	insertServer := func(id int, name string) {
		_, err := db.Exec(`INSERT INTO servers (id, name, host, port, username, password, ssh_key, passphrase, tags, environment, region, icon, color, favorite, bastion_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, name, name, 22, "user", "", "", "", "[]", "", "", "", "", 0, 0)
		if err != nil {
			t.Fatalf("failed to insert server %s: %v", name, err)
		}
	}
	insertServer(1, "source")
	insertServer(2, "target")

	serverRepo := newMockServerRepo()
	serverRepo.AddServer(&server.Server{ID: 1, Host: "source", Port: 22, Username: "user"})
	serverRepo.AddServer(&server.Server{ID: 2, Host: "target", Port: 22, Username: "user"})

	pool := newMockPool()
	authSvc := &mockAuthSvc{}
	hosts := &mockHostKeyStore{}

	rollbackOrder := []string{}
	registry := &CategoryRegistry{modules: make(map[string]CategoryModule)}
	for _, category := range categories {
		registry.Register(category, nil, &testRollbackApplier{
			name:        category,
			rollbackErr: rollbackErrs[category],
			order:       &rollbackOrder,
		})
	}

	manager := NewRecoveryManager(repo, serverRepo, pool, authSvc, hosts, registry)

	migrationID, err := repo.CreateMigration(1, 2, categories)
	if err != nil {
		t.Fatalf("failed to create migration: %v", err)
	}
	if err := repo.UpdateMigrationStatus(migrationID, StatusInterrupted, ""); err != nil {
		t.Fatalf("failed to update migration status: %v", err)
	}
	if err := repo.SetMigrationStateContext(context.Background(), migrationID, StateInterrupted); err != nil {
		t.Fatalf("failed to set interrupted state: %v", err)
	}

	for idx, category := range categories {
		stepData, err := json.Marshal(CategoryData{Type: category, Data: json.RawMessage(fmt.Sprintf(`{"seed":%q}`, category))})
		if err != nil {
			t.Fatalf("failed to marshal category data for %s: %v", category, err)
		}

		stepID, err := repo.CreateStep(migrationID, category, "collect", string(stepData))
		if err != nil {
			t.Fatalf("failed to create collect step for %s: %v", category, err)
		}
		if err := repo.UpdateStepStatus(stepID, StepStatusCompleted, ""); err != nil {
			t.Fatalf("failed to update step status for %s: %v", category, err)
		}

		jobStepID, err := repo.CreateJobStep(migrationID, category, idx, "category")
		if err != nil {
			t.Fatalf("failed to create job step for %s: %v", category, err)
		}
		backupJSON, err := json.Marshal(BackupData{Type: category, Data: json.RawMessage(fmt.Sprintf(`{"seed":%q}`, category))})
		if err != nil {
			t.Fatalf("failed to marshal backup data for %s: %v", category, err)
		}
		if err := repo.SetJobStepData(jobStepID, "apply", string(backupJSON)); err != nil {
			t.Fatalf("failed to set job step data for %s: %v", category, err)
		}
		if err := repo.CreateCheckpoint(migrationID, category, idx, fmt.Sprintf("checkpoint-%s", category)); err != nil {
			t.Fatalf("failed to create checkpoint for %s: %v", category, err)
		}
	}

	return manager, repo, migrationID, &rollbackOrder
}

func TestCancelMigrationMarksRollbackDegradedWhenSomeStagesFail(t *testing.T) {
	manager, repo, migrationID, rollbackOrder := newInterruptedMigrationTestSetup(
		t,
		[]string{"alpha", "beta"},
		map[string]error{"alpha": errors.New("alpha rollback failed")},
	)

	result, err := manager.CancelMigration(context.Background(), migrationID, nil)
	if err != nil {
		t.Fatalf("CancelMigration returned error: %v", err)
	}
	if result.FinalState != StateRollbackDegraded {
		t.Fatalf("expected final state %s, got %s", StateRollbackDegraded, result.FinalState)
	}
	if result.Action != "cancelled_degraded" {
		t.Fatalf("expected cancelled_degraded action, got %q", result.Action)
	}

	if !reflect.DeepEqual(*rollbackOrder, []string{"beta", "alpha"}) {
		t.Fatalf("expected rollback order [beta alpha], got %v", *rollbackOrder)
	}

	checkpoints, err := repo.GetVerifiedCheckpoints(migrationID)
	if err != nil {
		t.Fatalf("failed to load checkpoints: %v", err)
	}
	if len(checkpoints) != 2 {
		t.Fatalf("expected checkpoints to remain after degraded rollback, got %d", len(checkpoints))
	}

	state, err := repo.GetMigrationState(migrationID)
	if err != nil {
		t.Fatalf("failed to get final migration state: %v", err)
	}
	if state != StateRollbackDegraded {
		t.Fatalf("expected persisted final state %s, got %s", StateRollbackDegraded, state)
	}
}

func TestCancelMigrationMarksFailedWhenAllRollbacksFail(t *testing.T) {
	manager, repo, migrationID, rollbackOrder := newInterruptedMigrationTestSetup(
		t,
		[]string{"alpha", "beta"},
		map[string]error{
			"alpha": errors.New("alpha rollback failed"),
			"beta":  errors.New("beta rollback failed"),
		},
	)

	result, err := manager.CancelMigration(context.Background(), migrationID, nil)
	if err != nil {
		t.Fatalf("CancelMigration returned error: %v", err)
	}
	if result.FinalState != StateFailed {
		t.Fatalf("expected final state %s, got %s", StateFailed, result.FinalState)
	}
	if result.Action != "cancel_failed" {
		t.Fatalf("expected cancel_failed action, got %q", result.Action)
	}

	if !reflect.DeepEqual(*rollbackOrder, []string{"beta", "alpha"}) {
		t.Fatalf("expected rollback order [beta alpha], got %v", *rollbackOrder)
	}

	checkpoints, err := repo.GetVerifiedCheckpoints(migrationID)
	if err != nil {
		t.Fatalf("failed to load checkpoints: %v", err)
	}
	if len(checkpoints) != 2 {
		t.Fatalf("expected checkpoints to remain after failed rollback, got %d", len(checkpoints))
	}

	state, err := repo.GetMigrationState(migrationID)
	if err != nil {
		t.Fatalf("failed to get final migration state: %v", err)
	}
	if state != StateFailed {
		t.Fatalf("expected persisted final state %s, got %s", StateFailed, state)
	}
}
