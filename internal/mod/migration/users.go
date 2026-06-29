package migration

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// UsersData holds collected users, groups, cron jobs, and firewall rules.
type UsersData struct {
	Users    []UserData        `json:"users"`
	Groups   []GroupData       `json:"groups"`
	CronJobs map[string]string `json:"cronJobs"` // user -> crontab content
	Firewall string            `json:"firewall"`
}

// UsersBackup holds the target's original user/group/firewall state.
// Note: /etc/shadow is intentionally NOT backed up to avoid storing
// password hashes in the database. User passwords are not migrated.
type UsersBackup struct {
	PasswdContent string            `json:"passwdContent"`
	GroupContent  string            `json:"groupContent"`
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
		if !validateIdentifier(user.Name) {
			continue
		}
		if stdout, ok := getUserCrontab(ssh, user.Name); ok {
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
	passwdContent := stdout

	stdout, _, _, _ = ssh.Exec("cat /etc/group")
	backup.GroupContent = stdout

	// Note: /etc/shadow is intentionally NOT backed up to avoid storing
	// password hashes in the database. User passwords are not migrated.

	// Backup crontabs for non-system users
	for _, line := range strings.Split(strings.TrimSpace(passwdContent), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) < 3 {
			continue
		}
		user := fields[0]
		if !validateIdentifier(user) {
			continue
		}
		if parseIntSafe(fields[2]) < 1000 {
			continue
		}
		if cronOut, ok := getUserCrontab(ssh, user); ok {
			backup.CronJobs[user] = cronOut
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
		if !validateIdentifier(group.Name) {
			continue
		}
		_, _, exitCode, _ := ssh.Exec(fmt.Sprintf("groupadd -g %d %s 2>/dev/null", group.GID, shellQuote(group.Name)))
		if exitCode != 0 {
			// Group may already exist, try to modify
			ssh.Exec(fmt.Sprintf("groupmod -g %d %s 2>/dev/null", group.GID, shellQuote(group.Name)))
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
		if !validateIdentifier(user.Name) || !validatePath(user.HomeDir) || !validatePath(user.Shell) {
			continue
		}
		cmd := fmt.Sprintf("useradd -u %d -g %d -d %s -s %s -m %s 2>/dev/null",
			user.UID, user.GID, shellQuote(user.HomeDir), shellQuote(user.Shell), shellQuote(user.Name))
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
		if !validateIdentifier(user) {
			continue
		}
		tmpPath, err := uploadTempFile(ssh, "crontab", crontab)
		if err != nil {
			continue
		}
		_, _, _, _ = ssh.Exec(fmt.Sprintf("crontab -u %s %s 2>/dev/null", shellQuote(user), shellQuote(tmpPath)))
		cleanupRemoteTempFile(ssh, tmpPath)
	}

	// Apply firewall rules
	if ud.Firewall != "" {
		tmpPath, err := uploadTempFile(ssh, "firewall", ud.Firewall)
		if err == nil {
			_, _, _, _ = ssh.Exec(fmt.Sprintf("iptables-restore < %s 2>/dev/null", shellQuote(tmpPath)))
			cleanupRemoteTempFile(ssh, tmpPath)
		}
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
		if err := ssh.Upload(strings.NewReader(ub.PasswdContent), "/etc/passwd"); err != nil {
			return err
		}
	}

	// Restore /etc/group
	if ub.GroupContent != "" {
		if err := ssh.Upload(strings.NewReader(ub.GroupContent), "/etc/group"); err != nil {
			return err
		}
	}

	// Note: /etc/shadow is intentionally NOT restored.
	// User passwords should not be migrated between servers.

	// Restore firewall rules
	if ub.FirewallRules != "" {
		tmpPath, err := uploadTempFile(ssh, "firewall-rollback", ub.FirewallRules)
		if err == nil {
			_, _, _, _ = ssh.Exec(fmt.Sprintf("iptables-restore < %s 2>/dev/null", shellQuote(tmpPath)))
			cleanupRemoteTempFile(ssh, tmpPath)
		}
	}

	return nil
}

func getUserCrontab(ssh SSHExecuter, user string) (string, bool) {
	commands := []string{
		fmt.Sprintf("crontab -u %s -l 2>/dev/null", shellQuote(user)),
		fmt.Sprintf("crontab -u %s -l 2>/dev/null", user),
	}

	for _, cmd := range commands {
		stdout, _, exitCode, _ := ssh.Exec(cmd)
		if exitCode == 0 && strings.TrimSpace(stdout) != "" {
			return stdout, true
		}
	}

	return "", false
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

func validateIdentifier(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, c := range s {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.') {
			return false
		}
	}
	return true
}

func validatePath(p string) bool {
	if !strings.HasPrefix(p, "/") {
		return false
	}
	if strings.Contains(p, "..") {
		return false
	}
	return true
}

func remoteTempPath(prefix string) string {
	return fmt.Sprintf("/tmp/meshium-%s-%d", prefix, time.Now().UnixNano())
}

func uploadTempFile(ssh SSHExecuter, prefix, content string) (string, error) {
	remotePath := remoteTempPath(prefix)
	if err := ssh.Upload(strings.NewReader(content), remotePath); err != nil {
		return "", err
	}
	return remotePath, nil
}

func cleanupRemoteTempFile(ssh SSHExecuter, remotePath string) {
	if remotePath == "" {
		return
	}
	_, _, _, _ = ssh.Exec("rm -f " + shellQuote(remotePath))
}
