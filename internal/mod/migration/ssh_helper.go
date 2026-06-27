package migration

import (
	"fmt"

	"meshium/internal/mod/server"
	modssh "meshium/internal/mod/ssh"
	"meshium/internal/shared"
)

// resolveBastion checks if the server has a bastion configured and returns
// the bastion config if so. Returns nil if no bastion is configured.
func resolveBastion(srv *server.Server, srvRepo server.Repo, aesKey []byte) *modssh.BastionConfig {
	if srv.BastionID == 0 {
		return nil
	}

	bastion, err := srvRepo.GetByID(srv.BastionID)
	if err != nil {
		return nil
	}

	bPassword, _ := shared.Decrypt(aesKey, []byte(bastion.Password))
	bSSHKey, _ := shared.Decrypt(aesKey, []byte(bastion.SSHKey))
	bPassphrase, _ := shared.Decrypt(aesKey, []byte(bastion.Passphrase))

	bastionPort := bastion.Port
	if bastionPort == 0 {
		bastionPort = 22
	}

	cfg := &modssh.BastionConfig{
		Host:       bastion.Host,
		Port:       bastionPort,
		Username:   bastion.Username,
		Password:   string(bPassword),
		Passphrase: string(bPassphrase),
	}
	if len(bSSHKey) > 0 {
		cfg.PrivateKey = bSSHKey
	}

	return cfg
}

// buildSSHConfig creates an SSH config for a server, including bastion support if configured.
func buildSSHConfig(srv *server.Server, srvRepo server.Repo, aesKey []byte) (modssh.ServerConfig, error) {
	password, _ := shared.Decrypt(aesKey, []byte(srv.Password))
	sshKey, _ := shared.Decrypt(aesKey, []byte(srv.SSHKey))
	passphrase, _ := shared.Decrypt(aesKey, []byte(srv.Passphrase))

	sshConfig := modssh.ServerConfig{
		ID:         srv.ID,
		Host:       srv.Host,
		Port:       srv.Port,
		Username:   srv.Username,
		Password:   string(password),
		Passphrase: string(passphrase),
	}
	if len(sshKey) > 0 {
		sshConfig.PrivateKey = sshKey
	}

	// Add bastion if configured
	sshConfig.Bastion = resolveBastion(srv, srvRepo, aesKey)

	return sshConfig, nil
}

// getSSHClientForServer is a shared helper that creates an SSH connection
// for a server, resolving bastion/jump host if configured.
func getSSHClientForServer(serverID int, srv *server.Server, srvRepo server.Repo, pool ConnectionPool, authSvc AESKeyProvider, hosts HostKeyStore) (SSHExecuter, error) {
	aesKey := authSvc.GetAESKey()
	if aesKey == nil {
		return nil, fmt.Errorf("app is locked")
	}

	sshConfig, err := buildSSHConfig(srv, srvRepo, aesKey)
	if err != nil {
		return nil, err
	}

	hostKeyCallback := hosts.MakeHostKeyCallback()
	return pool.Get(serverID, sshConfig, hostKeyCallback)
}
