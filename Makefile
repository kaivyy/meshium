.PHONY: build dev test clean

build:
	mkdir -p bin
	go build -o bin/meshium ./cmd/server/

dev:
	go run ./cmd/server/

test:
	go test ./... -v

clean:
	rm -rf bin/
