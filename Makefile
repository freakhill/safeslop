# safeslop — Go engine build (SP1). See specs/0003-sp1-go-engine-foundation.md.

BINARY  := safeslop
PKG     := ./cmd/safeslop
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X github.com/freakhill/safeslop/internal/cli.Version=$(VERSION)
GOFILES := cmd internal

CONTAINER_SRC := library/layer/container
CONTAINER_DST := internal/engine/container/assets
SYNCED        := allowlist.domains Dockerfile.agent Dockerfile.agent.tools

.PHONY: build test vet fmt fmtcheck check check-assets sync-container-assets dist clean test-integration

## Build the local binary (static — no cgo, immune to the WARP/uv install path).
build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $(BINARY) $(PKG)

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w $(GOFILES)

fmtcheck:
	@test -z "$$(gofmt -l $(GOFILES))" || { echo "unformatted files:"; gofmt -l $(GOFILES); exit 1; }

## Sync the canonical container assets into the Go embed dir (library/ stays the
## single source of truth), and gate on drift.
sync-container-assets:
	@for f in $(SYNCED); do cp $(CONTAINER_SRC)/$$f $(CONTAINER_DST)/$$f; done
	@cp $(CONTAINER_SRC)/agent-tools.env.example $(CONTAINER_DST)/agent-tools.env
	@echo "synced $(SYNCED) agent-tools.env -> $(CONTAINER_DST)"

check-assets:
	@for f in $(SYNCED); do \
	  diff -q $(CONTAINER_SRC)/$$f $(CONTAINER_DST)/$$f >/dev/null || { \
	    echo "drift: $(CONTAINER_DST)/$$f (run 'make sync-container-assets')"; exit 1; }; \
	done
	@diff -q $(CONTAINER_SRC)/agent-tools.env.example $(CONTAINER_DST)/agent-tools.env >/dev/null || { \
	  echo "drift: agent-tools.env (run 'make sync-container-assets')"; exit 1; }

## The full local gate, mirrored by .github/workflows/go.yml.
check: check-assets vet fmtcheck test

## Opt-in integration tests behind the `integration` build tag — currently the install->uninstall->install
## idempotency proof on a real tart VM (specs/0041 task 6). NOT part of `check`: it boots a VM and does
## real network installs. Needs tart on a darwin/arm64 host; self-skips when tart is absent. Wired as a
## manual/cron Woodpecker pipeline (.woodpecker/integration.yml), never on every push.
test-integration:
	go test -tags integration -timeout 35m ./...

## Cross-compile the two macOS arches into dist/ (signing-ready static binaries).
dist:
	mkdir -p dist
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-darwin-arm64 $(PKG)
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-darwin-amd64 $(PKG)

clean:
	rm -f $(BINARY)
	rm -rf dist
