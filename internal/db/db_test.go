package db

import (
	"testing"
)

func TestMigrateCreatesTables(t *testing.T) {
	dbConn, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer dbConn.Close()

	if err := Migrate(dbConn); err != nil {
		t.Fatalf("Migrate failed: %v", err)
	}

	var name string
	if err := dbConn.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='app_config'").Scan(&name); err != nil {
		t.Error("app_config table not created")
	}
	if err := dbConn.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='servers'").Scan(&name); err != nil {
		t.Error("servers table not created")
	}
	if err := dbConn.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='server_info'").Scan(&name); err != nil {
		t.Error("server_info table not created")
	}
	if err := dbConn.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='known_hosts'").Scan(&name); err != nil {
		t.Error("known_hosts table not created")
	}
}
