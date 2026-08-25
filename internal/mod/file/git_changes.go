package file

import (
	"strconv"
	"strings"
)

// parseGitChanges parses `git status --porcelain=v1` output into per-file
// changes. Each line is "XY <path>" where X is the index (staged) column and Y
// the worktree column; "??" marks untracked. Renames/copies read
// "R  old -> new" and keep the NEW path.
func parseGitChanges(out string) []GitChange {
	var out2 []GitChange
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if len(line) < 4 {
			continue
		}
		x, y := line[0], line[1]
		rest := line[3:]

		ch := GitChange{}
		switch {
		case x == '?' && y == '?':
			ch.Untracked = true
		default:
			if x != ' ' && x != '?' {
				ch.Staged = true
			}
			if x == 'A' || y == 'A' {
				ch.Added = true
			}
			if x == 'D' || y == 'D' {
				ch.Deleted = true
			}
			if x == 'R' || x == 'C' {
				ch.Renamed = true
				if i := strings.Index(rest, " -> "); i >= 0 {
					rest = rest[i+4:]
				}
			}
		}
		ch.Path = unquoteGitPath(strings.TrimSpace(rest))
		out2 = append(out2, ch)
	}
	return out2
}

// unquoteGitPath reverses git's C-style quoting ("path with \"quote\"").
func unquoteGitPath(p string) string {
	if len(p) < 2 || p[0] != '"' || p[len(p)-1] != '"' {
		return p
	}
	if s, err := strconv.Unquote(p); err == nil {
		return s
	}
	return p
}
