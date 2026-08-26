package file

import "strings"

// gitNotARepo is emitted by the batched git command when the target directory
// is not inside a git work tree (or git is absent remotely).
const gitNotARepo = "NOT_A_REPO"

// parseGitStatus parses the batched output produced by (*Service).GitStatus:
//
//	BRANCH:<name>
//	HEAD:<sha>
//	<porcelain v1 lines>
//
// or the single line NOT_A_REPO.
func parseGitStatus(out string) *GitStatus {
	st := &GitStatus{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		switch {
		case strings.TrimSpace(line) == "":
		case line == gitNotARepo:
			return &GitStatus{IsRepo: false}
		case strings.HasPrefix(line, "ROOT:"):
			st.Root = strings.TrimSpace(strings.TrimPrefix(line, "ROOT:"))
		case strings.HasPrefix(line, "BRANCH:"):
			st.IsRepo = true
			st.Branch = strings.TrimSpace(strings.TrimPrefix(line, "BRANCH:"))
		case strings.HasPrefix(line, "HEAD:"):
			st.Head = strings.TrimSpace(strings.TrimPrefix(line, "HEAD:"))
		default:
			st.Dirty++
			if len(line) >= 3 {
				switch x := line[0]; {
				case x == '?':
					st.Untracked++
				case x != ' ':
					st.Staged++
				}
			}
		}
	}
	return st
}
