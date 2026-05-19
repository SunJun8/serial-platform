.PHONY: test build fmt

test:
	go test ./...

fmt:
	gofmt -w cmd internal

build:
	mkdir -p bin
	go build -o bin/central-server ./cmd/central-server
	go build -o bin/host-agent ./cmd/host-agent
	go build -o bin/serialctl ./cmd/serialctl
