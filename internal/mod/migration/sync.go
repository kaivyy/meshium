package migration

import (
	"context"
	"fmt"
	"log"
	"path"
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
	SourcePath        string   `json:"sourcePath"`
	TargetPath        string   `json:"targetPath"`
	TargetHost        string   `json:"targetHost"`
	TargetPort        int      `json:"targetPort"`
	TargetUser        string   `json:"targetUser"`
	BandwidthLimit    int64    `json:"bandwidthLimit,omitempty"`
	ParallelTransfers int      `json:"parallelTransfers,omitempty"`
	ExcludePatterns   []string `json:"excludePatterns,omitempty"`
	Compress          bool     `json:"compress,omitempty"`
	ChecksumVerify    bool     `json:"checksumVerify,omitempty"`
	PreservePerms     bool     `json:"preservePerms"`
	PreserveOwner     bool     `json:"preserveOwner"`
	PreserveGroup     bool     `json:"preserveGroup"`
	PreserveSymlinks  bool     `json:"preserveSymlinks"`
	PreserveTimes     bool     `json:"preserveTimes"`
	Sparse            bool     `json:"sparse"`
	DeleteExtraneous  bool     `json:"deleteExtraneous"`
	DryRun            bool     `json:"dryRun"`
	MigrationID       int      `json:"migrationId"`
}

// rsyncProgressRegex matches rsync --progress output lines.
var rsyncProgressRegex = regexp.MustCompile(`(\d+)\s+(\d+)%\s+([\d.]+)([KMG]?)B/s`)

func normalizeSyncPath(p string) string {
	if p == "" {
		return "/"
	}
	if p == "/" {
		return "/"
	}
	return strings.TrimRight(p, "/")
}

func syncPathWithTrailingSlash(p string) string {
	normalized := normalizeSyncPath(p)
	if normalized == "/" {
		return "/"
	}
	return normalized + "/"
}

func buildRsyncSSHTransport(port int) string {
	if port <= 0 {
		port = 22
	}
	return fmt.Sprintf("ssh -p %d -o StrictHostKeyChecking=accept-new", port)
}

func buildRsyncRemoteSpec(user, host, targetPath string) string {
	remotePath := syncPathWithTrailingSlash(targetPath)
	if user == "" {
		return fmt.Sprintf("%s:%s", host, remotePath)
	}
	return fmt.Sprintf("%s@%s:%s", user, host, remotePath)
}

func syncFileCountCommand(rootPath string) string {
	return fmt.Sprintf("find %s -type f 2>/dev/null | wc -l", shared.ShellQuote(normalizeSyncPath(rootPath)))
}

func syncChecksumCommand(filePath string) string {
	return fmt.Sprintf("md5sum %s 2>/dev/null", shared.ShellQuote(filePath))
}

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
	output, stderr, exitCode, err := e.sourceSSH.ExecContext(ctx, cmd)
	if err != nil || exitCode != 0 {
		msg := rsyncExecError(err, exitCode, stderr)
		if cerr := e.repo.CompleteSyncSession(ctx, sessionID, "error", msg); cerr != nil {
			log.Printf("initial sync: failed to mark session %d as error: %v", sessionID, cerr)
		}
		return nil, fmt.Errorf("rsync failed: %s", msg)
	}

	// Parse rsync output for stats
	bytesTransferred, filesTransferred, speed := parseRsyncOutput(output)
	if uerr := e.repo.UpdateSyncSession(ctx, sessionID, bytesTransferred, filesTransferred, speed, "completed"); uerr != nil {
		log.Printf("initial sync: failed to update session %d stats: %v", sessionID, uerr)
	}
	if cerr := e.repo.CompleteSyncSession(ctx, sessionID, "completed", ""); cerr != nil {
		log.Printf("initial sync: failed to mark session %d as completed: %v", sessionID, cerr)
	}

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

	output, stderr, exitCode, err := e.sourceSSH.ExecContext(ctx, cmd)
	if err != nil || exitCode != 0 {
		msg := rsyncExecError(err, exitCode, stderr)
		if cerr := e.repo.CompleteSyncSession(ctx, newSessionID, "error", msg); cerr != nil {
			log.Printf("delta sync: failed to mark session %d as error: %v", newSessionID, cerr)
		}
		return nil, fmt.Errorf("delta rsync failed: %s", msg)
	}

	bytesTransferred, filesTransferred, speed := parseRsyncOutput(output)
	if uerr := e.repo.UpdateSyncSession(ctx, newSessionID, bytesTransferred, filesTransferred, speed, "completed"); uerr != nil {
		log.Printf("delta sync: failed to update session %d stats: %v", newSessionID, uerr)
	}
	if cerr := e.repo.CompleteSyncSession(ctx, newSessionID, "completed", ""); cerr != nil {
		log.Printf("delta sync: failed to mark session %d as completed: %v", newSessionID, cerr)
	}

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

	// Compare checksums by content, keyed on the path relative to each root.
	// The raw md5sum lines are "<hash>  <absolute-path>"; source and target
	// roots differ, so comparing whole lines always mismatches. Strip the root
	// prefix and compare only the hash per relative path.
	sourceHashes := parseChecksumsByRelPath(sourceChecksums, sourcePath)
	targetHashes := parseChecksumsByRelPath(targetChecksums, targetPath)

	if len(sourceHashes) != len(targetHashes) {
		return fmt.Errorf("file count mismatch: source=%d target=%d", len(sourceHashes), len(targetHashes))
	}

	mismatches := 0
	for rel, srcHash := range sourceHashes {
		if tgtHash, ok := targetHashes[rel]; !ok || tgtHash != srcHash {
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
		Expected:         fmt.Sprintf("%d files", len(sourceHashes)),
		Actual:           fmt.Sprintf("%d files, %d mismatches", len(targetHashes), mismatches),
		Passed:           mismatches == 0,
	})

	return nil
}

// parseChecksumsByRelPath parses `md5sum` output lines of the form
// "<hash>  <absolute-path>" into a map of path-relative-to-root -> hash.
// Stripping the differing source/target root lets callers compare content
// hashes for the same logical file rather than lines that include the path.
func parseChecksumsByRelPath(output, root string) map[string]string {
	result := make(map[string]string)
	root = strings.TrimRight(root, "/")
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		hash := fields[0]
		// The filename follows the hash and a 2-char delimiter (space + space
		// for text mode, or space + '*' for binary mode). Preserve any spaces
		// within the filename by slicing rather than re-splitting on fields.
		full := strings.TrimLeft(line[len(hash):], " ")
		full = strings.TrimPrefix(full, "*")
		rel := full
		if root != "" && root != "/" {
			rel = strings.TrimPrefix(full, root)
		}
		rel = strings.TrimPrefix(rel, "/")
		result[rel] = hash
	}
	return result
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

	// Re-run with the same resumable rsync command.
	cmd := e.buildRsyncCommand(config, true)

	output, stderr, exitCode, err := e.sourceSSH.ExecContext(ctx, cmd)
	if err != nil || exitCode != 0 {
		msg := rsyncExecError(err, exitCode, stderr)
		if cerr := e.repo.CompleteSyncSession(ctx, sessionID, "error", msg); cerr != nil {
			log.Printf("resume sync: failed to mark session %d as error: %v", sessionID, cerr)
		}
		return fmt.Errorf("resume rsync failed: %s", msg)
	}

	bytesTransferred, filesTransferred, speed := parseRsyncOutput(output)
	if uerr := e.repo.UpdateSyncSession(ctx, sessionID, bytesTransferred, filesTransferred, speed, "completed"); uerr != nil {
		log.Printf("resume sync: failed to update session %d stats: %v", sessionID, uerr)
	}
	if cerr := e.repo.CompleteSyncSession(ctx, sessionID, "completed", ""); cerr != nil {
		log.Printf("resume sync: failed to mark session %d as completed: %v", sessionID, cerr)
	}
	return nil
}

// buildRsyncCommand constructs an rsync command string.
func (e *SyncEngine) buildRsyncCommand(config SyncConfig, incremental bool) string {
	args := []string{"rsync", "-avz", "--progress", "--stats", "--partial", "--append-verify"}

	if incremental {
		args = append(args, "--update")
	}

	if config.BandwidthLimit > 0 {
		args = append(args, fmt.Sprintf("--bwlimit=%d", config.BandwidthLimit))
	}

	if config.DeleteExtraneous {
		args = append(args, "--delete")
	}

	if config.DryRun {
		args = append(args, "--dry-run")
	}

	if config.Sparse {
		args = append(args, "--sparse")
	}

	if config.ChecksumVerify {
		args = append(args, "--checksum")
	}

	for _, pattern := range config.ExcludePatterns {
		args = append(args, fmt.Sprintf("--exclude=%s", shared.ShellQuote(pattern)))
	}

	if config.PreservePerms {
		args = append(args, "-p")
	}
	if config.PreserveOwner {
		args = append(args, "-o")
	}
	if config.PreserveGroup {
		args = append(args, "-g")
	}
	if config.PreserveSymlinks {
		args = append(args, "-l")
	}
	if config.PreserveTimes {
		args = append(args, "-t")
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

	sourcePath := syncPathWithTrailingSlash(config.SourcePath)
	targetPath := syncPathWithTrailingSlash(config.TargetPath)
	if config.TargetHost != "" {
		if config.TargetPath == "" {
			targetPath = sourcePath
		}
		args = append(args, "-e", fmt.Sprintf("\"%s\"", buildRsyncSSHTransport(config.TargetPort)))
		args = append(args, shared.ShellQuote(sourcePath), shared.ShellQuote(buildRsyncRemoteSpec(config.TargetUser, config.TargetHost, targetPath)))
		return strings.Join(args, " ")
	}

	if config.TargetPath == "" {
		targetPath = sourcePath
	}
	args = append(args, shared.ShellQuote(sourcePath), shared.ShellQuote(targetPath))
	return strings.Join(args, " ")
}

// VerifySync compares file counts and critical checksums between source and target.
func (e *SyncEngine) VerifySync(ctx context.Context, config SyncConfig) error {
	if e == nil || e.sourceSSH == nil || e.targetSSH == nil {
		return fmt.Errorf("sync verification requires source and target SSH connections")
	}

	sourceRoot := normalizeSyncPath(config.SourcePath)
	targetRoot := normalizeSyncPath(config.TargetPath)
	if config.TargetPath == "" {
		targetRoot = sourceRoot
	}

	sourceCount, err := e.runSyncFileCount(ctx, e.sourceSSH, sourceRoot)
	if err != nil {
		return fmt.Errorf("count source files: %w", err)
	}
	targetCount, err := e.runSyncFileCount(ctx, e.targetSSH, targetRoot)
	if err != nil {
		return fmt.Errorf("count target files: %w", err)
	}

	if sourceCount != targetCount {
		e.recordSyncVerificationResult(ctx, config.MigrationID, sourceRoot, targetRoot, false, fmt.Sprintf("%d files", sourceCount), fmt.Sprintf("%d files", targetCount), fmt.Sprintf("file count mismatch: source=%d target=%d", sourceCount, targetCount))
		return fmt.Errorf("file count mismatch: source=%d target=%d", sourceCount, targetCount)
	}

	if config.ChecksumVerify {
		if err := e.verifyCriticalSyncFiles(ctx, sourceRoot, targetRoot); err != nil {
			e.recordSyncVerificationResult(ctx, config.MigrationID, sourceRoot, targetRoot, false, fmt.Sprintf("%d files", sourceCount), fmt.Sprintf("%d files", targetCount), err.Error())
			return err
		}
	}

	e.recordSyncVerificationResult(ctx, config.MigrationID, sourceRoot, targetRoot, true, fmt.Sprintf("%d files", sourceCount), fmt.Sprintf("%d files", targetCount), "")
	return nil
}

func (e *SyncEngine) runSyncFileCount(ctx context.Context, ssh SSHExecuter, rootPath string) (int, error) {
	cmd := syncFileCountCommand(rootPath)
	stdout, _, _, err := ssh.ExecContext(ctx, cmd)
	if err != nil {
		return 0, err
	}

	count, err := strconv.Atoi(strings.TrimSpace(stdout))
	if err != nil {
		return 0, fmt.Errorf("parse file count %q: %w", strings.TrimSpace(stdout), err)
	}
	return count, nil
}

func (e *SyncEngine) verifyCriticalSyncFiles(ctx context.Context, sourceRoot, targetRoot string) error {
	criticalFiles := []string{
		"etc/passwd",
		"etc/group",
		"etc/hosts",
		"etc/ssh/sshd_config",
	}

	for _, rel := range criticalFiles {
		sourceChecksum, err := e.runSyncChecksum(ctx, e.sourceSSH, path.Join(sourceRoot, rel))
		if err != nil {
			return fmt.Errorf("source checksum %s: %w", rel, err)
		}
		targetChecksum, err := e.runSyncChecksum(ctx, e.targetSSH, path.Join(targetRoot, rel))
		if err != nil {
			return fmt.Errorf("target checksum %s: %w", rel, err)
		}

		if sourceChecksum == "" || targetChecksum == "" {
			continue
		}
		if sourceChecksum != targetChecksum {
			return fmt.Errorf("checksum mismatch for %s", rel)
		}
	}
	return nil
}

func (e *SyncEngine) runSyncChecksum(ctx context.Context, ssh SSHExecuter, filePath string) (string, error) {
	cmd := syncChecksumCommand(filePath)
	stdout, _, _, err := ssh.ExecContext(ctx, cmd)
	if err != nil {
		return "", err
	}

	fields := strings.Fields(strings.TrimSpace(stdout))
	if len(fields) == 0 {
		return "", nil
	}
	return fields[0], nil
}

func (e *SyncEngine) recordSyncVerificationResult(ctx context.Context, migrationID int, sourceRoot, targetRoot string, passed bool, expected, actual, errMsg string) {
	if e == nil || e.repo == nil {
		return
	}

	_, _ = e.repo.CreateVerificationResult(ctx, VerificationResult{
		MigrationID:      migrationID,
		VerificationType: "sync",
		Target:           targetRoot,
		Expected:         expected,
		Actual:           actual,
		Passed:           passed,
		ErrorMessage:     errMsg,
	})
}

// rsyncExecError builds a diagnostic message for a failed rsync invocation.
// ExecContext returns a nil error when the remote command runs but exits
// non-zero (the failure is carried in exitCode), so callers must inspect the
// exit code as well as err. rsync exit codes such as 23 (partial transfer),
// 24 (files vanished), 12 (protocol error), and 11 (I/O error) all indicate
// an incomplete transfer that must not be treated as success.
func rsyncExecError(err error, exitCode int, stderr string) string {
	stderr = strings.TrimSpace(stderr)
	if err != nil {
		if stderr != "" {
			return fmt.Sprintf("%v: %s", err, stderr)
		}
		return err.Error()
	}
	if stderr != "" {
		return fmt.Sprintf("rsync exited with code %d: %s", exitCode, stderr)
	}
	return fmt.Sprintf("rsync exited with code %d", exitCode)
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
