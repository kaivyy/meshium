package auth

import (
	"database/sql"
	"errors"
)

type Repo interface {
	GetConfigValue(key string) (string, error)
	SetConfigValue(key, value string) error
	HasMasterPassword() (bool, error)
	SetMasterPassword(hash string) error
	GetEncryptedSSHKey() (string, error)
	SetEncryptedSSHKey(key string) error
	GetSSHPublicKey() (string, error)
	SetSSHPublicKey(key string) error
}

type sqliteRepo struct {
	db *sql.DB
}

func NewRepo(db *sql.DB) Repo {
	return &sqliteRepo{db: db}
}

func (r *sqliteRepo) GetConfigValue(key string) (string, error) {
	var value string
	err := r.db.QueryRow("SELECT value FROM app_config WHERE key = ?", key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return value, err
}

func (r *sqliteRepo) SetConfigValue(key, value string) error {
	_, err := r.db.Exec(
		"INSERT INTO app_config (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = ?",
		key, value, value,
	)
	return err
}

func (r *sqliteRepo) HasMasterPassword() (bool, error) {
	val, err := r.GetConfigValue("master_password_hash")
	if err != nil {
		return false, err
	}
	return val != "", nil
}

func (r *sqliteRepo) SetMasterPassword(hash string) error {
	return r.SetConfigValue("master_password_hash", hash)
}

func (r *sqliteRepo) GetEncryptedSSHKey() (string, error) {
	return r.GetConfigValue("ssh_key_private_encrypted")
}

func (r *sqliteRepo) SetEncryptedSSHKey(key string) error {
	return r.SetConfigValue("ssh_key_private_encrypted", key)
}

func (r *sqliteRepo) GetSSHPublicKey() (string, error) {
	return r.GetConfigValue("ssh_key_public")
}

func (r *sqliteRepo) SetSSHPublicKey(key string) error {
	return r.SetConfigValue("ssh_key_public", key)
}
