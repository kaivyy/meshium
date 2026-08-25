package gsync

import (
	"context"
	"fmt"
	"os/exec"
)

// BinaryRunner shells out to the real rclone binary. The config path, source,
// and destination go into argv — never through a shell — so no injection
// surface exists beyond what UpsertPair already rejects.
type BinaryRunner struct {
	// Bin is the binary name/path; defaults to "rclone".
	Bin string
}

func (r *BinaryRunner) Run(ctx context.Context, cfgPath, src, dst string) (string, error) {
	bin := r.Bin
	if bin == "" {
		bin = "rclone"
	}
	cmd := exec.CommandContext(ctx, bin, "sync", src, dst,
		"--config", cfgPath,
		"--auto-confirm",
	)
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return string(out), fmt.Errorf("sync cancelled: %w", ctx.Err())
	}
	return string(out), err
}

// Available reports whether the binary can be located.
func (r *BinaryRunner) Available() bool {
	bin := r.Bin
	if bin == "" {
		bin = "rclone"
	}
	_, err := exec.LookPath(bin)
	return err == nil
}
