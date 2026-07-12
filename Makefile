.PHONY: build dev test clean

build:
	rm -rf cmd/server/web/build
	cd web && npm install && npm run build
	test -f cmd/server/web/build/index.html
	mkdir -p bin
	go build -o bin/meshium ./cmd/server/

dev:
	( cd web && npm install && npm run dev ) & \
	WEB_PID=$$!; \
	trap 'kill $$WEB_PID' INT TERM EXIT; \
	go run ./cmd/server/

test:
	cd web && npm install && npm run check && npm run build
	go test ./...

# Integration tests require the docker CLI and pull postgres:15 on demand.
test-integration:
	go test -tags integration ./internal/mod/migration/ -run 'PGCutoverPrimitivesLive|PGPreflightFailsWhenTargetNotStandby|FencedCutoverWithRealPG' -v -timeout 300s

# Phase 2B transfer integration tests (alpine+rsync pulled on demand).
test-transfer-integration:
	go test -tags integration ./internal/mod/transfer/ -run 'LiveRsync' -v -timeout 300s

clean:
	rm -rf bin/ cmd/server/web/build/ web/.svelte-kit/
