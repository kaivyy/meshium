package shared

import (
	"strings"
	"testing"
)

func TestValidateRemotePathAcceptsOrdinaryPaths(t *testing.T) {
	valid := []string{
		"/etc/nginx/nginx.conf",
		"/var/log",
		"relative/dir",
		".",
		"..",
		"../sibling",           // relative navigation is a legit file-browser feature
		"/home/user/my file",   // spaces are fine; ShellQuote handles them
		"/data/'quoted'",       // single quotes are fine; ShellQuote handles them
		"/tmp/a;b|c&d",         // shell metacharacters are fine; ShellQuote handles them
	}
	for _, p := range valid {
		if err := ValidateRemotePath(p); err != nil {
			t.Errorf("ValidateRemotePath(%q) = %v, want nil", p, err)
		}
	}
}

func TestValidateRemotePathRejectsEmpty(t *testing.T) {
	if err := ValidateRemotePath(""); err == nil {
		t.Error("expected error for empty path")
	}
}

func TestValidateRemotePathRejectsNUL(t *testing.T) {
	if err := ValidateRemotePath("/etc/passwd\x00.txt"); err == nil {
		t.Error("expected error for NUL byte in path")
	}
}

func TestValidateRemotePathRejectsControlChars(t *testing.T) {
	// A newline could smuggle a second command into an unquoted call site.
	cases := []string{"/etc/\npasswd", "/var/\rlog", "/tmp/\x1bfile"}
	for _, p := range cases {
		if err := ValidateRemotePath(p); err == nil {
			t.Errorf("expected error for control character in %q", p)
		}
	}
}

func TestValidateRemotePathRejectsOverlong(t *testing.T) {
	long := "/" + strings.Repeat("a", MaxPathLength)
	if err := ValidateRemotePath(long); err == nil {
		t.Error("expected error for overlong path")
	}
}

func TestIsPathWithinRoot(t *testing.T) {
	cases := []struct {
		name   string
		root   string
		target string
		want   bool
	}{
		{"direct child", "/srv/data", "/srv/data/file.txt", true},
		{"root itself", "/srv/data", "/srv/data", true},
		{"nested child", "/srv/data", "/srv/data/a/b/c", true},
		{"traversal escapes", "/srv/data", "/srv/data/../secret", false},
		{"sibling prefix not contained", "/srv/data", "/srv/data-evil/x", false},
		{"unrelated path", "/srv/data", "/etc/passwd", false},
		{"traversal back in", "/srv/data", "/srv/data/../data/ok", true},
		{"filesystem root contains all", "/", "/anything/here", true},
		{"empty root", "", "/srv/data", false},
		{"empty target", "/srv/data", "", false},
	}
	for _, c := range cases {
		if got := IsPathWithinRoot(c.root, c.target); got != c.want {
			t.Errorf("%s: IsPathWithinRoot(%q, %q) = %v, want %v", c.name, c.root, c.target, got, c.want)
		}
	}
}

func TestIsPathWithinRootBlocksClassicTraversal(t *testing.T) {
	// The canonical attack: dotted segments trying to climb out of the root.
	if IsPathWithinRoot("/srv/uploads", "/srv/uploads/../../etc/shadow") {
		t.Error("path traversal to /etc/shadow was not blocked")
	}
}
