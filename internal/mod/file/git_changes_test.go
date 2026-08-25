package file

import (
	"reflect"
	"testing"
)

func TestParseChangesEmpty(t *testing.T) {
	got := parseGitChanges("")
	if len(got) != 0 {
		t.Errorf("empty porcelain should give no changes, got %+v", got)
	}
}

// XY codes map to per-file flags: staged = X set; untracked = ??; deleted =
// D in either column.
func TestParseChangesCodes(t *testing.T) {
	got := parseGitChanges(
		"M  staged-mod.txt\n" +
			" M unstaged.txt\n" +
			"A  new-file.txt\n" +
			"D  gone.txt\n" +
			" D deleted-in-worktree.txt\n" +
			"MM both.txt\n" +
			"?? untracked.log\n" +
			"R  old.txt -> new.txt\n")
	want := []GitChange{
		{Path: "staged-mod.txt", Staged: true},
		{Path: "unstaged.txt"},
		{Path: "new-file.txt", Staged: true, Added: true},
		{Path: "gone.txt", Staged: true, Deleted: true},
		{Path: "deleted-in-worktree.txt", Deleted: true},
		{Path: "both.txt", Staged: true},
		{Path: "untracked.log", Untracked: true},
		{Path: "new.txt", Staged: true, Renamed: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got:\n%+v\nwant:\n%+v", got, want)
	}
}

// Quoted paths (spaces/special chars) must be unquoted back to real names —
// git emits C-style quotes when the path needs them.
func TestParseChangesUnquotesPaths(t *testing.T) {
	got := parseGitChanges("?? \"my file.txt\"\n")
	if len(got) != 1 || got[0].Path != "my file.txt" {
		t.Errorf("quoted path not unquoted: %+v", got)
	}
}
