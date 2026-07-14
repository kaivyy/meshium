package migration

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestDatabaseResumableMapping (Phase 5E, D5/H): file-path engines are
// resumable; streaming engines RESTART and must not be shown a resumable badge.
func TestDatabaseResumableMapping(t *testing.T) {
	cases := map[string]bool{
		"postgres": true,
		"redis":    true,
		"mysql":    false,
		"mongodb":  false,
		"unknown":  false,
	}
	for engine, want := range cases {
		if got := DatabaseResumable(engine); got != want {
			t.Errorf("DatabaseResumable(%q) = %v, want %v", engine, got, want)
		}
	}
}

// TestDowntimeClassForOfflineHonest (Phase 5E, G): database/docker cap at
// offline_copy while zeroDowntimeCapable is false. No minimal_downtime leak.
func TestDowntimeClassForOfflineHonest(t *testing.T) {
	for _, cat := range []string{"database", "docker", "packages", "configs", "services", "users"} {
		if got := DowntimeClassFor(cat, false); got != DowntimeOfflineCopy {
			t.Errorf("DowntimeClassFor(%q, false) = %q, want %q", cat, got, DowntimeOfflineCopy)
		}
	}
	// Only a true global gate may elevate database to zero_downtime.
	if got := DowntimeClassFor("database", true); got != DowntimeZero {
		t.Errorf("DowntimeClassFor(database, true) = %q, want %q", got, DowntimeZero)
	}
}

// TestDatabaseCollectResumeNote (Phase 5E, D3): honest per-engine resume
// disclosure is produced by Collect and differs by engine path.
func TestDatabaseCollectResumeNote(t *testing.T) {
	mk := func(engine, detectCmd string) DatabaseCollectData {
		ssh := newMockSSH()
		ssh.addOutput("pgrep -x "+engine, "yes")
		ssh.addOutput(detectCmd, "appdb\t10\n")
		c := &DatabaseCollector{Creds: &DatabaseConfig{Engine: engine, Username: "u", Password: "p", Host: "h", Port: 1}}
		cd, err := c.Collect(context.Background(), ssh)
		if err != nil {
			t.Fatalf("collect %s: %v", engine, err)
		}
		var d DatabaseCollectData
		if err := json.Unmarshal(cd.Data, &d); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return d
	}

	pg := mk("postgres", "psql")
	if pg.ResumeNote == "" {
		t.Errorf("postgres: empty ResumeNote")
	}
	if !strings.Contains(pg.ResumeNote, "can resume") {
		t.Errorf("postgres ResumeNote should disclose upload resume: %q", pg.ResumeNote)
	}
	if !strings.Contains(pg.ResumeNote, "runs again") {
		t.Errorf("postgres ResumeNote should disclose download-leg restart: %q", pg.ResumeNote)
	}

	my := mk("mysql", "mysql")
	if my.ResumeNote == "" {
		t.Errorf("mysql: empty ResumeNote")
	}
	if !strings.Contains(my.ResumeNote, "restarts") {
		t.Errorf("mysql ResumeNote should disclose restart (not resumable): %q", my.ResumeNote)
	}
}

// TestDatabaseCollectEstimatedBytes (Phase 5E, J): collect sums selected DB
// sizes into EstimatedBytes so the wizard can show a real estimate.
func TestDatabaseCollectEstimatedBytes(t *testing.T) {
	ssh := newMockSSH()
	ssh.addOutput("pgrep -x postgres", "yes")
	ssh.addOutput("psql", "a\t10\nb\t20\n") // 30 MiB total
	c := &DatabaseCollector{Creds: &DatabaseConfig{Engine: "postgres", Username: "u", Password: "p", Host: "h", Port: 1}}
	cd, err := c.Collect(context.Background(), ssh)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	var d DatabaseCollectData
	if err := json.Unmarshal(cd.Data, &d); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := int64(30) * 1024 * 1024
	if d.EstimatedBytes != want {
		t.Errorf("EstimatedBytes = %d, want %d", d.EstimatedBytes, want)
	}
}

// TestResumeStateConstantsDistinct (Phase 5E, D2): every explicit resume state
// is a distinct, non-empty token so the FE never collapses them.
func TestResumeStateConstantsDistinct(t *testing.T) {
	states := []string{
		ResumeFreshTransfer, ResumeResumingUpload, ResumeRestartingDownload,
		ResumeRefusedSourceChanged, ResumeRefusedPartialInvalid,
		ResumeNotSupported, ResumeManualIntervention,
		ResumeVerificationInProgress, ResumeVerifiedComplete,
	}
	seen := map[string]bool{}
	for _, s := range states {
		if s == "" {
			t.Errorf("empty resume state")
		}
		if seen[s] {
			t.Errorf("duplicate resume state %q", s)
		}
		seen[s] = true
	}
}
