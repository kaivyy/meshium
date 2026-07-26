package migration

import (
	"fmt"
	"strings"
	"testing"
)

// `find -size -1M` does NOT mean "smaller than 1 MiB". GNU find rounds the file
// size UP to the given unit before comparing, so a 5-byte file counts as 1M and
// `-1M` ("rounded size < 1") matches only zero-byte files.
//
// Live check that produced this test:
//
//	$ ls -l /tmp/szt      -> empty.txt 0, small.txt 5, mid.bin 512000
//	$ find /tmp/szt -type f -size -1M     -> empty.txt
//	$ find /tmp/szt -type f -size -1024k  -> all three
//
// The 'c' suffix compares raw bytes with no rounding, which is what a byte-
// valued constant like maxConfigFileSize means.
func TestConfigSizeCapComparesBytesNotRoundedMegabytes(t *testing.T) {
	arg := configSizeCapArg()
	if strings.HasSuffix(arg, "M") {
		t.Fatalf("size cap uses M units, which round up and match only empty files: %q", arg)
	}
	want := fmt.Sprintf("-size -%dc", maxConfigFileSize)
	if arg != want {
		t.Errorf("size cap = %q, want %q", arg, want)
	}
}

// `-prune` evaluates to true, and with no explicit -print GNU find applies the
// implicit -print to the whole expression — so the pruned directory itself is
// printed. `tar -T -` then recurses into that directory, collecting the entire
// subtree the prune existed to skip.
//
// Live check:
//
//	$ find /tmp/pt -path '/tmp/pt/ssl' -prune -o -type f -size -1024k
//	  /tmp/pt/keep/app.conf
//	  /tmp/pt/ssl          <-- leaked, tar then walks all of it
//	$ ... -print           -> only /tmp/pt/keep/app.conf
func TestCollectFindTerminatesWithExplicitPrint(t *testing.T) {
	cmd := buildCollectFind("/etc")
	if !strings.Contains(cmd, "-prune") {
		t.Fatal("expected prune args in the collect find")
	}
	if !strings.HasSuffix(strings.TrimSpace(cmd), "-print") {
		t.Errorf("collect find does not end in -print, so pruned directories are\n"+
			"printed and tar recurses into them:\n  %s", cmd)
	}
}

// Without -type f the expression can emit directories, which tar expands
// recursively — the same blowup the prune and the size cap try to prevent.
func TestCollectFindRestrictsToRegularFiles(t *testing.T) {
	cmd := buildCollectFind("/etc")
	if !strings.Contains(cmd, "-type f") {
		t.Errorf("collect find does not restrict to regular files:\n  %s", cmd)
	}
}

// The pruned subtrees must not survive into the printed branch.
func TestPruneArgsCoverKnownHugeDirs(t *testing.T) {
	args := buildPruneArgs()
	for _, d := range hugeConfigDirs {
		if !strings.Contains(args, fmt.Sprintf("-path '%s'", d)) {
			t.Errorf("prune args missing %s: %s", d, args)
		}
	}
	if !strings.HasSuffix(strings.TrimSpace(args), "-o") {
		t.Errorf("prune args must end in -o so the print branch follows: %s", args)
	}
}
