package db

import (
	"database/sql"
	"fmt"
)

// MaintainResult reports what a maintenance pass reclaimed.
type MaintainResult struct {
	StepsPruned   int64
	VacuumRan     bool
	FreelistPages int64
	PageCount     int64
}

// prunedMarker replaces a pruned step payload. It is explicit so a reader of
// an old migration sees "payload removed by retention", not an empty collect.
const prunedMarker = `{"pruned":true,"reason":"payload removed by retention policy"}`

// activeStatuses are migration states whose payloads must never be pruned.
const activeStatuses = `('running','pending','paused','awaiting_cutover','observation')`

// Maintain applies the retention policy and reclaims disk space.
//
// The production database grew to 429MB: one stale plan carried a 113MB step
// payload indefinitely (migration_steps holds the full collected snapshot per
// category) and pages freed by deletes were never vacuumed. This prunes step
// payloads for migrations older than retentionDays that are not in an active
// state — the metadata rows stay, so history/audit remain intact; only the
// bulk snapshot is replaced with an explicit marker. A pruned migration can
// no longer be compared/re-applied and must be re-planned, which is the
// point of a retention window.
//
// It is schema-tolerant: on a database without the migration tables it is a
// no-op, so tools reusing this package do not break.
func Maintain(db *sql.DB, retentionDays int) (MaintainResult, error) {
	var res MaintainResult
	if retentionDays <= 0 {
		retentionDays = 30
	}

	var hasTables int
	err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name IN ('migrations','migration_steps')`).Scan(&hasTables)
	if err != nil {
		return res, fmt.Errorf("maintain: schema probe: %w", err)
	}
	if hasTables == 2 {
		cutoff := fmt.Sprintf("-%d days", retentionDays)
		r, err := db.Exec(`
			UPDATE migration_steps
			   SET data = ?
			 WHERE length(data) > 65536
			   AND data != ?
			   AND migration_id IN (
			       SELECT id FROM migrations
			        WHERE status NOT IN `+activeStatuses+`
			          AND created_at < datetime('now', ?)
			   )`, prunedMarker, prunedMarker, cutoff)
		if err != nil {
			return res, fmt.Errorf("maintain: prune steps: %w", err)
		}
		res.StepsPruned, _ = r.RowsAffected()
	}

	// Reclaim free pages when they dominate the file: WAL checkpoints and
	// pruned payloads leave the space on the freelist forever otherwise.
	if err := db.QueryRow(`PRAGMA freelist_count`).Scan(&res.FreelistPages); err == nil {
		_ = db.QueryRow(`PRAGMA page_count`).Scan(&res.PageCount)
		// >20% free and at least ~8MB (2000 pages @4k) — don't churn small DBs.
		if res.PageCount > 0 && res.FreelistPages > 2000 && res.FreelistPages*5 > res.PageCount {
			if _, err := db.Exec(`VACUUM`); err == nil {
				res.VacuumRan = true
			}
		}
	}
	return res, nil
}
