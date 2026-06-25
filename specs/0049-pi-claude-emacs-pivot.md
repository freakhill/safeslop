# 0049 — Pi + Claude Code + Emacs pivot

## Goal

Pivot safeslop to a smaller, safer product surface:

- Supported agents: `pi` and Claude Code (`agent: "claude"` in today's policy schema).
- Removed surfaces: OpenCode, VS Code/Visual Studio Code, Swift/macOS cockpit, and the Go control-plane/server UI.
- First-class UI: canonical raw Emacs Lisp package for Emacs 32.1+, with Doom Emacs examples optional only.
- Runtime invariant: the signed Go `safeslop` binary remains the only required runtime; Emacs invokes it with argv lists, never shell strings.
- Contract invariant: Go and Emacs parse the same versioned JSON golden fixtures from the same checkout.

`specs/0047` is merged. Its remaining `creds gc` and live-smoke items stay separate except where they intersect session cancellation/revocation.

## Locked decisions

Keep:

- `pi`
- Claude Code. For the first migration slice, preserve the existing schema value `agent: "claude"`; a later harness-registry PR may decide whether to introduce a user-facing alias `claude-code`.

Drop:

- `opencode`
- `open-code`
- `vscode`
- `vs code`
- `visual studio code`
- Swift cockpit / `SafeSlopCockpit`
- `safeslop serve` and old control-plane UI paths

Dropped names may remain only in this migration spec and in negative tests proving rejection.

## PR sequence

### PR1 — Repair main and narrow agent launch/seed surface

Purpose:

- Fix the current broken main caused by `agentseed.go` embedding a gitignored, untracked `agentfixtures/opencode.json`.
- Remove OpenCode from launch argv, seed fixtures, doctor inventory, and README's active agent list.
- Add negative tests for OpenCode and VS Code at the CLI seed/argv boundary.

Files:

- `.gitignore`
- `README.md`
- `internal/cli/agentseed.go`
- `internal/cli/agentseed_test.go`
- `internal/cli/cli.go`
- `internal/cli/cli_agentargv_test.go`
- `internal/cli/cli_test.go`

Required tests:

- `TestAgentSeedAcceptsPiAsNoop`
- `TestAgentSeedAcceptsClaudeCode`
- `TestAgentSeedClaudeCodeIsNonClobbering`
- `TestAgentSeedRejectsOpenCode`
- `TestAgentSeedRejectsVSCode`
- `TestAgentSeedDoesNotEmbedOpenCodeFixture`
- `TestAgentArgvAcceptsPi`
- `TestAgentArgvAcceptsClaudeCode`
- `TestAgentArgvRejectsOpenCode`
- `TestAgentArgvRejectsVSCode`
- `TestAgentArgvRejectsUnknownAgent`
- `TestDoctorDoesNotReportOpenCode`

Gates:

```sh
go test ./internal/cli -run 'TestAgentSeed|TestAgentArgv|TestDoctorDoesNotReportOpenCode' -count=1
make check
make build
```

Merge gate:

- no missing `go:embed`
- no live OpenCode seed/argv/doctor path
- OpenCode/VS Code remain only as negative tests in `internal/cli` and as broader follow-up work tracked below

### PR2 — Remove Swift cockpit and Go control plane

Delete:

- `app/`
- `internal/engine/control/`
- `internal/cli/cli_cockpit_smoke_test.go`
- `.woodpecker/cockpit.yml`

Edit:

- `internal/cli/cli.go`
- `internal/cli/cli_resolve_test.go`
- `internal/engine/buildinfo/buildinfo.go`
- `internal/engine/buildinfo/buildinfo_test.go`
- `internal/engine/install/desired.go`
- `internal/engine/install/plan.go`
- `internal/engine/tools/tools.go`
- `internal/engine/tools/tools_test.go`
- `internal/engine/sandbox/sandbox.go`
- `internal/engine/sandbox/sandbox_test.go`
- `internal/engine/vm/launch.go`
- `internal/engine/container/container.go`
- `internal/engine/container/launch.go`
- `internal/engine/container/runtime/lima.go`
- `internal/engine/hostenv/reconstruct.go`
- `Makefile`
- `README.md`
- `CLAUDE.md`
- `RELEASE.md`
- `.woodpecker/*.yml`
- `.github/workflows/*.yml`

Remove:

- `safeslop serve`
- cockpit command/help/docs
- protobuf sync targets
- Swift app build/signing/notarization targets
- gRPC/control server runtime assumptions

Grep gate:

```sh
git grep -nEi 'SafeSlopCockpit|Package\.swift|xcodebuild|swiftc|control\.proto|grpc|proto-sync|sign-notarize' -- . ':!specs/**'
```

Expected: no matches in live code/docs. Historical specs are archival design records and are excluded from the live-surface denylist.

### PR3 — Add versioned Go ↔ Emacs JSON contract

Add:

- `internal/jsoncontract/contract.go`
- `internal/jsoncontract/errors.go`
- `internal/jsoncontract/contract_test.go`
- `internal/jsoncontract/testdata/*.golden.json`

Contract envelope:

```json
{
  "schema_version": 1,
  "ok": true,
  "data": {},
  "warnings": [],
  "errors": []
}
```

Error/warning shape:

```json
{
  "code": "AGENT_UNSUPPORTED",
  "message": "unsupported agent",
  "details": {},
  "retryable": false
}
```

Append-only v1 error codes:

- `INVALID_ARGUMENT`
- `SCHEMA_UNSUPPORTED`
- `SCHEMA_VIOLATION`
- `NOT_FOUND`
- `CONFLICT`
- `PERMISSION_DENIED`
- `AUTH_REQUIRED`
- `CREDENTIAL_REVOKED`
- `CREDENTIAL_REVOKE_FAILED`
- `POLICY_DENIED`
- `NETWORK_DENIED`
- `SANDBOX_DENIED`
- `SANDBOX_UNAVAILABLE`
- `RUNTIME_UNAVAILABLE`
- `TOOL_UNAVAILABLE`
- `AGENT_UNSUPPORTED`
- `SESSION_NOT_FOUND`
- `SESSION_ALREADY_RUNNING`
- `SESSION_STOPPED`
- `SESSION_CANCELLED`
- `PTY_UNAVAILABLE`
- `TIMEOUT`
- `RATE_LIMITED`
- `IO_ERROR`
- `INTERNAL`

Minimum golden fixtures:

- `ok-minimal.golden.json`
- `ok-session-create.golden.json`
- `ok-session-status.golden.json`
- `ok-policy-check-with-warning.golden.json`
- `error-invalid-argument.golden.json`
- `error-agent-unsupported.golden.json`
- `error-credential-revoked.golden.json`
- `error-pty-unavailable.golden.json`

### PR4 — Add canonical Emacs package and pinned Emacs CI

Add:

- `emacs/safeslop.el`
- `emacs/safeslop-contract.el`
- `emacs/safeslop-session.el`
- `emacs/test/safeslop-test.el`
- `emacs/test/safeslop-contract-test.el`
- `emacs/test/fixtures/**`
- `emacs/examples/doom/config.el`
- `emacs/README.md`
- `ci/emacs32/build-emacs.sh`
- `ci/emacs32/emacs-32.1.tar.xz.sha256`

Makefile:

- add `test-emacs`
- wire `test-emacs` into `check` from PR4 onward

CI rule:

- build/download pinned Emacs 32.1 only
- verify SHA256
- no mutable `brew install emacs`, `apt install emacs`, `emacs-snapshot`, or `latest`

Local fallback is allowed:

```sh
make test-emacs EMACS=/absolute/path/to/emacs
```

but the target must print the version and fail if older than Emacs 32.1.

### PR5 — Add Emacs session PTY, JSONL status, cancellation, revocation

Go commands:

```text
safeslop session create --agent <pi|claude> --workspace <dir> --output json
safeslop session run --session-id <id>
safeslop session status --session-id <id> --output jsonl
safeslop session stop --session-id <id> --revoke-credentials --output json
```

Emacs process paths:

1. One-shot JSON control via argv list and built-in JSON parsing.
2. Interactive PTY via built-in `make-term` / `term-mode`, launching exact argv:

   ```text
   safeslop session run --session-id <id>
   ```

3. PTY-unavailable fallback: read-only `compilation-mode` monitor over:

   ```text
   safeslop session status --session-id <id> --output jsonl
   ```

Credential invariant:

- stop/signal paths revoke ephemeral credentials before forced kill
- stop/revoke is idempotent
- JSONL redacts before write

### PR6 — Remove remaining OpenCode policy/container/library refs and final denylist

Delete:

- `library/layer/policy/fixtures/opencode/`
- `library/layer/policy/opencode.restrictive.json`
- `library/layer/policy/presets/opencode.cue`

Edit OpenCode references in:

- `internal/engine/container/assets/agent-tools.env`
- `internal/engine/container/assets/Dockerfile.agent.tools`
- `internal/engine/container/container.go`
- `internal/engine/policy/egress_test.go`
- `internal/engine/policy/risk.go`
- `internal/engine/policy/schema/schema.cue`
- `internal/engine/tools/tools.go`
- `library/layer/container/agent-tools.env.example`
- `library/layer/container/docker-compose.yml`
- `library/layer/container/Dockerfile.agent.tools`
- `library/layer/policy/samples/slop/slop.cue`
- `library/layer/policy/schema/schema.cue`
- `library/task/launch-agent/README.md`
- `library/task/README.md`
- `README.md`

Add:

- `ci/pivot-denylist.sh`
- `make check-pivot-denylist`

Final denylist:

```sh
git grep -nEi '\b(opencode|open-code|vscode|vs code|visual studio code|SafeSlopCockpit|cockpit|Package\.swift|xcodebuild|swiftc|control\.proto|grpc|proto-sync|sign-notarize)\b' -- . \
  ':!specs/**' \
  ':!internal/cli/agentseed_test.go' \
  ':!internal/cli/cli_agentargv_test.go' \
  ':!ci/pivot-denylist.sh'
```

Expected: no output outside allowed negative tests and the denylist script itself. Historical specs are archival design records and are excluded from the live-surface denylist.

## Emacs architecture

Raw package is canonical:

```elisp
(add-to-list 'load-path "/abs/path/to/safeslop/emacs")
(require 'safeslop)
```

Requirements:

- Emacs 32.1+
- raw Emacs Lisp
- no Doom APIs in core
- no `vterm`, `magit`, or `transient` dependency
- no shell interpolation
- built-in JSON parsing

Keymap prefix:

```text
C-c s
```

Bindings:

- `C-c s d` → `safeslop-doctor`
- `C-c s p` → `safeslop-policy-check-file`
- `C-c s n` → `safeslop-session-new`
- `C-c s a` → `safeslop-session-attach`
- `C-c s l` → `safeslop-session-list`
- `C-c s t` → `safeslop-session-status`
- `C-c s s` → `safeslop-session-stop`
- `C-c s r` → `safeslop-session-restart`
- `C-c s b` → `safeslop-switch-to-session-buffer`
- `C-c s e` → `safeslop-show-last-error`
- `C-c s ?` → `safeslop-help`

Doom is optional discoverability only. Example `packages.el`:

```elisp
(package! safeslop
  :recipe (:local-repo "/abs/path/to/safeslop/emacs"
           :files ("*.el")))
```

Example `config.el`:

```elisp
(use-package! safeslop
  :commands (safeslop-doctor
             safeslop-policy-check-file
             safeslop-session-new
             safeslop-session-list
             safeslop-session-status
             safeslop-session-stop)
  :init
  (define-key global-map (kbd "C-c s d") #'safeslop-doctor)
  (define-key global-map (kbd "C-c s p") #'safeslop-policy-check-file)
  (define-key global-map (kbd "C-c s n") #'safeslop-session-new)
  (define-key global-map (kbd "C-c s l") #'safeslop-session-list)
  (define-key global-map (kbd "C-c s s") #'safeslop-session-stop))
```

## Hermetic Emacs test harness

Implement `safeslop-test--with-fake-cli`:

- create a temporary fake executable
- route exact argv vectors to stubbed stdout/stderr/exit
- log every argv
- exit `97` on unregistered argv
- clean up with `unwind-protect`
- never invoke the real `safeslop`
- never use a shell

Required ERT contracts:

- `safeslop-test-doctor-parses-ok-envelope`
- `safeslop-test-policy-check-surfaces-warning`
- `safeslop-test-session-new-claude-code-exact-argv`
- `safeslop-test-session-new-pi-exact-argv`
- `safeslop-test-unsupported-agent-error-code`
- `safeslop-test-session-stop-revokes-credentials`
- `safeslop-test-workspace-path-never-shell-expanded`
- `safeslop-test-fallback-compilation-mode-on-pty-unavailable`

Critical shell-injection fixture:

```text
/tmp/safeslop a;b $(touch pwn)
```

Expected:

- passed as one argv element
- no `pwn` file created

## Cross-fixture drift mechanism

Both Go and Emacs consume:

```text
internal/jsoncontract/testdata/*.golden.json
```

Emacs resolves repo root from the test file:

```elisp
(defun safeslop-test--repo-root ()
  (file-truename
   (expand-file-name
    "../.."
    (file-name-directory
     (or load-file-name buffer-file-name default-directory)))))
```

CI runs in the same checkout:

```sh
go test ./internal/jsoncontract
make test-emacs
```

No fixture copying, artifact sharing, or second source of truth.

## Final acceptance checklist

Complete only when:

1. `go test ./...` passes.
2. `make test-emacs` passes using pinned Emacs 32.1.
3. `make check` passes.
4. `make build` passes.
5. `make check-pivot-denylist` passes.
6. `app/` is gone.
7. `internal/engine/control/` is gone.
8. OpenCode policy fixtures/presets are gone.
9. Public docs mention only Pi, Claude Code, Go CLI, and Emacs.
10. Emacs tests use fake CLI, not the real binary.
11. Shell-injection ERT test proves no `pwn` file is created.
12. Go and Emacs parse the same golden JSON fixtures.
13. Session stop/signal paths revoke ephemeral credentials before forced kill.
14. No CI path uses mutable/unpinned Emacs.
