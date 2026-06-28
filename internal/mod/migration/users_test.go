package migration

import (
	"encoding/json"
	"testing"
)

func TestUsersCollector(t *testing.T) {
	ssh := newMockSSH()
	ssh.execOutput["cat /etc/passwd"] = "root:x:0:0:root:/root:/bin/bash\nwww-data:x:33:33:www-data:/var/www:/usr/sbin/nologin\nappuser:x:1000:1000:App User:/home/appuser:/bin/bash\ndeploy:x:1001:1001:Deploy:/home/deploy:/bin/bash\n"
	ssh.execOutput["cat /etc/group"] = "root:x:0:\nappuser:x:1000:\ndeploy:x:1001:\n"
	ssh.execOutput["crontab -u 'appuser' -l 2>/dev/null"] = "0 2 * * * /usr/bin/backup.sh\n"
	ssh.execOutput["iptables-save 2>/dev/null || ufw status 2>/dev/null"] = "*filter\n:INPUT ACCEPT [0:0]\nCOMMIT\n"

	collector := &UsersCollector{}
	data, err := collector.Collect(ssh)
	if err != nil {
		t.Fatalf("Collect failed: %v", err)
	}

	var ud UsersData
	json.Unmarshal(data.Data, &ud)

	// Should only collect users with UID >= 1000
	if len(ud.Users) != 2 {
		t.Errorf("expected 2 users, got %d", len(ud.Users))
	}
	if ud.Users[0].Name != "appuser" {
		t.Errorf("expected first user 'appuser', got '%s'", ud.Users[0].Name)
	}
	if ud.Users[0].UID != 1000 {
		t.Errorf("expected UID 1000, got %d", ud.Users[0].UID)
	}

	// Should collect groups with GID >= 1000
	if len(ud.Groups) != 2 {
		t.Errorf("expected 2 groups, got %d", len(ud.Groups))
	}

	// Should collect cron jobs
	if len(ud.CronJobs) != 1 {
		t.Errorf("expected 1 cron job, got %d", len(ud.CronJobs))
	}

	// Should collect firewall rules
	if ud.Firewall == "" {
		t.Error("expected firewall rules, got empty string")
	}
}

func TestUsersApplierBackup(t *testing.T) {
	ssh := newMockSSH()
	ssh.execOutput["cat /etc/passwd"] = "root:x:0:0:root:/root:/bin/bash\n"
	ssh.execOutput["cat /etc/group"] = "root:x:0:\n"
	ssh.execOutput["cat /etc/shadow 2>/dev/null"] = "root:!:19000:0:99999:7:::\n"

	applier := &UsersApplier{}
	backup, err := applier.Backup(ssh)
	if err != nil {
		t.Fatalf("Backup failed: %v", err)
	}

	var ub UsersBackup
	json.Unmarshal(backup.Data, &ub)
	if ub.PasswdContent == "" {
		t.Error("expected passwd content, got empty")
	}
	if ub.GroupContent == "" {
		t.Error("expected group content, got empty")
	}
}

func TestUsersApplierApply(t *testing.T) {
	ssh := newMockSSH()

	ud := UsersData{
		Users: []UserData{
			{Name: "appuser", UID: 1000, GID: 1000, HomeDir: "/home/appuser", Shell: "/bin/bash"},
		},
		Groups: []GroupData{
			{Name: "appuser", GID: 1000},
		},
		CronJobs: map[string]string{
			"appuser": "0 2 * * * /usr/bin/backup.sh",
		},
	}
	raw, _ := json.Marshal(ud)

	var progressMsgs []WSMessage
	applier := &UsersApplier{}
	err := applier.Apply(ssh, CategoryData{Type: "users", Data: raw}, func(msg WSMessage) {
		progressMsgs = append(progressMsgs, msg)
	})
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}

	last := progressMsgs[len(progressMsgs)-1]
	if last.Status != "success" {
		t.Errorf("expected last status 'success', got '%s'", last.Status)
	}
}
