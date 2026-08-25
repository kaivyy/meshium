package gsync

import (
	"context"
	"strings"
	"sync"
	"testing"
)

// fakeRunner records what Service asked the rclone binary to do.
type fakeRunner struct {
	mu       sync.Mutex
	calls    []string // "src -> dst" lines
	cfgSeen  []string // config file contents at call time
	failFrom int      // calls >= failFrom fail
	err      error
}

func (f *fakeRunner) Run(ctx context.Context, cfgPath, src, dst string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	call := len(f.calls)
	f.calls = append(f.calls, src+" -> "+dst)
	if b, err := osReadFile(cfgPath); err == nil {
		f.cfgSeen = append(f.cfgSeen, string(b))
	} else {
		f.cfgSeen = append(f.cfgSeen, "<unreadable>")
	}
	if call >= f.failFrom {
		return "", f.err
	}
	return "Transferred: 12 B, 3 files", nil
}

// --- Config round-trip -------------------------------------------------------

func TestSaveTokenEncryptsAndStatusHidesIt(t *testing.T) {
	s := newTestService(t)

	if err := s.SaveToken(context.Background(), `{"access_token":"SECRET","refresh_token":"R2"}`); err != nil {
		t.Fatalf("SaveToken: %v", err)
	}

	raw := s.store.(*memStore).get(t, tokenKey)
	if strings.Contains(raw, "SECRET") {
		t.Fatal("token stored in plaintext")
	}

	st, err := s.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !st.Configured {
		t.Error("status says not configured after SaveToken")
	}
	for _, blob := range []string{marshalJSON(t, st)} {
		if strings.Contains(blob, "SECRET") || strings.Contains(blob, "R2\"") {
			t.Errorf("status leaks token material: %s", blob)
		}
	}
}

// rclone authorize prints an INI section, not bare JSON — the real user paste
// must be accepted, and garbage rejected.
func TestSaveTokenAcceptsConfigSectionAndRejectsGarbage(t *testing.T) {
	s := newTestService(t)
	if err := s.SaveToken(context.Background(), "[gdrive]\ntype = drive\ntoken = {\"access_token\":\"SECRET\"}\n"); err != nil {
		t.Fatalf("config-section token rejected: %v", err)
	}
	if err := s.SaveToken(context.Background(), "not json at all <<<"); err == nil {
		t.Error("garbage accepted as a token")
	}
}

func TestDeleteTokenDisconnects(t *testing.T) {
	s := newTestService(t)
	_ = s.SaveToken(context.Background(), "{}")
	if err := s.DeleteToken(context.Background()); err != nil {
		t.Fatalf("DeleteToken: %v", err)
	}
	st, _ := s.Status(context.Background())
	if st.Configured {
		t.Error("still configured after DeleteToken")
	}
}

func TestUpsertPairRoundTrip(t *testing.T) {
	s := newTestService(t)
	p, err := s.UpsertPair(context.Background(), Pair{Name: "db", LocalPath: "/root/.meshium", RemotePath: "backup/meshium", IntervalHours: 24})
	if err != nil {
		t.Fatalf("UpsertPair: %v", err)
	}
	if p.ID == "" {
		t.Fatal("pair got no ID")
	}

	// Same ID updates instead of duplicating.
	p.Enabled = false
	if _, err := s.UpsertPair(context.Background(), p); err != nil {
		t.Fatalf("UpsertPair #2: %v", err)
	}
	pairs, _ := s.ListPairs(context.Background())
	if len(pairs) != 1 || pairs[0].Enabled {
		t.Fatalf("want one disabled pair, got %+v", pairs)
	}
}

func TestUpsertPairValidation(t *testing.T) {
	s := newTestService(t)
	for name, p := range map[string]Pair{
		"empty local":  {Name: "x", RemotePath: "b"},
		"empty remote": {Name: "x", LocalPath: "/a"},
		// Trust boundary: a crafted path could smuggle rclone flags or shell
		// metacharacters; anything with a colon is also a remote reference,
		// never a plain path.
		"flag injection local":  {Name: "x", LocalPath: "/a --password-file x", RemotePath: "b"},
		"colon remote (remote)": {Name: "x", LocalPath: "/a", RemotePath: "other:host/path"},
	} {
		if _, err := s.UpsertPair(context.Background(), p); err == nil {
			t.Errorf("%s: accepted invalid pair %+v", name, p)
		}
	}
}

// --- Run ---------------------------------------------------------------------

func TestRunSyncsPairAndRecordsResult(t *testing.T) {
	s := newTestService(t)
	fr := s.useRunner(t)
	_ = s.SaveToken(context.Background(), `{"access_token":"SECRET"}`)
	p, _ := s.UpsertPair(context.Background(), Pair{Name: "db", LocalPath: "/tmp/src", RemotePath: "backup/db", IntervalHours: 24})

	res, err := s.RunNow(context.Background(), p.ID)
	if err != nil {
		t.Fatalf("RunNow: %v", err)
	}
	if !res.OK {
		t.Errorf("run reported not-OK: %+v", res)
	}
	if len(fr.calls) != 1 || fr.calls[0] != "/tmp/src -> gdrive:backup/db" {
		t.Fatalf("runner was called with %v", fr.calls)
	}
	// The temp config must have carried the decrypted token...
	if len(fr.cfgSeen) != 1 || !strings.Contains(fr.cfgSeen[0], "SECRET") {
		t.Fatalf("rclone config passed to runner lacked the token: %q", fr.cfgSeen)
	}
	// ...but the temp file must be gone afterwards.
	if entries, err := osReadDir(tempDir); err == nil {
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "gsync-") {
				t.Errorf("temp rclone config left behind: %s", e.Name())
			}
		}
	}
	// And the result record must not quote the token.
	last := s.lastRun(t, p.ID)
	if strings.Contains(marshalJSON(t, last), "SECRET") {
		t.Error("last-run record contains token material")
	}
}

func TestRunFailsWithoutTokenOrBinary(t *testing.T) {
	s := newTestService(t)
	s.useRunner(t)
	p, _ := s.UpsertPair(context.Background(), Pair{Name: "x", LocalPath: "/a", RemotePath: "b", IntervalHours: 24})

	if _, err := s.RunNow(context.Background(), p.ID); err == nil {
		t.Error("run succeeded with no Google token configured")
	}

	_ = s.SaveToken(context.Background(), "{}")
	s2 := newTestService(t) // no runner injected => binary missing
	p2, _ := s2.UpsertPair(context.Background(), Pair{Name: "y", LocalPath: "/a", RemotePath: "b", IntervalHours: 24})
	if _, err := s2.RunNow(context.Background(), p2.ID); err == nil {
		t.Error("run succeeded without the rclone binary")
	}
}

func TestRunFailureIsRecordedNotSwallowed(t *testing.T) {
	s := newTestService(t)
	fr := s.useRunner(t)
	fr.failFrom = 0
	fr.err = context.DeadlineExceeded
	_ = s.SaveToken(context.Background(), "{}")
	p, _ := s.UpsertPair(context.Background(), Pair{Name: "x", LocalPath: "/a", RemotePath: "b", IntervalHours: 24})

	if _, err := s.RunNow(context.Background(), p.ID); err == nil {
		t.Fatal("failed run returned nil error")
	}
	last := s.lastRun(t, p.ID)
	if last.OK || last.Summary == "" {
		t.Errorf("failure not recorded for the user: %+v", last)
	}
}

func TestDuePairsScheduling(t *testing.T) {
	s := newTestService(t)
	p1, _ := s.UpsertPair(context.Background(), Pair{Name: "due", LocalPath: "/a", RemotePath: "b", Enabled: true, IntervalHours: 1})
	_, _ = s.UpsertPair(context.Background(), Pair{Name: "off", LocalPath: "/c", RemotePath: "d", Enabled: false})

	due, err := s.DuePairs(context.Background())
	if err != nil {
		t.Fatalf("DuePairs: %v", err)
	}
	if len(due) != 1 || due[0].ID != p1.ID {
		t.Fatalf("want only the enabled pair due on first run, got %+v", due)
	}

	// After a successful run inside the interval it must not be due again.
	_ = s.SaveToken(context.Background(), "{}")
	s.useRunner(t)
	if _, err := s.RunNow(context.Background(), p1.ID); err != nil {
		t.Fatalf("RunNow: %v", err)
	}
	due, _ = s.DuePairs(context.Background())
	if len(due) != 0 {
		t.Fatalf("pair ran 0 minutes ago but is due again: %+v", due)
	}
}
