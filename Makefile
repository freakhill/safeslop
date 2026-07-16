# safeslop — Go engine build (SP1). See specs/0003-sp1-go-engine-foundation.md.

BINARY  := safeslop
PKG     := ./cmd/safeslop
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X github.com/freakhill/safeslop/internal/cli.Version=$(VERSION)
GOFILES := cmd internal
EMACS  ?= emacs
# Local floor for the Emacs 32 line. CI pins exactly 32.1 via ci/emacs32; this
# default keeps `make check` runnable on a 32.1 pretest (reports 32.0.x).
EMACS_MIN ?= 32.0

CONTAINER_SRC := library/layer/container
CONTAINER_DST := internal/engine/container/assets
SYNCED        := allowlist.domains Dockerfile.agent Dockerfile.agent.tools

.PHONY: build test test-emacs test-emacs-ui-matrix test-progressive-egress-smoke vet fmt fmtcheck check check-assets check-catalog-sync check-pivot-denylist check-host-helper-exec sync-container-assets render-catalog install install-emacs install-mcp dist clean

## Build the local binary (static — no cgo, immune to the WARP/uv install path).
build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $(BINARY) $(PKG)

test:
	go test ./...

test-emacs:
	@$(EMACS) --batch --eval '(princ (format "emacs %s\n" emacs-version))'
	@$(EMACS) --batch --eval '(if (version< emacs-version "$(EMACS_MIN)") (progn (princ (format "emacs %s is older than required $(EMACS_MIN)\n" emacs-version)) (kill-emacs 1)))'
	$(EMACS) --batch -L emacs -l ert -l emacs/test/safeslop-test.el -l emacs/test/safeslop-contract-test.el -l emacs/test/safeslop-profiles-test.el -l emacs/test/safeslop-credentials-test.el -l emacs/test/safeslop-ui-probe.el -f ert-run-tests-batch-and-exit
	$(EMACS) --batch -L emacs -l emacs/safeslop.el -l emacs/safeslop-doom.el -l emacs/safeslop-session.el --eval '(message "safeslop emacs ok")'
	## Byte-compile gate (specs/0063 F10): fails on ERRORS; warnings stay advisory
	## because warning sets differ across the local floor vs CI-pinned Emacs.
	## SAFESLOP_ELISP_WERROR=1 escalates warnings locally. .elc goes to a temp dir.
	$(EMACS) --batch -L emacs \
	  --eval '(let ((d (make-temp-file "safeslop-elc" t))) (setq byte-compile-dest-file-function (lambda (f) (expand-file-name (concat (file-name-nondirectory f) "c") d))))' \
	  --eval '(setq byte-compile-error-on-warn (not (null (getenv "SAFESLOP_ELISP_WERROR"))))' \
	  -f batch-byte-compile emacs/*.el

test-emacs-ui-matrix:
	EMACS="$(EMACS)" ci/emacs-ui-matrix.sh

test-progressive-egress-smoke: build
	SAFESLOP_BIN="$(CURDIR)/$(BINARY)" ci/progressive-egress-smoke.sh

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

## Render the authored catalog.cue into the embedded catalog.json (specs/0059 W2).
## In-process cuelang (no external `cue` binary); validates against schema/catalog.cue.
render-catalog:
	go run ./internal/engine/policy/cmd/rendercatalog

## Fail CI if catalog.cue and the committed catalog.json have drifted (mirrors
## check-assets): the embedded artifact must always be the render of the source.
check-catalog-sync:
	@tmp=$$(mktemp); \
	go run ./internal/engine/policy/cmd/rendercatalog $$tmp >/dev/null 2>&1 || { echo "render failed"; rm -f $$tmp; exit 1; }; \
	diff -q internal/engine/policy/catalog.json $$tmp >/dev/null || { echo "drift: catalog.json (run 'make render-catalog')"; rm -f $$tmp; exit 1; }; \
	rm -f $$tmp

## The full local gate, mirrored by .github/workflows/go.yml.
check-pivot-denylist:
	ci/pivot-denylist.sh

check-host-helper-exec:
	ci/host-helper-exec-denylist.sh

check: check-assets check-catalog-sync check-pivot-denylist check-host-helper-exec vet fmtcheck test test-emacs

install-emacs:
	mkdir -p "$(HOME)/.local/share/safeslop/emacs"
	rsync -a --delete --include='*.el' --exclude='*' emacs/ "$(HOME)/.local/share/safeslop/emacs/"
	@echo "installed safeslop Emacs package -> $(HOME)/.local/share/safeslop/emacs"

install-mcp:
	@found=0; \
	for d in cmd/*mcp*; do \
	  [ -d "$$d" ] || continue; \
	  found=1; \
	  name=$$(basename "$$d"); \
	  mkdir -p "$(HOME)/.local/bin"; \
	  CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o "$(HOME)/.local/bin/$$name" "./$$d"; \
	  echo "installed $$name -> $(HOME)/.local/bin/$$name"; \
	done; \
	if [ "$$found" = 0 ]; then echo "no safeslop MCP server package found; skipped MCP install"; fi

install: build install-emacs install-mcp
	mkdir -p "$(HOME)/.local/bin"
	install -m755 $(BINARY) "$(HOME)/.local/bin/$(BINARY)"
	@echo "installed $(BINARY) -> $(HOME)/.local/bin/$(BINARY)"

## Cross-compile the two macOS arches into dist/ (signing-ready static binaries).
dist:
	mkdir -p dist
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-darwin-arm64 $(PKG)
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-darwin-amd64 $(PKG)

clean:
	rm -f $(BINARY)
	rm -rf dist
