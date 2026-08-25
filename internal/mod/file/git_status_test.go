package file

import "testing"

func TestParseGitStatusNotARepo(t *testing.T) {
	got := parseGitStatus("NOT_A_REPO\n")
	if got.IsRepo || got.Branch != "" || got.Head != "" || got.Dirty != 0 {
		t.Errorf("expected empty not-a-repo status, got %+v", got)
	}
}

func TestParseGitStatusCleanRepo(t *testing.T) {
	got := parseGitStatus("BRANCH:main\nHEAD:a1b2c3d\n")
	if !got.IsRepo || got.Branch != "main" || got.Head != "a1b2c3d" {
		t.Errorf("unexpected %+v", got)
	}
	if got.Dirty != 0 || got.Staged != 0 || got.Untracked != 0 {
		t.Errorf("clean repo should have zero counts, got %+v", got)
	}
}

func TestParseGitStatusCounts(t *testing.T) {
	out := "BRANCH:feature/x\nHEAD:deadbee\n" +
		"M  staged.txt\n" + // staged modify
		"A  added.txt\n" + // staged add
		" M unstaged.txt\n" + // unstaged modify
		"MM both.txt\n" + // staged + unstaged
		"?? untracked.log\n" + // untracked
		"\n" // trailing newline
	got := parseGitStatus(out)
	if !got.IsRepo || got.Branch != "feature/x" || got.Head != "deadbee" {
		t.Fatalf("header parse wrong: %+v", got)
	}
	if got.Dirty != 5 {
		t.Errorf("Dirty: expected 5, got %d", got.Dirty)
	}
	if got.Staged != 3 { // M_, A_, MM
		t.Errorf("Staged: expected 3, got %d", got.Staged)
	}
	if got.Untracked != 1 {
		t.Errorf("Untracked: expected 1, got %d", got.Untracked)
	}
}

func TestParseGitStatusDetachedHead(t *testing.T) {
	got := parseGitStatus("BRANCH:\nHEAD:0f2a4a9\n")
	if !got.IsRepo || got.Branch != "" || got.Head != "0f2a4a9" {
		t.Errorf("detached HEAD parse wrong: %+v", got)
	}
}
