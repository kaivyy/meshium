package transfer

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// execLocalRsync runs rsync locally using os/exec.
// It captures stdout/stderr and parses progress output.
// argv is the full argument vector (argv[0] is the program name); it is passed
// straight to exec.CommandContext with no shell, so arguments containing spaces
// or quote characters are handled correctly without any quoting.
func execLocalRsync(ctx context.Context, argv []string, opts TransferOptions, progressParser *rsyncProgressParser) (*TransferResult, error) {
	if len(argv) == 0 {
		return nil, fmt.Errorf("empty rsync command")
	}

	// Create exec.Command
	execCmd := exec.CommandContext(ctx, argv[0], argv[1:]...)

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
			return nil, fmt.Errorf("rsync failed: %w: %v", ErrTransferInactivity, ctx.Err())
		}
		// Map rsync's exit code to a typed error when available; otherwise
		// wrap the raw failure so a partial/unknown exit is never swallowed.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			if codeErr := classifyRsyncExit(exitErr.ExitCode()); codeErr != nil {
				return nil, fmt.Errorf("rsync failed: %w: %s", codeErr, stderrBuilder.String())
			}
			// exit 0 would not have errored; fall through to generic wrap
		}
		return nil, fmt.Errorf("rsync failed: %w: %s", err, stderrBuilder.String())
	}

	result := &TransferResult{
		BytesTransferred: 0, // rsync doesn't report exact bytes in a simple way
	}
	return result, nil
}
