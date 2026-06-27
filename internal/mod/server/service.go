package server

import (
	"errors"

	"meshium/internal/mod/auth"
	"meshium/internal/shared"
)

type Service struct {
	repo    Repo
	authSvc *auth.Service
}

func NewService(repo Repo, authSvc *auth.Service) *Service {
	return &Service{repo: repo, authSvc: authSvc}
}

func (s *Service) encryptCredential(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	if s.authSvc == nil {
		return "", errors.New("app is locked")
	}
	key := s.authSvc.GetAESKey()
	if key == nil {
		return "", errors.New("app is locked")
	}
	encrypted, err := shared.Encrypt(key, []byte(plaintext))
	if err != nil {
		return "", err
	}
	return string(encrypted), nil
}

func (s *Service) decryptCredential(ciphertext string) (string, error) {
	if ciphertext == "" {
		return "", nil
	}
	if s.authSvc == nil {
		return "", errors.New("app is locked")
	}
	key := s.authSvc.GetAESKey()
	if key == nil {
		return "", errors.New("app is locked")
	}
	decrypted, err := shared.Decrypt(key, []byte(ciphertext))
	if err != nil {
		return "", err
	}
	return string(decrypted), nil
}

func (s *Service) redactServer(srv *Server) *Server {
	if srv == nil {
		return nil
	}
	srv.Password = ""
	srv.SSHKey = ""
	srv.Passphrase = ""
	return srv
}

func (s *Service) Create(req CreateRequest) (*Server, error) {
	password, err := s.encryptCredential(req.Password)
	if err != nil {
		return nil, err
	}
	sshKey, err := s.encryptCredential(req.SSHKey)
	if err != nil {
		return nil, err
	}
	passphrase, err := s.encryptCredential(req.Passphrase)
	if err != nil {
		return nil, err
	}

	port := req.Port
	if port == 0 {
		port = 22
	}

	id, err := s.repo.Create(Server{
		Name:        req.Name,
		Description: req.Description,
		Host:        req.Host,
		Port:        port,
		Username:    req.Username,
		Password:    password,
		SSHKey:      sshKey,
		Passphrase:  passphrase,
		Tags:        req.Tags,
		Environment: req.Environment,
		Region:      req.Region,
		Icon:        req.Icon,
		Color:       req.Color,
	})
	if err != nil {
		return nil, err
	}

	return s.GetByID(id)
}

func (s *Service) GetByID(id int) (*Server, error) {
	srv, err := s.repo.GetByID(id)
	if err != nil {
		return nil, err
	}
	return s.redactServer(srv), nil
}

func (s *Service) List(filter ListFilter) ([]Server, error) {
	servers, err := s.repo.List(filter)
	if err != nil {
		return nil, err
	}
	for i := range servers {
		servers[i].Password = ""
		servers[i].SSHKey = ""
		servers[i].Passphrase = ""
	}
	return servers, nil
}

func (s *Service) Update(id int, req UpdateRequest) error {
	srv, err := s.repo.GetByID(id)
	if err != nil {
		return err
	}

	if req.Name != nil {
		srv.Name = *req.Name
	}
	if req.Description != nil {
		srv.Description = *req.Description
	}
	if req.Host != nil {
		srv.Host = *req.Host
	}
	if req.Port != nil {
		srv.Port = *req.Port
	}
	if req.Username != nil {
		srv.Username = *req.Username
	}
	if req.Tags != nil {
		srv.Tags = *req.Tags
	}
	if req.Environment != nil {
		srv.Environment = *req.Environment
	}
	if req.Region != nil {
		srv.Region = *req.Region
	}
	if req.Icon != nil {
		srv.Icon = *req.Icon
	}
	if req.Color != nil {
		srv.Color = *req.Color
	}

	if req.Password != nil {
		encrypted, err := s.encryptCredential(*req.Password)
		if err != nil {
			return err
		}
		srv.Password = encrypted
	}
	if req.SSHKey != nil {
		encrypted, err := s.encryptCredential(*req.SSHKey)
		if err != nil {
			return err
		}
		srv.SSHKey = encrypted
	}
	if req.Passphrase != nil {
		encrypted, err := s.encryptCredential(*req.Passphrase)
		if err != nil {
			return err
		}
		srv.Passphrase = encrypted
	}

	return s.repo.Update(id, *srv)
}

func (s *Service) Delete(id int) error {
	return s.repo.Delete(id)
}

func (s *Service) ToggleFavorite(id int) error {
	return s.repo.ToggleFavorite(id)
}

func (s *Service) GetServerInfo(serverID int) (*ServerInfo, error) {
	return s.repo.GetServerInfo(serverID)
}

// GetDecryptedCredentials returns decrypted credentials for SSH connection consumers.
func (s *Service) GetDecryptedCredentials(serverID int) (password, sshKey, passphrase string, err error) {
	srv, err := s.repo.GetByID(serverID)
	if err != nil {
		return "", "", "", err
	}

	password, err = s.decryptCredential(srv.Password)
	if err != nil {
		return "", "", "", err
	}
	sshKey, err = s.decryptCredential(srv.SSHKey)
	if err != nil {
		return "", "", "", err
	}
	passphrase, err = s.decryptCredential(srv.Passphrase)
	if err != nil {
		return "", "", "", err
	}

	return password, sshKey, passphrase, nil
}
