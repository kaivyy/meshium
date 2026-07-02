package migration

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"meshium/internal/shared"
)

// SyncEngine handles data synchronization between source and target servers.
// Supports rsync-based initial sync, incremental delta sync, and checksum verification.
type SyncEngine struct {
	sourceSSH SSHExecuter
	targetSSH SSHExecuter
	repo      PipelineRepo
}

// NewSyncEngine creates a new sync engine.
func NewSyncEngine(sourceSSH, targetSSH SSHExecuter, repo PipelineRepo) *SyncEngine {
	return &SyncEngine{
		sourceSSH: sourceSSH,
		targetSSH: targetSSH,
		repo:      repo,
	}
}

// SyncConfig configures a data sync operation.
type SyncConfig struct {
	SourcePath      string   `json:"sourcePath"`
	TargetPath      string   `json:"targetPath"`
	BandwidthLimit  int64    `json:"bandwidthLimit,omitempty"`
	ParallelTransfers int     `json:"parallelTransfers,omitempty"`
	ExcludePatterns []string `json:"excludePatterns,omitempty"`
	Compress        bool     `json:"compress,omitempty"`
	ChecksumVerify  bool     `json:"checksumVerify,omitempty"`
	MigrationID     int      `json:"migrationId"`
}

// rsyncProgressRegex matches rsync --progress output lines.
var rsyncProgressRegex = regexp.MustCompile(`(\d+)\s+(\d+)%\s+([\d.]+)([KMG]?)B/s`)

// InitialSync performs a full rsync from source to target.
func (e *SyncEngine) InitialSync(ctx context.Context, migrationID int, config SyncConfig) (*SyncSession, error) {
	config.MigrationID = migrationID

	session := SyncSession{
		MigrationID: migrationID,
		SyncType:    "initial",
		SourcePath:  config.SourcePath,
		TargetPath:  config.TargetPath,
		Status:      "running",
	}

	sessionID, err := e.repo.CreateSyncSession(ctx, session)
	if err != nil {
		return nil, fmt.Errorf("create sync session: %w", err)
	}
	session.ID = sessionID

	// Build rsync command
	cmd := e.buildRsyncCommand(config, false)

	// Execute rsync via SSH on source, pushing to target
	output, _, _, err := e.sourceSSH.ExecContext(ctx, cmd)
	if err != nil {
		e.repo.CompleteSyncSession(ctx, sessionID, "error", err.Error())
		return nil, fmt.Errorf("rsync failed: %w", err)
	}

	// Parse rsync output for stats
	bytesTransferred, filesTransferred, speed := parseRsyncOutput(output)
	e.repo.UpdateSyncSession(ctx, sessionID, bytesTransferred, filesTransferred, speed, "completed")
	e.repo.CompleteSyncSession(ctx, sessionID, "completed", "")

	session.BytesTransferred = bytesTransferred
	session.FilesTransferred = filesTransferred
	session.SpeedBytesSec = speed
	session.Status = "completed"
	session.ChecksumVerified = config.ChecksumVerify

	return &session, nil
}

// DeltaSync performs an incremental sync (only changed files).
func (e *SyncEngine) DeltaSync(ctx context.Context, migrationID int, sessionID int64, config SyncConfig) (*SyncSession, error) {
	config.MigrationID = migrationID

	session := SyncSession{
		MigrationID: migrationID,
		SyncType:    "delta",
		SourcePath:  config.SourcePath,
		TargetPath:  config.TargetPath,
		Status:      "running",
	}

	newSessionID, err := e.repo.CreateSyncSession(ctx, session)
	if err != nil {
		return nil, fmt.Errorf("create sync session: %w", err)
	}
	session.ID = newSessionID

	// Build rsync command with --update flag for incremental
	cmd := e.buildRsyncCommand(config, true)

	output, _, _, err := e.sourceSSH.ExecContext(ctx, cmd)
	if err != nil {
		e.repo.CompleteSyncSession(ctx, newSessionID, "error", err.Error())
		return nil, fmt.Errorf("delta rsync failed: %w", err)
	}

	bytesTransferred, filesTransferred, speed := parseRsyncOutput(output)
	e.repo.UpdateSyncSession(ctx, newSessionID, bytesTransferred, filesTransferred, speed, "completed")
	e.repo.CompleteSyncSession(ctx, newSessionID, "completed", "")

	session.BytesTransferred = bytesTransferred
	session.FilesTransferred = filesTransferred
	session.SpeedBytesSec = speed
	session.Status = "completed"
	return &session, nil
}

// VerifyChecksums verifies that source and target files have matching checksums.
func (e *SyncEngine) VerifyChecksums(ctx context.Context, migrationID int, sessionID int64) error {
	sessions, err := e.repo.GetSyncSessions(migrationID)
	if err != nil || len(sessions) == 0 {
		return fmt.Errorf("no sync sessions found for migration %d", migrationID)
	}

	// Get file list from source
	sourcePath := sessions[0].SourcePath
	if sourcePath == "" {
		sourcePath = "/"
	}

	// Generate checksums on source
	sourceCmd := fmt.Sprintf("find %s -type f -exec md5sum {} \\; 2>/dev/null | sort", shared.ShellQuote(sourcePath))
	sourceChecksums, _, _, err := e.sourceSSH.ExecContext(ctx, sourceCmd)
	if err != nil {
		return fmt.Errorf("generate source checksums: %w", err)
	}

	// Generate checksums on target
	targetPath := sessions[0].TargetPath
	if targetPath == "" {
		targetPath = sourcePath
	}
	targetCmd := fmt.Sprintf("find %s -type f -exec md5sum {} \\; 2>/dev/null | sort", shared.ShellQuote(targetPath))
	targetChecksums, _, _, err := e.targetSSH.ExecContext(ctx, targetCmd)
	if err != nil {
		return fmt.Errorf("generate target checksums: %w", err)
	}

	// Compare checksums
	sourceLines := strings.Split(strings.TrimSpace(sourceChecksums), "\n")
	targetLines := strings.Split(strings.TrimSpace(targetChecksums), "\n")

	if len(sourceLines) != len(targetLines) {
		return fmt.Errorf("file count mismatch: source=%d target=%d", len(sourceLines), len(targetLines))
	}

	mismatches := 0
	for i := range sourceLines {
		if sourceLines[i] != targetLines[i] {
			mismatches++
		}
	}

	if mismatches > 0 {
		return fmt.Errorf("%d file checksum mismatches detected", mismatches)
	}

	// Record verification result
	e.repo.CreateVerificationResult(ctx, VerificationResult{
		MigrationID:      migrationID,
		VerificationType: "checksum",
		Target:           sourcePath,
		Expected:         fmt.Sprintf("%d files", len(sourceLines)),
		Actual:           fmt.Sprintf("%d files, %d mismatches", len(targetLines), mismatches),
		Passed:           mismatches == 0,
	})

	return nil
}

// ResumeSync resumes an interrupted sync session.
func (e *SyncEngine) ResumeSync(ctx context.Context, migrationID int, sessionID int64) error {
	sessions, err := e.repo.GetSyncSessions(migrationID)
	if err != nil {
		return fmt.Errorf("get sync sessions: %w", err)
	}

	var session *SyncSession
	for i := range sessions {
		if sessions[i].ID == sessionID {
			session = &sessions[i]
			break
		}
	}
	if session == nil {
		return fmt.Errorf("sync session %d not found", sessionID)
	}

	if session.Status != "error" && session.Status != "running" {
		return fmt.Errorf("cannot resume session in state %s", session.Status)
	}

	config := SyncConfig{
		SourcePath:  session.SourcePath,
		TargetPath:  session.TargetPath,
		MigrationID: migrationID,
	}

	// Re-run with --partial --append-verify to resume
	cmd := e.buildRsyncCommand(config, true)
	cmd = strings.Replace(cmd, "rsync", "rsync --partial --append-verify", 1)

	output, _, _, err := e.sourceSSH.ExecContext(ctx, cmd)
	if err != nil {
		e.repo.CompleteSyncSession(ctx, sessionID, "error", err.Error())
		return fmt.Errorf("resume rsync failed: %w", err)
	}

	bytesTransferred, filesTransferred, speed := parseRsyncOutput(output)
	e.repo.UpdateSyncSession(ctx, sessionID, bytesTransferred, filesTransferred, speed, "completed")
	e.repo.CompleteSyncSession(ctx, sessionID, "completed", "")
	return nil
}

// buildRsyncCommand constructs an rsync command string.
func (e *SyncEngine) buildRsyncCommand(config SyncConfig, incremental bool) string {
	args := []string{"rsync", "-avz", "--progress", "--stats"}

	if incremental {
		args = append(args, "--update")
	}

	if config.BandwidthLimit > 0 {
		args = append(args, fmt.Sprintf("--bwlimit=%d", config.BandwidthLimit))
	}

	if !config.Compress {
		// Remove -z if compress is disabled
		for i, arg := range args {
			if arg == "-avz" {
				args[i] = "-av"
				break
			}
		}
	}

	if config.ChecksumVerify {
		args = append(args, "--checksum")
	}

	for _, pattern := range config.ExcludePatterns {
		args = append(args, fmt.Sprintf("--exclude=%s", shared.ShellQuote(pattern)))
	}

	sourcePath := config.SourcePath
	if sourcePath == "" {
		sourcePath = "/"
	}
	targetPath := config.TargetPath
	if targetPath == "" {
		targetPath = sourcePath
	}

	args = append(args, shared.ShellQuote(sourcePath+"/"), shared.ShellQuote(targetPath+"/"))
	return strings.Join(args, " ")
}

// parseRsyncOutput extracts transfer statistics from rsync output.
func parseRsyncOutput(output string) (bytesTransferred int64, filesTransferred int, speed int64) {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)

		// Parse "Total file size: X bytes"
		if strings.Contains(line, "Total file size:") {
			parts := strings.Fields(line)
			for i, p := range parts {
				if p == "size:" && i+1 < len(parts) {
					if val, err := strconv.ParseInt(strings.ReplaceAll(parts[i+1], ",", ""), 10, 64); err == nil {
						bytesTransferred = val
					}
				}
			}
		}

		// Parse "Number of regular files transferred: X"
		if strings.Contains(line, "Number of regular files transferred:") {
			parts := strings.Fields(line)
			if len(parts) > 0 {
				if val, err := strconv.Atoi(strings.ReplaceAll(parts[len(parts)-1], ",", "")); err == nil {
					filesTransferred = val
				}
			}
		}

		// Parse "sent X bytes/sec"
		if strings.Contains(line, "bytes/sec") {
			matches := rsyncProgressRegex.FindStringSubmatch(line)
			if len(matches) >= 4 {
				val, _ := strconv.ParseFloat(matches[3], 64)
				switch matches[4] {
				case "K":
					speed = int64(val * 1024)
				case "M":
					speed = int64(val * 1024 * 1024)
				case "G":
					speed = int64(val * 1024 * 1024 * 1024)
				default:
					speed = int64(val)
				}
			}
		}
	}
	return
}
