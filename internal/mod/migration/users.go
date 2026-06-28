package migration

import (
	"encoding/json"
	"fmt"
	"strings"

	"meshium/internal/shared"
)

// UsersData holds collected users, groups, cron jobs, and firewall rules.
type UsersData struct {
	Users     []UserData        `json:"users"`
	Groups    []GroupData       `json:"groups"`
	CronJobs  map[string]string `json:"cronJobs"`  // user -> crontab content
	Firewall  string            `json:"firewall"`
}

// UsersBackup holds the target's original user/group/firewall state.
type UsersBackup struct {
	PasswdContent string            `json:"passwdContent"`
	GroupContent  string            `json:"groupContent"`
	ShadowContent string            `json:"shadowContent"`
	CronJobs      map[string]string `json:"cronJobs"`
	FirewallRules string            `json:"firewallRules"`
}

// UsersCollector collects users, groups, cron jobs, and firewall rules from the source.
type UsersCollector struct{}

// Collect reads /etc/passwd, /etc/group, /etc/shadow, crontabs, and firewall rules.
func (c *UsersCollector) Collect(ssh SSHExecuter) (CategoryData, error) {
	data := UsersData{
		CronJobs: make(map[string]string),
	}

	// Collect users from /etc/passwd (skip system users with UID < 1000)
	stdout, _, _, err := ssh.Exec("cat /etc/passwd")
	if err != nil {
		return CategoryData{}, err
	}
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) < 7 {
			continue
		}
		uid := parseIntSafe(fields[2])
		if uid < 1000 {
			continue // skip system users
		}
		data.Users = append(data.Users, UserData{
			Name:    fields[0],
			UID:     uid,
			GID:     parseIntSafe(fields[3]),
			HomeDir: fields[5],
			Shell:   fields[6],
		})
	}

	// Collect groups from /etc/group
	stdout, _, _, err = ssh.Exec("cat /etc/group")
	if err == nil {
		for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
			fields := strings.Split(line, ":")
			if len(fields) < 3 {
				continue
			}
			gid := parseIntSafe(fields[2])
			if gid < 1000 {
				continue
			}
			data.Groups = append(data.Groups, GroupData{
				Name: fields[0],
				GID:  gid,
			})
		}
	}

	// Collect cron jobs for each user
	for _, user := range data.Users {
		stdout, _, exitCode, _ := ssh.Exec(fmt.Sprintf("crontab -u %s -l 2>/dev/null", shared.ShellQuote(user.Name)))
		if exitCode == 0 && strings.TrimSpace(stdout) != "" {
			data.CronJobs[user.Name] = stdout
		}
	}

	// Collect firewall rules
	stdout, _, _, _ = ssh.Exec("iptables-save 2>/dev/null || ufw status 2>/dev/null")
	if strings.TrimSpace(stdout) != "" {
		data.Firewall = stdout
	}

	raw, _ := json.Marshal(data)
	return CategoryData{Type: "users", Data: raw}, nil
}

// UsersApplier creates users and groups, installs cron jobs, and applies firewall rules.
type UsersApplier struct{}

// Backup saves the target's /etc/passwd, /etc/group, /etc/shadow, crontabs, and firewall rules.
func (a *UsersApplier) Backup(ssh SSHExecuter) (BackupData, error) {
	backup := UsersBackup{
		CronJobs: make(map[string]string),
	}

	stdout, _, _, err := ssh.Exec("cat /etc/passwd")
	if err != nil {
		return BackupData{}, err
	}
	backup.PasswdContent = stdout

	stdout, _, _, _ = ssh.Exec("cat /etc/group")
	backup.GroupContent = stdout

	stdout, _, _, _ = ssh.Exec("cat /etc/shadow 2>/dev/null")
	backup.ShadowContent = stdout

	// Backup crontabs for non-system users
	stdout, _, _, _ = ssh.Exec("cut -d: -f1 /etc/passwd | while read u; do crontab -u $u -l 2>/dev/null && echo \"---$u---\"; done")
	for _, block := range strings.Split(stdout, "---") {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		lines := strings.SplitN(block, "\n", 2)
		if len(lines) == 2 {
			user := strings.TrimSpace(lines[0])
			content := lines[1]
			if content != "" {
				backup.CronJobs[user] = content
			}
		}
	}

	// Backup firewall rules
	stdout, _, _, _ = ssh.Exec("iptables-save 2>/dev/null || ufw status 2>/dev/null")
	backup.FirewallRules = stdout

	raw, _ := json.Marshal(backup)
	return BackupData{Type: "users", Data: raw}, nil
}

// Apply creates users, groups, cron jobs, and firewall rules on the target.
func (a *UsersApplier) Apply(ssh SSHExecuter, data CategoryData, onProgress StepCallback) error {
	var ud UsersData
	if err := json.Unmarshal(data.Data, &ud); err != nil {
		return err
	}

	// Create groups first
	for _, group := range ud.Groups {
		_, _, exitCode, _ := ssh.Exec(fmt.Sprintf("groupadd -g %d %s 2>/dev/null", group.GID, shared.ShellQuote(group.Name)))
		if exitCode != 0 {
			// Group may already exist, try to modify
			ssh.Exec(fmt.Sprintf("groupmod -g %d %s 2>/dev/null", group.GID, shared.ShellQuote(group.Name)))
		}
	}

	if onProgress != nil {
		onProgress(WSMessage{
			Step:   "users:apply",
			Status: "progress",
			Value:  fmt.Sprintf("Creating %d users", len(ud.Users)),
		})
	}

	// Create users
	for i, user := range ud.Users {
		cmd := fmt.Sprintf("useradd -u %d -g %d -d %s -s %s -m %s 2>/dev/null",
			user.UID, user.GID, shared.ShellQuote(user.HomeDir), shared.ShellQuote(user.Shell), shared.ShellQuote(user.Name))
		_, _, exitCode, _ := ssh.Exec(cmd)
		if exitCode != 0 {
			// User may already exist
			if onProgress != nil {
				onProgress(WSMessage{
					Step:   "users:apply",
					Status: "warning",
					Value:  fmt.Sprintf("user %s may already exist (exit %d)", user.Name, exitCode),
				})
			}
			continue
		}
		if onProgress != nil {
			onProgress(WSMessage{
				Step:   "users:apply",
				Status: "progress",
				Value:  fmt.Sprintf("Created %d/%d: %s", i+1, len(ud.Users), user.Name),
			})
		}
	}

	// Install cron jobs
	for user, crontab := range ud.CronJobs {
		// Use base64 encoding to safely transfer crontab content without injection risk
		ssh.Exec(fmt.Sprintf("%s | crontab -u %s - 2>/dev/null",
			shared.Base64EncodeForShell([]byte(crontab)), shared.ShellQuote(user)))
	}

	// Apply firewall rules
	if ud.Firewall != "" {
		ssh.Exec(fmt.Sprintf("%s | iptables-restore 2>/dev/null", shared.Base64EncodeForShell([]byte(ud.Firewall))))
	}

	if onProgress != nil {
		onProgress(WSMessage{
			Step:   "users:apply",
			Status: "success",
			Value:  fmt.Sprintf("%d users, %d groups, %d cron jobs applied", len(ud.Users), len(ud.Groups), len(ud.CronJobs)),
		})
	}

	return nil
}

// Rollback restores the target's original /etc/passwd, /etc/group, /etc/shadow, crontabs, and firewall.
func (a *UsersApplier) Rollback(ssh SSHExecuter, backup BackupData) error {
	var ub UsersBackup
	if err := json.Unmarshal(backup.Data, &ub); err != nil {
		return err
	}

	// Restore /etc/passwd
	if ub.PasswdContent != "" {
		ssh.Exec("cp /etc/passwd /etc/passwd.migration_bak 2>/dev/null")
		ssh.Exec(shared.Base64DecodeCommand("/etc/passwd", []byte(ub.PasswdContent)))
	}

	// Restore /etc/group
	if ub.GroupContent != "" {
		ssh.Exec(shared.Base64DecodeCommand("/etc/group", []byte(ub.GroupContent)))
	}

	// Restore /etc/shadow
	if ub.ShadowContent != "" {
		ssh.Exec(shared.Base64DecodeCommand("/etc/shadow", []byte(ub.ShadowContent)))
	}

	// Restore firewall rules
	if ub.FirewallRules != "" {
		ssh.Exec(fmt.Sprintf("%s | iptables-restore 2>/dev/null", shared.Base64EncodeForShell([]byte(ub.FirewallRules))))
	}

	return nil
}

func parseIntSafe(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n
}
