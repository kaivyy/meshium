package migration

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"time"
)

// The apply stage and its per-item machinery.
//
// This is where operator selections become writes on the target. The item
// helpers (filterCategoryToApplySet, itemKeysFor, applyItemPlan) exist so a
// coarse per-category Applier only ever receives the items the operator
// actually chose — the "catMixed applied the whole category" class of bug.
func filterCategoryToApplySet(category string, raw json.RawMessage, applySet map[string]bool) CategoryData {
	keep := func(key string) bool {
		if len(applySet) == 0 {
			return true // no explicit selection ⇒ apply whole category (legacy default)
		}
		return applySet[key]
	}
	switch category {
	case "packages":
		var d PackagesData
		if err := json.Unmarshal(raw, &d); err != nil {
			return CategoryData{Type: "packages", Data: raw}
		}
		out := make([]string, 0, len(d.Packages))
		for _, p := range d.Packages {
			if keep("package:" + p) {
				out = append(out, p)
			}
		}
		d.Packages = out
		b, _ := json.Marshal(d)
		return CategoryData{Type: "packages", Data: b}
	case "configs":
		var d ConfigsData
		if err := json.Unmarshal(raw, &d); err != nil {
			return CategoryData{Type: "configs", Data: raw}
		}
		files := map[string][]byte{}
		for path, content := range d.Files {
			if keep("config:" + path) {
				files[path] = content
			}
		}
		d.Files = files
		b, _ := json.Marshal(d)
		return CategoryData{Type: "configs", Data: b}
	case "services":
		var d ServicesData
		if err := json.Unmarshal(raw, &d); err != nil {
			return CategoryData{Type: "services", Data: raw}
		}
		out := make([]string, 0, len(d.Services))
		for _, s := range d.Services {
			if keep("service:" + s) {
				out = append(out, s)
			}
		}
		d.Services = out
		b, _ := json.Marshal(d)
		return CategoryData{Type: "services", Data: b}
	case "users":
		var d UsersData
		if err := json.Unmarshal(raw, &d); err != nil {
			return CategoryData{Type: "users", Data: raw}
		}
		out := make([]UserData, 0, len(d.Users))
		for _, u := range d.Users {
			if keep("user:" + u.Name) {
				out = append(out, u)
			}
		}
		d.Users = out
		b, _ := json.Marshal(d)
		return CategoryData{Type: "users", Data: b}
	case "docker":
		var d DockerData
		if err := json.Unmarshal(raw, &d); err != nil {
			return CategoryData{Type: "docker", Data: raw}
		}
		imgs := make([]string, 0, len(d.Images))
		for _, img := range d.Images {
			if keep("docker-image:" + img) {
				imgs = append(imgs, img)
			}
		}
		d.Images = imgs
		vols := make([]DockerVolume, 0, len(d.Volumes))
		for _, v := range d.Volumes {
			if keep("docker-volume:" + v.Name) {
				vols = append(vols, v)
			}
		}
		d.Volumes = vols
		cts := make([]DockerContainer, 0, len(d.Containers))
		for _, c := range d.Containers {
			if keep("docker-container:" + c.Name) {
				cts = append(cts, c)
			}
		}
		d.Containers = cts
		comps := make([]DockerComposeFile, 0, len(d.ComposeFiles))
		for _, c := range d.ComposeFiles {
			if keep("docker-compose:" + c.Path) {
				comps = append(comps, c)
			}
		}
		d.ComposeFiles = comps
		b, _ := json.Marshal(d)
		return CategoryData{Type: "docker", Data: b}
	case "database":
		var d DatabaseCollectData
		if err := json.Unmarshal(raw, &d); err != nil {
			return CategoryData{Type: "database", Data: raw}
		}
		dbs := make([]DBCatalogEntry, 0, len(d.Databases))
		for _, db := range d.Databases {
			if keep("database:" + d.Engine + ":" + db.Name) {
				dbs = append(dbs, db)
			}
		}
		d.Databases = dbs
		b, _ := json.Marshal(d)
		return CategoryData{Type: "database", Data: b}
	default:
		return CategoryData{Type: category, Data: raw}
	}
}

// itemKeysFor returns the parity ItemKeys present in a collected CategoryData,
// mirroring the key prefixes in parity_engine.go compareCategory. Used to
// enumerate per-item decisions + hard-block at apply time.
// itemKeysForStep derives item keys from a stored migration step.
//
// migration_steps.data holds the collector's ENVELOPE —
// {"type":…,"meta":…,"data":{…}} — not the payload itself. Callers used to
// pass the raw envelope straight into itemKeysFor as if it were the payload;
// json.Unmarshal is lenient about unknown fields, so decoding an envelope into
// PackagesData did not error, it just yielded a struct with no packages. Zero
// item keys meant an empty apply-set, which the apply stage reads as "the
// operator deselected everything" — so a full migration skipped every category
// and still reported success. Unwrap once, here, so no caller can repeat it.
func itemKeysForStep(category, stepData string) []string {
	if stepData == "" {
		return nil
	}
	var envelope CategoryData
	if err := json.Unmarshal([]byte(stepData), &envelope); err != nil {
		return nil
	}
	return itemKeysFor(category, envelope)
}

func itemKeysFor(category string, data CategoryData) []string {
	switch category {
	case "packages":
		var d PackagesData
		if json.Unmarshal(data.Data, &d) != nil {
			return nil
		}
		keys := make([]string, 0, len(d.Packages))
		for _, p := range d.Packages {
			keys = append(keys, "package:"+p)
		}
		return keys
	case "configs":
		var d ConfigsData
		if json.Unmarshal(data.Data, &d) != nil {
			return nil
		}
		keys := make([]string, 0, len(d.Files))
		for path := range d.Files {
			keys = append(keys, "config:"+path)
		}
		return keys
	case "services":
		var d ServicesData
		if json.Unmarshal(data.Data, &d) != nil {
			return nil
		}
		keys := make([]string, 0, len(d.Services))
		for _, s := range d.Services {
			keys = append(keys, "service:"+s)
		}
		return keys
	case "users":
		var d UsersData
		if json.Unmarshal(data.Data, &d) != nil {
			return nil
		}
		keys := make([]string, 0, len(d.Users))
		for _, u := range d.Users {
			keys = append(keys, "user:"+u.Name)
		}
		return keys
	case "docker":
		var d DockerData
		if json.Unmarshal(data.Data, &d) != nil {
			return nil
		}
		keys := make([]string, 0, len(d.Images)+len(d.Volumes)+len(d.Containers)+len(d.ComposeFiles))
		for _, img := range d.Images {
			keys = append(keys, "docker-image:"+img)
		}
		for _, v := range d.Volumes {
			keys = append(keys, "docker-volume:"+v.Name)
		}
		for _, c := range d.Containers {
			keys = append(keys, "docker-container:"+c.Name)
		}
		for _, c := range d.ComposeFiles {
			keys = append(keys, "docker-compose:"+c.Path)
		}
		return keys
	case "database":
		var d DatabaseCollectData
		if json.Unmarshal(data.Data, &d) != nil {
			return nil
		}
		keys := make([]string, 0, len(d.Databases))
		for _, db := range d.Databases {
			keys = append(keys, "database:"+d.Engine+":"+db.Name)
		}
		return keys
	default:
		return nil
	}
}

// buildTargetInventory does a live target collect for the migration's categories so
// hard-dependency satisfaction can be recomputed at apply time (defense-in-depth:
// the FE already blocks apply decisions on hard deps, but selections may also
// arrive via API/bulk, so the backend must re-verify). Mirrors the target
// loop in ParityEngine.ComputeParity.
func buildTargetInventory(ctx context.Context, pc *PipelineContext) *targetInventory {
	inv := newTargetInventory()
	if pc.TargetSSH == nil || pc.Registry == nil || pc.Migration == nil {
		return inv
	}
	for _, catName := range pc.Migration.Categories {
		mod, ok := pc.Registry.Get(catName)
		if !ok {
			continue
		}
		td, err := mod.Collector.Collect(ctx, pc.TargetSSH)
		if err != nil {
			continue
		}
		mergeInventory(inv, catName, td)
	}
	return inv
}

// itemHardBlocked recomputes whether one item's hard dependencies are unsatisfied
// against the live target inventory (parity_engine.go hasUnsatisfiedHardDep
// operates on a ParityItem; here we derive deps directly so no parity re-run is
// needed in the apply stage).
func itemHardBlocked(category, itemKey string, inv *targetInventory) bool {
	for _, d := range depsFor(category, itemKey, inv) {
		if d.Kind == DepHard && !d.Satisfied {
			return true
		}
	}
	return false
}

// applyItemPlan classifies every item in a category against the operator's
// per-item decisions + hard-dep enforcement, producing:
//   - applySet: keys to actually send to the (coarse) Applier after filtering
//   - results:  one ItemResult row per item (blocked / skipped / not_applicable
//     / pending). Applied rows are finalized to ExecApplied by the caller after a
//     successful Applier.Apply.
//
// Honest rules (contract §F/G): a hard-blocked item is NEVER in applySet and
// is recorded ExecBlocked even if its decision was apply_from_source; keep_target
// / skip → ExecSkipped; review_manual → ExecNotApplicable.
func applyItemPlan(ctx context.Context, pc *PipelineContext, step MigrationStepRecord,
	decisions map[string]ParityAction, inv *targetInventory) (applySet map[string]bool, results []ItemResult) {

	applySet = map[string]bool{}
	now := time.Now().UTC().Format(time.RFC3339)
	for _, key := range itemKeysForStep(step.Category, step.Data) {
		action := decisions[key] // "" ⇒ undecided
		hard := itemHardBlocked(step.Category, key, inv)
		rec := ItemResult{
			MigrationID:     pc.MigrationID,
			ItemKey:         key,
			Category:        step.Category,
			LastExecutionAt: now,
		}
		switch {
		case hard:
			// Unsatisfied hard dep → blocked, never applied (spec §G).
			rec.ExecutionState = ExecBlocked
			rec.ExecutionNotes = "hard dependency unsatisfied at apply time"
		case action == ActionKeepTarget || action == ActionSkip:
			rec.ExecutionState = ExecSkipped
		case action == ActionReviewManual:
			rec.ExecutionState = ExecNotApplicable
		case action == ActionApplyFromSource || action == "":
			// apply_from_source (or undecided ⇒ legacy apply-whole default).
			applySet[key] = true
			rec.ExecutionState = ExecPending
		default:
			applySet[key] = true
			rec.ExecutionState = ExecPending
		}
		results = append(results, rec)
	}
	return applySet, results
}

// initialSyncStage applies the data collected during the collect phase to the
// target by replaying each category's Applier.Apply. Despite the stage name,
// this is NOT a file-level rsync transfer: the SyncEngine (InitialSync /
// DeltaSync) is not driven by the live pipeline (see the NOTE in Execute), so
// only what each category collector captured is reproduced on the target.
type initialSyncStage struct {
	repo PipelineRepo
}

func (s *initialSyncStage) Name() PipelineStageName { return StageInitialSync }

func (s *initialSyncStage) Execute(ctx context.Context, pc *PipelineContext) error {
	pc.OnProgress(WSMessage{Step: "initial_sync", Status: "progress", Value: "Applying collected data to target..."})

	// NOTE: this stage does not perform a file-level rsync data transfer. The
	// SyncEngine (InitialSync/DeltaSync in sync.go) is constructed but has no
	// live pipeline caller, so syncConfigFromPipelineContext(pc) is not built or
	// consumed here. Instead the stage replays each collected category via
	// mod.Applier.Apply below. If/when this stage is wired to the SyncEngine,
	// pass syncConfigFromPipelineContext(pc) into it and update the progress
	// messages to reflect the real transfer.

	// Apply collected data to target (this is the "initial sync" for file-based categories)
	steps, err := pc.JobRepo.GetSteps(pc.MigrationID)
	if err != nil {
		return fmt.Errorf("failed to load migration steps: %w", err)
	}

	categories := pc.Migration.Categories
	_ = categories

	appliedOrder := make([]string, 0)
	skippedSet := make(map[string]struct{})

	// Phase 6B4: load operator selections once, bucket by category. Category
	// appliers are granular at the CATEGORY level (a packages applier installs
	// the whole list, not one package), so the category-level decision is derived
	// from the items the operator chose:
	//   - no selections for the category → apply (backward-compatible default)
	//   - every item keep_target/skip     → skip the whole category
	// Phase 6C-BE: per-item decisions + a live target inventory so hard
	// dependencies can be re-verified at apply time (defense-in-depth). The
	// coarse category Applier applies whatever we hand it, so we filter the
	// collected data to the apply-set and persist one honest ItemResult row
	// per item (blocked / skipped / not_applicable / applied / failed).
	decisions := loadItemDecisions(ctx, pc)
	inv := buildTargetInventory(ctx, pc)

	for _, step := range steps {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Only apply freshly-collected steps. Applied steps have their status
		// flipped to "applied" (UpdateStepStatus below), so this also skips
		// already-applied categories on a stage retry/resume — which for the
		// database category would mean re-dumping/re-restoring. Idempotent
		// restore flags (--clean/--drop) are defense-in-depth.
		if step.Action != "collect" || step.Status != StepStatusCompleted {
			continue
		}

		applySet, itemResults := applyItemPlan(ctx, pc, step, decisions, inv)

		// Whole category skipped when NOTHING is in the apply-set (every item
		// keep_target/skip/blocked/review_manual) — honest, no silent apply.
		if len(applySet) == 0 {
			// Persist the per-item results (blocked/skipped/not_applicable).
			for _, r := range itemResults {
				if err := pc.JobRepo.UpsertItemResult(ctx, pc.MigrationID, r); err != nil {
					log.Printf("warning: failed to persist item result for %s/%s: %v", step.Category, r.ItemKey, err)
				}
			}
			if err := pc.JobRepo.UpdateStepStatus(step.ID, StepStatusSkipped, "no items selected to apply (all keep_target/skip/blocked/review_manual)"); err != nil {
				log.Printf("warning: failed to persist skipped step for %s: %v", step.Category, err)
			}
			skippedSet[step.Category] = struct{}{}
			pc.OnProgress(WSMessage{Step: "initial_sync", Status: "warning", Value: fmt.Sprintf("Skipped %s: no items selected to apply", step.Category)})
			continue
		}

		// Get the category module
		mod, ok := getRegistryFromContext(pc).Get(step.Category)
		if !ok {
			skippedSet[step.Category] = struct{}{}
			pc.OnProgress(WSMessage{Step: "initial_sync", Status: "warning", Value: fmt.Sprintf("Skipping %s: category not registered", step.Category)})
			continue
		}

		// Parse the collected data
		var data CategoryData
		if err := json.Unmarshal([]byte(step.Data), &data); err != nil {
			skippedSet[step.Category] = struct{}{}
			pc.OnProgress(WSMessage{Step: "initial_sync", Status: "warning", Value: fmt.Sprintf("Skipping %s: invalid step data: %v", step.Category, err)})
			continue
		}

		// The database applier needs the source SSH (for the dump half) and the
		// decrypted DB config, neither of which the Apply signature carries.
		// Inject them here, mirroring the planner's configs/database
		// special-case (type-assert + configure). ponytail: interface stays
		// stable at the cost of a type-assertion in this stage.
		if dba, ok := mod.Applier.(*DatabaseApplier); ok {
			dba.SetSourceSSH(pc.SourceSSH)
			if pc.Config != nil {
				dba.SetConfig(pc.Config.DatabaseConfig)
			}
			// Resumable DB dump/restore: give the applier the checkpoint store +
			// owning migration so a killed multi-GB transfer resumes from the last
			// byte offset instead of restarting (Part 5). nil store ⇒ one-shot.
			dba.SetCheckpointStore(pc.MigrationID, pc.CheckpointStore)
		}

		// Phase 6C-BE: hand the coarse Applier ONLY the apply-set items.
		// Non-applied items (keep_target/skip/blocked/review_manual) are
		// excluded here so they are never silently applied (fixes the old
		// catMixed "applied whole category" bug).
		filtered := filterCategoryToApplySet(step.Category, data.Data, applySet)

		pc.OnProgress(WSMessage{Step: "initial_sync", Status: "progress", Value: fmt.Sprintf("Applying %s (%d of %d items)...", step.Category, len(applySet), len(itemResults))})

		// Apply
		err := mod.Applier.Apply(ctx, pc.TargetSSH, filtered, func(msg WSMessage) {
			msg.Step = "initial_sync:" + step.Category + ":" + msg.Step
			pc.OnProgress(msg)
		})
		if err != nil {
			// Mark the apply-set items failed; persist all per-item rows.
			for i := range itemResults {
				if applySet[itemResults[i].ItemKey] {
					itemResults[i].ExecutionState = ExecFailed
					itemResults[i].ExecutionNotes = "apply failed: " + err.Error()
				}
				if uerr := pc.JobRepo.UpsertItemResult(ctx, pc.MigrationID, itemResults[i]); uerr != nil {
					log.Printf("warning: failed to persist item result for %s/%s: %v", step.Category, itemResults[i].ItemKey, uerr)
				}
			}
			// Roll back already-applied categories
			s.rollbackApplied(ctx, pc, appliedOrder)
			return fmt.Errorf("apply %s failed: %w", step.Category, err)
		}

		// Success: finalize apply-set rows to ExecApplied; persist all rows.
		for i := range itemResults {
			if applySet[itemResults[i].ItemKey] {
				itemResults[i].ExecutionState = ExecApplied
				itemResults[i].LastExecutionAt = time.Now().UTC().Format(time.RFC3339)
				itemResults[i].StepRefs = []int{step.ID}
				// Per-item restore point: without this, rollback can only reason
				// per category ("something here was applied, restore all of it").
				applyBackupRef(&itemResults[i], pc.BackupRefs)
			}
			if uerr := pc.JobRepo.UpsertItemResult(ctx, pc.MigrationID, itemResults[i]); uerr != nil {
				log.Printf("warning: failed to persist item result for %s/%s: %v", step.Category, itemResults[i].ItemKey, uerr)
			}
		}

		// Checkpoint
		if err := pc.JobRepo.UpdateStepStatus(step.ID, StepStatusApplied, ""); err != nil {
			log.Printf("warning: failed to persist applied step for %s: %v", step.Category, err)
			pc.OnProgress(WSMessage{Step: "initial_sync", Status: "warning", Value: fmt.Sprintf("Failed to persist applied state for %s: %v", step.Category, err)})
		}
		appliedOrder = append(appliedOrder, step.Category)

		pc.OnProgress(WSMessage{Step: "initial_sync", Status: "success", Value: fmt.Sprintf("Applied %s", step.Category)})
	}

	if len(skippedSet) > 0 {
		skippedCategories := make([]string, 0, len(skippedSet))
		for category := range skippedSet {
			skippedCategories = append(skippedCategories, category)
		}
		sort.Strings(skippedCategories)
		if len(appliedOrder) > 0 {
			s.rollbackApplied(ctx, pc, appliedOrder)
		}
		return fmt.Errorf("initial sync skipped %d categories: %v", len(skippedCategories), skippedCategories)
	}

	pc.OnProgress(WSMessage{Step: "initial_sync", Status: "success", Value: "Collected data applied to target"})
	return nil
}

// --- Phase 6C-BE: per-item selective apply ---

// loadItemDecisions reads the operator's per-item selections for the migration
// and returns them keyed by ItemKey. An absent key means "undecided" → the
// apply stage treats it as apply (legacy default), but only after hard-dep
// re-verification (applyItemPlan).
func loadItemDecisions(ctx context.Context, pc *PipelineContext) map[string]ParityAction {
	out := map[string]ParityAction{}
	decisions, err := pc.JobRepo.GetSelections(ctx, pc.MigrationID)
	if err != nil {
		// No selections persisted (legacy migration) → every item applies.
		return out
	}
	for _, d := range decisions {
		out[d.ItemKey] = ParityAction(d.Action)
	}
	return out
}

func syncConfigFromPipelineContext(pc *PipelineContext) SyncConfig {
	cfg := SyncConfig{}
	if pc == nil {
		return cfg
	}

	cfg.MigrationID = pc.MigrationID
	if pc.TargetServer != nil {
		cfg.TargetHost = pc.TargetServer.Host
		cfg.TargetPort = pc.TargetServer.Port
		cfg.TargetUser = pc.TargetServer.Username
	}
	// Honor the per-migration transfer controls declared on MigrationConfig so
	// they actually reach rsync (--bwlimit / --parallel). Previously this
	// builder ignored pc.Config, so the limits were config-only and never
	// applied when the SyncEngine is wired in.
	if pc.Config != nil {
		cfg.BandwidthLimit = pc.Config.BandwidthLimit
		cfg.ParallelTransfers = pc.Config.ParallelTransfers
	}
	return cfg
}

func (s *initialSyncStage) Rollback(ctx context.Context, pc *PipelineContext) error {
	// Roll back applied categories using stored backups
	backups, err := pc.JobRepo.GetBackups(pc.MigrationID)
	if err != nil {
		return fmt.Errorf("failed to load backups: %w", err)
	}

	// Get applied categories
	appliedCats, err := pc.JobRepo.GetAppliedCategories(pc.MigrationID)
	if err != nil {
		return fmt.Errorf("failed to load applied categories: %w", err)
	}
	appliedSet := make(map[string]bool, len(appliedCats))
	for _, c := range appliedCats {
		appliedSet[c] = true
	}

	// Roll back in reverse order
	for i := len(backups) - 1; i >= 0; i-- {
		backup := backups[i]
		if !appliedSet[backup.Category] {
			continue
		}

		mod, ok := getRegistryFromContext(pc).Get(backup.Category)
		if !ok {
			continue
		}

		var backupData BackupData
		if err := json.Unmarshal([]byte(backup.Data), &backupData); err != nil {
			continue
		}

		pc.OnProgress(WSMessage{Step: "rollback", Status: "progress", Value: fmt.Sprintf("Rolling back %s...", backup.Category)})
		if err := mod.Applier.Rollback(ctx, pc.TargetSSH, backupData); err != nil {
			pc.OnProgress(WSMessage{Step: "rollback", Status: "warning", Value: fmt.Sprintf("Rollback warning for %s: %v", backup.Category, err)})
		}
	}

	return nil
}

func (s *initialSyncStage) rollbackApplied(ctx context.Context, pc *PipelineContext, appliedOrder []string) {
	// Phase 6C-BE: honest rollback scope. The coarse Applier.Rollback is
	// per-category, but the ITEM-LEVEL applied subset is the only thing that
	// was actually written to the target. keep_target / skip / review_manual /
	// blocked / failed items were never applied, so they are excluded by
	// construction (they have no ExecApplied row). We roll back ONLY the
	// categories that contain at least one ExecApplied item, and record the
	// rolled-back items as ExecRolledBack.
	appliedCats := map[string]bool{}
	if itemResults, err := pc.JobRepo.GetItemResults(ctx, pc.MigrationID); err == nil {
		for _, r := range itemResults {
			if r.ExecutionState == ExecApplied {
				appliedCats[r.Category] = true
			}
		}
		// Report the real boundary from per-item restore evidence before doing
		// anything: an applied item with no backup_ref will NOT be undone, and
		// the operator must see that rather than infer "rollback ran, so we are
		// back where we started".
		scope := DescribeRollbackScope(itemResults)
		status := "progress"
		if !scope.FullyCovered {
			status = "warning"
		}
		pc.OnProgress(WSMessage{Step: "rollback", Status: status, Value: scope.Summary()})
	}

	// Load backups from DB
	backups, err := pc.JobRepo.GetBackups(pc.MigrationID)
	if err != nil {
		return
	}
	backupMap := make(map[string]BackupData)
	for _, b := range backups {
		var bd BackupData
		if json.Unmarshal([]byte(b.Data), &bd) == nil {
			backupMap[b.Category] = bd
		}
	}

	for i := len(appliedOrder) - 1; i >= 0; i-- {
		catName := appliedOrder[i]
		// Only roll back categories that actually had applied items.
		if !appliedCats[catName] {
			continue
		}
		backup, ok := backupMap[catName]
		if !ok {
			continue
		}
		mod, ok := getRegistryFromContext(pc).Get(catName)
		if !ok {
			continue
		}
		pc.OnProgress(WSMessage{Step: "rollback", Status: "progress", Value: fmt.Sprintf("Rolling back %s...", catName)})
		if err := mod.Applier.Rollback(ctx, pc.TargetSSH, backup); err != nil {
			pc.OnProgress(WSMessage{Step: "rollback", Status: "warning", Value: fmt.Sprintf("Rollback warning: %v", err)})
		} else {
			// Mark applied items in this category as rolled back.
			if itemResults, gerr := pc.JobRepo.GetItemResults(ctx, pc.MigrationID); gerr == nil {
				for _, r := range itemResults {
					if r.Category == catName && r.ExecutionState == ExecApplied {
						r.ExecutionState = ExecRolledBack
						if uerr := pc.JobRepo.UpsertItemResult(ctx, pc.MigrationID, r); uerr != nil {
							log.Printf("warning: failed to record rollback for %s: %v", r.ItemKey, uerr)
						}
					}
				}
			}
		}
	}
}

// liveReplicationStage sets up database/Redis replication.
