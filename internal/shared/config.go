package shared

import (
	"os"
	"path/filepath"
)

type Config struct {
	DBPath     string
	ServerPort string
	DataDir    string
	TLSCertFile string // path to TLS certificate file (empty = no TLS)
	TLSKeyFile  string // path to TLS private key file (empty = no TLS)
}

func LoadConfig() (*Config, error) {
	dataDir := os.Getenv("MESHium_DATA_DIR")
	if dataDir == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		dataDir = filepath.Join(homeDir, ".meshium")
	}
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return nil, err
	}

	return &Config{
		DBPath:      filepath.Join(dataDir, "meshium.db"),
		ServerPort:  getEnv("MESHium_PORT", "8080"),
		DataDir:     dataDir,
		TLSCertFile: os.Getenv("MESHium_TLS_CERT"),
		TLSKeyFile:  os.Getenv("MESHium_TLS_KEY"),
	}, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
