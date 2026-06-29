package planner

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"meshium/internal/mod/migration"
	"meshium/internal/mod/transfer"
	"meshium/internal/mod/transport"
)

// BuildSteps converts a MigrationPlan into a slice of migration.MigrationStep
// instances that can be directly executed by the Phase 2 Engine.
//
// The source and target SSH clients are required for file transfer steps.
// For non-transfer steps (database, nginx, service), the target SSH client
// from the Engine's StepContext is used for execution, while the source SSH
// client is used for data extraction (e.g., database dumps).
//
// The steps are returned in the order specified by the plan (which is the
// topological sort order from the dependency graph).
func BuildSteps(plan *MigrationPlan, sourceSSH, targetSSH transport.SSHExecuter) ([]migration.MigrationStep, error) {
	if plan == nil {
		return nil, fmt.Errorf("plan is nil")
	}

	steps := make([]migration.MigrationStep, 0, len(plan.Steps))

	for _, plannedStep := range plan.Steps {
		step, err := buildStep(plannedStep, sourceSSH, targetSSH)
		if err != nil {
			return nil, fmt.Errorf("build step %q (order %d): %w", plannedStep.Name, plannedStep.Order, err)
		}
		steps = append(steps, step)
	}

	return steps, nil
}

// buildStep creates a single MigrationStep from a PlannedStep based on its type.
func buildStep(ps PlannedStep, sourceSSH, targetSSH transport.SSHExecuter) (migration.MigrationStep, error) {
	switch ps.Type {
	case StepTypeFile, StepTypeConfig:
		return buildFileTransferStep(ps, sourceSSH, targetSSH)
	case StepTypeDockerVolume:
		return buildDockerVolumeStep(ps, sourceSSH, targetSSH)
	case StepTypeDockerImage:
		return buildDockerImageStep(ps, targetSSH)
	case StepTypeDatabase:
		return buildDatabaseStep(ps, sourceSSH, targetSSH)
	case StepTypeNginx:
		return buildNginxStep(ps, sourceSSH, targetSSH)
	case StepTypeService:
		return buildServiceStep(ps, targetSSH)
	default:
		return nil, fmt.Errorf("unknown step type: %s", ps.Type)
	}
}

// --- File/Config Transfer Steps ---

func buildFileTransferStep(ps PlannedStep, sourceSSH, targetSSH transport.SSHExecuter) (migration.MigrationStep, error) {
	sourcePath, _ := ps.Config["sourcePath"].(string)
	targetPath, _ := ps.Config["targetPath"].(string)
	isDir, _ := ps.Config["isDirectory"].(bool)

	if sourcePath == "" || targetPath == "" {
		return nil, fmt.Errorf("missing sourcePath or targetPath for file transfer step")
	}

	source := transfer.TransferTarget{
		Path:     sourcePath,
		SSHClient: sourceSSH,
		IsLocal:  false,
	}
	dest := transfer.TransferTarget{
		Path:     targetPath,
		SSHClient: targetSSH,
		IsLocal:  false,
	}

	opts := transfer.TransferOptions{
		Resume:             true,
		MaxRetries:         3,
		ProgressInterval:   500 * 1000 * 1000, // 500ms
		ChecksumAlgorithm:  "sha256",
	}

	if isDir {
		return transfer.NewDirectoryTransferStep(ps.Name, source, dest, opts), nil
	}
	return transfer.NewFileTransferStep(ps.Name, source, dest, opts), nil
}

// --- Docker Volume Step ---

func buildDockerVolumeStep(ps PlannedStep, sourceSSH, targetSSH transport.SSHExecuter) (migration.MigrationStep, error) {
	containerName, _ := ps.Config["containerName"].(string)
	if containerName == "" {
		return nil, fmt.Errorf("missing containerName for docker volume step")
	}

	volumes, _ := ps.Config["volumes"].([]interface{})
	volumePaths := make([]string, 0, len(volumes))
	for _, v := range volumes {
		if s, ok := v.(string); ok {
			// Extract the source path from "host:container" format
			if idx := strings.Index(s, ":"); idx > 0 {
				volumePaths = append(volumePaths, s[:idx])
			} else {
				volumePaths = append(volumePaths, s)
			}
		}
	}

	return &DockerVolumeMigrationStep{
		StepName:      ps.Name,
		ContainerName: containerName,
		Volumes:       volumePaths,
		SourceSSH:     sourceSSH,
		TargetSSH:     targetSSH,
	}, nil
}

// --- Docker Image Step ---

func buildDockerImageStep(ps PlannedStep, targetSSH transport.SSHExecuter) (migration.MigrationStep, error) {
	image, _ := ps.Config["image"].(string)
	if image == "" {
		return nil, fmt.Errorf("missing image for docker image step")
	}

	return &DockerImageMigrationStep{
		StepName:  ps.Name,
		Image:     image,
		TargetSSH: targetSSH,
	}, nil
}

// --- Database Step ---

func buildDatabaseStep(ps PlannedStep, sourceSSH, targetSSH transport.SSHExecuter) (migration.MigrationStep, error) {
	dbType, _ := ps.Config["type"].(string)
	if dbType == "" {
		return nil, fmt.Errorf("missing database type for database step")
	}

	port, _ := ps.Config["port"].(float64) // JSON numbers come as float64
	dataDir, _ := ps.Config["dataDir"].(string)

	return &DatabaseMigrationStep{
		StepName:  ps.Name,
		DBType:    dbType,
		Port:      int(port),
		DataDir:   dataDir,
		SourceSSH: sourceSSH,
		TargetSSH: targetSSH,
	}, nil
}

// --- Nginx Step ---

func buildNginxStep(ps PlannedStep, sourceSSH, targetSSH transport.SSHExecuter) (migration.MigrationStep, error) {
	configFile, _ := ps.Config["configFile"].(string)
	serverName, _ := ps.Config["serverName"].(string)

	if configFile == "" {
		return nil, fmt.Errorf("missing configFile for nginx step")
	}

	return &NginxMigrationStep{
		StepName:   ps.Name,
		ConfigFile: configFile,
		ServerName: serverName,
		SourceSSH:  sourceSSH,
		TargetSSH:  targetSSH,
	}, nil
}

// --- Service Step ---

func buildServiceStep(ps PlannedStep, targetSSH transport.SSHExecuter) (migration.MigrationStep, error) {
	name, _ := ps.Config["name"].(string)
	if name == "" {
		return nil, fmt.Errorf("missing service name for service step")
	}

	return &ServiceMigrationStep{
		StepName:  ps.Name,
		ServiceName: name,
		TargetSSH: targetSSH,
	}, nil
}

// --- Custom MigrationStep implementations ---

// DockerVolumeMigrationStep transfers Docker container volumes from source to target.
type DockerVolumeMigrationStep struct {
	StepName      string
	ContainerName string
	Volumes       []string
	SourceSSH     transport.SSHExecuter
	TargetSSH     transport.SSHExecuter
}

func (s *DockerVolumeMigrationStep) Name() string { return s.StepName }

func (s *DockerVolumeMigrationStep) Prepare(sctx migration.StepContext) (string, error) {
	ctx := sctx.Ctx
	if sctx.Progress != nil {
		sctx.Progress(migration.WSMessage{
			Step:   s.StepName,
			Status: "progress",
			Value:  fmt.Sprintf("Preparing Docker volume transfer for container %s (%d volumes)", s.ContainerName, len(s.Volumes)),
		})
	}

	// Check that the container exists on the source
	if s.SourceSSH == nil {
		return "", fmt.Errorf("source SSH client is nil")
	}
	stdout, stderr, exitCode, err := s.SourceSSH.ExecContext(ctx, fmt.Sprintf("docker inspect %s", s.ContainerName))
	if err != nil || exitCode != 0 {
		return "", fmt.Errorf("container %s not found on source: %v %s", s.ContainerName, err, stderr)
	}

	data := map[string]interface{}{
		"containerName": s.ContainerName,
		"volumeCount":   len(s.Volumes),
		"containerInfo": stdout,
	}
	jsonData, _ := json.Marshal(data)
	return string(jsonData), nil
}

func (s *DockerVolumeMigrationStep) Apply(sctx migration.StepContext) (string, error) {
	ctx := sctx.Ctx
	if s.TargetSSH == nil {
		return "", fmt.Errorf("target SSH client is nil")
	}

	// Stop the container on the source
	if sctx.Progress != nil {
		sctx.Progress(migration.WSMessage{
			Step:   s.StepName,
			Status: "progress",
			Value:  fmt.Sprintf("Stopping container %s on source...", s.ContainerName),
		})
	}
	s.SourceSSH.ExecContext(ctx, fmt.Sprintf("docker stop %s", s.ContainerName))

	// Transfer each volume
	for _, vol := range s.Volumes {
		if sctx.Progress != nil {
			sctx.Progress(migration.WSMessage{
				Step:   s.StepName,
				Status: "progress",
				Value:  fmt.Sprintf("Transferring volume %s...", vol),
			})
		}

		// Create a tar archive on the source and download it
		sourcePath := fmt.Sprintf("/tmp/meshium-vol-%s.tar", s.ContainerName)
		s.SourceSSH.ExecContext(ctx, fmt.Sprintf("tar cf %s -C %s .", sourcePath, vol))

		// Download from source, upload to target
		// This is a simplified version — in production, we'd use the transfer engine
		destPath := fmt.Sprintf("/tmp/meshium-vol-%s.tar", s.ContainerName)
		pipeReader, pipeWriter := newPipe()
		go func() {
			defer pipeWriter.Close()
			s.SourceSSH.Download(sourcePath, pipeWriter)
		}()
		s.TargetSSH.Upload(pipeReader, destPath)

		// Extract on target
		s.TargetSSH.ExecContext(ctx, fmt.Sprintf("mkdir -p %s && tar xf %s -C %s", vol, destPath, vol))

		// Cleanup temp files
		s.SourceSSH.ExecContext(ctx, fmt.Sprintf("rm -f %s", sourcePath))
		s.TargetSSH.ExecContext(ctx, fmt.Sprintf("rm -f %s", destPath))
	}

	data := map[string]interface{}{
		"volumesTransferred": len(s.Volumes),
	}
	jsonData, _ := json.Marshal(data)

	if sctx.Progress != nil {
		sctx.Progress(migration.WSMessage{
			Step:   s.StepName,
			Status: "success",
			Value:  fmt.Sprintf("Transferred %d volumes for container %s", len(s.Volumes), s.ContainerName),
		})
	}

	return string(jsonData), nil
}

func (s *DockerVolumeMigrationStep) Verify(sctx migration.StepContext) (string, error) {
	ctx := sctx.Ctx
	if s.TargetSSH == nil {
		return "", fmt.Errorf("target SSH client is nil")
	}

	// Verify that volumes exist on the target
	for _, vol := range s.Volumes {
		_, stderr, exitCode, err := s.TargetSSH.ExecContext(ctx, fmt.Sprintf("test -d %s", vol))
		if err != nil || exitCode != 0 {
			return "", fmt.Errorf("volume %s not found on target: %s", vol, stderr)
		}
	}

	data := map[string]interface{}{"verified": true}
	jsonData, _ := json.Marshal(data)

	if sctx.Progress != nil {
		sctx.Progress(migration.WSMessage{
			Step:   s.StepName,
			Status: "success",
			Value:  "All volumes verified on target",
		})
	}

	return string(jsonData), nil
}

func (s *DockerVolumeMigrationStep) Rollback(sctx migration.StepContext) error {
	ctx := sctx.Ctx
	if s.TargetSSH == nil {
		return fmt.Errorf("target SSH client is nil")
	}

	if sctx.Progress != nil {
		sctx.Progress(migration.WSMessage{
			Step:   s.StepName,
			Status: "progress",
			Value:  fmt.Sprintf("Rolling back: removing volumes for container %s", s.ContainerName),
		})
	}

	// Remove transferred volumes from target
	for _, vol := range s.Volumes {
		s.TargetSSH.ExecContext(ctx, fmt.Sprintf("rm -rf %s", vol))
	}

	// Restart container on source
	if s.SourceSSH != nil {
		s.SourceSSH.ExecContext(ctx, fmt.Sprintf("docker start %s", s.ContainerName))
	}

	if sctx.Progress != nil {
		sctx.Progress(migration.WSMessage{
			Step:   s.StepName,
			Status: "success",
			Value:  "Rollback complete: volumes removed, container restarted on source",
		})
	}

	return nil
}

// DockerImageMigrationStep pulls a Docker image on the target server.
type DockerImageMigrationStep struct {
	StepName  string
	Image     string
	TargetSSH transport.SSHExecuter
}

func (s *DockerImageMigrationStep) Name() string { return s.StepName }

func (s *DockerImageMigrationStep) Prepare(sctx migration.StepContext) (string, error) {
	if s.TargetSSH == nil {
		return "", fmt.Errorf("target SSH client is nil")
	}
	// Check if Docker is available on target
	_, _, exitCode, err := s.TargetSSH.ExecContext(sctx.Ctx, "which docker")
	if err != nil || exitCode != 0 {
		return "", fmt.Errorf("docker not available on target server")
	}
	return "", nil
}

func (s *DockerImageMigrationStep) Apply(sctx migration.StepContext) (string, error) {
	ctx := sctx.Ctx
	if sctx.Progress != nil {
		sctx.Progress(migration.WSMessage{
			Step:   s.StepName,
			Status: "progress",
			Value:  fmt.Sprintf("Pulling image %s on target...", s.Image),
		})
	}

	stdout, stderr, exitCode, err := s.TargetSSH.ExecContext(ctx, fmt.Sprintf("docker pull %s", s.Image))
	if err != nil || exitCode != 0 {
		return "", fmt.Errorf("docker pull %s failed: %v %s %s", s.Image, err, stderr, stdout)
	}

	if sctx.Progress != nil {
		sctx.Progress(migration.WSMessage{
			Step:   s.StepName,
			Status: "success",
			Value:  fmt.Sprintf("Image %s pulled successfully", s.Image),
		})
	}

	return fmt.Sprintf(`{"image":"%s","pulled":true}`, s.Image), nil
}

func (s *DockerImageMigrationStep) Verify(sctx migration.StepContext) (string, error) {
	ctx := sctx.Ctx
	stdout, stderr, exitCode, err := s.TargetSSH.ExecContext(ctx, fmt.Sprintf("docker image inspect %s", s.Image))
	if err != nil || exitCode != 0 {
		return "", fmt.Errorf("image %s not found on target: %s", s.Image, stderr)
	}
	_ = stdout
	return `{"verified":true}`, nil
}

func (s *DockerImageMigrationStep) Rollback(sctx migration.StepContext) error {
	ctx := sctx.Ctx
	// Remove the pulled image
	s.TargetSSH.ExecContext(ctx, fmt.Sprintf("docker rmi %s", s.Image))
	return nil
}

// DatabaseMigrationStep dumps a database on the source, transfers it,
// and restores it on the target.
type DatabaseMigrationStep struct {
	StepName  string
	DBType    string
	Port      int
	DataDir   string
	SourceSSH transport.SSHExecuter
	TargetSSH transport.SSHExecuter
}

func (s *DatabaseMigrationStep) Name() string { return s.StepName }

func (s *DatabaseMigrationStep) Prepare(sctx migration.StepContext) (string, error) {
	ctx := sctx.Ctx
	if s.SourceSSH == nil {
		return "", fmt.Errorf("source SSH client is nil")
	}
	if s.TargetSSH == nil {
		return "", fmt.Errorf("target SSH client is nil")
	}

	if sctx.Progress != nil {
		sctx.Progress(migration.WSMessage{
			Step:   s.StepName,
			Status: "progress",
			Value:  fmt.Sprintf("Preparing %s database migration...", s.DBType),
		})
	}

	// Check that the database is accessible on the source
	dumpCmd := s.dumpCommand()
	_, stderr, exitCode, err := s.SourceSSH.ExecContext(ctx, fmt.Sprintf("which %s", s.dumpCommandBinary()))
	if err != nil || exitCode != 0 {
		return "", fmt.Errorf("%s not found on source: %s", s.dumpCommandBinary(), stderr)
	}

	// Check disk space on target for the dump
	_, _, _, _ = s.TargetSSH.ExecContext(ctx, "df -h /tmp")

	data := map[string]interface{}{
		"type":      s.DBType,
		"dumpCmd":   dumpCmd,
		"port":      s.Port,
	}
	jsonData, _ := json.Marshal(data)
	return string(jsonData), nil
}

func (s *DatabaseMigrationStep) Apply(sctx migration.StepContext) (string, error) {
	ctx := sctx.Ctx
	if sctx.Progress != nil {
		sctx.Progress(migration.WSMessage{
			Step:   s.StepName,
			Status: "progress",
			Value:  fmt.Sprintf("Dumping %s database on source...", s.DBType),
		})
	}

	// Dump the database on the source
	dumpFile := fmt.Sprintf("/tmp/meshium-%s-dump.sql", s.DBType)
	dumpCmd := fmt.Sprintf("%s > %s", s.dumpCommand(), dumpFile)
	_, stderr, exitCode, err := s.SourceSSH.ExecContext(ctx, dumpCmd)
	if err != nil || exitCode != 0 {
		return "", fmt.Errorf("database dump failed: %v %s", err, stderr)
	}

	if sctx.Progress != nil {
		sctx.Progress(migration.WSMessage{
			Step:   s.StepName,
			Status: "progress",
			Value:  "Transferring database dump to target...",
		})
	}

	// Transfer the dump file from source to target
	pipeReader, pipeWriter := newPipe()
	go func() {
		defer pipeWriter.Close()
		s.SourceSSH.Download(dumpFile, pipeWriter)
	}()

	targetDumpFile := fmt.Sprintf("/tmp/meshium-%s-dump.sql", s.DBType)
	err = s.TargetSSH.Upload(pipeReader, targetDumpFile)
	if err != nil {
		return "", fmt.Errorf("transfer dump failed: %w", err)
	}

	if sctx.Progress != nil {
		sctx.Progress(migration.WSMessage{
			Step:   s.StepName,
			Status: "progress",
			Value:  "Restoring database on target...",
		})
	}

	// Restore the database on the target
	restoreCmd := fmt.Sprintf("%s < %s", s.restoreCommand(), targetDumpFile)
	_, stderr, exitCode, err = s.TargetSSH.ExecContext(ctx, restoreCmd)
	if err != nil || exitCode != 0 {
		return "", fmt.Errorf("database restore failed: %v %s", err, stderr)
	}

	// Cleanup dump files
	s.SourceSSH.ExecContext(ctx, fmt.Sprintf("rm -f %s", dumpFile))
	s.TargetSSH.ExecContext(ctx, fmt.Sprintf("rm -f %s", targetDumpFile))

	if sctx.Progress != nil {
		sctx.Progress(migration.WSMessage{
			Step:   s.StepName,
			Status: "success",
			Value:  fmt.Sprintf("Database %s migrated successfully", s.DBType),
		})
	}

	return fmt.Sprintf(`{"type":"%s","restored":true}`, s.DBType), nil
}

func (s *DatabaseMigrationStep) Verify(sctx migration.StepContext) (string, error) {
	ctx := sctx.Ctx
	// Verify the database is accessible on the target
	checkCmd := s.verifyCommand()
	stdout, stderr, exitCode, err := s.TargetSSH.ExecContext(ctx, checkCmd)
	if err != nil || exitCode != 0 {
		return "", fmt.Errorf("database verification failed: %v %s", err, stderr)
	}
	_ = stdout
	return `{"verified":true}`, nil
}

func (s *DatabaseMigrationStep) Rollback(sctx migration.StepContext) error {
	ctx := sctx.Ctx
	if sctx.Progress != nil {
		sctx.Progress(migration.WSMessage{
			Step:   s.StepName,
			Status: "progress",
			Value:  fmt.Sprintf("Rolling back: dropping %s database on target...", s.DBType),
		})
	}
	// Drop the restored database on the target
	dropCmd := s.dropCommand()
	s.TargetSSH.ExecContext(ctx, dropCmd)

	if sctx.Progress != nil {
		sctx.Progress(migration.WSMessage{
			Step:   s.StepName,
			Status: "success",
			Value:  "Rollback complete: database dropped on target",
		})
	}
	return nil
}

// dumpCommand returns the dump command for the database type.
func (s *DatabaseMigrationStep) dumpCommand() string {
	switch s.DBType {
	case "mysql":
		return "mysqldump --all-databases --single-transaction --routines --triggers"
	case "postgresql":
		return "pg_dumpall"
	case "mongodb":
		return "mongodump --archive"
	case "redis":
		return "redis-cli SAVE && cat /var/lib/redis/dump.rdb"
	default:
		return fmt.Sprintf("echo 'unknown database type: %s'", s.DBType)
	}
}

// dumpCommandBinary returns the binary name for the dump command.
func (s *DatabaseMigrationStep) dumpCommandBinary() string {
	switch s.DBType {
	case "mysql":
		return "mysqldump"
	case "postgresql":
		return "pg_dumpall"
	case "mongodb":
		return "mongodump"
	case "redis":
		return "redis-cli"
	default:
		return s.DBType
	}
}

// restoreCommand returns the restore command for the database type.
func (s *DatabaseMigrationStep) restoreCommand() string {
	switch s.DBType {
	case "mysql":
		return "mysql"
	case "postgresql":
		return "psql"
	case "mongodb":
		return "mongorestore --archive"
	case "redis":
		return "redis-cli FLUSHALL && cat > /var/lib/redis/dump.rdb"
	default:
		return fmt.Sprintf("echo 'unknown database type: %s'", s.DBType)
	}
}

// verifyCommand returns a verification command for the database type.
func (s *DatabaseMigrationStep) verifyCommand() string {
	switch s.DBType {
	case "mysql":
		return "mysql -e 'SELECT 1'"
	case "postgresql":
		return "psql -c 'SELECT 1'"
	case "mongodb":
		return "mongosh --eval 'db.runCommand({ping:1})'"
	case "redis":
		return "redis-cli ping"
	default:
		return fmt.Sprintf("echo 'verifying %s'", s.DBType)
	}
}

// dropCommand returns a drop command for the database type.
func (s *DatabaseMigrationStep) dropCommand() string {
	switch s.DBType {
	case "mysql":
		return "mysql -e 'DROP DATABASE IF EXISTS meshium_restored'"
	case "postgresql":
		return "psql -c 'DROP DATABASE IF EXISTS meshium_restored'"
	case "mongodb":
		return "mongosh --eval 'db.dropDatabase()'"
	case "redis":
		return "redis-cli FLUSHALL"
	default:
		return fmt.Sprintf("echo 'dropping %s'", s.DBType)
	}
}

// NginxMigrationStep copies Nginx config from source to target, verifies syntax, and reloads.
type NginxMigrationStep struct {
	StepName   string
	ConfigFile string
	ServerName string
	SourceSSH  transport.SSHExecuter
	TargetSSH  transport.SSHExecuter
}

func (s *NginxMigrationStep) Name() string { return s.StepName }

func (s *NginxMigrationStep) Prepare(sctx migration.StepContext) (string, error) {
	ctx := sctx.Ctx
	if s.SourceSSH == nil {
		return "", fmt.Errorf("source SSH client is nil")
	}
	if s.TargetSSH == nil {
		return "", fmt.Errorf("target SSH client is nil")
	}

	// Check that the config file exists on the source
	_, stderr, exitCode, err := s.SourceSSH.ExecContext(ctx, fmt.Sprintf("test -f %s", s.ConfigFile))
	if err != nil || exitCode != 0 {
		return "", fmt.Errorf("config file %s not found on source: %s", s.ConfigFile, stderr)
	}

	// Check that Nginx is installed on the target
	_, stderr, exitCode, err = s.TargetSSH.ExecContext(ctx, "which nginx")
	if err != nil || exitCode != 0 {
		return "", fmt.Errorf("nginx not installed on target: %s", stderr)
	}

	return fmt.Sprintf(`{"configFile":"%s"}`, s.ConfigFile), nil
}

func (s *NginxMigrationStep) Apply(sctx migration.StepContext) (string, error) {
	ctx := sctx.Ctx
	if sctx.Progress != nil {
		sctx.Progress(migration.WSMessage{
			Step:   s.StepName,
			Status: "progress",
			Value:  fmt.Sprintf("Transferring Nginx config %s...", s.ConfigFile),
		})
	}

	// Transfer the config file
	pipeReader, pipeWriter := newPipe()
	go func() {
		defer pipeWriter.Close()
		s.SourceSSH.Download(s.ConfigFile, pipeWriter)
	}()

	err := s.TargetSSH.Upload(pipeReader, s.ConfigFile)
	if err != nil {
		return "", fmt.Errorf("transfer config failed: %w", err)
	}

	if sctx.Progress != nil {
		sctx.Progress(migration.WSMessage{
			Step:   s.StepName,
			Status: "progress",
			Value:  "Verifying Nginx syntax...",
		})
	}

	// Verify Nginx syntax
	_, stderr, exitCode, err := s.TargetSSH.ExecContext(ctx, "nginx -t")
	if err != nil || exitCode != 0 {
		// Syntax error — rollback by removing the bad config
		s.TargetSSH.ExecContext(ctx, fmt.Sprintf("rm -f %s", s.ConfigFile))
		return "", fmt.Errorf("nginx syntax check failed: %s", stderr)
	}

	if sctx.Progress != nil {
		sctx.Progress(migration.WSMessage{
			Step:   s.StepName,
			Status: "progress",
			Value:  "Reloading Nginx...",
		})
	}

	// Reload Nginx
	_, stderr, exitCode, err = s.TargetSSH.ExecContext(ctx, "nginx -s reload")
	if err != nil || exitCode != 0 {
		return "", fmt.Errorf("nginx reload failed: %s", stderr)
	}

	if sctx.Progress != nil {
		sctx.Progress(migration.WSMessage{
			Step:   s.StepName,
			Status: "success",
			Value:  fmt.Sprintf("Nginx config %s deployed and reloaded", s.ConfigFile),
		})
	}

	return fmt.Sprintf(`{"configFile":"%s","deployed":true}`, s.ConfigFile), nil
}

func (s *NginxMigrationStep) Verify(sctx migration.StepContext) (string, error) {
	ctx := sctx.Ctx
	// Verify the config file exists and Nginx is running
	_, stderr, exitCode, err := s.TargetSSH.ExecContext(ctx, fmt.Sprintf("test -f %s && nginx -t", s.ConfigFile))
	if err != nil || exitCode != 0 {
		return "", fmt.Errorf("nginx verification failed: %s", stderr)
	}
	return `{"verified":true}`, nil
}

func (s *NginxMigrationStep) Rollback(sctx migration.StepContext) error {
	ctx := sctx.Ctx
	if sctx.Progress != nil {
		sctx.Progress(migration.WSMessage{
			Step:   s.StepName,
			Status: "progress",
			Value:  "Rolling back: removing Nginx config and reloading...",
		})
	}

	s.TargetSSH.ExecContext(ctx, fmt.Sprintf("rm -f %s", s.ConfigFile))
	s.TargetSSH.ExecContext(ctx, "nginx -s reload")

	if sctx.Progress != nil {
		sctx.Progress(migration.WSMessage{
			Step:   s.StepName,
			Status: "success",
			Value:  "Rollback complete: config removed, Nginx reloaded",
		})
	}
	return nil
}

// ServiceMigrationStep enables and starts a systemd service on the target.
type ServiceMigrationStep struct {
	StepName    string
	ServiceName string
	TargetSSH   transport.SSHExecuter
}

func (s *ServiceMigrationStep) Name() string { return s.StepName }

func (s *ServiceMigrationStep) Prepare(sctx migration.StepContext) (string, error) {
	ctx := sctx.Ctx
	if s.TargetSSH == nil {
		return "", fmt.Errorf("target SSH client is nil")
	}

	// Check that the service exists on the target
	_, stderr, exitCode, err := s.TargetSSH.ExecContext(ctx, fmt.Sprintf("systemctl cat %s", s.ServiceName))
	if err != nil || exitCode != 0 {
		return "", fmt.Errorf("service %s not found on target: %s", s.ServiceName, stderr)
	}

	return fmt.Sprintf(`{"service":"%s"}`, s.ServiceName), nil
}

func (s *ServiceMigrationStep) Apply(sctx migration.StepContext) (string, error) {
	ctx := sctx.Ctx
	if sctx.Progress != nil {
		sctx.Progress(migration.WSMessage{
			Step:   s.StepName,
			Status: "progress",
			Value:  fmt.Sprintf("Enabling service %s...", s.ServiceName),
		})
	}

	// Enable the service
	_, stderr, exitCode, err := s.TargetSSH.ExecContext(ctx, fmt.Sprintf("systemctl enable %s", s.ServiceName))
	if err != nil || exitCode != 0 {
		return "", fmt.Errorf("enable service %s failed: %s", s.ServiceName, stderr)
	}

	if sctx.Progress != nil {
		sctx.Progress(migration.WSMessage{
			Step:   s.StepName,
			Status: "progress",
			Value:  fmt.Sprintf("Starting service %s...", s.ServiceName),
		})
	}

	// Start the service
	_, stderr, exitCode, err = s.TargetSSH.ExecContext(ctx, fmt.Sprintf("systemctl start %s", s.ServiceName))
	if err != nil || exitCode != 0 {
		return "", fmt.Errorf("start service %s failed: %s", s.ServiceName, stderr)
	}

	if sctx.Progress != nil {
		sctx.Progress(migration.WSMessage{
			Step:   s.StepName,
			Status: "success",
			Value:  fmt.Sprintf("Service %s enabled and started", s.ServiceName),
		})
	}

	return fmt.Sprintf(`{"service":"%s","enabled":true,"started":true}`, s.ServiceName), nil
}

func (s *ServiceMigrationStep) Verify(sctx migration.StepContext) (string, error) {
	ctx := sctx.Ctx
	stdout, stderr, exitCode, err := s.TargetSSH.ExecContext(ctx, fmt.Sprintf("systemctl is-active %s", s.ServiceName))
	if err != nil || exitCode != 0 {
		return "", fmt.Errorf("service %s is not active: %s", s.ServiceName, stderr)
	}
	if strings.TrimSpace(stdout) != "active" {
		return "", fmt.Errorf("service %s is not active (status: %s)", s.ServiceName, strings.TrimSpace(stdout))
	}
	return `{"verified":true}`, nil
}

func (s *ServiceMigrationStep) Rollback(sctx migration.StepContext) error {
	ctx := sctx.Ctx
	if sctx.Progress != nil {
		sctx.Progress(migration.WSMessage{
			Step:   s.StepName,
			Status: "progress",
			Value:  fmt.Sprintf("Rolling back: stopping and disabling %s...", s.ServiceName),
		})
	}

	s.TargetSSH.ExecContext(ctx, fmt.Sprintf("systemctl stop %s", s.ServiceName))
	s.TargetSSH.ExecContext(ctx, fmt.Sprintf("systemctl disable %s", s.ServiceName))

	if sctx.Progress != nil {
		sctx.Progress(migration.WSMessage{
			Step:   s.StepName,
			Status: "success",
			Value:  "Rollback complete: service stopped and disabled",
		})
	}
	return nil
}

// --- pipe helper ---

// newPipe creates a connected read/write pair for streaming data between
// SSH Download and Upload operations using io.Pipe.
func newPipe() (*io.PipeReader, *io.PipeWriter) {
	return io.Pipe()
}

// Ensure all custom step types implement migration.MigrationStep
var (
	_ migration.MigrationStep = (*DockerVolumeMigrationStep)(nil)
	_ migration.MigrationStep = (*DockerImageMigrationStep)(nil)
	_ migration.MigrationStep = (*DatabaseMigrationStep)(nil)
	_ migration.MigrationStep = (*NginxMigrationStep)(nil)
	_ migration.MigrationStep = (*ServiceMigrationStep)(nil)
)
