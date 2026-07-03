package migration

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

type provisionUpdate struct {
	stateID   int64
	installed bool
	configured bool
	verified  bool
	version   string
	errMsg    string
}

type provisionRepoSpy struct {
	mockPipelineRepo
	mu      sync.Mutex
	created []ProvisionState
	updates []provisionUpdate
}

func (r *provisionRepoSpy) CreateProvisionState(ctx context.Context, p ProvisionState) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.created = append(r.created, p)
	return int64(len(r.created)), nil
}

func (r *provisionRepoSpy) UpdateProvisionState(ctx context.Context, id int64, installed, configured, verified bool, version, errMsg string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.updates = append(r.updates, provisionUpdate{
		stateID:   id,
		installed: installed,
		configured: configured,
		verified:  verified,
		version:   version,
		errMsg:    errMsg,
	})
	return nil
}

type provisionRecordingSSH struct {
	*mockSSH
	mu       sync.Mutex
	commands []string
}

func newProvisionRecordingSSH() *provisionRecordingSSH {
	return &provisionRecordingSSH{mockSSH: newMockSSH()}
}

func (r *provisionRecordingSSH) ExecContext(ctx context.Context, cmd string) (string, string, int, error) {
	r.mu.Lock()
	r.commands = append(r.commands, cmd)
	r.mu.Unlock()
	return r.mockSSH.ExecContext(ctx, cmd)
}

func (r *provisionRecordingSSH) Exec(cmd string) (string, string, int, error) {
	r.mu.Lock()
	r.commands = append(r.commands, cmd)
	r.mu.Unlock()
	return r.mockSSH.Exec(cmd)
}

func (r *provisionRecordingSSH) CommandIndex(cmd string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, recorded := range r.commands {
		if recorded == cmd {
			return i
		}
	}
	return -1
}

func TestProvisionVerifiesAfterInstall(t *testing.T) {
	ssh := newProvisionRecordingSSH()
	ssh.execOutput["cat /etc/os-release 2>/dev/null || cat /etc/redhat-release 2>/dev/null || uname -s"] = "Linux"
	ssh.execOutput["mkdir -p ~/.ssh && chmod 700 ~/.ssh"] = ""
	ssh.execOutput["touch ~/.ssh/authorized_keys && chmod 600 ~/.ssh/authorized_keys"] = ""
	ssh.execOutput["sshd -T 2>&1"] = "port 22\npermitrootlogin yes\n"

	repo := &provisionRepoSpy{}
	engine := NewProvisionEngine(ssh, repo)

	if err := engine.Provision(context.Background(), 42, ProvisionConfig{Components: []string{"ssh"}}); err != nil {
		t.Fatalf("Provision returned error: %v", err)
	}

	if len(repo.created) != 1 {
		t.Fatalf("expected 1 provision state, got %d", len(repo.created))
	}
	if len(repo.updates) != 2 {
		t.Fatalf("expected 2 state updates (installed + verified), got %d", len(repo.updates))
	}

	installIdx := ssh.CommandIndex("mkdir -p ~/.ssh && chmod 700 ~/.ssh")
	verifyIdx := ssh.CommandIndex("sshd -T 2>&1")
	if installIdx == -1 || verifyIdx == -1 {
		t.Fatalf("expected install and verify commands to be executed, commands=%v", ssh.commands)
	}
	if verifyIdx < installIdx {
		t.Fatalf("expected verification after installation, commands=%v", ssh.commands)
	}

	lastUpdate := repo.updates[len(repo.updates)-1]
	if !lastUpdate.verified {
		t.Fatalf("expected final provision update to be verified, got %+v", lastUpdate)
	}
	if lastUpdate.errMsg != "" {
		t.Fatalf("expected final provision update error to be empty, got %q", lastUpdate.errMsg)
	}
}

func TestProvisionMarksUnverifiedWhenVerificationFails(t *testing.T) {
	ssh := newProvisionRecordingSSH()
	ssh.execOutput["cat /etc/os-release 2>/dev/null || cat /etc/redhat-release 2>/dev/null || uname -s"] = "Linux"
	ssh.execOutput["mkdir -p ~/.ssh && chmod 700 ~/.ssh"] = ""
	ssh.execOutput["touch ~/.ssh/authorized_keys && chmod 600 ~/.ssh/authorized_keys"] = ""
	ssh.execErr["sshd -T 2>&1"] = errors.New("verification failed")

	repo := &provisionRepoSpy{}
	engine := NewProvisionEngine(ssh, repo)

	err := engine.Provision(context.Background(), 42, ProvisionConfig{Components: []string{"ssh"}})
	if err == nil {
		t.Fatal("expected provisioning error, got nil")
	}
	if !strings.Contains(err.Error(), "verification failed") {
		t.Fatalf("expected verification failure in error, got %v", err)
	}

	if len(repo.updates) != 2 {
		t.Fatalf("expected 2 state updates, got %d", len(repo.updates))
	}
	lastUpdate := repo.updates[len(repo.updates)-1]
	if lastUpdate.verified {
		t.Fatalf("expected final provision update to remain unverified, got %+v", lastUpdate)
	}
	if !strings.Contains(lastUpdate.errMsg, "verification failed") {
		t.Fatalf("expected final provision update to include verification error, got %+v", lastUpdate)
	}
}

func TestVerifyComponentUsesShellQuotedFallback(t *testing.T) {
	ssh := newProvisionRecordingSSH()
	ssh.execOutput["which 'git' 2>&1"] = "/usr/bin/git\n"

	repo := &provisionRepoSpy{}
	engine := NewProvisionEngine(ssh, repo)

	if err := engine.verifyComponent(context.Background(), "git"); err != nil {
		t.Fatalf("verifyComponent returned error: %v", err)
	}

	if ssh.CommandIndex("which 'git' 2>&1") == -1 {
		t.Fatalf("expected shell-quoted which command to be executed, commands=%v", ssh.commands)
	}
	if got := fmt.Sprintf("which %s 2>&1", "'git'"); got != "which 'git' 2>&1" {
		t.Fatalf("unexpected command formatting: %s", got)
	}
}
