package migration

import "testing"

// TestAutoCutoverDefaultsOff asserts the fenced cutover orchestrator (Phase 2A/
// 2C) is strictly opt-in: the default must never silently enable automatic
// cutover. This is the single feature gate for the orchestrator; regressing it
// would violate the "no auto-forward past the manual cutover" contract.
func TestAutoCutoverDefaultsOff(t *testing.T) {
	if AutoCutoverDefault {
		t.Fatal("AutoCutoverDefault must be false — fenced cutover is opt-in only")
	}

	// A default config must not request automatic cutover.
	cfg := DefaultMigrationConfig()
	if cfg.AutoCutover {
		t.Fatal("DefaultMigrationConfig must leave AutoCutover disabled")
	}
}
