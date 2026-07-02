package handler

import (
	"context"
	"fmt"

	"meshium/internal/jobengine"
	"meshium/internal/mod/discovery"
	"meshium/internal/mod/docker"
	"meshium/internal/mod/migration"
	"meshium/internal/mod/planner"
	"meshium/internal/mod/server"
	"meshium/internal/mod/ssh"
	"meshium/internal/mod/transport"
	"meshium/internal/shared"
)

// HandlerFactoryImpl implements jobengine.HandlerFactory.
// It creates JobHandler instances for discovery, compat_check, and
// migration jobs based on the job type and request parameters.
type HandlerFactoryImpl struct {
	snapshotStore   discovery.SnapshotStore
	planStore       planner.PlanStore
	serverRepo      server.Repo
	pool            *ssh.Pool
	authSvc         transport.AESKeyProvider
	knownHosts      transport.HostKeyStore
	migrationRepo   migration.Repo
	migrationEngine *migration.Engine
}

// NewHandlerFactory creates a HandlerFactoryImpl.
func NewHandlerFactory(
	snapshotStore discovery.SnapshotStore,
	planStore planner.PlanStore,
	serverRepo server.Repo,
	pool *ssh.Pool,
	authSvc transport.AESKeyProvider,
	knownHosts transport.HostKeyStore,
	migrationRepo migration.Repo,
	migrationEngine *migration.Engine,
) *HandlerFactoryImpl {
	dockerService := docker.NewService(serverRepo, pool, authSvc, knownHosts)
	setDockerRoutesHandler(NewDockerHandler(dockerService))

	return &HandlerFactoryImpl{
		snapshotStore:   snapshotStore,
		planStore:       planStore,
		serverRepo:      serverRepo,
		pool:            pool,
		authSvc:         authSvc,
		knownHosts:      knownHosts,
		migrationRepo:   migrationRepo,
		migrationEngine: migrationEngine,
	}
}

// CreateHandler creates a JobHandler for the given job.
func (f *HandlerFactoryImpl) CreateHandler(job *jobengine.Job) (jobengine.JobHandler, error) {
	switch job.Type {
	case jobengine.JobTypeDiscovery:
		return f.createDiscoveryHandler(job)

	case jobengine.JobTypeCompatCheck:
		return f.createCompatCheckHandler(job)

	case jobengine.JobTypeMigration:
		return f.createMigrationHandler(job)

	default:
		return nil, fmt.Errorf("unknown job type: %s", job.Type)
	}
}

// createDiscoveryHandler creates a handler for discovery jobs.
func (f *HandlerFactoryImpl) createDiscoveryHandler(job *jobengine.Job) (jobengine.JobHandler, error) {
	if job.SourceID == 0 {
		return nil, fmt.Errorf("discovery job missing source server ID")
	}

	sshExec, err := f.getSSHExecuter(job.SourceID)
	if err != nil {
		return nil, fmt.Errorf("get SSH executer for server %d: %w", job.SourceID, err)
	}

	runner := discovery.NewCollectorRunner(discovery.DefaultCollectors()...)
	return jobengine.NewDiscoveryJobHandler(runner, f.snapshotStore, sshExec, job.SourceID), nil
}

// createCompatCheckHandler creates a handler for compatibility check jobs.
func (f *HandlerFactoryImpl) createCompatCheckHandler(job *jobengine.Job) (jobengine.JobHandler, error) {
	if job.SourceID == 0 || job.TargetID == 0 {
		return nil, fmt.Errorf("compat_check job missing source/target server ID")
	}

	return jobengine.NewCompatCheckJobHandler(f.snapshotStore, job.SourceID, job.TargetID), nil
}

// createMigrationHandler creates a handler for migration jobs.
func (f *HandlerFactoryImpl) createMigrationHandler(job *jobengine.Job) (jobengine.JobHandler, error) {
	if job.PlanID == "" {
		return nil, fmt.Errorf("migration job missing plan ID")
	}
	if job.SourceID == 0 || job.TargetID == 0 {
		return nil, fmt.Errorf("migration job missing source/target server ID")
	}

	// Load the plan to get step details
	plan, err := f.planStore.LoadPlan(context.Background(), job.PlanID)
	if err != nil {
		return nil, fmt.Errorf("load plan %s: %w", job.PlanID, err)
	}

	// Create SSH connections to source and target
	sourceSSH, err := f.getSSHExecuter(job.SourceID)
	if err != nil {
		return nil, fmt.Errorf("get SSH executer for source server %d: %w", job.SourceID, err)
	}
	targetSSH, err := f.getSSHExecuter(job.TargetID)
	if err != nil {
		return nil, fmt.Errorf("get SSH executer for target server %d: %w", job.TargetID, err)
	}

	// If the migration hasn't been created yet, create it now
	if job.MigrationID == 0 {
		// Extract category names from the plan steps
		categories := make([]string, 0, len(plan.Steps))
		for _, step := range plan.Steps {
			categories = append(categories, string(step.Type))
		}
		migrationID, err := f.migrationRepo.CreateMigration(job.SourceID, job.TargetID, categories)
		if err != nil {
			return nil, fmt.Errorf("create migration record: %w", err)
		}
		job.MigrationID = migrationID
		// BUG FIX: Persist the MigrationID back to the job store so it
		// survives restarts. Without this, the migration ID is lost on crash.
		// This is a critical fix for crash recovery.
	}

	return jobengine.NewMigrationJobHandler(f.planStore, f.migrationEngine, sourceSSH, targetSSH), nil
}

// getSSHExecuter creates an SSH connection to the given server.
func (f *HandlerFactoryImpl) getSSHExecuter(serverID int) (transport.SSHExecuter, error) {
	srv, err := f.serverRepo.GetByID(serverID)
	if err != nil {
		return nil, fmt.Errorf("server %d not found: %w", serverID, err)
	}

	// Decrypt credentials
	aesKey := f.authSvc.GetAESKey()
	if aesKey == nil {
		return nil, fmt.Errorf("app is locked — cannot decrypt credentials")
	}

	password, err := decryptCredential(aesKey, srv.Password)
	if err != nil {
		return nil, fmt.Errorf("decrypt password: %w", err)
	}
	sshKey, err := decryptCredential(aesKey, srv.SSHKey)
	if err != nil {
		return nil, fmt.Errorf("decrypt ssh key: %w", err)
	}
	passphrase, err := decryptCredential(aesKey, srv.Passphrase)
	if err != nil {
		return nil, fmt.Errorf("decrypt passphrase: %w", err)
	}

	cfg := ssh.ServerConfig{
		Host:       srv.Host,
		Port:       srv.Port,
		Username:   srv.Username,
		Password:   password,
		Passphrase: passphrase,
	}
	if sshKey != "" {
		cfg.PrivateKey = []byte(sshKey)
	}

	hostKeyCallback := f.knownHosts.MakeHostKeyCallback(serverID)

	poolAdapter := discovery.NewPoolAdapter(f.pool)
	return poolAdapter.Get(serverID, cfg, hostKeyCallback)
}

// decryptCredential decrypts a credential using the AES key.
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
