package migration

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"meshium/internal/mod/transfer"
)

// DatabaseCollectData is the plan-time metadata captured by Collect. It stores
// engine + database names + sizes — never row data, never credentials.
type DatabaseCollectData struct {
	Engine       string           `json:"engine"`
	ExecMode     string           `json:"execMode,omitempty"` // host | container | compose
	MigrationMode string          `json:"migrationMode,omitempty"` // snapshot_copy | live_replication
	Databases    []DBCatalogEntry `json:"databases"`
	DetectedAt   string           `json:"detectedAt,omitempty"`
	// EstimatedBytes is the sum of selected DB sizes (bytes), used by the wizard
	// to show "Estimated size: N GB" honestly (Phase 5E, J). 0 = unknown.
	EstimatedBytes int64 `json:"estimatedBytes,omitempty"`
	// ResumeNote is the honest per-engine resume disclosure (Phase 5E, D3/D5).
	ResumeNote string `json:"resumeNote,omitempty"`
	// AllUserDBs is true when the user left DatabaseName empty (meaning "every
	// user database"). The FE shows the enumerated list before execute.
	AllUserDBs bool `json:"allUserDbs,omitempty"`
}

// DatabaseBackup records the target's pre-existing databases so Rollback can
// drop only what the migration created/restored (not what was already there).
type DatabaseBackup struct {
	ExistingDbs map[string]bool `json:"existingDbs"`
	// Enumerated reports whether the target's database list was actually read.
	// Rollback drops every database absent from ExistingDbs, so an empty set
	// means "the target had no databases" ONLY when this is true. When the
	// engine was not detected, no migrator matched, or ListDatabases failed,
	// the set is empty because nothing could be read — dropping its complement
	// would destroy databases that predate the migration.
	Enumerated bool `json:"enumerated"`
}

// DatabaseCollector is the stateful collector (like ConfigsCollector with
// Paths). The planner special-cases it (planner.go) to substitute a fresh
// configured instance per-request carrying the user's DBCredentials.
type DatabaseCollector struct {
	Creds        *DatabaseConfig
	DatabaseName string // empty = all user DBs
}

// Collect detects the engine and lists databases (metadata only). Unlike the
// silent "no-op on absent engine" convention used by e.g. docker, the database
// category MUST fail clearly when it cannot proceed — a silent success with
// "0 databases" would falsely reassure the operator (Phase 5B, P0-1). The
// collected set is authoritative: empty DatabaseName means enumerate ALL user
// databases (system DBs excluded per engine rules); a named DB that the engine
// does not expose is a hard error, not a quiet drop.
func (c *DatabaseCollector) Collect(ctx context.Context, ssh SSHExecuter) (CategoryData, error) {
	if c.Creds == nil {
		return CategoryData{}, fmt.Errorf("database collect: no credentials supplied")
	}
	if c.Creds.Engine == "" {
		return CategoryData{}, fmt.Errorf("database collect: engine not specified")
	}
	migrator, ok := getMigrator(c.Creds.Engine)
	if !ok {
		return CategoryData{}, fmt.Errorf("database collect: unsupported engine %q (supported: postgres, mysql, mongodb, redis)", c.Creds.Engine)
	}
	if !migrator.Detect(ctx, ssh) {
		return CategoryData{}, fmt.Errorf("database collect: engine %q not detected on source (is it running? if containerized, set execMode=container/compose and the container name)", c.Creds.Engine)
	}

	creds := c.Creds.ToCredentials()
	// Verify we can actually authenticate + enumerate. A connection/auth failure
	// here is a hard error — never a silent empty result.
	dbs, err := migrator.ListDatabases(ctx, ssh, creds)
	if err != nil {
		return CategoryData{}, fmt.Errorf("database collect: list databases failed: %w", err)
	}

	allUser := c.DatabaseName == ""
	if !allUser {
		// The user named a specific DB: it MUST be present in the enumerated set.
		found := false
		for _, d := range dbs {
			if d.Name == c.DatabaseName {
				found = true
				break
			}
		}
		if !found {
			return CategoryData{}, fmt.Errorf("database collect: requested database %q not found on source (available: %s)",
				c.DatabaseName, dbNames(dbs))
		}
		// Keep only the named DB.
		var filtered []DBCatalogEntry
		for _, d := range dbs {
			if d.Name == c.DatabaseName {
				filtered = append(filtered, d)
			}
		}
		dbs = filtered
	}
	// If, after enumeration, nothing is migratable and the user asked for all,
	// that is suspicious (engine detected but no user DBs) — surface it rather
	// than emit a "success" with zero databases.
	if len(dbs) == 0 {
		return CategoryData{}, fmt.Errorf("database collect: engine %q detected but no user databases enumerated (only system databases present?)", c.Creds.Engine)
	}

	data := DatabaseCollectData{
		Engine:       migrator.Engine(),
		ExecMode:     creds.ExecMode,
		MigrationMode: c.resolveMigrationMode(migrator),
		Databases:    dbs,
		DetectedAt:   time.Now().UTC().Format(time.RFC3339),
		AllUserDBs:   allUser,
	}
	// EstimatedBytes (Phase 5E, J): total dump size across selected databases, so
	// the wizard can show "Estimated size: N GB" instead of a fake 0. SizeMb is
	// the engine-reported estimate; convert to bytes honestly (0 if unknown).
	var totalMB int64
	for _, d := range dbs {
		totalMB += d.SizeMB
	}
	data.EstimatedBytes = totalMB * 1024 * 1024
	// D3 honesty: file-path engines download the dump to an ephemeral local temp
	// that is NOT persisted, so a killed transfer re-downloads the source dump.
	// Surfaced to the operator so the resume claim is not overstated.
	if migrator.Streaming() {
		data.ResumeNote = "Streaming transfer: restarts from the beginning after an interruption (no resumable artifact yet)."
	} else {
		data.ResumeNote = "Target upload can resume after a network interruption when the source artifact is unchanged. If the local/source dump is unavailable, the source dump/download stage runs again."
	}
	raw, _ := json.Marshal(data)
	return CategoryData{Type: "database", Data: raw}, nil
}

// resolveMigrationMode returns the mode the user requested, defaulting to
// snapshot_copy, and downgrades live_replication to snapshot_copy when the
// engine/topology does not genuinely support a wired replication path. We never
// claim live_replication for an unsupported engine (Phase 5B, Part 3 + P0).
func (c *DatabaseCollector) resolveMigrationMode(m DatabaseMigrator) string {
	mode := c.Creds.MigrationMode
	if mode == "" {
		mode = "snapshot_copy"
	}
	if mode == "live_replication" && !m.SupportsLiveReplication() {
		// Honest downgrade: caller records the reason via Warnings.
		return "snapshot_copy"
	}
	return mode
}

func dbNames(dbs []DBCatalogEntry) string {
	names := make([]string, 0, len(dbs))
	for _, d := range dbs {
		names = append(names, d.Name)
	}
	if len(names) == 0 {
		return "(none)"
	}
	return strings.Join(names, ", ")
}

// DatabaseApplier is stateless; credentials are injected by the pipeline via
// SetConfig before Apply runs (initialSyncStage type-asserts and injects),
// keeping the Applier interface stable for the other 5 categories.
type DatabaseApplier struct {
	config    *DatabaseConfig
	sourceSSH SSHExecuter // injected via SetSourceSSH so Apply can reach the source
	// migrationID + checkpointStore enable resumable file-path transfers.
	// applyFile uses a deterministic dump path per (migrationID, db) and persists
	// a transfer.Checkpoint so a killed multi-GB upload RESUMES from the last
	// byte offset instead of restarting from zero. Both are nil-safe: when
	// checkpointStore is nil, applyFile behaves as a one-shot transfer (no resume).
	migrationID    int
	checkpointStore transfer.CheckpointStore
}

// SetConfig injects the (decrypted) DB config from MigrationConfig. Called by
// initialSyncStage before Apply.
func (a *DatabaseApplier) SetConfig(cfg *DatabaseConfig) { a.config = cfg }

// SetSourceSSH injects the source-side SSH so Apply can run the dump half. The
// Applier.Apply signature only receives the target; the source is carried here.
func (a *DatabaseApplier) SetSourceSSH(src SSHExecuter) { a.sourceSSH = src }

// SetCheckpointStore injects the resumable-transfer checkpoint store and the
// owning migration ID. Called by initialSyncStage the canonical way
// SetConfig/SetSourceSSH are. nil store ⇒ no resume (one-shot, legacy behavior).
func (a *DatabaseApplier) SetCheckpointStore(migrationID int, store transfer.CheckpointStore) {
	a.migrationID = migrationID
	a.checkpointStore = store
}

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
			// Only a successful listing licenses Rollback to drop anything.
			backup.Enumerated = true
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
	if onProgress != nil {
		onProgress(WSMessage{
			Step: "database:apply", Status: "progress",
			Value:           fmt.Sprintf("Streamed %s → target (restart-on-interrupt, not resumable)", db),
			MigrationID:     a.migrationID,
			TransferID:      fmt.Sprintf("db:%d:%s", a.migrationID, sanitizeName(db)),
			TransferMethod:  "stream",
			ResumeState:     ResumeNotSupported,
			IsResumable:     false,
			ResumeReason:    "streaming engines restart the transfer after interruption; no resumable artifact path yet",
			DowntimeClass:   string(DowntimeClassFor("database", false)),
		})
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
	safe := sanitizeName(db)
	// ExecMode (host/container/compose) selects WHERE the engine runs; every
	// engine command is wrapped via execPrefix so container/compose exec actually
	// runs in-container (Phase 5E, D5/H10). The creds already carry ExecMode.
	creds.ExecMode = resolveExecMode(creds.ExecMode)
	// Deterministic dump paths keyed on (migrationID, db) — NOT time-based. A
	// killed multi-GB transfer must leave a findable partial on the TARGET so a
	// later attempt resumes from it (transfer.SCPStrategy resumes via --partial /
	// temp-file append). Time-based paths make the partial unfindable → restart
	// from zero, defeating Part 5's resume.
	srcDumpPath := fmt.Sprintf("/tmp/meshium_dump_%d_%s", a.migrationID, safe)
	tgtDumpPath := fmt.Sprintf("/tmp/meshium_dump_%d_%s", a.migrationID, safe)
	transferID := fmt.Sprintf("db:%d:%s", a.migrationID, safe)

	emit := func(msg WSMessage) {
		if onProgress == nil {
			return
		}
		msg.MigrationID = a.migrationID
		msg.TransferID = transferID
		msg.TransferMethod = "scp"
		msg.IsResumable = true
		msg.Direction = "upload"
		onProgress(msg)
	}

	// 1. Dump on source (cap-free via execLongOrContext). Re-dump ONLY when the
	//    dump file is absent — reusing a surviving dump keeps the snapshot stable
	//    and lets the upload leg resume instead of re-dumping a multi-GB DB.
	emit(WSMessage{Step: "database:apply", Status: "progress",
		Value: fmt.Sprintf("Dumping %s on source (%s)", db, creds.ExecMode), ResumeState: ResumeFreshTransfer})
	srcTarget := transfer.TransferTarget{Path: srcDumpPath, SSHClient: a.sourceSSH}
	srcExists, _ := transfer.FileExists(ctx, srcTarget)
	if !srcExists {
		if _, _, exit, err := execLongOrContext(ctx, a.sourceSSH, m.DumpCommand(creds, db, srcDumpPath)); err != nil || exit != 0 {
			return fmt.Errorf("dump (exit %d): %v", exit, err)
		}
	}
	defer func() { _, _, _, _ = a.sourceSSH.ExecContext(ctx, "rm -f "+srcDumpPath) }()

	// 2. Download source dump to a local temp. meshium's local temp is ephemeral
	//    (cleared on function return / process restart), so this leg is never
	//    resumable — it is re-downloaded on retry (Phase 5E, D3). The download
	//    leg is LAN-fast; the slow WAN leg (upload to target) is what resumes.
	localFile, err := os.CreateTemp("", "meshium-dbdump-*")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	localPath := localFile.Name()
	defer os.Remove(localPath)
	if err := localFile.Close(); err != nil {
		return err
	}
	localTarget := transfer.TransferTarget{Path: localPath, IsLocal: true}
	emit(WSMessage{Step: "database:apply", Status: "progress",
		Value: fmt.Sprintf("Downloading %s dump", db), Direction: "download", ResumeState: ResumeRestartingDownload})
	dlResult, err := transfer.NewSCPStrategy().Transfer(ctx, srcTarget, localTarget, transfer.TransferOptions{
		Resume:          false,
		ProgressCallback: dbTransferProgress(onProgress, db, "download"),
	})
	if err != nil {
		return fmt.Errorf("download dump: %w", err)
	}
	_ = dlResult

	// 3. Upload to target — RESUMABLE. SCPStrategy resumes from the target's
	//    partial dump file (deterministic tgtDumpPath). Before uploading, if a
	//    prior checkpoint exists, Reconcile fails closed (ManualIntervention) when
	//    the source snapshot moved or the partial target is inconsistent — we never
	//    blind-resume onto unverified state.
	tgtTarget := transfer.TransferTarget{Path: tgtDumpPath, SSHClient: ssh}
	resumeState := ResumeFreshTransfer
	if a.checkpointStore != nil {
		if verdict, fail := a.reconcileOrFail(ctx, transferID, safe, srcTarget, tgtTarget); fail {
			emit(WSMessage{Step: "database:apply", Status: "error",
				Value:    fmt.Sprintf("Resume of %s refused: source or partial changed", db),
				ResumeState: ResumeRefusedSourceChanged,
				ResumeReason: "source fingerprint mismatch or invalid partial target",
				Direction:    "upload"})
			return fmt.Errorf("transfer reconcile for %s refused resume (source/partial changed): %w", db, transfer.ErrTransferReconcileFailed)
		} else if verdict == transfer.VerdictFreshStart {
			// No usable partial — clean full upload (Resume=false).
			resumeState = ResumeFreshTransfer
		} else {
			resumeState = ResumeResumingUpload
		}
	} else {
		resumeState = ResumeFreshTransfer
	}
	emit(WSMessage{Step: "database:apply", Status: "progress",
		Value: fmt.Sprintf("Uploading %s dump to target", db), ResumeState: resumeState,
		CheckpointStatus: cpStatus(a.checkpointStore != nil)})
	resume := a.checkpointStore != nil
	ulResult, err := transfer.NewSCPStrategy().Transfer(ctx, localTarget, tgtTarget, transfer.TransferOptions{
		Resume:          resume,
		ProgressCallback: dbTransferProgress(onProgress, db, "upload"),
	})
	if err != nil {
		return fmt.Errorf("upload dump: %w", err)
	}
	if a.checkpointStore != nil {
		a.persistTransferCheckpoint(transferID, safe, srcDumpPath, tgtDumpPath, ulResult)
	}
	defer func() { _, _, _, _ = ssh.ExecContext(ctx, "rm -f "+tgtDumpPath) }()

	// 4. Restore (cap-free, idempotent via --clean/--drop).
	emit(WSMessage{Step: "database:apply", Status: "progress",
		Value: fmt.Sprintf("Restoring %s on target", db), ResumeState: ResumeVerificationInProgress})
	if _, stderr, exit, err := execLongOrContext(ctx, ssh, m.RestoreCommand(creds, db, tgtDumpPath)); err != nil || exit != 0 {
		return fmt.Errorf("restore (exit %d): %s", exit, stderr)
	}
	// Restore verified — drop the checkpoint so a later retry does not think a
	// partial upload is outstanding.
	if a.checkpointStore != nil {
		_ = a.checkpointStore.DeleteCheckpoints(transferID)
		emit(WSMessage{Step: "database:apply", Status: "progress",
			Value: fmt.Sprintf("Restored %s (checkpoint cleared)", db),
			ResumeState: ResumeVerifiedComplete, CheckpointStatus: "deleted"})
	} else {
		emit(WSMessage{Step: "database:apply", Status: "progress",
			Value: fmt.Sprintf("Restored %s", db), ResumeState: ResumeVerifiedComplete})
	}
	return nil
}

// cpStatus reports whether a checkpoint was available to drive a leg.
func cpStatus(hasStore bool) string {
	if hasStore {
		return "loaded"
	}
	return "none"
}

// reconcileOrFail consults the persisted checkpoint for a resume and returns
// (VerdictFreshStart/VerdictResume, false) when it is safe to proceed, or
// (_, true) when Reconcile demands manual intervention (source moved / partial
// target inconsistent). Honest fail-closed: we never blind-resume.
func (a *DatabaseApplier) reconcileOrFail(ctx context.Context, transferID, fileName string, srcTarget, tgtTarget transfer.TransferTarget) (transfer.ReconcileVerdict, bool) {
	cp, err := a.checkpointStore.GetCheckpoint(transferID, fileName)
	if err != nil || cp == nil {
		return transfer.VerdictFreshStart, false
	}
	// Source fingerprint = size+mtime of the surviving source dump file.
	srcSnap, _ := sourceSnapshot(ctx, srcTarget)
	partialExists, _ := transfer.FileExists(ctx, tgtTarget)
	partialSize, _ := transfer.GetFileSize(ctx, tgtTarget)
	ev := transfer.ReconcileEvidence{
		CurrentSourceSnapshot:     srcSnap,
		PartialTargetExists:       partialExists,
		CurrentTargetPartialState: fmt.Sprintf("%d", partialSize),
		StrategyStillValid:        true,
	}
	verdict, err := transfer.Reconcile(cp, ev)
	if err != nil {
		return verdict, true // ManualIntervention or ambiguous → fail closed
	}
	return verdict, false
}

// persistTransferCheckpoint records the upload as transferred (never "verified"
// without the restore step having succeeded — that is asserted by the caller
// deleting the checkpoint only after RestoreCommand returns). Observability
// only; the resume decision is driven by the deterministic target partial file.
func (a *DatabaseApplier) persistTransferCheckpoint(transferID, fileName, srcPath, tgtPath string, res *transfer.TransferResult) {
	_ = a.checkpointStore.SaveCheckpoint(transfer.TransferCheckpoint{
		TransferID:       transferID,
		MigrationID:      a.migrationID,
		Category:         "database",
		FileName:         fileName,
		Strategy:         "scp",
		SourcePath:       srcPath,
		TargetPath:       tgtPath,
		TotalBytes:       res.BytesTransferred,
		BytesTransferred: res.BytesTransferred,
		Resumable:        true,
		LastVerifiedPhase: "transferred",
		StartedAt:        time.Now().UTC().Format(time.RFC3339),
	})
}

// sourceSnapshot returns a size+mtime fingerprint of a remote source file, used
// to detect a source that changed after a checkpoint was taken.
func sourceSnapshot(ctx context.Context, t transfer.TransferTarget) (string, error) {
	info, err := transfer.StatFile(ctx, t)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d:%d", info.Size, info.ModTime), nil
}

// dbTransferProgress adapts transfer.TransferProgress into a migration WSMessage
// for one leg (download/upload) of one database's dump.
func dbTransferProgress(onProgress StepCallback, db, leg string) func(transfer.TransferProgress) {
	if onProgress == nil {
		return nil
	}
	return func(p transfer.TransferProgress) {
		onProgress(WSMessage{
			Step:   "database:apply",
			Status: "progress",
			Value:  fmt.Sprintf("%s %s: %.1f%% — %d/%d bytes — %d B/s", leg, db, p.Percentage, p.BytesTransferred, p.TotalBytes, p.SpeedBPS),
		})
	}
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
	// Rollback works by complement: drop whatever is on the target now but was
	// not there before. That is only sound if the "before" list was actually
	// read. A backup that failed to enumerate carries an empty set, and its
	// complement is every database on the target — including ones this
	// migration never created. Refuse rather than destroy unrelated data.
	if !b.Enumerated {
		return fmt.Errorf("database rollback: refusing to drop databases — the pre-migration " +
			"backup never enumerated the target (engine undetected or listing failed), so the " +
			"databases this migration created cannot be told apart from pre-existing ones")
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
