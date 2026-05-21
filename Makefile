.PHONY: test test-unit test-real-serial-soft test-real-serial build fmt web

GO_TEST_PACKAGES := $(shell go list ./... | grep -v '/internal/e2e$$')

test: test-unit test-real-serial-soft

test-unit:
	go test $(GO_TEST_PACKAGES)

test-real-serial-soft:
	REAL_SERIAL_DEV="$(REAL_SERIAL_DEV)" REAL_SERIAL_BAUD="$(REAL_SERIAL_BAUD)" REAL_SERIAL_EXPECT_LOOPBACK="$(REAL_SERIAL_EXPECT_LOOPBACK)" REAL_SERIAL_SOFT=1 REAL_SERIAL_REQUIRED= go test -v ./internal/e2e -run 'TestRealSerial(Loopback|PlatformWorkflow)' -count=1

test-real-serial:
	REAL_SERIAL_DEV="$(REAL_SERIAL_DEV)" REAL_SERIAL_BAUD="$(REAL_SERIAL_BAUD)" REAL_SERIAL_EXPECT_LOOPBACK="$${REAL_SERIAL_EXPECT_LOOPBACK:-1}" REAL_SERIAL_REQUIRED=1 go test -v ./internal/e2e -run 'TestRealSerial(Loopback|PlatformWorkflow)' -count=1

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
