package shared

import (
	"os"
	"path/filepath"
)

type Config struct {
	DBPath     string
	ServerPort string
	DataDir    string
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
		DBPath:     filepath.Join(dataDir, "meshium.db"),
		ServerPort: getEnv("MESHium_PORT", "8080"),
		DataDir:    dataDir,
	}, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
