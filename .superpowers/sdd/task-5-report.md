# Task 5 Report: Database Layer

## What I implemented
- Added a new `internal/db` package with:
  - `Open(path string) (*sql.DB, error)` for opening SQLite databases via `modernc.org/sqlite`.
  - `Migrate(db *sql.DB) error` to create the four required tables:
    - `app_config`
    - `servers`
    - `server_info`
    - `known_hosts`
- Added `internal/db/db_test.go` to verify migrations create all required tables.
- Added SQLite dependency management through `go.mod` / `go.sum`.

## TDD evidence
### RED
Before implementation, the test run failed as expected because the package functions did not exist yet:

```bash
cd "/root/meshium" && go test ./internal/db -v
```

Result:
- `undefined: Open`
- `undefined: Migrate`

### GREEN
After implementation and dependency tidying, the database tests passed:

```bash
cd "/root/meshium" && go test ./internal/db -v
```

Result:
- `PASS`

I also ran the full suite:

```bash
cd "/root/meshium" && go test ./...
```

Result:
- `PASS` across `cmd/server`, `internal/db`, and `internal/shared`

## Files changed
- `internal/db/db_test.go` — added migration test
- `internal/db/db.go` — added SQLite open helper
- `internal/db/migrations.go` — added schema creation migrations
- `go.mod` — added SQLite driver dependency
- `go.sum` — added dependency checksums

## Self-review findings
- The implementation is intentionally small and matches the task brief closely.
- I added a small compatibility improvement for `:memory:` databases by using a shared-cache in-memory DSN and limiting the DB pool to one connection, which keeps the test database stable across queries.
- `go.mod` was updated to `go 1.25.0` because the current `modernc.org/sqlite` release selected by Go requires that toolchain version.
- No failing tests remain.
