package migration

import (
	"fmt"

	"meshium/internal/mod/server"
	modssh "meshium/internal/mod/ssh"
	"meshium/internal/shared"
)

// resolveBastion checks if the server has a bastion configured and returns
// the bastion config if so. Returns nil if no bastion is configured.
func resolveBastion(srv *server.Server, srvRepo server.Repo, aesKey []byte, hosts HostKeyStore) *modssh.BastionConfig {
	if srv.BastionID == 0 {
		return nil
	}

	bastion, err := srvRepo.GetByID(srv.BastionID)
	if err != nil {
		return nil
	}

	bastionPort := bastion.Port
	if bastionPort == 0 {
		bastionPort = 22
	}

	cfg := &modssh.BastionConfig{
		Host:            bastion.Host,
		Port:            bastionPort,
		Username:        bastion.Username,
		HostKeyCallback: hosts.MakeHostKeyCallback(bastion.ID),
	}

	// Decrypt bastion credentials — skip empty fields
	if bastion.Password != "" {
		bPassword, err := shared.Decrypt(aesKey, []byte(bastion.Password))
		if err != nil {
			return nil
		}
		cfg.Password = string(bPassword)
	}
	if bastion.SSHKey != "" {
		bSSHKey, err := shared.Decrypt(aesKey, []byte(bastion.SSHKey))
		if err != nil {
			return nil
		}
		cfg.PrivateKey = bSSHKey
	}
	if bastion.Passphrase != "" {
		bPassphrase, err := shared.Decrypt(aesKey, []byte(bastion.Passphrase))
		if err != nil {
			return nil
		}
		cfg.Passphrase = string(bPassphrase)
	}

	return cfg
}

// buildSSHConfig creates an SSH config for a server, including bastion support if configured.
func buildSSHConfig(srv *server.Server, srvRepo server.Repo, aesKey []byte, hosts HostKeyStore) (modssh.ServerConfig, error) {
	sshConfig := modssh.ServerConfig{
		ID:       srv.ID,
		Host:     srv.Host,
		Port:     srv.Port,
		Username: srv.Username,
	}

	// Decrypt credentials — skip empty fields (test servers may not have them)
	if srv.Password != "" {
		password, err := shared.Decrypt(aesKey, []byte(srv.Password))
		if err != nil {
			return modssh.ServerConfig{}, fmt.Errorf("decrypt password: %w", err)
		}
		sshConfig.Password = string(password)
	}
	if srv.SSHKey != "" {
		sshKey, err := shared.Decrypt(aesKey, []byte(srv.SSHKey))
		if err != nil {
			return modssh.ServerConfig{}, fmt.Errorf("decrypt ssh key: %w", err)
		}
		sshConfig.PrivateKey = sshKey
	}
	if srv.Passphrase != "" {
		passphrase, err := shared.Decrypt(aesKey, []byte(srv.Passphrase))
		if err != nil {
			return modssh.ServerConfig{}, fmt.Errorf("decrypt passphrase: %w", err)
		}
		sshConfig.Passphrase = string(passphrase)
	}

	// Add bastion if configured
	sshConfig.Bastion = resolveBastion(srv, srvRepo, aesKey, hosts)

	return sshConfig, nil
}

// getSSHClientForServer is a shared helper that creates an SSH connection
// for a server, resolving bastion/jump host if configured.
func getSSHClientForServer(serverID int, srv *server.Server, srvRepo server.Repo, pool ConnectionPool, authSvc AESKeyProvider, hosts HostKeyStore) (SSHExecuter, error) {
	aesKey := authSvc.GetAESKey()
	if aesKey == nil {
		return nil, fmt.Errorf("app is locked")
	}

	sshConfig, err := buildSSHConfig(srv, srvRepo, aesKey, hosts)
	if err != nil {
		return nil, err
	}

	hostKeyCallback := hosts.MakeHostKeyCallback(serverID)
	return pool.Get(serverID, sshConfig, hostKeyCallback)
}
