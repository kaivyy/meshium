package handler

import (
	"context"
	"fmt"

	"meshium/internal/jobengine"
	"meshium/internal/mod/discovery"
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
	snapshotStore discovery.SnapshotStore
	planStore     planner.PlanStore
	serverRepo    server.Repo
	pool          *ssh.Pool
	authSvc       transport.AESKeyProvider
	knownHosts    transport.HostKeyStore
}

// NewHandlerFactory creates a HandlerFactoryImpl.
func NewHandlerFactory(
	snapshotStore discovery.SnapshotStore,
	planStore planner.PlanStore,
	serverRepo server.Repo,
	pool *ssh.Pool,
	authSvc transport.AESKeyProvider,
	knownHosts transport.HostKeyStore,
) *HandlerFactoryImpl {
	return &HandlerFactoryImpl{
		snapshotStore: snapshotStore,
		planStore:     planStore,
		serverRepo:    serverRepo,
		pool:          pool,
		authSvc:       authSvc,
		knownHosts:    knownHosts,
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

	// Load the plan to get source and target server info
	_, err := f.planStore.LoadPlan(context.Background(), job.PlanID)
	if err != nil {
		return nil, fmt.Errorf("load plan %s: %w", job.PlanID, err)
	}

	// Migration jobs via the job engine require SSH connections to both
	// source and target servers. The plan stores server summaries (hostname,
	// OS, etc.) but not server IDs, so we cannot resolve SSH connections
	// from the plan alone.
	//
	// For now, migration jobs should use the existing WebSocket handler
	// at /ws/migrate/{id} which has full access to the migration runner.
	// The job engine migration path will be fully wired in a future phase.
	return nil, fmt.Errorf("migration jobs via job engine not yet supported — use /ws/migrate/{id} instead")
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
