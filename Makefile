.PHONY: build dev test clean

build:
	cd web && npm install && npm run build
	mkdir -p bin
	go build -o bin/meshium ./cmd/server/

dev:
	( cd web && npm install && npm run dev ) & \
	WEB_PID=$$!; \
	trap 'kill $$WEB_PID' INT TERM EXIT; \
	go run ./cmd/server/

test:
	go test ./...
	cd web && npm install && npm run check

clean:
	rm -rf bin/ cmd/server/web/build/ web/.svelte-kit/
