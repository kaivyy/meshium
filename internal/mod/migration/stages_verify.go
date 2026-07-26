package migration

import (
	"context"
	"fmt"
	"log"
	"strings"
)

// Health verification stage.
//
// Verification is per item and earns its level from evidence about that item:
// a package that is installed, a config whose hash matches, a unit that is
// active. Probes fail closed — one that cannot run proves nothing, so it
// proves failure. See verify_probe.go for the probes themselves.
type healthVerificationStage struct {
	repo PipelineRepo
}

func (s *healthVerificationStage) Name() PipelineStageName { return StageHealthVerification }

func (s *healthVerificationStage) Execute(ctx context.Context, pc *PipelineContext) error {
	pc.OnProgress(WSMessage{Step: "health_verification", Status: "progress", Value: "Verifying health on target..."})

	// Phase 6C-BE: load the applied item results so we can record honest
	// per-item verification evidence. Items NOT in the applied set (keep_target /
	// skip / review_manual / blocked) are deliberately left unresolved — they
	// were never applied, so verifying them would be a false green.
	applied, err := s.repo.GetItemResults(ctx, pc.MigrationID)
	if err != nil {
		log.Printf("warning: health_verification could not load item results: %v", err)
		applied = nil
	}
	appliedByCat := map[string][]ItemResult{}
	for _, r := range applied {
		if r.ExecutionState == ExecApplied {
			appliedByCat[r.Category] = append(appliedByCat[r.Category], r)
		}
	}

	// Basic health check: verify target is responsive. A responsive target
	// proves the INFRA layer (item reachable / applied state exists) — NOT
	// runtime or app health, which need deeper probes (below).
	output, _, _, err := pc.TargetSSH.ExecContext(ctx, "echo ok")
	if err != nil {
		s.failVerification(ctx, pc, appliedByCat, "target unreachable: "+err.Error())
		return fmt.Errorf("target health check failed: %w", err)
	}
	if output != "ok\n" {
		s.failVerification(ctx, pc, appliedByCat, "target health check returned unexpected output")
		return fmt.Errorf("target health check failed: unexpected output %q", output)
	}
	// Per-item probes. A responsive target proves only that the target is
	// responsive; each item must earn its level from evidence about ITSELF —
	// the package is installed, the file's hash matches, the unit is active.
	// Anything a probe cannot positively confirm is verify_failed, never green.
	verified, failed, unprobed := 0, 0, 0
	for cat, items := range appliedByCat {
		if cat == "docker" {
			continue // handled below with the container probe
		}
		verdicts := probeItems(ctx, pc.TargetSSH, cat, items)
		if len(verdicts) == 0 {
			// No probe exists for this category (e.g. database): leave the
			// items at whatever level they already hold rather than inventing
			// one. They are NOT counted as verified.
			unprobed += len(items)
			continue
		}
		for _, r := range items {
			v, ok := verdicts[r.ItemKey]
			if !ok {
				unprobed++
				continue
			}
			if v.OK {
				s.recordVerdict(ctx, pc, r, v.Level, string(v.Level), v.Detail, "")
				verified++
			} else {
				s.recordVerdict(ctx, pc, r, VerifyFailed, "", "", v.Detail)
				failed++
			}
		}
	}

	// Docker: a container reporting "Up" is a RUNTIME attestation — but only
	// for THAT container. This used to pass the whole appliedByCat map into
	// attestVerification, so one running container upgraded every applied item
	// in every category to runtime_verified, including packages that were
	// never installed.
	if dockerItems := appliedByCat["docker"]; len(dockerItems) > 0 {
		dockerOutput, _, _, err := pc.TargetSSH.ExecContext(ctx, "docker ps --format '{{.Names}} {{.Status}}' 2>/dev/null")
		if err == nil && strings.TrimSpace(dockerOutput) != "" {
			pc.OnProgress(WSMessage{Step: "health_verification", Status: "progress", Value: fmt.Sprintf("Docker containers: %s", dockerOutput)})
			up := map[string]bool{}
			for _, line := range strings.Split(strings.TrimSpace(dockerOutput), "\n") {
				fields := strings.Fields(line)
				if len(fields) >= 2 && strings.HasPrefix(fields[1], "Up") {
					up[fields[0]] = true
				}
			}
			for _, r := range dockerItems {
				if up[strings.TrimPrefix(r.ItemKey, "docker-container:")] {
					s.recordVerdict(ctx, pc, r, VerifyRuntime, "runtime", "container reported Up", "")
					verified++
				} else {
					s.recordVerdict(ctx, pc, r, VerifyFailed, "", "", "container is not running on the target")
					failed++
				}
			}
		} else {
			for _, r := range dockerItems {
				s.recordVerdict(ctx, pc, r, VerifyFailed, "", "", "could not list containers on the target")
				failed++
			}
		}
	}

	summary := fmt.Sprintf("Verified %d item(s); %d failed verification", verified, failed)
	if unprobed > 0 {
		summary += fmt.Sprintf("; %d left unverified (no probe for that category)", unprobed)
	}
	if failed > 0 {
		// Applied but not confirmed on the target: surface it as a warning and
		// let the operator decide. The per-item rows carry the detail.
		pc.OnProgress(WSMessage{Step: "health_verification", Status: "warning", Value: summary})
		return nil
	}
	pc.OnProgress(WSMessage{Step: "health_verification", Status: "success", Value: summary})
	return nil
}

// recordVerdict persists one item's verification outcome.
func (s *healthVerificationStage) recordVerdict(ctx context.Context, pc *PipelineContext, r ItemResult, state VerificationState, level, evidence, notes string) {
	r.VerificationState = state
	r.VerificationLevel = level
	r.VerifyEvidence = evidence
	r.VerifyNotes = notes
	if err := s.repo.UpsertItemResult(ctx, pc.MigrationID, r); err != nil {
		log.Printf("warning: failed to record verification for %s: %v", r.ItemKey, err)
	}
}

// failVerification marks applied items verify_failed with the given reason, so a
// broken target is never reported green.
func (s *healthVerificationStage) failVerification(ctx context.Context, pc *PipelineContext, byCat map[string][]ItemResult, reason string) {
	for _, items := range byCat {
		for _, r := range items {
			r.VerificationState = VerifyFailed
			r.VerificationLevel = ""
			r.VerifyEvidence = ""
			r.VerifyNotes = reason
			if uerr := s.repo.UpsertItemResult(ctx, pc.MigrationID, r); uerr != nil {
				log.Printf("warning: failed to record verification failure for %s: %v", r.ItemKey, uerr)
			}
		}
	}
}

func (s *healthVerificationStage) Rollback(ctx context.Context, pc *PipelineContext) error {
	return nil
}

// preCutoverValidationStage performs final checks before traffic switch.
