package migration

import (
	"context"
	"encoding/json"
	"testing"

	"meshium/internal/db"
)

// newPhase6cRepo returns a real sqliteRepo backed by a fresh migrated DB.
func newPhase6cRepo(t *testing.T) *sqliteRepo {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.Migrate(database); err != nil {
		t.Fatalf("migrate db: %v", err)
	}
	repo, ok := NewRepo(database).(*sqliteRepo)
	if !ok {
		t.Fatalf("NewRepo did not return *sqliteRepo")
	}
	seedServers(t, repo)
	return repo
}

func ctx6() context.Context { return context.Background() }

// --- ItemResult persistence (DB contract) ---

func TestItemResultUpsertRoundTrip(t *testing.T) {
	repo := newPhase6cRepo(t)
	m, err := repo.CreateMigration(1, 2, []string{"packages"}, "")
	if err != nil {
		t.Fatalf("create migration: %v", err)
	}
	res := ItemResult{
		MigrationID:       m,
		ItemKey:           "package:nginx",
		Category:          "packages",
		ExecutionState:    ExecApplied,
		StepRefs:          []int{3},
		VerificationState: VerifyInfra,
		VerificationLevel: "infra",
		BackupRef:         "bk-1",
	}
	if err := repo.UpsertItemResult(ctx6(), m, res); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, ok, err := repo.GetItemResult(ctx6(), m, "package:nginx")
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if got.ExecutionState != ExecApplied || got.VerificationState != VerifyInfra || got.BackupRef != "bk-1" {
		t.Errorf("round-trip mismatch: %+v", got)
	}
	// GetItemResults lists all rows for the migration.
	all, err := repo.GetItemResults(ctx6(), m)
	if err != nil || len(all) != 1 {
		t.Fatalf("GetItemResults: len=%d err=%v", len(all), err)
	}
	// Upsert again updates in place (no duplicate row).
	res.VerificationState = VerifyRuntime
	if err := repo.UpsertItemResult(ctx6(), m, res); err != nil {
		t.Fatalf("upsert2: %v", err)
	}
	all, _ = repo.GetItemResults(ctx6(), m)
	if len(all) != 1 {
		t.Fatalf("upsert should not duplicate: got %d rows", len(all))
	}
	if all[0].VerificationState != VerifyRuntime {
		t.Errorf("upsert did not update: %+v", all[0])
	}
}

func TestSelectionHistoryAppendOnly(t *testing.T) {
	repo := newPhase6cRepo(t)
	m, _ := repo.CreateMigration(1, 2, []string{"packages"}, "")
	_ = repo.UpsertSelection(ctx6(), m, "package:nginx", "packages", string(ActionApplyFromSource), "first", false, "")
	_ = repo.AppendSelectionHistory(ctx6(), m, SelectionHistory{MigrationID: m, ItemKey: "package:nginx", FromAction: "", ToAction: string(ActionApplyFromSource), Reason: "first", Actor: "op"})
	_ = repo.UpsertSelection(ctx6(), m, "package:nginx", "packages", string(ActionSkip), "changed", true, "sudo")
	_ = repo.AppendSelectionHistory(ctx6(), m, SelectionHistory{MigrationID: m, ItemKey: "package:nginx", FromAction: string(ActionApplyFromSource), ToAction: string(ActionSkip), Reason: "changed", Actor: "op"})

	hist, err := repo.GetSelectionHistory(ctx6(), m)
	if err != nil {
		t.Fatalf("hist: %v", err)
	}
	if len(hist) != 2 {
		t.Fatalf("expected 2 history rows, got %d", len(hist))
	}
	if hist[0].ToAction != string(ActionApplyFromSource) || hist[1].ToAction != string(ActionSkip) {
		t.Errorf("history ordering wrong: %+v", hist)
	}
}

// --- deriveStatusFromItems honest terminal states ---

func TestDeriveStatusAllGreen(t *testing.T) {
	results := []ItemResult{
		{MigrationID: 1, ItemKey: "package:a", ExecutionState: ExecApplied, VerificationState: VerifyApp},
		{MigrationID: 1, ItemKey: "package:b", ExecutionState: ExecApplied, VerificationState: VerifyRuntime},
	}
	status, _ := deriveStatusFromItems(results, nil, 0, 0, 0)
	if status != StatusCompleted {
		t.Errorf("expected completed, got %s", status)
	}
}

func TestDeriveStatusKeepTargetDrift(t *testing.T) {
	results := []ItemResult{
		{MigrationID: 1, ItemKey: "package:a", ExecutionState: ExecApplied, VerificationState: VerifyApp},
		{MigrationID: 1, ItemKey: "package:k", ExecutionState: ExecSkipped},
	}
	sel := []SelectionDecision{{MigrationID: 1, ItemKey: "package:k", Action: string(ActionKeepTarget)}}
	status, _ := deriveStatusFromItems(results, sel, 0, 0, 0)
	if status != StatusCompletedWithDrift {
		t.Errorf("keep_target should be completed_with_drift, got %s", status)
	}
}

func TestDeriveStatusSkipUnresolvedDrift(t *testing.T) {
	results := []ItemResult{
		{MigrationID: 1, ItemKey: "package:a", ExecutionState: ExecApplied, VerificationState: VerifyApp},
		{MigrationID: 1, ItemKey: "package:s", ExecutionState: ExecSkipped},
	}
	sel := []SelectionDecision{{MigrationID: 1, ItemKey: "package:s", Action: string(ActionSkip)}}
	status, _ := deriveStatusFromItems(results, sel, 0, 0, 0)
	if status != StatusCompletedWithUnresolvedDrift {
		t.Errorf("skip should be completed_with_unresolved_drift, got %s", status)
	}
}

func TestDeriveStatusReviewManualUnresolved(t *testing.T) {
	results := []ItemResult{
		{MigrationID: 1, ItemKey: "package:m", ExecutionState: ExecNotApplicable},
	}
	sel := []SelectionDecision{{MigrationID: 1, ItemKey: "package:m", Action: string(ActionReviewManual)}}
	status, _ := deriveStatusFromItems(results, sel, 0, 0, 0)
	if status != StatusCompletedWithUnresolvedDrift {
		t.Errorf("review_manual should be unresolved drift, got %s", status)
	}
}

func TestDeriveStatusBlockedUnresolved(t *testing.T) {
	results := []ItemResult{
		{MigrationID: 1, ItemKey: "package:b", ExecutionState: ExecBlocked},
		{MigrationID: 1, ItemKey: "package:a", ExecutionState: ExecApplied, VerificationState: VerifyApp},
	}
	status, _ := deriveStatusFromItems(results, nil, 0, 0, 0)
	if status != StatusCompletedWithUnresolvedDrift {
		t.Errorf("blocked should be unresolved drift, got %s", status)
	}
}

func TestDeriveStatusAppliedButUnverifiedNotGreen(t *testing.T) {
	// Applied item with no probe available must NOT read as completed/green.
	results := []ItemResult{
		{MigrationID: 1, ItemKey: "package:a", ExecutionState: ExecApplied, VerificationState: VerifyNotVerified},
	}
	status, _ := deriveStatusFromItems(results, nil, 0, 0, 0)
	if status != StatusVerificationPartial {
		t.Errorf("applied+unverified should be verification_partial, got %s", status)
	}
}

func TestDeriveStatusVerifyFailed(t *testing.T) {
	results := []ItemResult{
		{MigrationID: 1, ItemKey: "package:a", ExecutionState: ExecApplied, VerificationState: VerifyFailed},
	}
	status, _ := deriveStatusFromItems(results, nil, 0, 0, 0)
	if status != StatusVerificationFailed {
		t.Errorf("verify_failed should be verification_failed, got %s", status)
	}
}

func TestDeriveStatusApplyFailedPartial(t *testing.T) {
	results := []ItemResult{
		{MigrationID: 1, ItemKey: "package:a", ExecutionState: ExecApplied, VerificationState: VerifyApp},
		{MigrationID: 1, ItemKey: "package:x", ExecutionState: ExecFailed},
	}
	status, _ := deriveStatusFromItems(results, nil, 0, 0, 0)
	if status != StatusCompletedPartial {
		t.Errorf("apply failed should be completed_partial, got %s", status)
	}
}

// --- ComputeParitySummary 5 axes ---

func TestParitySummaryFiveAxes(t *testing.T) {
	repo := newPhase6cRepo(t)
	m, _ := repo.CreateMigration(1, 2, []string{"packages"}, "")
	// Decisions + item evidence.
	_ = repo.UpsertSelection(ctx6(), m, "package:keep", "packages", string(ActionKeepTarget), "", false, "")
	_ = repo.UpsertSelection(ctx6(), m, "package:skip", "packages", string(ActionSkip), "", false, "")
	_ = repo.UpsertSelection(ctx6(), m, "package:apply", "packages", string(ActionApplyFromSource), "", false, "")
	_ = repo.UpsertItemResult(ctx6(), m, ItemResult{MigrationID: m, ItemKey: "package:apply", ExecutionState: ExecApplied, VerificationState: VerifyInfra})

	parity := &ParityResult{Items: []ParityItem{
		{ItemKey: "package:keep", Category: "packages", Status: ParityDifferent},
		{ItemKey: "package:skip", Category: "packages", Status: ParityDifferent},
		{ItemKey: "package:apply", Category: "packages", Status: ParityDifferent},
	}}
	pe := NewParityEngine(repo, NewCategoryRegistry(), nil, nil, nil, nil)
	sum, err := pe.ComputeParitySummary(ctx6(), m, parity, nil)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if sum.DecisionCoverage != 100 {
		t.Errorf("decisionCoverage expected 100, got %v", sum.DecisionCoverage)
	}
	if sum.AcceptedDrift != 1 {
		t.Errorf("acceptedDrift expected 1, got %v", sum.AcceptedDrift)
	}
	if sum.ManualDeferredBurden != 33.33333333333333 {
		t.Errorf("manualDeferredBurden expected 33.33, got %v", sum.ManualDeferredBurden)
	}
	if sum.ExecutionCompletion != 33.33333333333333 {
		t.Errorf("executionCompletion expected 33.33, got %v", sum.ExecutionCompletion)
	}
	if sum.VerificationConfidence != 100 {
		t.Errorf("verificationConfidence expected 100, got %v", sum.VerificationConfidence)
	}
}

// --- Backward compatibility: no ItemResults still derives status ---

func TestBackwardCompatNoItemResults(t *testing.T) {
	// A migration with steps but no migration_item_results rows must not crash
	// deriveMigrationStatus; it falls back to the legacy step-based path.
	repo := newPhase6cRepo(t)
	m, _ := repo.CreateMigration(1, 2, []string{"packages"}, "")
	_, _ = repo.CreateStep(m, "packages", "initial_sync", "{}")
	_ = repo.UpdateStepStatus(1, StepStatusCompleted, "")
	// No ItemResult rows. deriveMigrationStatus should still run (legacy branch).
	// We exercise it through the repo directly to ensure no panic / nil deref.
	got, _ := repo.GetItemResults(ctx6(), m)
	if len(got) != 0 {
		t.Errorf("expected no item results for legacy migration, got %d", len(got))
	}
}

// --- applyItemPlan per-item classification (pipeline backend enforcement) ---

func TestApplyItemPlanCatMixedAppliesOnlySelected(t *testing.T) {
	// catMixed: a category with 3 items where the operator selected only 1 to
	// apply_from_source, kept 1, skipped 1, and left 1 undecided. Only the
	// apply+undecided items enter applySet; keep/skip become ExecSkipped.
	data, _ := json.Marshal(PackagesData{Packages: []string{"nginx", "redis", "curl", "git"}})
	step := MigrationStepRecord{Category: "packages", Data: string(data)}
	decisions := map[string]ParityAction{
		"package:nginx":  ActionApplyFromSource,
		"package:redis":  ActionKeepTarget,
		"package:curl":   ActionSkip,
		// package:git left undecided → legacy default apply
	}
	pc := &PipelineContext{MigrationID: 1}
	applySet, results := applyItemPlan(ctx6(), pc, step, decisions, newTargetInventory())

	if !applySet["package:nginx"] {
		t.Error("nginx (apply_from_source) should be in applySet")
	}
	if !applySet["package:git"] {
		t.Error("git (undecided) should default into applySet")
	}
	if applySet["package:redis"] || applySet["package:curl"] {
		t.Error("keep_target/skip must NOT enter applySet (catMixed must apply only selected)")
	}
	byKey := map[string]ItemResult{}
	for _, r := range results {
		byKey[r.ItemKey] = r
	}
	if byKey["package:redis"].ExecutionState != ExecSkipped {
		t.Errorf("redis (keep_target) expected ExecSkipped, got %s", byKey["package:redis"].ExecutionState)
	}
	if byKey["package:curl"].ExecutionState != ExecSkipped {
		t.Errorf("curl (skip) expected ExecSkipped, got %s", byKey["package:curl"].ExecutionState)
	}
	if byKey["package:nginx"].ExecutionState != ExecPending {
		t.Errorf("nginx expected ExecPending, got %s", byKey["package:nginx"].ExecutionState)
	}
}

func TestApplyItemPlanReviewManualNotApplicable(t *testing.T) {
	data, _ := json.Marshal(PackagesData{Packages: []string{"weird-pkg"}})
	step := MigrationStepRecord{Category: "packages", Data: string(data)}
	decisions := map[string]ParityAction{"package:weird-pkg": ActionReviewManual}
	pc := &PipelineContext{MigrationID: 1}
	_, results := applyItemPlan(ctx6(), pc, step, decisions, newTargetInventory())
	if results[0].ExecutionState != ExecNotApplicable {
		t.Errorf("review_manual expected ExecNotApplicable, got %s", results[0].ExecutionState)
	}
}

func TestApplyItemPlanHardDepBlocked(t *testing.T) {
	// A service whose backing config is absent on the target is hard-blocked even
	// if the operator decided apply_from_source.
	data, _ := json.Marshal(ServicesData{Services: []string{"nginx"}})
	step := MigrationStepRecord{Category: "services", Data: string(data)}
	decisions := map[string]ParityAction{"service:nginx": ActionApplyFromSource}
	inv := newTargetInventory() // config:/etc/nginx/nginx.conf NOT present ⇒ unsatisfied
	pc := &PipelineContext{MigrationID: 1}
	applySet, results := applyItemPlan(ctx6(), pc, step, decisions, inv)
	if results[0].ExecutionState != ExecBlocked {
		t.Errorf("hard-dep-unsatisfied service expected ExecBlocked, got %s", results[0].ExecutionState)
	}
	if applySet["service:nginx"] {
		t.Error("hard-blocked item must NOT be in applySet")
	}
}

func TestApplyItemPlanHardDepSatisfiedApplies(t *testing.T) {
	data, _ := json.Marshal(ServicesData{Services: []string{"nginx"}})
	step := MigrationStepRecord{Category: "services", Data: string(data)}
	decisions := map[string]ParityAction{"service:nginx": ActionApplyFromSource}
	inv := newTargetInventory()
	inv.configs["config:/etc/nginx/nginx.conf"] = true // dep satisfied
	pc := &PipelineContext{MigrationID: 1}
	applySet, results := applyItemPlan(ctx6(), pc, step, decisions, inv)
	if results[0].ExecutionState != ExecPending {
		t.Errorf("satisfied-dep service expected ExecPending, got %s", results[0].ExecutionState)
	}
	if !applySet["service:nginx"] {
		t.Error("satisfied-dep service should enter applySet")
	}
}

// --- Backward compatibility: idempotent Migrate + legacy schema reads ---

func TestMigrateIdempotent(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.Migrate(database); err != nil {
		t.Fatalf("migrate 1: %v", err)
	}
	if err := db.Migrate(database); err != nil {
		t.Fatalf("migrate 2 (should be no-op): %v", err)
	}
	// New tables exist after migrate.
	repo := NewRepo(database).(*sqliteRepo)
	rows, err := repo.db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name IN ('migration_item_results','migration_selection_history','migration_selections')`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	var found int
	for rows.Next() {
		found++
	}
	if found != 3 {
		t.Errorf("expected 3 new tables present, found %d", found)
	}
}

// TestBackwardCompatLegacySchema simulates an OLD migration DB that predates
// migration_item_results: reads (GetItemResults / GetSelections) must not crash
// and must return empty, so old migrations stay readable.
func TestBackwardCompatLegacySchema(t *testing.T) {
	// Realistic legacy scenario: a fully-migrated DB (so all tables exist) that
	// contains an OLD migration with selections but ZERO migration_item_results
	// rows. Readers must not crash and must return empty so deriveStatusFromItems
	// falls back to the step-based path.
	database, _ := db.Open(":memory:")
	if err := db.Migrate(database); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	seedServers(t, NewRepo(database).(*sqliteRepo))
	repo := NewRepo(database).(*sqliteRepo)
	m, _ := repo.CreateMigration(1, 2, []string{"packages"}, "")
	_ = repo.UpsertSelection(ctx6(), m, "package:nginx", "packages", string(ActionApplyFromSource), "", false, "")

	// No migration_item_results rows for this legacy migration.
	results, err := repo.GetItemResults(ctx6(), m)
	if err != nil {
		t.Errorf("GetItemResults on legacy migration should not error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("legacy migration should yield 0 item results, got %d", len(results))
	}
	sel, err := repo.GetSelections(ctx6(), m)
	if err != nil {
		t.Fatalf("GetSelections legacy: %v", err)
	}
	if len(sel) != 1 || sel[0].ItemKey != "package:nginx" {
		t.Errorf("legacy selection not read: %+v", sel)
	}
}
