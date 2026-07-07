package shared

import (
	"fmt"
	"path"
	"strings"
)

// MaxPathLength bounds a filesystem path. Linux PATH_MAX is 4096 bytes
// including the NUL terminator, so a path at or beyond it is malformed and is
// rejected before it can reach a remote shell.
const MaxPathLength = 4096

// ValidateRemotePath checks that a path is safe to embed in a remote shell
// command. It is defense-in-depth on top of ShellQuote: single-quoting already
// neutralizes shell metacharacters, but these invariants reject inputs that are
// dangerous regardless of quoting, or that signal a malformed/hostile request.
//
// It deliberately does NOT reject ".", "..", or relative paths. Meshium's file
// module is an authenticated admin browser over a remote filesystem, so relative
// navigation is a legitimate feature, not an attack; restricting it here would
// break that feature. A caller that must confine a path to a directory should
// layer IsPathWithinRoot on top.
func ValidateRemotePath(p string) error {
	if p == "" {
		return fmt.Errorf("path is empty")
	}
	if len(p) >= MaxPathLength {
		return fmt.Errorf("path exceeds %d bytes", MaxPathLength)
	}
	if strings.IndexByte(p, 0) >= 0 {
		return fmt.Errorf("path contains NUL byte")
	}
	// Control characters (including newline and carriage return) can smuggle a
	// second command line into any call site that ever forgets to quote, and
	// never appear in a legitimate path.
	for _, r := range p {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("path contains control character")
		}
	}
	return nil
}

// IsPathWithinRoot reports whether target resolves to a location at or below
// root after cleaning "." and ".." segments. Both are treated as absolute POSIX
// paths (remote hosts are Linux). It is the traversal-containment guard a caller
// uses when a path MUST stay inside a designated directory (for example a
// staging area). The file browser intentionally does not use it, because it
// allows full-filesystem navigation by design.
func IsPathWithinRoot(root, target string) bool {
	if root == "" || target == "" {
		return false
	}
	cleanRoot := path.Clean("/" + root)
	cleanTarget := path.Clean("/" + target)
	if cleanRoot == "/" {
		return true // root is the filesystem root; every absolute path is within it
	}
	if cleanTarget == cleanRoot {
		return true
	}
	return strings.HasPrefix(cleanTarget, cleanRoot+"/")
}
