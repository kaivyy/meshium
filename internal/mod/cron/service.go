package cron

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"meshium/internal/mod/discovery"
	"meshium/internal/mod/server"
	modssh "meshium/internal/mod/ssh"
	"meshium/internal/mod/transport"
	"meshium/internal/shared"
)

var validCronSchedule = regexp.MustCompile(`^(\S+\s+){4}\S+$`)
var envLinePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=.*$`)

// CronJob represents a parsed cron entry.
type CronJob struct {
	ID       string `json:"id"`       // line number as ID
	Schedule string `json:"schedule"` // cron expression
	Command  string `json:"command"`
	User     string `json:"user"`    // user whose crontab this belongs to
	Source   string `json:"source"`  // "user", "system", "cron.d", etc.
	Comment  string `json:"comment"` // comment lines above the job
	Enabled  bool   `json:"enabled"`
}

// CronJobRequest is the request payload for creating or updating cron jobs.
type CronJobRequest struct {
	Schedule string `json:"schedule"`
	Command  string `json:"command"`
	Comment  string `json:"comment"`
}

// Service manages cron jobs on remote servers.
type Service struct {
	srvRepo    server.Repo
	pool       *modssh.Pool
	authSvc    transport.AESKeyProvider
	knownHosts transport.HostKeyStore
}

// NewService creates a cron service.
func NewService(
	srvRepo server.Repo,
	pool *modssh.Pool,
	authSvc transport.AESKeyProvider,
	knownHosts transport.HostKeyStore,
) *Service {
	return &Service{srvRepo: srvRepo, pool: pool, authSvc: authSvc, knownHosts: knownHosts}
}

// ListCronJobs returns cron jobs from the user's crontab and common system locations.
func (s *Service) ListCronJobs(ctx context.Context, serverID int) ([]CronJob, error) {
	srv, sshClient, err := s.getSSHClient(ctx, serverID)
	if err != nil {
		return nil, err
	}

	jobs := make([]CronJob, 0)

	if stdout, err := s.execText(ctx, sshClient, "crontab -l 2>/dev/null"); err == nil && strings.TrimSpace(stdout) != "" {
		jobs = append(jobs, parseCronText(stdout, "user", srv.Username, true)...)
	}

	if stdout, err := s.execText(ctx, sshClient, "cat /etc/crontab 2>/dev/null"); err == nil && strings.TrimSpace(stdout) != "" {
		jobs = append(jobs, parseCronText(stdout, "system", "root", false)...)
	}

	if stdout, err := s.execText(ctx, sshClient, `for f in /etc/cron.d/*; do [ -f "$f" ] || continue; printf '###FILE:%s###\n' "$f"; cat "$f"; printf '\n###ENDFILE###\n'; done 2>/dev/null`); err == nil && strings.TrimSpace(stdout) != "" {
		jobs = append(jobs, parseCronDOutput(stdout)...)
	}

	jobs = append(jobs, s.listScheduledScripts(ctx, sshClient, "/etc/cron.hourly", "cron.hourly", "0 * * * *")...)
	jobs = append(jobs, s.listScheduledScripts(ctx, sshClient, "/etc/cron.daily", "cron.daily", "0 0 * * *")...)
	jobs = append(jobs, s.listScheduledScripts(ctx, sshClient, "/etc/cron.weekly", "cron.weekly", "0 0 * * 0")...)
	jobs = append(jobs, s.listScheduledScripts(ctx, sshClient, "/etc/cron.monthly", "cron.monthly", "0 0 1 * *")...)

	return jobs, nil
}

// AddCronJob adds a cron job to the user's crontab.
func (s *Service) AddCronJob(ctx context.Context, serverID int, job CronJobRequest) error {
	if err := validateCronJobRequest(job); err != nil {
		return err
	}

	_, sshClient, err := s.getSSHClient(ctx, serverID)
	if err != nil {
		return err
	}

	current, err := s.readUserCrontab(ctx, sshClient)
	if err != nil {
		return err
	}

	newContent := buildAppendedCrontab(current, job)
	return s.installCrontab(ctx, sshClient, newContent)
}

// UpdateCronJob updates a specific cron job by line number in the user's crontab.
func (s *Service) UpdateCronJob(ctx context.Context, serverID int, jobID string, job CronJobRequest) error {
	if err := validateCronJobRequest(job); err != nil {
		return err
	}

	lineNo, err := strconv.Atoi(jobID)
	if err != nil || lineNo <= 0 {
		return fmt.Errorf("invalid cron job id")
	}

	_, sshClient, err := s.getSSHClient(ctx, serverID)
	if err != nil {
		return err
	}

	current, err := s.readUserCrontab(ctx, sshClient)
	if err != nil {
		return err
	}

	updated, err := replaceCronLine(current, lineNo, job)
	if err != nil {
		return err
	}

	return s.installCrontab(ctx, sshClient, updated)
}

// DeleteCronJob removes a cron job by line number in the user's crontab.
func (s *Service) DeleteCronJob(ctx context.Context, serverID int, jobID string) error {
	lineNo, err := strconv.Atoi(jobID)
	if err != nil || lineNo <= 0 {
		return fmt.Errorf("invalid cron job id")
	}

	_, sshClient, err := s.getSSHClient(ctx, serverID)
	if err != nil {
		return err
	}

	current, err := s.readUserCrontab(ctx, sshClient)
	if err != nil {
		return err
	}

	updated, err := deleteCronLine(current, lineNo)
	if err != nil {
		return err
	}

	return s.installCrontab(ctx, sshClient, updated)
}

// GetCronJob returns a specific cron job by line number.
func (s *Service) GetCronJob(ctx context.Context, serverID int, jobID string) (*CronJob, error) {
	jobs, err := s.ListCronJobs(ctx, serverID)
	if err != nil {
		return nil, err
	}
	for _, job := range jobs {
		if job.ID == jobID {
			copy := job
			return &copy, nil
		}
	}
	return nil, fmt.Errorf("cron job not found")
}

func validateCronJobRequest(job CronJobRequest) error {
	if strings.TrimSpace(job.Schedule) == "" {
		return fmt.Errorf("schedule is required")
	}
	if !validCronSchedule.MatchString(strings.TrimSpace(job.Schedule)) {
		return fmt.Errorf("invalid cron schedule: must be a 5-field cron expression")
	}
	if strings.TrimSpace(job.Command) == "" {
		return fmt.Errorf("command is required")
	}
	return nil
}

func (s *Service) getSSHClient(ctx context.Context, serverID int) (*server.Server, transport.SSHExecuter, error) {
	srv, err := s.srvRepo.GetByID(serverID)
	if err != nil {
		return nil, nil, fmt.Errorf("server not found: %w", err)
	}
	if s.pool == nil {
		return nil, nil, fmt.Errorf("ssh pool is not configured")
	}

	aesKey := s.authSvc.GetAESKey()
	if aesKey == nil {
		return nil, nil, fmt.Errorf("app is locked")
	}

	password, err := decryptCredential(aesKey, srv.Password)
	if err != nil {
		return nil, nil, fmt.Errorf("decrypt password: %w", err)
	}
	sshKey, err := decryptCredential(aesKey, srv.SSHKey)
	if err != nil {
		return nil, nil, fmt.Errorf("decrypt ssh key: %w", err)
	}
	passphrase, err := decryptCredential(aesKey, srv.Passphrase)
	if err != nil {
		return nil, nil, fmt.Errorf("decrypt passphrase: %w", err)
	}

	cfg := modssh.ServerConfig{
		Host:       srv.Host,
		Port:       srv.Port,
		Username:   srv.Username,
		Password:   password,
		Passphrase: passphrase,
	}
	if sshKey != "" {
		cfg.PrivateKey = []byte(sshKey)
	}

	hostKeyCallback := s.knownHosts.MakeHostKeyCallback(serverID)
	client, err := discovery.NewPoolAdapter(s.pool).GetContext(ctx, serverID, cfg, hostKeyCallback)
	if err != nil {
		return nil, nil, fmt.Errorf("SSH connection failed: %w", err)
	}
	return srv, client, nil
}

func (s *Service) execText(ctx context.Context, sshClient transport.SSHExecuter, cmd string) (string, error) {
	stdout, stderr, exitCode, err := sshClient.ExecContext(ctx, cmd)
	if err != nil {
		if exitCode != 0 && strings.TrimSpace(stderr) != "" {
			return stdout, errors.New(strings.TrimSpace(stderr))
		}
		return stdout, err
	}
	if exitCode != 0 {
		if strings.TrimSpace(stderr) != "" {
			return stdout, errors.New(strings.TrimSpace(stderr))
		}
		return stdout, fmt.Errorf("command failed with exit code %d", exitCode)
	}
	return stdout, nil
}

func (s *Service) readUserCrontab(ctx context.Context, sshClient transport.SSHExecuter) (string, error) {
	stdout, stderr, _, err := sshClient.ExecContext(ctx, "crontab -l 2>/dev/null || true")
	if err != nil {
		if strings.TrimSpace(stderr) != "" {
			return "", errors.New(strings.TrimSpace(stderr))
		}
		return "", err
	}
	return stdout, nil
}

func (s *Service) installCrontab(ctx context.Context, sshClient transport.SSHExecuter, content string) error {
	cmd := fmt.Sprintf("tmp=$(mktemp) && %s > \"$tmp\" && crontab \"$tmp\" && rm -f \"$tmp\"", shared.Base64EncodeForShell([]byte(content)))
	_, stderr, _, err := sshClient.ExecContext(ctx, cmd)
	if err != nil {
		if strings.TrimSpace(stderr) != "" {
			return fmt.Errorf("install crontab failed: %s", strings.TrimSpace(stderr))
		}
		return fmt.Errorf("install crontab failed: %w", err)
	}
	return nil
}

func buildAppendedCrontab(current string, job CronJobRequest) string {
	lines := normalizeLines(current)
	block := append(splitCommentLines(job.Comment), fmt.Sprintf("%s %s", strings.TrimSpace(job.Schedule), strings.TrimSpace(job.Command)))
	lines = append(lines, block...)
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

func replaceCronLine(current string, lineNo int, job CronJobRequest) (string, error) {
	lines := normalizeLines(current)
	if lineNo > len(lines) {
		return "", fmt.Errorf("cron job not found")
	}

	start, end, err := findJobBlock(lines, lineNo)
	if err != nil {
		return "", err
	}

	replacement := append(splitCommentLines(job.Comment), fmt.Sprintf("%s %s", strings.TrimSpace(job.Schedule), strings.TrimSpace(job.Command)))
	updated := append([]string{}, lines[:start]...)
	updated = append(updated, replacement...)
	updated = append(updated, lines[end:]...)
	if len(updated) == 0 {
		return "", nil
	}
	return strings.Join(updated, "\n") + "\n", nil
}

func deleteCronLine(current string, lineNo int) (string, error) {
	lines := normalizeLines(current)
	if lineNo > len(lines) {
		return "", fmt.Errorf("cron job not found")
	}

	start, end, err := findJobBlock(lines, lineNo)
	if err != nil {
		return "", err
	}

	updated := append([]string{}, lines[:start]...)
	updated = append(updated, lines[end:]...)
	if len(updated) == 0 {
		return "", nil
	}
	return strings.Join(updated, "\n") + "\n", nil
}

func normalizeLines(content string) []string {
	if content == "" {
		return nil
	}
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func findJobBlock(lines []string, lineNo int) (int, int, error) {
	idx := lineNo - 1
	if idx < 0 || idx >= len(lines) {
		return 0, 0, fmt.Errorf("cron job not found")
	}
	line := strings.TrimSpace(lines[idx])
	if !isCronLine(line) {
		return 0, 0, fmt.Errorf("selected line is not a cron job")
	}

	start := idx
	for start > 0 {
		prev := strings.TrimSpace(lines[start-1])
		if prev == "" {
			break
		}
		if !strings.HasPrefix(prev, "#") {
			break
		}
		candidate := strings.TrimSpace(strings.TrimPrefix(prev, "#"))
		if candidate == "" || envLinePattern.MatchString(candidate) || isCronSyntaxLine(candidate, true) {
			break
		}
		start--
	}
	return start, idx + 1, nil
}

func isCronLine(line string) bool {
	if line == "" {
		return false
	}
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "#") {
		trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "#"))
	}
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return false
	}
	if strings.HasPrefix(trimmed, "@") {
		return true
	}
	fields := strings.Fields(trimmed)
	return len(fields) >= 6
}

func splitCommentLines(comment string) []string {
	comment = strings.TrimSpace(comment)
	if comment == "" {
		return nil
	}
	parts := strings.Split(comment, "\n")
	lines := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		lines = append(lines, "# "+part)
	}
	return lines
}

func parseCronText(output, source, user string, userCrontab bool) []CronJob {
	lines := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")
	jobs := make([]CronJob, 0)
	pendingComments := make([]string, 0)

	flush := func() {
		pendingComments = nil
	}

	for i, rawLine := range lines {
		lineNo := i + 1
		line := strings.TrimSpace(rawLine)
		if line == "" {
			flush()
			continue
		}
		if envLinePattern.MatchString(line) {
			flush()
			continue
		}
		if strings.HasPrefix(line, "#") {
			candidate := strings.TrimSpace(strings.TrimPrefix(line, "#"))
			if isCronSyntaxLine(candidate, userCrontab) {
				jobs = append(jobs, parseJobLine(candidate, source, user, lineNo, false, pendingComments, userCrontab))
				flush()
				continue
			}
			pendingComments = append(pendingComments, candidate)
			continue
		}

		if isCronSyntaxLine(line, userCrontab) {
			jobs = append(jobs, parseJobLine(line, source, user, lineNo, true, pendingComments, userCrontab))
			flush()
			continue
		}

		flush()
	}

	return jobs
}

func parseCronDOutput(output string) []CronJob {
	jobs := make([]CronJob, 0)
	lines := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")
	currentFile := ""
	buffer := make([]string, 0)

	flush := func() {
		if currentFile != "" {
			content := strings.Join(buffer, "\n")
			jobs = append(jobs, parseCronText(content, "cron.d", "root", false)...)
		}
		buffer = buffer[:0]
	}

	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "###FILE:"):
			flush()
			currentFile = strings.TrimSuffix(strings.TrimPrefix(line, "###FILE:"), "###")
		case line == "###ENDFILE###":
			flush()
			currentFile = ""
		default:
			if currentFile != "" {
				buffer = append(buffer, line)
			}
		}
	}
	flush()
	return jobs
}

func (s *Service) listScheduledScripts(ctx context.Context, sshClient transport.SSHExecuter, dir, source, schedule string) []CronJob {
	stdout, err := s.execText(ctx, sshClient, fmt.Sprintf(`for f in %s/*; do [ -x "$f" ] || continue; [ -f "$f" ] || continue; printf '%%s\n' "$f"; done 2>/dev/null`, shared.ShellQuote(dir)))
	if err != nil || strings.TrimSpace(stdout) == "" {
		return nil
	}

	files := strings.Split(strings.TrimSpace(stdout), "\n")
	sort.Strings(files)

	jobs := make([]CronJob, 0, len(files))
	for i, filePath := range files {
		filePath = strings.TrimSpace(filePath)
		if filePath == "" {
			continue
		}
		jobs = append(jobs, CronJob{
			ID:       strconv.Itoa(i + 1),
			Schedule: schedule,
			Command:  filePath,
			User:     "root",
			Source:   source,
			Comment:  "",
			Enabled:  true,
		})
	}
	return jobs
}

func isCronSyntaxLine(line string, userCrontab bool) bool {
	line = strings.TrimSpace(line)
	if line == "" {
		return false
	}
	if strings.HasPrefix(line, "@") {
		parts := strings.Fields(line)
		if userCrontab {
			return len(parts) >= 2
		}
		return len(parts) >= 3
	}
	fields := strings.Fields(line)
	if userCrontab {
		return len(fields) >= 6
	}
	return len(fields) >= 7
}

func parseJobLine(line, source, user string, lineNo int, enabled bool, comments []string, userCrontab bool) CronJob {
	job := CronJob{
		ID:       strconv.Itoa(lineNo),
		Source:   source,
		User:     user,
		Enabled:  enabled,
		Comment:  strings.Join(filterEmpty(comments), "\n"),
		Schedule: "",
		Command:  "",
	}

	if strings.HasPrefix(strings.TrimSpace(line), "@") {
		fields := strings.Fields(line)
		job.Schedule = fields[0]
		if userCrontab {
			job.Command = strings.Join(fields[1:], " ")
			return job
		}
		job.User = fields[1]
		job.Command = strings.Join(fields[2:], " ")
		return job
	}

	fields := strings.Fields(line)
	if userCrontab {
		job.Schedule = strings.Join(fields[:5], " ")
		job.Command = strings.Join(fields[5:], " ")
		return job
	}

	job.Schedule = strings.Join(fields[:5], " ")
	job.User = fields[5]
	job.Command = strings.Join(fields[6:], " ")
	return job
}

func filterEmpty(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if v := strings.TrimSpace(value); v != "" {
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func decryptCredential(key []byte, ciphertext string) (string, error) {
	if ciphertext == "" {
		return "", nil
	}
	decrypted, err := shared.Decrypt(key, []byte(ciphertext))
	if err != nil {
		return "", err
	}
	return string(decrypted), nil
}
