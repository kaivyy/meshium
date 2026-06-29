package transfer

import (
	"context"
	"fmt"
	"os"
	"strings"

	"meshium/internal/mod/transport"
)

// --- Local file helpers ---

func getLocalFileSize(path string) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, fmt.Errorf("stat local file: %w", err)
	}
	return info.Size(), nil
}

func localFileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func deleteLocalFile(path string) error {
	err := os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove local file: %w", err)
	}
	return nil
}

// --- Remote file helpers (via SSH) ---

func getRemoteFileSize(ctx context.Context, ssh transport.SSHExecuter, path string) (int64, error) {
	cmd := fmt.Sprintf("stat -c %%s '%s' 2>/dev/null || stat -f %%z '%s' 2>/dev/null", path, path)
	stdout, _, exitCode, err := ssh.ExecContext(ctx, cmd)
	if err != nil {
		return 0, fmt.Errorf("stat remote file: %w", err)
	}
	if exitCode != 0 {
		return 0, fmt.Errorf("stat remote file: exit code %d", exitCode)
	}
	stdout = strings.TrimSpace(stdout)
	var size int64
	if _, err := fmt.Sscanf(stdout, "%d", &size); err != nil {
		return 0, fmt.Errorf("parse file size from %q: %w", stdout, err)
	}
	return size, nil
}

func remoteFileExists(ctx context.Context, ssh transport.SSHExecuter, path string) (bool, error) {
	cmd := fmt.Sprintf("test -f '%s' && echo yes || echo no", path)
	stdout, _, _, err := ssh.ExecContext(ctx, cmd)
	if err != nil {
		return false, fmt.Errorf("check remote file: %w", err)
	}
	return strings.TrimSpace(stdout) == "yes", nil
}

func deleteRemoteFile(ctx context.Context, ssh transport.SSHExecuter, path string) error {
	cmd := fmt.Sprintf("rm -f '%s'", path)
	_, stderr, exitCode, err := ssh.ExecContext(ctx, cmd)
	if err != nil {
		return fmt.Errorf("remove remote file: %w", err)
	}
	if exitCode != 0 {
		return fmt.Errorf("remove remote file: exit code %d: %s", exitCode, stderr)
	}
	return nil
}

// GetRemoteDiskFree returns the available disk space (in bytes) at the
// given path on a remote server.
func GetRemoteDiskFree(ctx context.Context, ssh transport.SSHExecuter, path string) (int64, error) {
	cmd := fmt.Sprintf("df -B1 --output=avail '%s' 2>/dev/null | tail -1", path)
	stdout, _, exitCode, err := ssh.ExecContext(ctx, cmd)
	if err != nil {
		return 0, fmt.Errorf("df remote: %w", err)
	}
	if exitCode != 0 {
		return 0, fmt.Errorf("df remote: exit code %d", exitCode)
	}
	stdout = strings.TrimSpace(stdout)
	var avail int64
	if _, err := fmt.Sscanf(stdout, "%d", &avail); err != nil {
		return 0, fmt.Errorf("parse disk free from %q: %w", stdout, err)
	}
	return avail, nil
}
