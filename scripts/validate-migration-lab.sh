#!/usr/bin/env bash
#
# validate-migration-lab.sh — bring up a local Meshium instance and health-check
# it, as the entry point for the manual migration validation matrix in
# docs/validation/migration-matrix.md.
#
# WHAT THIS DOES (and does not) do:
#   - Builds the meshium binary (via `make build`) if one is not already present.
#   - Starts it on a local port with a throwaway data dir, waits for
#     /api/health to report {"status":"ok"}, prints the URL, and stops it again.
#   - It DOES NOT perform a migration. Meshium has no CLI flag to trigger one:
#     migrations are driven only through the web UI / WebSocket / REST surface
#     (/api/migrations, /api/pipeline/migrations, /ws/migrate/, /ws/pipeline/).
#     Running a matrix row therefore means: use the UI/API against real source
#     and target hosts YOU control, following migration-matrix.md. This script
#     only proves the app comes up so you can begin.
#
# NON-DESTRUCTIVE BY DESIGN: it writes only to a temp data dir, binds loopback,
# and never contacts a source/target host itself. It makes no changes to any
# server — the operator does that, deliberately, through the UI/API.
#
# Usage:
#   scripts/validate-migration-lab.sh            # build (if needed), start, health-check, stop
#   PORT=9600 scripts/validate-migration-lab.sh  # override port (default 9527)
#   KEEP_RUNNING=1 scripts/validate-migration-lab.sh  # leave it running for manual matrix work
#
# There is intentionally no "run the migration" flag here: automating a real
# migration would mean claiming an end-to-end capability this script cannot
# honestly verify without live hosts. See migration-matrix.md.

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

PORT="${PORT:-9527}"
KEEP_RUNNING="${KEEP_RUNNING:-0}"
bin="$repo_root/bin/meshium"

if [ ! -x "$bin" ]; then
	echo "==> No binary at $bin; building with 'make build'"
	if ! command -v make >/dev/null 2>&1; then
		echo "error: 'make' not found and no prebuilt binary; cannot continue" >&2
		exit 2
	fi
	make build
fi

data_dir="$(mktemp -d "${TMPDIR:-/tmp}/meshium-lab.XXXXXX")"
srv_pid=""
cleanup() {
	if [ -n "$srv_pid" ] && kill -0 "$srv_pid" 2>/dev/null; then
		kill "$srv_pid" 2>/dev/null || true
		wait "$srv_pid" 2>/dev/null || true
	fi
	rm -rf "$data_dir"
}
trap cleanup EXIT

echo "==> Starting meshium on 127.0.0.1:$PORT (data dir: $data_dir)"
MESHium_PORT="$PORT" MESHium_DATA_DIR="$data_dir" MESHium_LOG_LEVEL=info \
	"$bin" &
srv_pid=$!

health_url="http://127.0.0.1:$PORT/api/health"
echo "==> Waiting for $health_url"
ok=0
for _ in $(seq 1 30); do
	if ! kill -0 "$srv_pid" 2>/dev/null; then
		echo "FAIL: server process exited before becoming healthy" >&2
		exit 1
	fi
	if curl -fsS "$health_url" 2>/dev/null | grep -q '"status":"ok"'; then
		ok=1
		break
	fi
	sleep 1
done

if [ "$ok" -ne 1 ]; then
	echo "FAIL: /api/health did not report ok within 30s" >&2
	exit 1
fi

echo "==> PASS: meshium is up and healthy at http://127.0.0.1:$PORT"
echo "    Open the UI and follow docs/validation/migration-matrix.md to run a"
echo "    matrix row against source/target hosts you control."

if [ "$KEEP_RUNNING" = "1" ]; then
	echo "==> KEEP_RUNNING=1 — leaving the server up (Ctrl-C to stop). Data dir: $data_dir"
	wait "$srv_pid"
fi
