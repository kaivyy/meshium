package transfer

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// execLocalRsync runs rsync locally using os/exec.
// It captures stdout/stderr and parses progress output.
func execLocalRsync(ctx context.Context, cmd string, opts TransferOptions, progressParser *rsyncProgressParser) (*TransferResult, error) {
	// Parse the command string into args
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return nil, fmt.Errorf("empty rsync command")
	}

	// Create exec.Command
	execCmd := exec.CommandContext(ctx, parts[0], parts[1:]...)

	// Capture stdout and stderr
	stdoutPipe, err := execCmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("create stdout pipe: %w", err)
	}
	stderrPipe, err := execCmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("create stderr pipe: %w", err)
	}

	// Start the command
	if err := execCmd.Start(); err != nil {
		return nil, fmt.Errorf("start rsync: %w", err)
	}

	// Read stderr (rsync sends progress to stderr)
	var stderrBuilder strings.Builder
	stderrDone := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(stderrPipe)
		scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			stderrBuilder.WriteString(line + "\n")
			if progressParser != nil {
				progressParser.parse(line)
			}
		}
		stderrDone <- scanner.Err()
	}()

	// Read stdout (rsync sends file list to stdout)
	var stdoutBuilder strings.Builder
	stdoutDone := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(stdoutPipe)
		scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
		for scanner.Scan() {
			stdoutBuilder.WriteString(scanner.Text() + "\n")
		}
		stdoutDone <- scanner.Err()
	}()

	// Wait for both pipes to finish
	<-stderrDone
	<-stdoutDone

	// Wait for the command to complete
	err = execCmd.Wait()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("rsync failed: %w: %s", err, stderrBuilder.String())
	}

	result := &TransferResult{
		BytesTransferred: 0, // rsync doesn't report exact bytes in a simple way
	}
	return result, nil
}
