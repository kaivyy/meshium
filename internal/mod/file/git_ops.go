package file

import (
	"context"
	"fmt"
	"strings"
)

// GitChanges lists the working-tree changes of the repo at path, one entry per
// file (porcelain v1, untracked included).
func (s *Service) GitChanges(ctx context.Context, serverID int, path string) ([]GitChange, error) {
	stdout, _, err := s.gitExec(ctx, serverID, path,
		fmt.Sprintf("git -C %s status --porcelain=v1 -uall", shellEscape(path)))
	if err != nil {
		return nil, err
	}
	return parseGitChanges(stdout), nil
}

// GitDiff returns `git diff HEAD` output for one pathspec. Untracked files
// yield an empty Diff with Untracked=true (diff HEAD shows nothing for them).
func (s *Service) GitDiff(ctx context.Context, serverID int, repoPath, file string) (*GitDiff, error) {
	stdout, _, err := s.gitExec(ctx, serverID, repoPath,
		fmt.Sprintf("git -C %s diff HEAD -- %s", shellEscape(repoPath), shellEscape(file)))
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(stdout) == "" {
		// Either clean/staged-only or untracked — check which.
		chs, err := s.GitChanges(ctx, serverID, repoPath)
		if err != nil {
			return nil, err
		}
		for _, ch := range chs {
			if ch.Path == file && ch.Untracked {
				return &GitDiff{Untracked: true}, nil
			}
		}
	}
	return &GitDiff{Content: stdout}, nil
}

// GitStage stages (or unstages) the given files with `git add --` / `git reset --`.
func (s *Service) GitStage(ctx context.Context, serverID int, path string, files []string, unstage bool) error {
	sub := "add"
	if unstage {
		sub = "reset"
	}
	args := make([]string, 0, len(files))
	for _, f := range files {
		args = append(args, shellEscape(f))
	}
	_, stderr, err := s.gitExec(ctx, serverID, path,
		fmt.Sprintf("git -C %s %s -- %s", shellEscape(path), sub, strings.Join(args, " ")))
	if err != nil {
		return fmt.Errorf("git %s failed: %w (stderr: %s)", sub, err, stderr)
	}
	return nil
}

// GitCommit commits everything currently staged with the given message. A
// missing committer identity falls back to Meshium defaults so first-time repos
// on fresh servers still commit; `-c user.name=` would otherwise fail.
func (s *Service) GitCommit(ctx context.Context, serverID int, path, message string) (*GitStatus, error) {
	p := shellEscape(path)
	msg := shellEscape(message)
	stdout, stderr, err := s.gitExec(ctx, serverID, path, fmt.Sprintf(
		`if [ -z "$(git -C %s config user.name)" ]; then export GIT_AUTHOR_NAME=Meshium GIT_COMMITTER_NAME=Meshium GIT_AUTHOR_EMAIL=meshium@local GIT_COMMITTER_EMAIL=meshium@local; fi; `+
			`git -C %s commit -m %s && git -C %s status --porcelain=v1`,
		p, p, msg, p))
	if err != nil {
		return nil, fmt.Errorf("git commit failed: %w (stderr: %s)", err, stderr)
	}

	// Refresh branch/HEAD/dirty counts from the same response's porcelain tail.
	st, err := s.GitStatus(ctx, serverID, path)
	if err != nil {
		return nil, err
	}
	_ = stdout // ponytail: porcelain tail unused for now; GitStatus re-queries for authoritative counts
	return st, nil
}

// gitExec runs one git command in the repo at path over SSH after verifying it
// is a work tree (NOT_A_REPO sentinel otherwise).
func (s *Service) gitExec(ctx context.Context, serverID int, path string, cmd string) (string, string, error) {
	srv, err := s.srvRepo.GetByID(serverID)
	if err != nil {
		return "", "", fmt.Errorf("server not found: %w", err)
	}
	sshClient, err := s.getSSHClient(serverID, srv)
	if err != nil {
		return "", "", fmt.Errorf("SSH connection failed: %w", err)
	}

	guard := fmt.Sprintf(
		`git -C %s rev-parse --git-dir >/dev/null 2>&1 || { echo %s; exit 0; }; `,
		shellEscape(path), gitNotARepo)
	stdout, stderr, _, err := sshClient.ExecContext(ctx, guard+cmd)
	if err != nil {
		return "", stderr, fmt.Errorf("git command failed: %w (stderr: %s)", err, stderr)
	}
	if strings.Contains(stdout, gitNotARepo+"\n") || strings.TrimSpace(stdout) == gitNotARepo {
		return "", "", ErrNotARepo
	}
	return stdout, stderr, nil
}
