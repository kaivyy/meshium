package migration

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"meshium/internal/shared"
)

// UsersData holds collected users, groups, cron jobs, and firewall rules.
type UsersData struct {
	Users    []UserData        `json:"users"`
	Groups   []GroupData       `json:"groups"`
	CronJobs map[string]string `json:"cronJobs"` // user -> crontab content
	Firewall string            `json:"firewall"`
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
func (c *UsersCollector) Collect(ctx context.Context, ssh SSHExecuter) (CategoryData, error) {
	data := UsersData{
		CronJobs: make(map[string]string),
	}

	// Collect users from /etc/passwd (skip system users with UID < 1000)
	stdout, _, _, err := ssh.ExecContext(ctx, "cat /etc/passwd")
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
	stdout, _, _, err = ssh.ExecContext(ctx, "cat /etc/group")
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
		stdout, _, exitCode, _ := ssh.ExecContext(ctx, fmt.Sprintf("crontab -u %s -l 2>/dev/null", shared.ShellQuote(user.Name)))
		if exitCode == 0 && strings.TrimSpace(stdout) != "" {
			data.CronJobs[user.Name] = stdout
		}
	}

	// Collect firewall rules. Only iptables-save output is restorable — the
	// `|| ufw status` fallback yields human-readable status text, which
	// iptables-restore rejects; storing it meant Apply "restored" nothing while
	// reporting success, leaving the target with no firewall.
	stdout, _, _, _ = ssh.ExecContext(ctx, "iptables-save 2>/dev/null || ufw status 2>/dev/null")
	if isIptablesSaveFormat(stdout) {
		data.Firewall = stdout
	}

	raw, _ := json.Marshal(data)
	return CategoryData{Type: "users", Data: raw}, nil
}

// UsersApplier creates users and groups, installs cron jobs, and applies firewall rules.
type UsersApplier struct{}

// Backup saves the target's /etc/passwd, /etc/group, /etc/shadow, crontabs, and firewall rules.
func (a *UsersApplier) Backup(ctx context.Context, ssh SSHExecuter) (BackupData, error) {
	backup := UsersBackup{
		CronJobs: make(map[string]string),
	}

	stdout, _, _, err := ssh.ExecContext(ctx, "cat /etc/passwd")
	if err != nil {
		return BackupData{}, err
	}
	backup.PasswdContent = stdout

	stdout, _, _, _ = ssh.ExecContext(ctx, "cat /etc/group")
	backup.GroupContent = stdout

	stdout, _, _, _ = ssh.ExecContext(ctx, "cat /etc/shadow 2>/dev/null")
	backup.ShadowContent = stdout

	// Backup crontabs per user. The marker line is printed BEFORE the crontab
	// body — the old command printed the body first and then "---user---", so
	// the parser (which expects marker-first) keyed each block by its first
	// cron line and dropped single-line crontabs entirely.
	stdout, _, _, _ = ssh.ExecContext(ctx,
		`cut -d: -f1 /etc/passwd | while read u; do c=$(crontab -u "$u" -l 2>/dev/null); `+
			`[ -n "$c" ] && printf '===MESHIUM-CRON:%s===\n%s\n' "$u" "$c"; done`)
	for user, content := range parseCrontabBlocks(stdout) {
		backup.CronJobs[user] = content
	}

	// Backup firewall rules — same restorability guard as Collect.
	stdout, _, _, _ = ssh.ExecContext(ctx, "iptables-save 2>/dev/null || ufw status 2>/dev/null")
	if isIptablesSaveFormat(stdout) {
		backup.FirewallRules = stdout
	}

	raw, _ := json.Marshal(backup)
	return BackupData{Type: "users", Data: raw}, nil
}

// Apply creates users, groups, cron jobs, and firewall rules on the target.
func (a *UsersApplier) Apply(ctx context.Context, ssh SSHExecuter, data CategoryData, onProgress StepCallback) error {
	var ud UsersData
	if err := json.Unmarshal(data.Data, &ud); err != nil {
		return err
	}

	// Create groups first
	for _, group := range ud.Groups {
		_, _, exitCode, _ := ssh.ExecContext(ctx, fmt.Sprintf("groupadd -g %d %s 2>/dev/null", group.GID, shared.ShellQuote(group.Name)))
		if exitCode != 0 {
			// Group may already exist, try to modify
			ssh.ExecContext(ctx, fmt.Sprintf("groupmod -g %d %s 2>/dev/null", group.GID, shared.ShellQuote(group.Name)))
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
		_, _, exitCode, _ := ssh.ExecContext(ctx, cmd)
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
		tmpPath := fmt.Sprintf("/tmp/meshium-crontab-%d", time.Now().UnixNano())
		if err := ssh.Upload(strings.NewReader(crontab), tmpPath); err != nil {
			return err
		}
		ssh.ExecContext(ctx, fmt.Sprintf("crontab -u %s %s 2>/dev/null && rm -f %s",
			shared.ShellQuote(user), shared.ShellQuote(tmpPath), shared.ShellQuote(tmpPath)))
	}

	// Apply firewall rules. The format guard also protects against records
	// collected before the ufw-status fix; the exit check stops a rejected
	// restore from silently passing as success.
	if isIptablesSaveFormat(ud.Firewall) {
		_, stderr, exitCode, ferr := ssh.ExecContext(ctx, fmt.Sprintf("%s | iptables-restore", shared.Base64EncodeForShell([]byte(ud.Firewall))))
		if (ferr != nil || exitCode != 0) && onProgress != nil {
			onProgress(WSMessage{
				Step:   "users:apply",
				Status: "warning",
				Value:  fmt.Sprintf("firewall restore failed (exit %d): %s", exitCode, strings.TrimSpace(stderr)),
			})
		}
	} else if ud.Firewall != "" && onProgress != nil {
		onProgress(WSMessage{
			Step:   "users:apply",
			Status: "warning",
			Value:  "collected firewall data is not iptables-save format (likely ufw status text); skipping restore",
		})
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
func (a *UsersApplier) Rollback(ctx context.Context, ssh SSHExecuter, backup BackupData) error {
	var ub UsersBackup
	if err := json.Unmarshal(backup.Data, &ub); err != nil {
		return err
	}

	// Restore /etc/passwd
	if ub.PasswdContent != "" {
		ssh.ExecContext(ctx, "cp /etc/passwd /etc/passwd.migration_bak 2>/dev/null")
		ssh.ExecContext(ctx, shared.Base64DecodeCommand("/etc/passwd", []byte(ub.PasswdContent)))
	}

	// Restore /etc/group
	if ub.GroupContent != "" {
		ssh.ExecContext(ctx, shared.Base64DecodeCommand("/etc/group", []byte(ub.GroupContent)))
	}

	// Restore /etc/shadow
	if ub.ShadowContent != "" {
		ssh.ExecContext(ctx, shared.Base64DecodeCommand("/etc/shadow", []byte(ub.ShadowContent)))
	}

	// Restore firewall rules (same format guard as Apply).
	if isIptablesSaveFormat(ub.FirewallRules) {
		ssh.ExecContext(ctx, fmt.Sprintf("%s | iptables-restore 2>/dev/null", shared.Base64EncodeForShell([]byte(ub.FirewallRules))))
	}

	return nil
}

// isIptablesSaveFormat reports whether s looks like iptables-save output —
// the only format iptables-restore accepts. Every table dump contains a table
// declaration line beginning with '*' (e.g. "*filter"). `ufw status` text
// contains none.
func isIptablesSaveFormat(s string) bool {
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "*") {
			return true
		}
	}
	return false
}

// parseCrontabBlocks parses the marker-first crontab dump produced by Backup:
// each user's block starts with "===MESHIUM-CRON:<user>===" followed by the
// crontab body.
func parseCrontabBlocks(out string) map[string]string {
	const marker = "===MESHIUM-CRON:"
	jobs := make(map[string]string)
	current := ""
	var body []string
	flush := func() {
		if current != "" && len(body) > 0 {
			jobs[current] = strings.Join(body, "\n")
		}
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, marker) && strings.HasSuffix(line, "===") {
			flush()
			current = strings.TrimSuffix(strings.TrimPrefix(line, marker), "===")
			body = body[:0]
			continue
		}
		if current != "" && strings.TrimSpace(line) != "" {
			body = append(body, line)
		}
	}
	flush()
	return jobs
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
