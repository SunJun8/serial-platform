.PHONY: test build fmt web

test:
	go test ./...

fmt:
	gofmt -w cmd internal

web:
	cd web && npm ci && npm run lint && npm run build
	rm -rf internal/server/webdist
	mkdir -p internal/server/webdist
	cp -R web/dist/. internal/server/webdist/

build: web
	mkdir -p bin
	go build -o bin/central-server ./cmd/central-server
	go build -o bin/host-agent ./cmd/host-agent
	go build -o bin/serialctl ./cmd/serialctl
