package migration

import (
	"encoding/json"
	"fmt"
	"strings"

	"meshium/internal/shared"
)

// ServicesData holds the collected service list from the source server.
type ServicesData struct {
	Services []string `json:"services"`
	Count    int      `json:"count"`
}

// ServicesBackup holds the target's enabled services before migration.
type ServicesBackup struct {
	Services []string `json:"services"`
}

// ServicesCollector collects enabled services from the source server.
type ServicesCollector struct{}

// Collect runs systemctl list-unit-files to find enabled services.
func (c *ServicesCollector) Collect(ssh SSHExecuter) (CategoryData, error) {
	stdout, _, _, err := ssh.Exec("systemctl list-unit-files --type=service --state=enabled --no-legend 2>/dev/null || rc-update show 2>/dev/null")
	if err != nil {
		return CategoryData{}, err
	}

	data := ServicesData{}
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// systemctl format: "nginx.service enabled"
		// rc-update format: "sshd | default"
		var svc string
		if strings.Contains(line, ".service") {
			fields := strings.Fields(line)
			if len(fields) >= 1 {
				svc = strings.TrimSuffix(fields[0], ".service")
			}
		} else if strings.Contains(line, "|") {
			fields := strings.Split(line, "|")
			if len(fields) >= 1 {
				svc = strings.TrimSpace(fields[0])
			}
		}
		if svc != "" {
			data.Services = append(data.Services, svc)
		}
	}
	data.Count = len(data.Services)

	raw, _ := json.Marshal(data)
	return CategoryData{Type: "services", Data: raw}, nil
}

// ServicesApplier enables services on the target server.
type ServicesApplier struct{}

// Backup saves the target's currently enabled services.
func (a *ServicesApplier) Backup(ssh SSHExecuter) (BackupData, error) {
	stdout, _, _, err := ssh.Exec("systemctl list-unit-files --type=service --state=enabled --no-legend 2>/dev/null")
	if err != nil {
		return BackupData{}, err
	}

	backup := ServicesBackup{}
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.Contains(line, ".service") {
			fields := strings.Fields(line)
			if len(fields) >= 1 {
				backup.Services = append(backup.Services, strings.TrimSuffix(fields[0], ".service"))
			}
		}
	}

	raw, _ := json.Marshal(backup)
	return BackupData{Type: "services", Data: raw}, nil
}

// Apply enables and starts services on the target.
func (a *ServicesApplier) Apply(ssh SSHExecuter, data CategoryData, onProgress StepCallback) error {
	var sd ServicesData
	if err := json.Unmarshal(data.Data, &sd); err != nil {
		return err
	}

	info, err := DetectDistro(ssh)
	if err != nil {
		return err
	}
	adapter := GetAdapter(info)

	if onProgress != nil {
		onProgress(WSMessage{
			Step:   "services:apply",
			Status: "progress",
			Value:  fmt.Sprintf("Enabling %d services", sd.Count),
		})
	}

	for i, svc := range sd.Services {
		// Enable the service
		_, _, exitCode, err := ssh.Exec(adapter.EnableService(svc))
		if err != nil || exitCode != 0 {
			if onProgress != nil {
				onProgress(WSMessage{
					Step:   "services:apply",
					Status: "warning",
					Value:  fmt.Sprintf("failed to enable %s (exit %d)", svc, exitCode),
				})
			}
			continue // non-fatal
		}

		// Start the service
		_, _, exitCode, _ = ssh.Exec(adapter.StartService(svc))
		if exitCode != 0 {
			if onProgress != nil {
				onProgress(WSMessage{
					Step:   "services:apply",
					Status: "warning",
					Value:  fmt.Sprintf("failed to start %s (exit %d)", svc, exitCode),
				})
			}
			continue // non-fatal
		}

		if onProgress != nil {
			onProgress(WSMessage{
				Step:   "services:apply",
				Status: "progress",
				Value:  fmt.Sprintf("Enabled %d/%d: %s", i+1, sd.Count, svc),
			})
		}
	}

	if onProgress != nil {
		onProgress(WSMessage{
			Step:   "services:apply",
			Status: "success",
			Value:  fmt.Sprintf("%d services processed", sd.Count),
		})
	}

	return nil
}

// Rollback disables services that were enabled by the migration.
func (a *ServicesApplier) Rollback(ssh SSHExecuter, backup BackupData) error {
	var sb ServicesBackup
	if err := json.Unmarshal(backup.Data, &sb); err != nil {
		return err
	}

	// Get currently enabled services
	stdout, _, _, err := ssh.Exec("systemctl list-unit-files --type=service --state=enabled --no-legend 2>/dev/null")
	if err != nil {
		return err
	}

	currentServices := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		if strings.Contains(line, ".service") {
			fields := strings.Fields(strings.TrimSpace(line))
			if len(fields) >= 1 {
				currentServices[strings.TrimSuffix(fields[0], ".service")] = true
			}
		}
	}

	// Disable services that are enabled now but weren't in the backup
	for svc := range currentServices {
		if !contains(sb.Services, svc) {
			ssh.Exec(fmt.Sprintf("systemctl disable --now %s 2>/dev/null", shared.ShellQuote(svc)))
		}
	}

	return nil
}
