package migration

import (
	"context"
	"fmt"
	"strings"

	"meshium/internal/shared"
)

// ItemVerdict is one item's post-apply probe result.
//
// OK means the probe positively observed the expected state on the target.
// Anything else — probe failed, item absent, hash mismatch — is NOT OK, and
// the item must not be attested as verified. Fail-closed is the rule: a probe
// that cannot run proves nothing, so it proves failure.
type ItemVerdict struct {
	OK     bool
	Level  VerificationState // level EARNED by this probe (infra or runtime)
	Detail string            // evidence when OK, reason when not
}

// probeItems runs one batched probe per category against the target and
// returns a verdict per item key.
//
// Before this existed, health_verification proved only that the target
// answered `echo ok` and then attested every applied item as infra_verified —
// "verified" carried almost no information beyond "applied". Each category now
// earns its own level from real evidence:
//
//	packages → the package is installed          (infra)
//	configs  → the file's sha256 matches         (infra)
//	services → the unit reports active           (runtime)
//	users    → the account resolves              (infra)
//
// Categories without a probe (database, docker) return no verdicts rather than
// a fabricated one; the caller leaves those items at their existing level.
// Probes are batched into a single command per category so verification cost
// does not scale with item count.
func probeItems(ctx context.Context, ssh SSHExecuter, category string, items []ItemResult) map[string]ItemVerdict {
	return probeItemsMode(ctx, ssh, category, items, probeVerify)
}

// probeMode distinguishes the two different questions the probes answer.
//
//	probePresence — "did the change reach the target?"  (reconciliation, run
//	                right after apply, decides what rollback must undo)
//	probeVerify   — "is the target's state correct?"    (verification, decides
//	                whether the item can be called verified)
//
// They differ for configs: immediately after upload there is no recorded
// expected hash, so a hash comparison reports every file as unverified. Using
// the verify probe for reconciliation marked all 2051 uploaded config files
// `failed` even though every one had landed — the record contradicted the
// machine in the opposite direction to the bug reconciliation exists to fix.
type probeMode int

const (
	probeVerify probeMode = iota
	probePresence
)

// probeItemsPresence answers "did it land", for reconciliation after apply.
func probeItemsPresence(ctx context.Context, ssh SSHExecuter, category string, items []ItemResult) map[string]ItemVerdict {
	return probeItemsMode(ctx, ssh, category, items, probePresence)
}

func probeItemsMode(ctx context.Context, ssh SSHExecuter, category string, items []ItemResult, mode probeMode) map[string]ItemVerdict {
	switch category {
	case "packages":
		return probePackages(ctx, ssh, items)
	case "configs":
		if mode == probePresence {
			return probeConfigsPresence(ctx, ssh, items)
		}
		return probeConfigs(ctx, ssh, items)
	case "services":
		return probeServices(ctx, ssh, items)
	case "users":
		return probeUsers(ctx, ssh, items)
	default:
		return map[string]ItemVerdict{}
	}
}

// itemName strips the "<kind>:" prefix from an item key.
func itemName(key string) string {
	if i := strings.IndexByte(key, ':'); i >= 0 {
		return key[i+1:]
	}
	return key
}

// failAll marks every item not-OK with the same reason. Used when the probe
// command itself could not run — proving nothing must never read as success.
func failAll(items []ItemResult, reason string) map[string]ItemVerdict {
	out := make(map[string]ItemVerdict, len(items))
	for _, r := range items {
		out[r.ItemKey] = ItemVerdict{OK: false, Detail: reason}
	}
	return out
}

// probePackages verifies each package is installed on the target, using the
// target's own package manager (never the source's).
func probePackages(ctx context.Context, ssh SSHExecuter, items []ItemResult) map[string]ItemVerdict {
	info, err := DetectDistro(ctx, ssh)
	if err != nil {
		return failAll(items, "could not detect target distro: "+err.Error())
	}
	adapter, err := GetAdapter(info)
	if err != nil {
		return failAll(items, err.Error())
	}

	stdout, _, exit, err := ssh.ExecContext(ctx, adapter.ListPackages())
	if err != nil || exit != 0 {
		return failAll(items, fmt.Sprintf("package listing on target failed (exit %d)", exit))
	}
	installed := map[string]bool{}
	for _, p := range parsePackageList(stdout, adapter.PackageManager()) {
		installed[p] = true
		// apt reports multi-arch packages as "name:arch"; index the bare name
		// too so an item keyed "package:zlib1g" still matches "zlib1g:amd64".
		if i := strings.IndexByte(p, ':'); i > 0 {
			installed[p[:i]] = true
		}
	}

	out := make(map[string]ItemVerdict, len(items))
	for _, r := range items {
		name := itemName(r.ItemKey)
		if installed[name] {
			out[r.ItemKey] = ItemVerdict{OK: true, Level: VerifyInfra, Detail: "package installed on target"}
		} else {
			out[r.ItemKey] = ItemVerdict{OK: false, Detail: "package not installed on target"}
		}
	}
	return out
}

// probeConfigs compares each file's sha256 on the target against the hash the
// applier recorded. A file that is present but different is NOT verified —
// that is precisely the case a mere existence check would wave through.
func probeConfigs(ctx context.Context, ssh SSHExecuter, items []ItemResult) map[string]ItemVerdict {
	paths := make([]string, 0, len(items))
	for _, r := range items {
		paths = append(paths, itemName(r.ItemKey))
	}
	if len(paths) == 0 {
		return map[string]ItemVerdict{}
	}

	sums := map[string]string{}
	const batch = 200
	for start := 0; start < len(paths); start += batch {
		end := start + batch
		if end > len(paths) {
			end = len(paths)
		}
		quoted := make([]string, 0, end-start)
		for _, p := range paths[start:end] {
			quoted = append(quoted, shared.ShellQuote(p))
		}
		// sha256sum exits non-zero when ANY path is missing, which is normal
		// here — the per-line output for the paths that do exist is still
		// valid, so parse it and let missing paths fall through as not-OK.
		stdout, _, _, err := ssh.ExecContext(ctx, "sha256sum "+strings.Join(quoted, " ")+" 2>/dev/null")
		if err != nil {
			continue
		}
		for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
			fields := strings.SplitN(strings.TrimSpace(line), "  ", 2)
			if len(fields) != 2 {
				continue
			}
			sums[strings.TrimSpace(fields[1])] = strings.TrimSpace(fields[0])
		}
	}

	out := make(map[string]ItemVerdict, len(items))
	for _, r := range items {
		path := itemName(r.ItemKey)
		got, present := sums[path]
		want := strings.TrimPrefix(r.VerifyEvidence, "sha256:")
		switch {
		case !present:
			out[r.ItemKey] = ItemVerdict{OK: false, Detail: "file missing on target"}
		case want == "" || !strings.HasPrefix(r.VerifyEvidence, "sha256:"):
			// No expected hash recorded (legacy row): existence is all we can
			// honestly claim, and existence alone is not verification.
			out[r.ItemKey] = ItemVerdict{OK: false, Detail: "file present but no expected hash recorded"}
		case got == want:
			out[r.ItemKey] = ItemVerdict{OK: true, Level: VerifyInfra, Detail: "sha256 matches source"}
		default:
			out[r.ItemKey] = ItemVerdict{OK: false, Detail: "content differs from source (sha256 mismatch)"}
		}
	}
	return out
}

// probeConfigsPresence checks only that each file exists on the target.
//
// This is the reconciliation question — the file was just written, so there is
// no recorded expected hash to compare against yet. Verification (probeConfigs)
// is the one that must compare content; conflating the two marked every
// successfully uploaded file as failed.
func probeConfigsPresence(ctx context.Context, ssh SSHExecuter, items []ItemResult) map[string]ItemVerdict {
	if len(items) == 0 {
		return map[string]ItemVerdict{}
	}
	present := map[string]bool{}
	const batch = 200
	probed := false
	for start := 0; start < len(items); start += batch {
		end := start + batch
		if end > len(items) {
			end = len(items)
		}
		quoted := make([]string, 0, end-start)
		for _, r := range items[start:end] {
			quoted = append(quoted, shared.ShellQuote(itemName(r.ItemKey)))
		}
		// `ls -d` prints the paths that exist and errors on the rest; a
		// non-zero exit is expected when any path is missing.
		out, _, _, err := ssh.ExecContext(ctx, "ls -d "+strings.Join(quoted, " ")+" 2>/dev/null")
		if err != nil {
			continue
		}
		probed = true
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			if p := strings.TrimSpace(line); p != "" {
				present[p] = true
			}
		}
	}
	if !probed {
		return failAll(items, "could not list files on the target")
	}

	out := make(map[string]ItemVerdict, len(items))
	for _, r := range items {
		if present[itemName(r.ItemKey)] {
			out[r.ItemKey] = ItemVerdict{OK: true, Level: VerifyInfra, Detail: "file present on target"}
		} else {
			out[r.ItemKey] = ItemVerdict{OK: false, Detail: "file missing on target after apply"}
		}
	}
	return out
}

// probeServices checks each unit is actually active. This is the one probe
// that earns RUNTIME: a running service is qualitatively more than a file on
// disk.
func probeServices(ctx context.Context, ssh SSHExecuter, items []ItemResult) map[string]ItemVerdict {
	if len(items) == 0 {
		return map[string]ItemVerdict{}
	}
	names := make([]string, 0, len(items))
	for _, r := range items {
		names = append(names, shared.ShellQuote(itemName(r.ItemKey)))
	}
	// `systemctl is-active a b c` prints one state per unit, in order, and
	// exits non-zero when any is inactive — expected, so the exit code is not
	// a failure signal here; a missing systemctl yields no lines instead.
	stdout, _, _, err := ssh.ExecContext(ctx, "systemctl is-active "+strings.Join(names, " ")+" 2>/dev/null")
	if err != nil {
		return failAll(items, "service state probe failed: "+err.Error())
	}
	states := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(states) == 1 && strings.TrimSpace(states[0]) == "" {
		return failAll(items, "service state probe returned nothing (no systemd on target?)")
	}

	out := make(map[string]ItemVerdict, len(items))
	for i, r := range items {
		if i >= len(states) {
			out[r.ItemKey] = ItemVerdict{OK: false, Detail: "no state reported for unit"}
			continue
		}
		state := strings.TrimSpace(states[i])
		if state == "active" {
			out[r.ItemKey] = ItemVerdict{OK: true, Level: VerifyRuntime, Detail: "unit is active"}
		} else {
			out[r.ItemKey] = ItemVerdict{OK: false, Detail: "unit is " + state}
		}
	}
	return out
}

// probeUsers checks each account resolves on the target.
func probeUsers(ctx context.Context, ssh SSHExecuter, items []ItemResult) map[string]ItemVerdict {
	stdout, _, _, err := ssh.ExecContext(ctx, "getent passwd 2>/dev/null")
	if err != nil {
		return failAll(items, "user listing on target failed: "+err.Error())
	}
	exists := map[string]bool{}
	for _, line := range strings.Split(stdout, "\n") {
		if i := strings.IndexByte(line, ':'); i > 0 {
			exists[line[:i]] = true
		}
	}
	if len(exists) == 0 {
		return failAll(items, "user listing on target returned nothing")
	}

	out := make(map[string]ItemVerdict, len(items))
	for _, r := range items {
		if exists[itemName(r.ItemKey)] {
			out[r.ItemKey] = ItemVerdict{OK: true, Level: VerifyInfra, Detail: "account exists on target"}
		} else {
			out[r.ItemKey] = ItemVerdict{OK: false, Detail: "account does not exist on target"}
		}
	}
	return out
}
