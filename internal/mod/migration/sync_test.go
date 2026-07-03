package migration

import (
	"strings"
	"testing"
)

func TestBuildRsyncCommand_IncludesRemoteDestination(t *testing.T) {
	engine := &SyncEngine{}
	cmd := engine.buildRsyncCommand(SyncConfig{
		SourcePath: "/var/lib/app",
		TargetPath: "/srv/app",
		TargetHost: "10.0.0.2",
		TargetPort: 2222,
		TargetUser: "deploy",
	}, false)

	if !strings.Contains(cmd, "deploy@10.0.0.2:/srv/app/") {
		t.Fatalf("expected remote destination in command, got %q", cmd)
	}
	if !strings.Contains(cmd, "-e") || !strings.Contains(cmd, "ssh -p 2222") {
		t.Fatalf("expected ssh transport with target port in command, got %q", cmd)
	}
}

func TestBuildRsyncCommand_BandwidthLimit(t *testing.T) {
	engine := &SyncEngine{}
	cmd := engine.buildRsyncCommand(SyncConfig{
		SourcePath:     "/src",
		TargetPath:     "/dst",
		BandwidthLimit: 1024,
	}, false)

	if !strings.Contains(cmd, "--bwlimit=1024") {
		t.Fatalf("expected bandwidth limit flag, got %q", cmd)
	}
}

func TestBuildRsyncCommand_PreserveFlags(t *testing.T) {
	engine := &SyncEngine{}
	cmd := engine.buildRsyncCommand(SyncConfig{
		SourcePath:       "/src",
		TargetPath:       "/dst",
		PreservePerms:    true,
		PreserveOwner:    true,
		PreserveGroup:    true,
		PreserveSymlinks: true,
		PreserveTimes:    true,
	}, false)

	for _, flag := range []string{" -p", " -o", " -g", " -l", " -t"} {
		if !strings.Contains(cmd, flag) {
			t.Fatalf("expected preserve flag %q in command, got %q", flag, cmd)
		}
	}
}

func TestBuildRsyncCommand_DryRun(t *testing.T) {
	engine := &SyncEngine{}
	cmd := engine.buildRsyncCommand(SyncConfig{
		SourcePath: "/src",
		TargetPath: "/dst",
		DryRun:     true,
	}, false)

	if !strings.Contains(cmd, "--dry-run") {
		t.Fatalf("expected dry-run flag, got %q", cmd)
	}
}

func TestBuildRsyncCommand_Sparse(t *testing.T) {
	engine := &SyncEngine{}
	cmd := engine.buildRsyncCommand(SyncConfig{
		SourcePath: "/src",
		TargetPath: "/dst",
		Sparse:     true,
	}, false)

	if !strings.Contains(cmd, "--sparse") {
		t.Fatalf("expected sparse flag, got %q", cmd)
	}
}

func TestBuildRsyncCommand_DeleteExtraneous(t *testing.T) {
	engine := &SyncEngine{}
	cmd := engine.buildRsyncCommand(SyncConfig{
		SourcePath:       "/src",
		TargetPath:       "/dst",
		DeleteExtraneous: true,
	}, false)

	if !strings.Contains(cmd, "--delete") {
		t.Fatalf("expected delete flag, got %q", cmd)
	}
}

func TestBuildRsyncCommand_LocalFallbackWhenTargetHostEmpty(t *testing.T) {
	engine := &SyncEngine{}
	cmd := engine.buildRsyncCommand(SyncConfig{
		SourcePath: "/src",
		TargetPath: "/dst",
	}, false)

	if strings.Contains(cmd, "@") {
		t.Fatalf("expected local-to-local rsync command, got %q", cmd)
	}
	if !strings.Contains(cmd, "/src/") || !strings.Contains(cmd, "/dst/") {
		t.Fatalf("expected local paths in command, got %q", cmd)
	}
}
