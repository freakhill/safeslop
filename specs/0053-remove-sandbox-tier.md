# 0053 — Remove the macOS sandbox (Seatbelt) isolation tier

Status: accepted (2026-06-28)
Supersedes the sandbox parts of: 0001 §6.2, 0023 (isolation tiers), 0029 (file scope).

## Why

The `sandbox` tier (macOS `sandbox-exec` / Seatbelt) was the design's first-class
local boundary and the **default environment**. In practice it does not earn that
place for the agents safeslop actually launches:

- **It can't run the real agents.** Claude Code (and `pi`) are home-installed,
  network-bound binaries. The workspace-only Seatbelt profile denies the broad
  `$HOME` reads they need at startup and (by default) denies egress, so a
  `claude` session under `sandbox` dies immediately — verified: `claude` exits
  non-zero with `EPERM` / `Failed to connect to api.anthropic.com`. The shipped
  `examples/safeslop.cue` `review` profile (`claude`/`sandbox`/`deny`) is
  advertised as "launch sandboxed Claude Code" but cannot work.
- **It is only a "mistake-guard."** `EnvTier` already labels it honestly: it
  guards accidents, not a malicious-code escape. The two tiers that give real
  guarantees — `container` (egress-allowlisted) and `vm` (adversary-grade) — keep
  the agent's runtime *inside* the boundary and do not have this problem.
- **Carrying a broken default is worse than not having it.** A first-class tier
  that silently fails the headline use case is a footgun and a maintenance tax
  (a whole Seatbelt profile generator, symlink-farm exec handling, file-scope
  plumbing, per-tier lint/risk/consent branches).

So: drop the tier. The honest tiers become **host** (no boundary), **container**
(egress-allowlisted), **vm** (disposable). Network-bound agents belong in
`container`/`vm`; `host` stays for "I accept no isolation."

## Decisions

1. **`environment` is required — no default.** Previously `*"sandbox"`. A security
   tool must never silently run with weaker isolation than intended, so every
   profile and every `session create` must name `host`, `container`, or `vm`
   explicitly. (The Emacs picker still *preselects* `container` for convenience;
   that is UI, not an engine default.)
2. **`environment: "sandbox"` is a hard validation error.** It is removed from the
   `#Environment` enum, so a cue that names it fails `validate`/`run` with a clear
   message rather than being silently remapped. The CLI `--environment sandbox`
   override errors the same way.
3. **`files: {read, write, deny}` (`#FileScope`) is removed.** It only ever scoped
   the Seatbelt profile's extra read/write paths; `container`/`vm` do not use it.

## What changes

### Engine / CLI
- Delete `internal/engine/sandbox/` (package + tests).
- `policy.go`: drop `FileScope` struct and `Profile.Files`; rework `EnvTier` so
  `host`/`container`/`vm` are explicit and the `default:` is an honest "unknown,
  no boundary" rather than the old sandbox fall-through.
- `risk.go`: every env switch loses its `// sandbox` default; `host`/`container`/`vm`
  become explicit, default is the conservative (host-equivalent) case.
- `lint.go`: drop the `sandbox-open-egress-with-creds` rule; fix the
  `egress-ignored` message (no more "host/sandbox use Seatbelt").
- `consent.go`: the workspace-confinement cross-tier decoy re-points from
  `sandbox` to `container` (still a valid FALSE-for-host decoy).
- `cli.go`: remove the `sandbox` launch dispatch case, the `sandbox-exec` doctor
  report, the `sandbox`/seatbelt-profile branches in `plan`, `sandboxScope`, the
  `sandbox` entries in the doctor tier list and `--environment` validation/help;
  make `session create --environment` required.
- `session.go`: `Store.Create` takes `environment` (required); drop the hardcoded
  `"sandbox"` default.
- `childenv.go`: comments now describe the host child only.

### Schema / presets / examples
- `schema/schema.cue`: `#Environment: "container" | "vm" | "host"`; `environment`
  required (no `*`); remove `#FileScope` + `files?`; refresh `#Network` /
  `#Environment` comments.
- Delete sandbox presets: `claude-sandbox-offline.cue`, `claude-scoped-home.cue`,
  `shell-sandbox-offline.cue`.
- `examples/safeslop.cue` + `testdata/valid.cue`: move `sandbox` profiles to
  `container`/`host`; add explicit `environment` to the previously-defaulted ones.

### Emacs
- `safeslop-session.el`: env picker offers `container`/`vm`/`host`, preselects
  `container`.
- `safeslop-portal.el`: drop the `safeslop-tier-sandbox` face + legend entry.

### Tests / fixtures
- Delete `sandbox_test.go`, `sandbox_toolchain_test.go`.
- Update every `*_test.go` and golden fixture (`ok-session-create`,
  `ok-session-detached`) that names `sandbox` to a surviving tier; drop assertions
  that depend on the sandbox default / file scope.

### Docs (current-state only)
- `README.md`, `STATUS.md`, `CONTRIBUTING.md`, `AGENTS.md`, project `CLAUDE.md`.

## Out of scope (left as-is)
- Historical `specs/*` — design records; not rewritten.
- Legacy `library/layer/policy/**` (`*.sb` Seatbelt fixtures, duplicate schema,
  presets) — not Go-runtime (the engine generates profiles in code, not from
  these). A separate sweep can delete them; flagged, not done here.
- `skills/agent-sandbox-ops/SKILL.md` — operator doc, follow-up.

## Migration
A cue with `environment: "sandbox"` (or none) now fails validation:
`environment: 3 errors in empty disjunction` / required-field. Fix: set
`environment: "container"` (network-bound agents) or `"host"` (no isolation).

## Verification
`make check` (vet, gofmt, `go test ./...`, Emacs ERT, asset + denylist) and
`make build` both green.
