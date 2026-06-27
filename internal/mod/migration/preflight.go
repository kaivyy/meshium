package migration

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// PreFlightResult holds the result of pre-flight validation.
type PreFlightResult struct {
	Errors   []string `json:"errors"`
	Warnings []string `json:"warnings"`
	OK       bool     `json:"ok"`
}

// PreFlight performs validation checks before migration execution.
// It checks: target disk space, OS compatibility, Docker availability, SSH connectivity.
func (e *Executor) PreFlight(ctx context.Context, migrationID int, onProgress StepCallback) (*PreFlightResult, error) {
	if onProgress == nil {
		onProgress = func(WSMessage) {}
	}

	result := &PreFlightResult{}

	addWarning := func(msg string) {
		result.Warnings = append(result.Warnings, msg)
		onProgress(WSMessage{Step: "preflight", Status: "warning", Value: msg})
	}
	addError := func(msg string) {
		result.Errors = append(result.Errors, msg)
		onProgress(WSMessage{Step: "preflight", Status: "error", Error: msg})
	}

	if err := ctx.Err(); err != nil {
		return result, err
	}

	onProgress(WSMessage{Step: "preflight", Status: "progress", Value: "Loading migration..."})

	migration, err := e.repo.GetMigration(migrationID)
	if err != nil {
		addError(fmt.Sprintf("failed to load migration: %v", err))
		result.OK = false
		return result, nil
	}

	steps, err := e.repo.GetSteps(migrationID)
	if err != nil {
		addWarning(fmt.Sprintf("failed to load migration steps: %v", err))
	}

	targetServer, err := e.srvRepo.GetByID(migration.TargetID)
	if err != nil {
		addError(fmt.Sprintf("target server not found: %v", err))
		result.OK = false
		return result, nil
	}

	onProgress(WSMessage{Step: "preflight", Status: "progress", Value: "Connecting to target server..."})
	targetSSH, err := e.getSSHClient(migration.TargetID, targetServer)
	if err != nil {
		addError(fmt.Sprintf("failed to connect to target server: %v", err))
		result.OK = false
		return result, nil
	}

	onProgress(WSMessage{Step: "preflight", Status: "success", Value: "Connected to target server"})

	// Check SSH connectivity with a simple command.
	onProgress(WSMessage{Step: "preflight:ssh", Status: "progress", Value: "Verifying SSH connectivity..."})
	stdout, stderr, exitCode, err := targetSSH.Exec("echo ok")
	if err != nil || exitCode != 0 || strings.TrimSpace(stdout) != "ok" {
		msg := "SSH connectivity check failed"
		if err != nil {
			msg = fmt.Sprintf("%s: %v", msg, err)
		}
		if stderr != "" {
			msg = fmt.Sprintf("%s: %s", msg, strings.TrimSpace(stderr))
		}
		addError(msg)
	} else {
		onProgress(WSMessage{Step: "preflight:ssh", Status: "success", Value: "SSH connectivity verified"})
	}

	// Check target disk space.
	onProgress(WSMessage{Step: "preflight:disk", Status: "progress", Value: "Checking target disk space..."})
	stdout, stderr, exitCode, err = targetSSH.Exec("df -h /")
	if err != nil || exitCode != 0 {
		msg := "failed to check target disk space"
		if err != nil {
			msg = fmt.Sprintf("%s: %v", msg, err)
		}
		if stderr != "" {
			msg = fmt.Sprintf("%s: %s", msg, strings.TrimSpace(stderr))
		}
		addError(msg)
	} else if availableBytes, parseErr := parseDFAvailableBytes(stdout); parseErr != nil {
		addWarning(fmt.Sprintf("could not parse target disk space: %v", parseErr))
	} else if availableBytes < 1<<30 {
		addWarning(fmt.Sprintf("target root filesystem has only %s available", formatBytes(availableBytes)))
	} else {
		onProgress(WSMessage{Step: "preflight:disk", Status: "success", Value: "Target disk space is sufficient"})
	}

	// Check OS compatibility using cached/stored source info and the live target distro.
	onProgress(WSMessage{Step: "preflight:os", Status: "progress", Value: "Checking OS compatibility..."})
	sourceInfo, sourceOK := e.loadStoredDistroInfo(migration.SourceID, migration, steps, "source")
	targetInfo, targetOK := detectTargetDistroInfo(targetSSH)
	if !targetOK {
		addWarning("could not detect target distro")
	} else {
		onProgress(WSMessage{Step: "preflight:os", Status: "success", Value: fmt.Sprintf("Target distro detected: %s", targetInfo.Name)})
	}
	if !sourceOK {
		addWarning("could not determine source distro from stored migration data")
	} else if sourceInfo.Name != "" {
		onProgress(WSMessage{Step: "preflight:os", Status: "progress", Value: fmt.Sprintf("Source distro detected: %s", sourceInfo.Name)})
	}
	if sourceOK && targetOK && sourceInfo.Family != "" && targetInfo.Family != "" && sourceInfo.Family != targetInfo.Family {
		addWarning(fmt.Sprintf("source family %s differs from target family %s", sourceInfo.Family, targetInfo.Family))
	}

	// If Docker is part of the migration, ensure it exists on target.
	var categories []string
	if err := json.Unmarshal([]byte(migration.Categories), &categories); err == nil && contains(categories, "docker") {
		onProgress(WSMessage{Step: "preflight:docker", Status: "progress", Value: "Checking Docker availability..."})
		stdout, stderr, exitCode, err = targetSSH.Exec("which docker")
		if err != nil || exitCode != 0 || strings.TrimSpace(stdout) == "" {
			msg := "docker is not installed on the target server"
			if err != nil {
				msg = fmt.Sprintf("%s: %v", msg, err)
			}
			if stderr != "" {
				msg = fmt.Sprintf("%s: %s", msg, strings.TrimSpace(stderr))
			}
			addError(msg)
		} else {
			onProgress(WSMessage{Step: "preflight:docker", Status: "success", Value: "Docker is available on the target"})
		}
	} else if err != nil {
		addWarning(fmt.Sprintf("could not parse migration categories: %v", err))
	}

	result.OK = len(result.Errors) == 0
	if result.OK {
		onProgress(WSMessage{Step: "preflight", Status: "complete", Value: "Pre-flight checks passed"})
	} else {
		onProgress(WSMessage{Step: "preflight", Status: "complete", Value: "Pre-flight checks found blocking issues"})
	}

	return result, nil
}

func (e *Executor) loadStoredDistroInfo(serverID int, migration *Migration, steps []MigrationStep, side string) (DistroInfo, bool) {
	if migration != nil && migration.Plan != "" {
		if info, ok := distroInfoFromPlanJSON(migration.Plan, side); ok {
			return info, true
		}
	}

	if info, ok := distroInfoFromStepData(steps, side); ok {
		return info, true
	}

	if e != nil && e.srvRepo != nil {
		if serverInfo, err := e.srvRepo.GetServerInfo(serverID); err == nil && serverInfo != nil && serverInfo.OS != "" {
			return distroInfoFromOS(serverInfo.OS), true
		}
	}

	return DistroInfo{}, false
}

func detectTargetDistroInfo(ssh SSHExecuter) (DistroInfo, bool) {
	info, err := DetectDistro(ssh)
	if err != nil {
		return DistroInfo{}, false
	}
	return info, true
}

func distroInfoFromPlanJSON(planJSON, side string) (DistroInfo, bool) {
	var plan MigrationPlan
	if err := json.Unmarshal([]byte(planJSON), &plan); err != nil {
		return DistroInfo{}, false
	}

	switch side {
	case "source":
		if plan.Source.OS != "" {
			return distroInfoFromOS(plan.Source.OS), true
		}
	case "target":
		if plan.Target.OS != "" {
			return distroInfoFromOS(plan.Target.OS), true
		}
	}

	return DistroInfo{}, false
}

func distroInfoFromStepData(steps []MigrationStep, side string) (DistroInfo, bool) {
	for _, step := range steps {
		if step.Data == "" {
			continue
		}

		if info, ok := distroInfoFromPlanJSON(step.Data, side); ok {
			return info, true
		}

		var payload map[string]json.RawMessage
		if err := json.Unmarshal([]byte(step.Data), &payload); err == nil {
			for _, key := range []string{side, strings.ToLower(side), side + "Info", side + "_info"} {
				if raw, ok := payload[key]; ok {
					var system struct {
						OS string `json:"os"`
					}
					if err := json.Unmarshal(raw, &system); err == nil && system.OS != "" {
						return distroInfoFromOS(system.OS), true
					}
				}
			}
		}

		var system struct {
			OS string `json:"os"`
		}
		if err := json.Unmarshal([]byte(step.Data), &system); err == nil && system.OS != "" {
			return distroInfoFromOS(system.OS), true
		}
	}

	return DistroInfo{}, false
}

func distroInfoFromOS(osName string) DistroInfo {
	osName = strings.TrimSpace(osName)
	info := DistroInfo{Name: osName, Family: familyFromOS(osName)}
	return info
}

func familyFromOS(osName string) string {
	name := strings.ToLower(strings.TrimSpace(osName))
	switch {
	case strings.Contains(name, "debian"), strings.Contains(name, "ubuntu"), strings.Contains(name, "raspbian"), strings.Contains(name, "linux mint"):
		return "debian"
	case strings.Contains(name, "red hat"), strings.Contains(name, "rhel"), strings.Contains(name, "centos"), strings.Contains(name, "rocky"), strings.Contains(name, "alma"), strings.Contains(name, "fedora"), strings.Contains(name, "oracle linux"):
		return "rhel"
	case strings.Contains(name, "arch"):
		return "arch"
	case strings.Contains(name, "alpine"):
		return "alpine"
	case strings.Contains(name, "suse"), strings.Contains(name, "opensuse"):
		return "suse"
	default:
		return "unknown"
	}
}

func parseDFAvailableBytes(output string) (uint64, error) {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) == 0 {
		return 0, fmt.Errorf("empty df output")
	}

	line := strings.TrimSpace(lines[len(lines)-1])
	fields := strings.Fields(line)
	if len(fields) < 4 {
		return 0, fmt.Errorf("unexpected df output: %q", line)
	}

	return parseHumanBytes(fields[3])
}

func parseHumanBytes(raw string) (uint64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, fmt.Errorf("empty size value")
	}

	multiplier := float64(1)
	last := raw[len(raw)-1]
	switch last {
	case 'K', 'k':
		multiplier = 1 << 10
		raw = raw[:len(raw)-1]
	case 'M', 'm':
		multiplier = 1 << 20
		raw = raw[:len(raw)-1]
	case 'G', 'g':
		multiplier = 1 << 30
		raw = raw[:len(raw)-1]
	case 'T', 't':
		multiplier = 1 << 40
		raw = raw[:len(raw)-1]
	case 'P', 'p':
		multiplier = 1 << 50
		raw = raw[:len(raw)-1]
	case 'E', 'e':
		multiplier = 1 << 60
		raw = raw[:len(raw)-1]
	}

	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, err
	}

	return uint64(value * multiplier), nil
}

func formatBytes(v uint64) string {
	const unit = 1024
	if v < unit {
		return fmt.Sprintf("%dB", v)
	}
	d := float64(v)
	exponent := 0
	for d >= unit && exponent < 6 {
		d /= unit
		exponent++
	}
	suffixes := []string{"KB", "MB", "GB", "TB", "PB", "EB"}
	if exponent == 0 {
		return fmt.Sprintf("%dB", v)
	}
	return fmt.Sprintf("%.1f%s", d, suffixes[exponent-1])
}
