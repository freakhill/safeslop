# safeslop — Go engine build (SP1). See specs/0003-sp1-go-engine-foundation.md.

BINARY  := safeslop
PKG     := ./cmd/safeslop
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X github.com/freakhill/safeslop/internal/cli.Version=$(VERSION)
# RELEASE=1 stamps the binary as the notarized release artifact, which lets the cockpit claim the
# notarization-backed root of trust in its install precautions (internal/engine/buildinfo). Only
# `make sign` sets it (it rebuilds dist with RELEASE=1 right before notarizing). Dev `make build`
# leaves Release=false so the precaution wording stays honest about an unsigned/adhoc binary.
ifeq ($(RELEASE),1)
LDFLAGS += -X github.com/freakhill/safeslop/internal/engine/buildinfo.Release=true
endif
GOFILES := cmd internal

CONTAINER_SRC := library/layer/container
CONTAINER_DST := internal/engine/container/assets
SYNCED        := allowlist.domains Dockerfile.agent Dockerfile.agent.tools

# The control-plane schema lives in two hand-synced copies: the Go-side source
# protoc reads, and the Swift bundle the cockpit compiles. They must stay
# identical — a one-sided edit drifts silently and breaks the cockpit build.
PROTO_GO    := internal/engine/control/control.proto
PROTO_SWIFT := app/Sources/SafeSlopCockpit/proto/control.proto

.PHONY: build test vet fmt fmtcheck check check-assets proto-sync proto-sync-check sync-container-assets dist sign clean proto cockpit cockpit-fresh cockpit-app cockpit-icon test-integration

## Click-test the SwiftUI cockpit with zero setup: build + seed a test repo + serve + run the app
## (engine torn down on quit). You only deal with the GUI. `cockpit-fresh` also resets the trust store.
cockpit:
	@bash app/run-cockpit-test.sh

cockpit-fresh:
	@bash app/run-cockpit-test.sh --fresh

## Visual smoke: build + seed + serve + launch the cockpit, screenshot it to a PNG, then tear down.
## No click-test needed — inspect $$COCKPIT_SHOT (default /tmp/safeslop-cockpit.png). Needs a GUI session.
cockpit-shot:
	@bash app/screenshot-cockpit.sh

## Assemble the signed-ish SafeSlop.app bundle (icon + Info.plist) in app/.build/SafeSlop.app.
cockpit-app:
	@bash app/packaging/build-app.sh

## Regenerate app/packaging/SafeSlop.icns from the icon generator (run after editing the design).
cockpit-icon:
	@bash app/packaging/make-icns.sh

## Regenerate the gRPC control-plane stubs (dev-only; needs protoc + protoc-gen-go[-grpc]).
## Generated *.pb.go are committed, so CI/`make build` never run protoc.
proto:
	protoc --go_out=. --go_opt=module=github.com/freakhill/safeslop \
	       --go-grpc_out=. --go-grpc_opt=module=github.com/freakhill/safeslop \
	       internal/engine/control/control.proto

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
## single source of truth), and gate on drift — mirrors slop-sync-help.
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

## Copy the canonical Go-side control.proto over the Swift bundle copy (Go is the
## source protoc + the committed *.pb.go derive from). Run after editing the schema.
proto-sync:
	@cp $(PROTO_GO) $(PROTO_SWIFT)
	@echo "synced $(PROTO_GO) -> $(PROTO_SWIFT)"

## Gate on the two control.proto copies drifting apart — mirrors check-assets.
proto-sync-check:
	@diff -q $(PROTO_GO) $(PROTO_SWIFT) >/dev/null || { \
	  echo "drift: $(PROTO_SWIFT) != $(PROTO_GO) (run 'make proto-sync')"; exit 1; }

## The full local gate, mirrored by .github/workflows/go.yml.
check: check-assets proto-sync-check vet fmtcheck test

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

## Codesign + notarize the dist artifacts (needs an Apple Developer cert; see the script). Rebuilds
## dist with RELEASE=1 first, so the binary that gets notarized is the one stamped to claim the
## notarization-backed root of trust in the cockpit (internal/engine/buildinfo).
sign:
	$(MAKE) dist RELEASE=1
	bash scripts/sign-notarize.sh dist/$(BINARY)-darwin-arm64 dist/$(BINARY)-darwin-amd64

clean:
	rm -f $(BINARY)
	rm -rf dist
