// Package gsync syncs local folders to Google Drive via the rclone binary —
// rsync semantics (one-way mirror, delta transfer) against a remote that
// plain rsync cannot address.
package gsync

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"meshium/internal/shared"
)

const (
	tokenKey   = "gsync_token"
	pairsKey   = "gsync_pairs"
	lastRunKey = "gsync_last_run"

	remotePrefix = "gdrive:"
)

// Pair is one local→Drive folder mapping.
type Pair struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	LocalPath     string `json:"localPath"`
	RemotePath    string `json:"remotePath"`
	Enabled       bool   `json:"enabled"`
	IntervalHours int    `json:"intervalHours"`
}

// RunResult is what the user sees after a run. It must never carry token
// material — rclone's own output doesn't, and we only keep a truncated tail.
type RunResult struct {
	StartedAt  time.Time `json:"startedAt"`
	FinishedAt time.Time `json:"finishedAt"`
	OK         bool      `json:"ok"`
	Summary    string    `json:"summary"`
}

// Status is the API-facing snapshot. Deliberately has no token field.
type Status struct {
	Configured bool     `json:"configured"`
	BinaryOK   bool     `json:"binaryOk"`
	Pairs      []Pair   `json:"pairs"`
	LastRuns   map[string]RunResult `json:"lastRuns"`
}

// Store persists config values; implemented by auth.Repo's Get/SetConfigValue
// and faked in tests.
type Store interface {
	GetConfigValue(key string) (string, error)
	SetConfigValue(key, value string) error
}

// Runner executes one rclone sync. Real impl shells out; tests fake it.
type Runner interface {
	Run(ctx context.Context, cfgPath, src, dst string) (summary string, err error)
}

type Service struct {
	mu     sync.Mutex
	store  Store
	aesKey func() []byte
	runner Runner // nil => binary considered missing

	// tempDir is where runner writes its 0600 config file; overridable in tests.
	tempDir string
}

func NewService(store Store, aesKey func() []byte) *Service {
	return &Service{store: store, aesKey: aesKey, tempDir: os.TempDir()}
}

// SetBinaryRunner wires the real rclone-backed runner. Until called, runs fail
// with an explicit "rclone not installed" error rather than exec-not-found.
func (s *Service) SetBinaryRunner(r Runner) { s.mu.Lock(); s.runner = r; s.mu.Unlock() }

// --- Token -------------------------------------------------------------------

// SaveToken encrypts the pasted `rclone authorize drive` output at rest.
// rclone prints either a bare JSON token blob or a config section
// ("[gdrive]\ntype = drive\ntoken = {...}") — accept both.
func (s *Service) SaveToken(ctx context.Context, pasted string) error {
	pasted = strings.TrimSpace(pasted)
	if pasted == "" || (!json.Valid([]byte(pasted)) && !looksLikeConfigSection(pasted)) {
		return errors.New("paste the output of `rclone authorize \"drive\"` (JSON or [gdrive] config block)")
	}
	enc, err := encrypt(s.aesKey(), pasted)
	if err != nil {
		return fmt.Errorf("encrypt token: %w", err)
	}
	return s.store.SetConfigValue(tokenKey, enc)
}

func (s *Service) DeleteToken(ctx context.Context) error {
	return s.store.SetConfigValue(tokenKey, "")
}

// --- Pairs -------------------------------------------------------------------

var ErrBadPair = errors.New("invalid pair")

// UpsertPair validates at the trust boundary: local paths and remote names go
// verbatim into an argv (no shell), but a crafted path could still smuggle
// rclone flags or a second remote, so both sides are pinned to plain-path form.
func (s *Service) UpsertPair(ctx context.Context, p Pair) (Pair, error) {
	if strings.TrimSpace(p.LocalPath) == "" || !filepath.IsAbs(p.LocalPath) ||
		strings.ContainsAny(p.LocalPath, " \t\n\"'") {
		return p, fmt.Errorf("%w: localPath must be an absolute path without spaces", ErrBadPair)
	}
	if strings.TrimSpace(p.RemotePath) == "" || strings.ContainsAny(p.RemotePath, ": \t\n") {
		return p, fmt.Errorf("%w: remotePath must be a relative Drive path without colons/spaces", ErrBadPair)
	}
	if p.IntervalHours < 1 {
		p.IntervalHours = 24
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	pairs, err := s.loadPairs()
	if err != nil {
		return p, err
	}
	if p.ID == "" {
		b := make([]byte, 8)
		if _, err := rand.Read(b); err != nil {
			return p, err
		}
		p.ID = hex.EncodeToString(b)
		pairs = append(pairs, p)
	} else {
		repl := false
		for i := range pairs {
			if pairs[i].ID == p.ID {
				pairs[i] = p
				repl = true
			}
		}
		if !repl {
			return p, fmt.Errorf("%w: unknown id", ErrBadPair)
		}
	}
	return p, s.savePairs(pairs)
}

func (s *Service) DeletePair(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	pairs, err := s.loadPairs()
	if err != nil {
		return err
	}
	out := pairs[:0]
	found := false
	for _, q := range pairs {
		if q.ID == id {
			found = true
			continue
		}
		out = append(out, q)
	}
	if !found {
		return fmt.Errorf("%w: unknown id", ErrBadPair)
	}
	return s.savePairs(out)
}

func (s *Service) ListPairs(ctx context.Context) ([]Pair, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadPairs()
}

// --- Status / run ------------------------------------------------------------

func (s *Service) Status(ctx context.Context) (Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Status{Pairs: []Pair{}, LastRuns: map[string]RunResult{}}
	tok, _ := s.store.GetConfigValue(tokenKey)
	st.Configured = tok != ""
	st.BinaryOK = s.runner != nil
	if last, _ := s.store.GetConfigValue(lastRunKey); last != "" {
		_ = json.Unmarshal([]byte(last), &st.LastRuns)
	}
	pairs, err := s.loadPairs()
	if err != nil {
		return st, err
	}
	st.Pairs = pairs
	return st, nil
}

// RunNow syncs one pair immediately and records the outcome under gsync_last_run.
func (s *Service) RunNow(ctx context.Context, pairID string) (RunResult, error) {
	s.mu.Lock()
	pairs, err := s.loadPairs()
	if err != nil {
		s.mu.Unlock()
		return RunResult{}, err
	}
	var pair *Pair
	for i := range pairs {
		if pairs[i].ID == pairID {
			pair = &pairs[i]
		}
	}
	if pair == nil {
		s.mu.Unlock()
		return RunResult{}, fmt.Errorf("%w: unknown id", ErrBadPair)
	}
	tokEnc, _ := s.store.GetConfigValue(tokenKey)
	runner := s.runner
	s.mu.Unlock()

	if tokEnc == "" {
		return RunResult{}, errors.New("Google Drive is not connected yet — paste the rclone token in Settings first")
	}
	if runner == nil {
		return RunResult{}, errors.New("the rclone binary is not available on this host (apt install rclone)")
	}

	cfgFile, cleanup, err := writeTempConfig(s.aesKey(), tokEnc)
	if err != nil {
		return RunResult{}, err
	}
	defer cleanup()

	res := RunResult{StartedAt: time.Now()}
	summary, runErr := runner.Run(ctx, cfgFile, pair.LocalPath, remotePrefix+pair.RemotePath)
	res.FinishedAt = time.Now()
	res.Summary = truncate(runErrText(summary, runErr), 2048)
	res.OK = runErr == nil

	s.recordLastRun(pair.ID, res)

	// The applier's verdict is also the caller's verdict: a failed sync is an
	// error for the scheduler/job layer, not just a red row.
	if runErr != nil {
		return res, fmt.Errorf("rclone sync %s failed: %w", pair.Name, runErr)
	}
	return res, nil
}

// DuePairs returns enabled pairs whose interval has elapsed (or never ran).
func (s *Service) DuePairs(ctx context.Context) ([]Pair, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pairs, err := s.loadPairs()
	if err != nil {
		return nil, err
	}
	last := map[string]RunResult{}
	if raw, _ := s.store.GetConfigValue(lastRunKey); raw != "" {
		_ = json.Unmarshal([]byte(raw), &last)
	}
	var due []Pair
	for _, p := range pairs {
		if !p.Enabled {
			continue
		}
		r, ran := last[p.ID]
		if !ran || time.Since(r.StartedAt) > time.Duration(p.IntervalHours)*time.Hour {
			due = append(due, p)
		}
	}
	return due, nil
}

func (s *Service) recordLastRun(pairID string, res RunResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	all := map[string]RunResult{}
	if raw, _ := s.store.GetConfigValue(lastRunKey); raw != "" {
		_ = json.Unmarshal([]byte(raw), &all)
	}
	all[pairID] = res
	b, _ := json.Marshal(all)
	_ = s.store.SetConfigValue(lastRunKey, string(b))
}

func (s *Service) loadPairs() ([]Pair, error) {
	raw, err := s.store.GetConfigValue(pairsKey)
	if err != nil || raw == "" {
		return []Pair{}, err
	}
	var pairs []Pair
	if err := json.Unmarshal([]byte(raw), &pairs); err != nil {
		return []Pair{}, fmt.Errorf("corrupt gsync_pairs config: %w", err)
	}
	return pairs, nil
}

func (s *Service) savePairs(pairs []Pair) error {
	b, err := json.Marshal(pairs)
	if err != nil {
		return err
	}
	return s.store.SetConfigValue(pairsKey, string(b))
}

// --- helpers -----------------------------------------------------------------

// writeTempConfig decrypts the stored token into a fresh rclone config file
// (0600) and returns it with a cleanup func. The plaintext token exists only
// inside this file, only for the duration of one sync.
func writeTempConfig(aesKey []byte, encToken string) (string, func(), error) {
	tok, err := decrypt(aesKey, encToken)
	if err != nil {
		return "", nil, fmt.Errorf("decrypt stored token: %w", err)
	}
	f, err := os.CreateTemp("", "gsync-*.conf")
	if err != nil {
		return "", nil, err
	}
	path := f.Name()
	cleanup := func() { _ = os.Remove(path) }
	if err := os.Chmod(path, 0o600); err != nil {
		cleanup()
		return "", nil, err
	}
	if _, err := f.WriteString(tok + "\n"); err != nil {
		f.Close()
		cleanup()
		return "", nil, err
	}
	if err := f.Close(); err != nil {
		cleanup()
		return "", nil, err
	}
	return path, cleanup, nil
}

func runErrText(summary string, err error) string {
	switch {
	case summary != "" && err != nil:
		return strings.TrimSpace(summary) + "\n" + err.Error()
	case summary != "":
		return summary
	case err != nil:
		return err.Error()
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:] // keep the tail: rclone puts totals at the end
}

// looksLikeConfigSection checks the INI shape rclone's authorize command prints.
func looksLikeConfigSection(s string) bool {
	lines := strings.Split(s, "\n")
	if len(lines) < 2 || !strings.HasPrefix(strings.TrimSpace(lines[0]), "[") {
		return false
	}
	for _, l := range lines[1:] {
		if strings.Contains(l, "=") {
			return true
		}
	}
	return false
}

// --- crypto (shared AES-GCM, same wire format as SSH credentials) ------------

func encrypt(key []byte, plaintext string) (string, error) {
	b, err := shared.Encrypt(key, []byte(plaintext))
	return string(b), err
}

func decrypt(key []byte, enc string) (string, error) {
	b, err := shared.Decrypt(key, []byte(enc))
	return string(b), err
}
