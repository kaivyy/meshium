package migration

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"meshium/internal/mod/server"
)

// parity_engine.go (Phase 6B2) — ComputeParity.
//
// Source side derives from the plan-time collect steps (migration_steps.data,
// action="collect", status=StepStatusApplied) — the SAME data the applier will
// replay, so the compare is honest about what would actually be applied. Target
// side is ALWAYS a fresh live collect via the category collectors (DiffService
// pattern), never the onboarding snapshot — reuse.go refuses all categories, so
// the snapshot is never authoritative for parity.
//
// Secrets/cert/DNS/app-payload do not appear here: they are L4 manual-required
// placeholders added in a later sub-phase. This engine covers the six collectable
// categories (packages, configs, services, users, docker, database).

// ParityEngine builds a ParityResult for one migration.
type ParityEngine struct {
	repo     Repo
	registry *CategoryRegistry
	srvRepo  server.Repo
	pool     ConnectionPool
	authSvc  AESKeyProvider
	hosts    HostKeyStore
}

// NewParityEngine creates a ParityEngine from the same dependencies the Executor
// and DiffService use, plus the migration Repo to read the plan steps and record.
func NewParityEngine(repo Repo, registry *CategoryRegistry, srvRepo server.Repo, pool ConnectionPool, authSvc AESKeyProvider, hosts HostKeyStore) *ParityEngine {
	return &ParityEngine{repo: repo, registry: registry, srvRepo: srvRepo, pool: pool, authSvc: authSvc, hosts: hosts}
}

// ComputeParity collects the live target, reads the source plan steps, and emits
// a flattened ParityResult. onProgress is optional.
func (e *ParityEngine) ComputeParity(ctx context.Context, migrationID int, onProgress StepCallback) (*ParityResult, error) {
	if onProgress == nil {
		onProgress = func(WSMessage) {}
	}

	m, err := e.repo.GetMigration(migrationID)
	if err != nil {
		return nil, fmt.Errorf("migration not found: %w", err)
	}

	onProgress(WSMessage{Step: "parity", Status: "progress", Value: "Connecting to target..."})

	targetServer, err := e.srvRepo.GetByID(m.TargetID)
	if err != nil {
		return nil, fmt.Errorf("target server not found: %w", err)
	}

	// Source side derives from the persisted plan-time collect steps, not a live
	// source connection (honest: those steps are exactly what the applier replays).
	targetSSH, err := getSSHClientForServer(m.TargetID, targetServer, e.srvRepo, e.pool, e.authSvc, e.hosts)
	if err != nil {
		return nil, fmt.Errorf("target SSH connection failed: %w", err)
	}

	onProgress(WSMessage{Step: "parity", Status: "success", Value: "Connected to both servers"})

	// Source side: the plan-time collect steps already persisted.
	steps, err := e.repo.GetSteps(migrationID)
	if err != nil {
		return nil, fmt.Errorf("read plan steps: %w", err)
	}
	sourceByCat := map[string]CategoryData{}
	for _, s := range steps {
		if s.Action != "collect" || s.Status != StepStatusApplied {
			continue
		}
		var cd CategoryData
		if err := json.Unmarshal([]byte(s.Data), &cd); err != nil {
			continue
		}
		sourceByCat[s.Category] = cd
	}

	// Target side: live collect for the same categories.
	targetByCat := map[string]CategoryData{}
	inv := newTargetInventory()
	for _, catName := range m.Categories {
		mod, ok := e.registry.Get(catName)
		if !ok {
			continue
		}
		onProgress(WSMessage{Step: "parity:" + catName, Status: "progress", Value: "Collecting target " + catName + "..."})
		td, err := mod.Collector.Collect(ctx, targetSSH)
		if err != nil {
			onProgress(WSMessage{Step: "parity:" + catName, Status: "error", Error: err.Error()})
			continue
		}
		targetByCat[catName] = td
		mergeInventory(inv, catName, td)
	}

	// Build items.
	result := &ParityResult{MigrationID: migrationID, ComputedAt: time.Now().UTC().Format(time.RFC3339)}
	items := make([]ParityItem, 0)
	for _, catName := range m.Categories {
		src, okSrc := sourceByCat[catName]
		if !okSrc {
			// No source collect step → cannot compare. Skip honestly.
			continue
		}
		tgt, okTgt := targetByCat[catName]
		items = append(items, e.compareCategory(catName, src, tgt, okTgt, inv)...)
	}
	result.Items = items

	// Phase 6C-BE: enrich each item with the four independent state dimensions
	// so the FE never has to guess. DecisionState derives from selections;
	// HardBlocked is recomputed from the dependency graph; ExecutionState /
	// VerificationState are read from migration_item_results when present
	// (best-effort: legacy migrations without rows stay not_applicable /
	// not_verified and are reported honestly, never green).
	e.enrichItems(ctx, migrationID, items)

	// Whole-result freshness: target collect is live, so fresh by construction.
	result.Freshness = "fresh"

	onProgress(WSMessage{Step: "parity", Status: "complete", Value: fmt.Sprintf("Compared %d items", len(items))})
	return result, nil
}

// enrichItems lays the four independent state dimensions onto each ParityItem
// (contract §F). It is read-only against selections + migration_item_results;
// it never mutates persisted state. Best-effort for legacy migrations: if no
// item result exists, ExecutionState stays not_applicable and VerificationState
// stays not_verified (never fabricated green).
func (e *ParityEngine) enrichItems(ctx context.Context, migrationID int, items []ParityItem) {
	selByKey := map[string]SelectionDecision{}
	if sels, err := e.repo.GetSelections(ctx, migrationID); err == nil {
		for _, s := range sels {
			selByKey[s.ItemKey] = s
		}
	}
	resByKey := map[string]ItemResult{}
	if res, err := e.repo.GetItemResults(ctx, migrationID); err == nil {
		for _, r := range res {
			resByKey[r.ItemKey] = r
		}
	}
	for i := range items {
		it := &items[i]
		// DecisionState from selection; default undecided.
		if sel, ok := selByKey[it.ItemKey]; ok {
			it.DecisionState = DecisionState(sel.Action)
			it.DecisionReason = sel.DecisionReason
			it.RiskAcknowledged = sel.RiskAcknowledged
			it.ManualFollowup = sel.ManualFollowup
		} else {
			it.DecisionState = DecisionUndecided
		}
		// HardBlocked: any unsatisfied hard dependency forces ExecBlocked.
		it.HardBlocked = hasUnsatisfiedHardDep(*it)
		// Execution/Verification from item results when present.
		if r, ok := resByKey[it.ItemKey]; ok {
			it.ExecutionState = string(r.ExecutionState)
			it.LastExecutionAt = r.LastExecutionAt
			it.VerificationState = string(r.VerificationState)
			it.VerificationLevel = r.VerificationLevel
			it.VerifyEvidence = r.VerifyEvidence
		}
	}
}

// ComputeParitySummary derives the post-apply verification report (spec §H.5)
// from the compare items and the actual step outcomes. The three independent
// scores keep "file copied" (infra) distinct from "app healthy" (app health) —
// the kancasoft lesson: copying configs is not the same as a running app.
//
// Layer semantics (spec §E):
//   - InfraScore: fraction of applied/accepted items whose target state exists
//     (same / accepted_target / applied).
//   - RuntimeScore: fraction of applied items whose target state is "same" or
//     accepted (proxy for "present and matching"); unresolved/missing lower it.
//   - AppHealthScore: fraction of items with an acknowledged/verified state;
//     items we cannot probe are NOT counted as healthy (no false success).
func (e *ParityEngine) ComputeParitySummary(ctx context.Context, migrationID int, parity *ParityResult, steps []MigrationStepRecord) (*ParitySummary, error) {
	summary := &ParitySummary{MigrationID: migrationID, ComputedAt: time.Now().UTC().Format(time.RFC3339)}

	// Phase 6C-BE: derive the five honest axes + counters from the
	// per-item execution + verification evidence when present, falling back to
	// selections + parity for legacy migrations. Applied != Verified are
	// tracked separately so "selected" never reads as "green".
	selections, err := e.repo.GetSelections(ctx, migrationID)
	if err != nil {
		selections = nil
	}
	selByKey := map[string]ParityAction{}
	for _, d := range selections {
		selByKey[d.ItemKey] = ParityAction(d.Action)
	}
	itemResults, rerr := e.repo.GetItemResults(ctx, migrationID)
	resByKey := map[string]ItemResult{}
	if rerr == nil {
		for _, r := range itemResults {
			resByKey[r.ItemKey] = r
		}
	}

	if parity == nil || len(parity.Items) == 0 {
		return summary, nil
	}

	total := len(parity.Items)
	var decided, executed, verified, manualDeferred, acceptedDrift, unresolvedDrift, failed, passed int
	for _, it := range parity.Items {
		action := selByKey[it.ItemKey]
		res, hasRes := resByKey[it.ItemKey]

		// Decision coverage: item carries an explicit decision.
		if action != "" {
			decided++
		}
		// Accepted drift: keep_target (target is canonical, not a gap).
		if action == ActionKeepTarget || it.Status == ParityAcceptedTarget {
			acceptedDrift++
			passed++
			continue
		}
		// Manual deferred burden: skip / review_manual / undecired-and-differs.
		if action == ActionSkip || action == ActionReviewManual {
			manualDeferred++
			summary.ManualGaps++
			continue
		}
		// Execution completion: decided items that actually executed (applied).
		if hasRes && res.ExecutionState == ExecApplied {
			executed++
			// Verification confidence: applied items that reached a real verify layer.
			switch res.VerificationState {
			case VerifyInfra, VerifyRuntime, VerifyApp, VerifyPartial:
				verified++
				passed++
			case VerifyFailed:
				failed++
				unresolvedDrift++
				summary.UnresolvedDrift++
				summary.Failed++
			default:
				// applied but never probed → unresolved, NOT green.
				unresolvedDrift++
				summary.UnresolvedDrift++
			}
		} else if action == ActionApplyFromSource {
			// decided to apply but no execution evidence yet → incomplete.
			unresolvedDrift++
			summary.UnresolvedDrift++
		}
	}

	summary.ObservedParity = pct(observedSameCount(parity), total)
	summary.DecisionCoverage = pct(decided, total)
	summary.ExecutionCompletion = pct(executed, maxInt(1, decided))
	summary.VerificationConfidence = pct(verified, maxInt(1, executed))
	summary.ManualDeferredBurden = pct(manualDeferred, total)
	summary.AcceptedDrift = acceptedDrift
	summary.UnresolvedDrift = unresolvedDrift
	summary.ManualGaps = manualDeferred
	summary.Failed = failed
	summary.Passed = passed
	return summary, nil
}

// observedSameCount counts items whose observed state is `same`.
func observedSameCount(parity *ParityResult) int {
	n := 0
	for _, it := range parity.Items {
		if it.Status == ParitySame {
			n++
		}
	}
	return n
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func pct(n, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(n) / float64(total) * 100
}

// BulkPolicy describes a 6B6 progressive-automation sweep over the current
// parity items. Each policy is conservative by design: it only acts on items
// the ApplyLevel + dependency graph classify as safe to act on without an
// explicit operator sign-off.
type BulkPolicy string

const (
	// BulkApplySafe: apply_from_source for items that are low-risk (applyLevel <=
	// 2 = Safe/Warn) AND have no UNsatisfied hard dependency. Never touches
	// guarded/manual items. This is the L1/L2 automation tier.
	BulkApplySafe BulkPolicy = "apply_safe"
	// BulkAcceptRiskyUnchanged: keep_target for guarded (applyLevel == 3) items
	// that are already ParitySame on the target — they need no change, so the
	// operator's "keep target" is mechanically honest. Guarded items that DIFFER
	// are left as review_manual (we will not silently overwrite target state).
	BulkAcceptRiskyUnchanged BulkPolicy = "accept_risky_unchanged"
	// BulkSkipSelected: skip for every item that is NOT already a clean
	// keep_target — used to "defer everything" before a careful review.
	BulkSkipSelected BulkPolicy = "skip_selected"
	// BulkReviewManualSelected: review_manual for every item that is not a
	// clean keep_target — pushes undecided/apply items to manual review.
	BulkReviewManualSelected BulkPolicy = "review_manual_selected"
	// BulkClear: remove ALL selection decisions for the migration (reset).
	BulkClear BulkPolicy = "clear"
)

// BulkResult reports what a BulkApply changed.
type BulkResult struct {
	Policy   BulkPolicy `json:"policy"`
	Applied  int        `json:"applied"`
	Accepted int        `json:"accepted"`
	Skipped  int        `json:"skipped"`
	Manual   int        `json:"manual"`
	Items    []SelectionDecision `json:"items"`
}

// BulkApply computes fresh parity then writes selection decisions according to
// policy. It is idempotent: re-running yields the same decisions. Items already
// carrying an explicit operator selection are left untouched (explicit wins).
func (e *ParityEngine) BulkApply(ctx context.Context, migrationID int, policy BulkPolicy) (*BulkResult, error) {
	parity, err := e.ComputeParity(ctx, migrationID, nil)
	if err != nil {
		return nil, err
	}
	existing, err := e.repo.GetSelections(ctx, migrationID)
	if err != nil {
		existing = nil
	}
	// BulkClear wipes ALL decisions first (a reset), then the loop skips
	// writing anything — honest "start over" before a fresh sweep.
	if policy == BulkClear {
		if cerr := e.repo.ClearSelections(ctx, migrationID); cerr != nil {
			return nil, cerr
		}
		existing = nil
	}
	explicit := map[string]bool{}
	for _, d := range existing {
		explicit[d.ItemKey] = true
	}

	res := &BulkResult{Policy: policy, Items: []SelectionDecision{}}
	for _, it := range parity.Items {
		if explicit[it.ItemKey] {
			res.Skipped++
			continue
		}
		var action ParityAction
		switch policy {
		case BulkApplySafe:
			if it.ApplyLevel > ApplyLevelWarn {
				res.Manual++
				continue
			}
			if hasUnsatisfiedHardDep(it) {
				res.Manual++
				continue
			}
			action = ActionApplyFromSource
			res.Applied++
		case BulkAcceptRiskyUnchanged:
			if it.ApplyLevel != ApplyLevelGuarded {
				res.Skipped++
				continue
			}
			if it.Status != ParitySame {
				// Guarded and differs on target → operator must decide; never
				// silently keep a target that diverges from source intent.
				res.Manual++
				continue
			}
			action = ActionKeepTarget
			res.Accepted++
		case BulkSkipSelected:
			// Defer everything that is not already a clean keep_target.
			if ParityAction(existingAction(it, existing)) == ActionKeepTarget {
				res.Skipped++
				continue
			}
			action = ActionSkip
			res.Skipped++
		case BulkReviewManualSelected:
			// Push undecided/apply items to manual review.
			if ParityAction(existingAction(it, existing)) == ActionKeepTarget {
				res.Skipped++
				continue
			}
			action = ActionReviewManual
			res.Manual++
		case BulkClear:
			// Handled before the loop (clears all decisions); here we
			// simply skip writing anything.
			res.Skipped++
			continue
		default:
			res.Skipped++
			continue
		}
		if err := e.repo.UpsertSelection(ctx, migrationID, it.ItemKey, it.Category, string(action), "", false, ""); err != nil {
			return nil, err
		}
		res.Items = append(res.Items, SelectionDecision{
			MigrationID: migrationID,
			ItemKey:     it.ItemKey,
			Category:    it.Category,
			Action:      string(action),
		})
	}
	return res, nil
}

// existingAction returns the persisted decision action for an item key, or "".
func existingAction(it ParityItem, existing []SelectionDecision) string {
	for _, d := range existing {
		if d.ItemKey == it.ItemKey {
			return d.Action
		}
	}
	return ""
}

// GetFollowUp returns the parity items that still require operator follow-up
// (contract §N): items decided skip / review_manual / unknown, plus any
// applied item whose verification FAILED. These are the open burden a
// "completed" migration still carries.
func (e *ParityEngine) GetFollowUp(ctx context.Context, migrationID int) (*ParityResult, error) {
	parity, err := e.ComputeParity(ctx, migrationID, nil)
	if err != nil {
		return nil, err
	}
	selections, err := e.repo.GetSelections(ctx, migrationID)
	if err != nil {
		selections = nil
	}
	selByKey := map[string]ParityAction{}
	for _, d := range selections {
		selByKey[d.ItemKey] = ParityAction(d.Action)
	}
	results, rerr := e.repo.GetItemResults(ctx, migrationID)
	verifyByKey := map[string]VerificationState{}
	if rerr == nil {
		for _, r := range results {
			verifyByKey[r.ItemKey] = r.VerificationState
		}
	}

	out := &ParityResult{MigrationID: migrationID, ComputedAt: time.Now().UTC().Format(time.RFC3339), Freshness: "fresh"}
	for _, it := range parity.Items {
		action := selByKey[it.ItemKey]
		switch {
		case action == ActionSkip || action == ActionReviewManual:
			out.Items = append(out.Items, it)
		case action == "" && it.Status != ParitySame:
			// undecired AND not already identical → still needs a call.
			out.Items = append(out.Items, it)
		case verifyByKey[it.ItemKey] == VerifyFailed:
			out.Items = append(out.Items, it)
		}
	}
	return out, nil
}

// hasUnsatisfiedHardDep reports whether an item has a hard dependency whose
// target state is absent. We only KNOW satisfaction for live-collected items
// (deps carry a satisfied bool); treat missing info as satisfied (don't block
// on unknowns we can't prove).
func hasUnsatisfiedHardDep(it ParityItem) bool {
	for _, d := range it.Deps {
		if d.Kind == "hard" && !d.Satisfied {
			return true
		}
	}
	return false
}

// compareCategory emits ParityItems for one category, branching on the concrete
// CategoryData shape. Each branch mirrors the corresponding diff.go comparison
// but produces itemized ParityItem values + ApplyLevel + deps.
func (e *ParityEngine) compareCategory(category string, src, tgt CategoryData, okTgt bool, inv *targetInventory) []ParityItem {
	switch category {
	case "packages":
		return e.comparePackages(src, tgt, okTgt, inv)
	case "configs":
		return e.compareConfigs(src, tgt, okTgt, inv)
	case "services":
		return e.compareServices(src, tgt, okTgt, inv)
	case "users":
		return e.compareUsers(src, tgt, okTgt, inv)
	case "docker":
		return e.compareDocker(src, tgt, okTgt, inv)
	case "database":
		return e.compareDatabase(src, tgt, okTgt, inv)
	}
	return nil
}

func (e *ParityEngine) comparePackages(src, tgt CategoryData, okTgt bool, inv *targetInventory) []ParityItem {
	var sd, td PackagesData
	_ = json.Unmarshal(src.Data, &sd)
	if okTgt {
		_ = json.Unmarshal(tgt.Data, &td)
	}
	tgtSet := map[string]bool{}
	for _, p := range td.Packages {
		tgtSet[p] = true
	}
	items := make([]ParityItem, 0, len(sd.Packages))
	for _, p := range sd.Packages {
		item := ParityItem{Category: "packages", ItemKey: "package:" + p, SourceValue: p}
		if tgtSet[p] {
			item.Status = ParitySame
			item.TargetValue = p
		} else {
			item.Status = ParityMissingOnTarget
			item.TargetValue = ""
		}
		item.ApplyLevel = ApplyLevelSafe
		item.Suggested = ActionApplyFromSource
		items = append(items, item)
	}
	return items
}

func (e *ParityEngine) compareConfigs(src, tgt CategoryData, okTgt bool, inv *targetInventory) []ParityItem {
	var sd, td ConfigsData
	_ = json.Unmarshal(src.Data, &sd)
	if okTgt {
		_ = json.Unmarshal(tgt.Data, &td)
	}
	items := make([]ParityItem, 0, len(sd.Files))
	for path, content := range sd.Files {
		key := "config:" + path
		item := ParityItem{Category: "configs", ItemKey: key, SourceValue: path}
		tgtContent, exists := td.Files[path]
		if !exists {
			item.Status = ParityMissingOnTarget
			item.TargetValue = ""
		} else if !bytesEqual(content, tgtContent) {
			item.Status = ParityDifferent
			item.TargetValue = path
		} else {
			item.Status = ParitySame
			item.TargetValue = path
		}
		// Configs overwrite target state → warn-level.
		item.ApplyLevel = ApplyLevelWarn
		if item.Status == ParityDifferent {
			item.Warnings = append(item.Warnings, "target config differs — apply overwrites it")
		}
		item.Suggested = ActionApplyFromSource
		item.Deps = buildDeps("configs", key, inv, nil)
		items = append(items, item)
	}
	return items
}

func (e *ParityEngine) compareServices(src, tgt CategoryData, okTgt bool, inv *targetInventory) []ParityItem {
	var sd, td ServicesData
	_ = json.Unmarshal(src.Data, &sd)
	if okTgt {
		_ = json.Unmarshal(tgt.Data, &td)
	}
	tgtSet := map[string]bool{}
	for _, s := range td.Services {
		tgtSet[s] = true
	}
	items := make([]ParityItem, 0, len(sd.Services))
	for _, s := range sd.Services {
		key := "service:" + s
		item := ParityItem{Category: "services", ItemKey: key, SourceValue: s}
		if tgtSet[s] {
			item.Status = ParitySame
			item.TargetValue = s
		} else {
			item.Status = ParityMissingOnTarget
			item.TargetValue = ""
		}
		// Services need prereqs (config + runtime) → guarded.
		item.ApplyLevel = ApplyLevelGuarded
		item.Suggested = ActionApplyFromSource
		item.Deps = buildDeps("services", key, inv, nil)
		items = append(items, item)
	}
	return items
}

func (e *ParityEngine) compareUsers(src, tgt CategoryData, okTgt bool, inv *targetInventory) []ParityItem {
	var sd, td UsersData
	_ = json.Unmarshal(src.Data, &sd)
	if okTgt {
		_ = json.Unmarshal(tgt.Data, &td)
	}
	tgtSet := map[string]bool{}
	for _, u := range td.Users {
		tgtSet[u.Name] = true
	}
	items := make([]ParityItem, 0, len(sd.Users))
	for _, u := range sd.Users {
		key := "user:" + u.Name
		item := ParityItem{Category: "users", ItemKey: key, SourceValue: u.Name}
		if tgtSet[u.Name] {
			item.Status = ParitySame
			item.TargetValue = u.Name
		} else {
			item.Status = ParityMissingOnTarget
			item.TargetValue = ""
		}
		item.ApplyLevel = ApplyLevelGuarded
		item.Suggested = ActionApplyFromSource
		items = append(items, item)
	}
	return items
}

func (e *ParityEngine) compareDocker(src, tgt CategoryData, okTgt bool, inv *targetInventory) []ParityItem {
	var sd, td DockerData
	_ = json.Unmarshal(src.Data, &sd)
	if okTgt {
		_ = json.Unmarshal(tgt.Data, &td)
	}
	items := make([]ParityItem, 0)
	// Images (safe).
	for _, img := range sd.Images {
		key := "docker-image:" + img
		item := ParityItem{Category: "docker", ItemKey: key, SourceValue: img}
		if listHas(td.Images, img) {
			item.Status = ParitySame
			item.TargetValue = img
		} else {
			item.Status = ParityMissingOnTarget
			item.TargetValue = ""
		}
		item.ApplyLevel = ApplyLevelSafe
		item.Suggested = ActionApplyFromSource
		items = append(items, item)
	}
	// Volumes (guarded: data transfer).
	for _, v := range sd.Volumes {
		key := "docker-volume:" + v.Name
		item := ParityItem{Category: "docker", ItemKey: key, SourceValue: v.Name}
		if volumeExists(td.Volumes, v.Name) {
			item.Status = ParitySame
			item.TargetValue = v.Name
		} else {
			item.Status = ParityMissingOnTarget
			item.TargetValue = ""
		}
		item.ApplyLevel = ApplyLevelGuarded
		item.Suggested = ActionApplyFromSource
		items = append(items, item)
	}
	// Containers (guarded: recreate from def + image).
	for _, c := range sd.Containers {
		key := "docker-container:" + c.Name
		item := ParityItem{Category: "docker", ItemKey: key, SourceValue: c.Name}
		if containerExists(td.Containers, c.Name) {
			item.Status = ParitySame
			item.TargetValue = c.Name
		} else {
			item.Status = ParityMissingOnTarget
			item.TargetValue = ""
		}
		item.ApplyLevel = ApplyLevelGuarded
		item.Suggested = ActionApplyFromSource
		item.Deps = buildDeps("docker", key, inv, nil)
		items = append(items, item)
	}
	return items
}

func (e *ParityEngine) compareDatabase(src, tgt CategoryData, okTgt bool, inv *targetInventory) []ParityItem {
	var sd, td DatabaseCollectData
	_ = json.Unmarshal(src.Data, &sd)
	if okTgt {
		_ = json.Unmarshal(tgt.Data, &td)
	}
	items := make([]ParityItem, 0, len(sd.Databases))
	tgtSet := map[string]bool{}
	for _, db := range td.Databases {
		tgtSet[db.Name] = true
	}
	for _, db := range sd.Databases {
		key := "database:" + sd.Engine + ":" + db.Name
		item := ParityItem{Category: "database", ItemKey: key, SourceValue: db.Name}
		if tgtSet[db.Name] {
			item.Status = ParitySame
			item.TargetValue = db.Name
		} else {
			item.Status = ParityMissingOnTarget
			item.TargetValue = ""
		}
		item.ApplyLevel = ApplyLevelGuarded
		item.Suggested = ActionApplyFromSource
		item.Deps = buildDeps("database", key, inv, nil)
		items = append(items, item)
	}
	return items
}

// mergeInventory folds a live target collect into the dependency-satisfaction
// inventory used by buildDeps.
func mergeInventory(inv *targetInventory, category string, data CategoryData) {
	switch category {
	case "packages":
		var d PackagesData
		if json.Unmarshal(data.Data, &d) == nil {
			for _, p := range d.Packages {
				inv.packages[p] = true
			}
		}
	case "configs":
		var d ConfigsData
		if json.Unmarshal(data.Data, &d) == nil {
			for path := range d.Files {
				inv.configs["config:"+path] = true
			}
		}
	case "services":
		var d ServicesData
		if json.Unmarshal(data.Data, &d) == nil {
			for _, s := range d.Services {
				inv.services["service:"+s] = true
			}
		}
	case "docker":
		var d DockerData
		if json.Unmarshal(data.Data, &d) == nil {
			for _, img := range d.Images {
				inv.images["docker-image:"+img] = true
			}
			for _, v := range d.Volumes {
				inv.volumes["docker-volume:"+v.Name] = true
			}
		}
	case "database":
		// engine runtime presence is resolved via inv.packages/services in
		// engineRuntime; nothing extra to fold here.
	}
}

// --- small helpers ---

func listHas(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func volumeExists(list []DockerVolume, name string) bool {
	for _, v := range list {
		if v.Name == name {
			return true
		}
	}
	return false
}

func containerExists(list []DockerContainer, name string) bool {
	for _, c := range list {
		if c.Name == name {
			return true
		}
	}
	return false
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
