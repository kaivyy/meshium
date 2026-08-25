# Google Drive Sync (gsync) — Design

Approved 2026-08-26. Scope: rsync-style one-way sync of local folders to
Google Drive, scheduled or manual.

## Decisions

- **rclone shell-out** (not native Drive API): `rclone sync <local> gdrive:<remote>`
  gives exactly the requested "rsync" semantics — mirror, delta transfer,
  deletions. rclone owns OAuth refresh, chunked upload, retries. Requires the
  `rclone` binary on the meshium host; absence is a first-class error surfaced
  in status/UI.
- **Generic folder pairs**: user defines N pairs `{localPath, remotePath}`.
  No pair is pre-created.
- **Trigger**: per-pair `intervalHours` schedule + manual "Sync now".
- **Auth**: user pastes the JSON emitted by `rclone authorize drive` (run on
  any laptop). Stored AES-encrypted under `app_config` key `gsync_token`,
  same key discipline as SSH credentials (`authSvc.GetAESKey()`). Never
  returned by any API.

## Storage (app_config keys — no new tables)

| key | value |
|---|---|
| `gsync_token` | AES-encrypted rclone token/config JSON |
| `gsync_pairs` | JSON array of Pair |
| `gsync_last_run` | JSON map pairID → RunResult |

Pair: `{id, name, localPath, remotePath, enabled, intervalHours}`.
RunResult: `{startedAt, finishedAt, ok, summary}` (summary = rclone stats tail;
stderr is truncated to ~2 KiB and never contains the token).

## Backend: internal/mod/gsync

`Service` with an injectable runner interface (`type runner interface {
Run(ctx, cfgPath, src, dst string) (string, error) }`) so unit tests fake the
binary. Real runner writes a temp rclone config file (0600), runs
`rclone sync --config <tmp> --auto-confirm`, deletes the temp file.

REST (all behind existing auth):
- `GET /api/gsync/status` → configured?, pairs, last runs, binary present?
- `POST /api/gsync/config` → store pasted token (encrypted)
- `DELETE /api/gsync/config` → disconnect
- `POST /api/gsync/pairs` → upsert pair; `DELETE /api/gsync/pairs/{id}`
- `POST /api/gsync/run/{id}` → run now (synchronous for manual; scheduler uses job engine)

Scheduler: one goroutine, 1-minute tick, enqueues a `gsync` job into the
existing job engine when a pair is due. Jobs appear on the existing `/jobs`
page with logs.

## Security

- Token encrypted at rest; APIs return only expiry metadata.
- Temp config 0600, removed on exit (defer).
- rclone stderr goes to run summary only after redaction check; token never
  logged (it lives only inside the temp config file).

## UI

Settings page card: connection status, token paste form, pair list
(add/edit/delete/enable), **Sync now** button, last-run result per pair.

## Testing

Unit: service with fake runner (success, failure, missing binary, unconfigured),
handler round-trip via httptest. Live end-to-end requires the user's real
Google token — explicitly out of scope until provided.

## Skipped (YAGNI)

Bi-directional sync, multiple remote providers, bandwidth limits, per-file
filters — add when actually needed; rclone flags make them one-line additions.
