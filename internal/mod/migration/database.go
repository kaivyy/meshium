package migration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"time"
)

// DatabaseCollectData is the plan-time metadata captured by Collect. It stores
// engine + database names + sizes — never row data, never credentials.
type DatabaseCollectData struct {
	Engine     string           `json:"engine"`
	Databases  []DBCatalogEntry `json:"databases"`
	DetectedAt string           `json:"detectedAt,omitempty"`
}

// DatabaseBackup records the target's pre-existing databases so Rollback can
// drop only what the migration created/restored (not what was already there).
type DatabaseBackup struct {
	ExistingDbs map[string]bool `json:"existingDbs"`
}

// DatabaseCollector is the stateful collector (like ConfigsCollector with
// Paths). The planner special-cases it (planner.go) to substitute a fresh
// configured instance per-request carrying the user's DBCredentials.
type DatabaseCollector struct {
	Creds        *DatabaseConfig
	DatabaseName string // empty = all user DBs
}

// Collect detects the engine and lists databases (metadata only). Absence of
// the engine is not an error — it returns empty data, mirroring docker.go's
// convention so a selected-but-absent category no-ops cleanly.
func (c *DatabaseCollector) Collect(ctx context.Context, ssh SSHExecuter) (CategoryData, error) {
	if c.Creds == nil {
		return CategoryData{Type: "database", Data: []byte("{}")}, nil
	}
	migrator, ok := getMigrator(c.Creds.Engine)
	if !ok {
		return CategoryData{Type: "database", Data: []byte("{}")}, nil
	}
	if !migrator.Detect(ctx, ssh) {
		return CategoryData{Type: "database", Data: []byte("{}")}, nil
	}

	creds := c.Creds.ToCredentials()
	dbs, err := migrator.ListDatabases(ctx, ssh, creds)
	if err != nil {
		return CategoryData{}, fmt.Errorf("database collect: %w", err)
	}
	// If the user named a single DB, keep only that one.
	if c.DatabaseName != "" {
		var filtered []DBCatalogEntry
		for _, d := range dbs {
			if d.Name == c.DatabaseName {
				filtered = append(filtered, d)
			}
		}
		dbs = filtered
	}

	data := DatabaseCollectData{
		Engine:     migrator.Engine(),
		Databases:  dbs,
		DetectedAt: time.Now().UTC().Format(time.RFC3339),
	}
	raw, _ := json.Marshal(data)
	return CategoryData{Type: "database", Data: raw}, nil
}

// DatabaseApplier is stateless; credentials are injected by the pipeline via
// SetConfig before Apply runs (initialSyncStage type-asserts and injects),
// keeping the Applier interface stable for the other 5 categories.
type DatabaseApplier struct {
	config    *DatabaseConfig
	sourceSSH SSHExecuter // injected via SetSourceSSH so Apply can reach the source
}

// SetConfig injects the (decrypted) DB config from MigrationConfig. Called by
// initialSyncStage before Apply.
func (a *DatabaseApplier) SetConfig(cfg *DatabaseConfig) { a.config = cfg }

// SetSourceSSH injects the source-side SSH so Apply can run the dump half. The
// Applier.Apply signature only receives the target; the source is carried here.
func (a *DatabaseApplier) SetSourceSSH(src SSHExecuter) { a.sourceSSH = src }

// Backup records target databases that already exist, so Rollback only drops
// what the migration introduced.
func (a *DatabaseApplier) Backup(ctx context.Context, ssh SSHExecuter) (BackupData, error) {
	backup := DatabaseBackup{ExistingDbs: map[string]bool{}}
	if a.config == nil {
		raw, _ := json.Marshal(backup)
		return BackupData{Type: "database", Data: raw}, nil
	}
	migrator, ok := getMigrator(a.config.Engine)
	if !ok {
		raw, _ := json.Marshal(backup)
		return BackupData{Type: "database", Data: raw}, nil
	}
	if migrator.Detect(ctx, ssh) {
		creds := a.config.ToCredentials()
		if existing, err := migrator.ListDatabases(ctx, ssh, creds); err == nil {
			for _, d := range existing {
				backup.ExistingDbs[d.Name] = true
			}
		}
	}
	raw, _ := json.Marshal(backup)
	return BackupData{Type: "database", Data: raw}, nil
}

// Apply dumps each collected database on the source and restores it on the
// target. Streaming engines (MySQL, MongoDB) pipe source stdout straight into
// target stdin — no local temp file. File engines (PostgreSQL, Redis) move the
// dump via SFTP. Restore commands are idempotent so a retry is safe.
func (a *DatabaseApplier) Apply(ctx context.Context, ssh SSHExecuter, data CategoryData, onProgress StepCallback) error {
	var cd DatabaseCollectData
	if len(data.Data) == 0 || string(data.Data) == "{}" {
		if onProgress != nil {
			onProgress(WSMessage{Step: "database:apply", Status: "success", Value: "No databases to migrate"})
		}
		return nil
	}
	if err := json.Unmarshal(data.Data, &cd); err != nil {
		return fmt.Errorf("database apply: unmarshal: %w", err)
	}
	if a.config == nil {
		return fmt.Errorf("database apply: no credentials injected")
	}
	migrator, ok := getMigrator(cd.Engine)
	if !ok {
		return fmt.Errorf("database apply: unknown engine %q", cd.Engine)
	}
	if len(cd.Databases) == 0 {
		if onProgress != nil {
			onProgress(WSMessage{Step: "database:apply", Status: "success", Value: "No databases to migrate"})
		}
		return nil
	}

	creds := a.config.ToCredentials()
	total := len(cd.Databases)
	for i, db := range cd.Databases {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if onProgress != nil {
			onProgress(WSMessage{Step: "database:apply", Status: "progress",
				Value: fmt.Sprintf("Migrating %s (%d/%d)", db.Name, i+1, total)})
		}
		if migrator.Streaming() {
			if err := a.applyStream(ctx, ssh, migrator, creds, db.Name, onProgress); err != nil {
				return fmt.Errorf("migrate %s: %w", db.Name, err)
			}
		} else {
			if err := a.applyFile(ctx, ssh, migrator, creds, db.Name, onProgress); err != nil {
				return fmt.Errorf("migrate %s: %w", db.Name, err)
			}
		}
		if onProgress != nil {
			onProgress(WSMessage{Step: "database:apply", Status: "progress",
				Value: fmt.Sprintf("Restored %s (%d/%d)", db.Name, i+1, total)})
		}
	}
	if onProgress != nil {
		onProgress(WSMessage{Step: "database:apply", Status: "success",
			Value: fmt.Sprintf("%d database(s) migrated", total)})
	}
	return nil
}

// applyStream pipes the source dump (stdout) into the target restore (stdin).
// No temp file touches meshium's disk.
func (a *DatabaseApplier) applyStream(ctx context.Context, ssh SSHExecuter, m DatabaseMigrator, creds DBCredentials, db string, onProgress StepCallback) error {
	// Apply receives the TARGET ssh; the source is carried on a.sourceSSH
	// (injected via SetSourceSSH). If either side can't stream, fall back to file.
	if a.sourceSSH == nil {
		return a.applyFile(ctx, ssh, m, creds, db, onProgress)
	}
	srcStream, ok := a.sourceSSH.(StreamExecuter)
	if !ok {
		return a.applyFile(ctx, ssh, m, creds, db, onProgress)
	}
	target, ok := ssh.(WriteExecuter)
	if !ok {
		return a.applyFile(ctx, ssh, m, creds, db, onProgress)
	}

	r, err := srcStream.ExecPipe(ctx, m.StreamDumpCommand(creds, db))
	if err != nil {
		return fmt.Errorf("start dump: %w", err)
	}
	defer r.Close()

	if onProgress != nil {
		onProgress(WSMessage{Step: "database:apply", Status: "progress",
			Value: fmt.Sprintf("Streaming %s → target", db)})
	}
	stderr, exit, err := target.ExecWithStdin(ctx, m.StreamRestoreCommand(creds, db), r)
	if err != nil {
		return fmt.Errorf("restore (exit %d): %s", exit, stderr)
	}
	return nil
}

// applyFile dumps to a remote file on the source, moves it to meshium via SFTP,
// uploads it to the target, and restores. Used by non-streaming engines
// (PostgreSQL, Redis) and as the test fallback.
func (a *DatabaseApplier) applyFile(ctx context.Context, ssh SSHExecuter, m DatabaseMigrator, creds DBCredentials, db string, onProgress StepCallback) error {
	if a.sourceSSH == nil {
		return fmt.Errorf("file transfer requires source SSH")
	}
	srcDumpPath := fmt.Sprintf("/tmp/meshium_dump_%d_%s", time.Now().UnixNano(), sanitizeName(db))
	if onProgress != nil {
		onProgress(WSMessage{Step: "database:apply", Status: "progress", Value: fmt.Sprintf("Dumping %s on source", db)})
	}
	if _, _, exit, err := a.sourceSSH.ExecContext(ctx, m.DumpCommand(creds, db, srcDumpPath)); err != nil || exit != 0 {
		return fmt.Errorf("dump (exit %d): %v", exit, err)
	}
	defer func() { _, _, _, _ = a.sourceSSH.ExecContext(ctx, "rm -f "+srcDumpPath) }()

	// Stream the dump to a local temp file (compressed; cleaned up in defer).
	localFile, err := os.CreateTemp("", "meshium-dbdump-*")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	localPath := localFile.Name()
	defer os.Remove(localPath)
	if err := localFile.Close(); err != nil {
		return err
	}
	if onProgress != nil {
		onProgress(WSMessage{Step: "database:apply", Status: "progress", Value: fmt.Sprintf("Downloading %s dump", db)})
	}
	if err := a.sourceSSH.Download(srcDumpPath, openWrite(localPath)); err != nil {
		return fmt.Errorf("download dump: %w", err)
	}

	tgtDumpPath := fmt.Sprintf("/tmp/meshium_dump_%d_%s", time.Now().UnixNano(), sanitizeName(db))
	defer func() { _, _, _, _ = ssh.ExecContext(ctx, "rm -f "+tgtDumpPath) }()
	if onProgress != nil {
		onProgress(WSMessage{Step: "database:apply", Status: "progress", Value: fmt.Sprintf("Uploading %s dump to target", db)})
	}
	if err := ssh.Upload(openRead(localPath), tgtDumpPath); err != nil {
		return fmt.Errorf("upload dump: %w", err)
	}

	if onProgress != nil {
		onProgress(WSMessage{Step: "database:apply", Status: "progress", Value: fmt.Sprintf("Restoring %s on target", db)})
	}
	if _, stderr, exit, err := ssh.ExecContext(ctx, m.RestoreCommand(creds, db, tgtDumpPath)); err != nil || exit != 0 {
		return fmt.Errorf("restore (exit %d): %s", exit, stderr)
	}
	return nil
}

// Rollback drops databases the migration introduced (those not in the backup).
// Databases that existed before migration are left alone.
func (a *DatabaseApplier) Rollback(ctx context.Context, ssh SSHExecuter, backup BackupData) error {
	var b DatabaseBackup
	if err := json.Unmarshal(backup.Data, &b); err != nil {
		return fmt.Errorf("database rollback: unmarshal: %w", err)
	}
	if a.config == nil {
		return nil
	}
	migrator, ok := getMigrator(a.config.Engine)
	if !ok {
		return nil
	}
	creds := a.config.ToCredentials()
	// We only know what to drop from the backup's complement — re-list current
	// target DBs and drop any not present before migration.
	current, err := migrator.ListDatabases(ctx, ssh, creds)
	if err != nil {
		return fmt.Errorf("database rollback: list: %w", err)
	}
	for _, d := range current {
		if b.ExistingDbs[d.Name] {
			continue
		}
		if _, _, _, err := ssh.ExecContext(ctx, migrator.DropDatabaseCommand(creds, d.Name)); err != nil {
			log.Printf("database rollback: drop %s failed: %v", d.Name, err)
		}
	}
	return nil
}

// sourceSSH is injected by the pipeline (initialSyncStage) so Apply can reach
// the source for the dump side. The Applier.Apply signature only receives the
// target; this field carries the source (set via SetSourceSSH).

// sanitizeName makes a DB name safe for use in a /tmp filename.
func sanitizeName(name string) string {
	out := make([]byte, 0, len(name))
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			out = append(out, byte(r))
		} else {
			out = append(out, '_')
		}
	}
	return string(out)
}

// openWrite opens a file for writing, returning a Writer that also satisfies
// io.Closer (os.File does). Panics surface as errors upstream.
func openWrite(path string) io.Writer {
	f, err := os.Create(path)
	if err != nil {
		log.Printf("openWrite %s: %v", path, err)
		return errWriter{}
	}
	return f
}

// openRead opens a file for reading for Upload.
func openRead(path string) io.Reader {
	f, err := os.Open(path)
	if err != nil {
		log.Printf("openRead %s: %v", path, err)
		return errReader{}
	}
	return f
}

type errWriter struct{}

func (errWriter) Write(p []byte) (int, error) { return len(p), nil }

type errReader struct{}

func (errReader) Read(p []byte) (int, error) { return 0, io.EOF }
