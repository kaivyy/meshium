package shared

import "testing"

func TestLoadConfigDefaultsServerPortTo9527(t *testing.T) {
	t.Setenv("MESHium_PORT", "")
	t.Setenv("MESHium_DATA_DIR", t.TempDir())

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig(): %v", err)
	}
	if cfg.ServerPort != "9527" {
		t.Fatalf("ServerPort = %q, want 9527", cfg.ServerPort)
	}
}

func TestLoadConfigUsesServerPortEnvOverride(t *testing.T) {
	t.Setenv("MESHium_PORT", "1234")
	t.Setenv("MESHium_DATA_DIR", t.TempDir())

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig(): %v", err)
	}
	if cfg.ServerPort != "1234" {
		t.Fatalf("ServerPort = %q, want env override", cfg.ServerPort)
	}
}
