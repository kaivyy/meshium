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

func LoadConfig() *Config {
	dataDir := os.Getenv("MESHium_DATA_DIR")
	if dataDir == "" {
		homeDir, _ := os.UserHomeDir()
		dataDir = filepath.Join(homeDir, ".meshium")
	}
	_ = os.MkdirAll(dataDir, 0700)

	return &Config{
		DBPath:     filepath.Join(dataDir, "meshium.db"),
		ServerPort: getEnv("MESHium_PORT", "8080"),
		DataDir:    dataDir,
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
