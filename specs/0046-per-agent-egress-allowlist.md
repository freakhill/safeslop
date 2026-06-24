# 0046 — Per-agent (+ per-profile) container egress allowlist

## Problem (one line)
The container egress allowlist is one global file, so #24's pi providers
(deepseek/mistral/gemini/…) widened egress for **every** container agent. Scope
each agent's provider reach to itself, and let a profile add its own domains.

## Success criteria
- A `pi` container run can egress to pi's providers; a `claude`/`opencode` run
  **cannot** reach pi's extra providers (deepseek/mistral/gemini/moonshot/z.ai/
  sakana/exa) — only the shared base (incl. anthropic + openrouter).
- A profile may add domains via `egress: [...]` in safeslop.cue; they are unioned
  into that profile's allowlist.
- `network:"allow"` behaviour unchanged (allowlist bypassed); host/sandbox/vm
  unchanged. `make check` + fish gates green.

## Design (signed off 2026-06-24)
Effective container allowlist, composed **per-run** in `materializeRun`:
`effective = base ∪ AgentEgress(agent) ∪ profile.egress`.
- **base** (always): the current `allowlist.domains` MINUS pi's 8 lines — i.e. base
  infra (github/npm/pypi/pythonhosted) + the shared providers `.anthropic.com` +
  `.openrouter.ai`. (Shared-base decision: claude/opencode are NOT tightened.)
- **AgentEgress(agent)**: built-in per-agent extras. Only `pi` returns a set (its 8
  providers); all others return nil.
- **profile.egress**: optional user additions (new `#Profile` field).

The allowlist is materialized per-run into the stage dir and bind-mounted at
`/etc/squid/allowlist.domains` (squid reads it as file content), so composition is
purely "write the right file" — no squid.conf change.

## Scope / off-limits
- **Container tier only.** The VM tier uses a proxy egress model (not this
  allowlist file) — out of scope. Host/sandbox use Seatbelt allow/deny (no domain
  allowlist) — out of scope.
- Container assets (`library/layer/container/allowlist.domains`) are the canonical
  source synced into the Go embed via `make sync-container-assets` (gated by
  `make check-assets`) — edit the library/ source and sync; never the embed alone.
- Do NOT touch the fish/Python launchers or `library/layer/policy/**`.
- Additive: existing claude/opencode container egress is preserved exactly.

---

## Phase A — schema + data (no behavior change yet)

- [ ] A1. Add the `egress` field to the engine schema
  FILE:     internal/engine/policy/schema/schema.cue
  CHANGE:   In `#Profile`, after `network:`, add:
            `// Extra egress domains for environment:container with network:deny —`
            `// unioned with the base allowlist + the agent's built-in providers. A`
            `// leading dot (".example.com") is a subdomain suffix match; bare host is`
            `// exact. Ignored on network:allow and on host/sandbox/vm.`
            `egress?: [...string]`
  VERIFY:   cd /Users/jojo/workspace/safeslop && go build ./... && printf 'package safeslop\n\nsafeslop: {profiles: {p: {agent: "pi", environment: "container", network: "deny", egress: [".internal.example.com"]}}}\n' > /tmp/ss-eg.cue && go run ./cmd/safeslop validate /tmp/ss-eg.cue
  EXPECTED: "ok: /tmp/ss-eg.cue is valid".

- [ ] A2. Decode `egress` into the Go Profile
  FILE:     internal/engine/policy/policy.go  (type Profile struct, ~line 106)
  CHANGE:   Add `Egress []string \`json:"egress,omitempty"\`` with a one-line comment
            (mirror the Secrets/Toolchain comment style).
  VERIFY:   cd /Users/jojo/workspace/safeslop && go vet ./internal/engine/policy/ && echo OK
  EXPECTED: vet clean.

- [ ] A3. Built-in per-agent provider defaults
  FILE:     internal/engine/policy/egress.go  (NEW) + internal/engine/policy/egress_test.go (NEW)
  CHANGE:   `func AgentEgress(agent string) []string` returning, for "pi", the verified
            provider hosts (see A3-verify): `.pi.dev`, gemini, kimi/moonshot, `.z.ai`,
            deepseek, mistral, sakana, exa — and nil for every other agent (claude/
            opencode/shell rely on the shared base). Document that anthropic+openrouter
            live in the base, not here. Test: AgentEgress("pi") is non-empty and contains
            ".pi.dev"; AgentEgress("claude") and AgentEgress("shell") are nil.
  A3-VERIFY-HOSTS: before authoring the pi set, confirm each host against the provider's
            API base URL (#3): `.z.ai` is confirmed from ai-router GLM_BASE_URL; verify
            moonshot (.ai vs .cn), `.deepseek.com`, `.mistral.ai`,
            `.generativelanguage.googleapis.com`, `.sakana.ai`, `.exa.ai`.
  VERIFY:   cd /Users/jojo/workspace/safeslop && go test ./internal/engine/policy/ -run AgentEgress && echo OK
  EXPECTED: pass.

- [ ] A4. Lint: warn when `egress` is set but ignored
  FILE:     internal/engine/policy/lint.go (+ lint_test.go)
  CHANGE:   Add a Warning (code `egress-ignored`) when `len(p.Egress) > 0` AND
            (`p.Network == "allow"` OR `p.Environment != "container"`): egress is honored
            only on environment:container with network:deny. Test the warn fires for
            container+allow and for sandbox, and does NOT fire for container+deny.
  VERIFY:   cd /Users/jojo/workspace/safeslop && go test ./internal/engine/policy/ -run Lint && echo OK
  EXPECTED: pass.

---

## Phase B — re-home pi's providers out of the global base

- [ ] B1. Remove pi's 8 lines from the base allowlist (canonical source + sync)
  FILE:     library/layer/container/allowlist.domains  (then `make sync-container-assets`)
  CHANGE:   Delete the 8 lines added in #24 (`.pi.dev`, `.generativelanguage.googleapis.com`,
            `.moonshot.ai`, `.z.ai`, `.deepseek.com`, `.mistral.ai`, `.sakana.ai`, `.exa.ai`).
            Keep base infra + `.anthropic.com` + `.openrouter.ai`. Run
            `make sync-container-assets` to regenerate the embed copy.
  VERIFY:   cd /Users/jojo/workspace/safeslop && make check-assets && ! rg -q '\.pi\.dev|\.deepseek\.com' internal/engine/container/assets/allowlist.domains && rg -q '\.anthropic\.com' internal/engine/container/assets/allowlist.domains && echo OK
  EXPECTED: check-assets green; base no longer contains pi extras; still has anthropic.

---

## Phase C — compose the allowlist per-run (behavior)

- [ ] C1. Carry the extra domains through composeParams
  FILE:     internal/engine/container/compose.go
  CHANGE:   Add `Egress []string` to `composeParams` (doc: extra allowlist domains beyond
            the base asset; agent defaults + profile.egress, already unioned by the caller).
  VERIFY:   cd /Users/jojo/workspace/safeslop && go build ./... && echo OK
  EXPECTED: builds.

- [ ] C2. Compose base ∪ extras in materializeRun; thread the param
  FILE:     internal/engine/container/launch.go
  CHANGE:   (a) In `materializeRun`, after `allow, _ := readAsset("allowlist.domains")`,
            build the written bytes = base lines + `p.Egress`, trimmed, empties dropped,
            de-duplicated (base order preserved, new extras appended once). Write that as
            "allowlist.domains" instead of the raw asset.
            (b) Add an `egress []string` parameter to `provision`, `Launch`, and
            `PrepareSession`; set `p.Egress = egress` in the composeParams built by
            `provision`. (ComposeForDown passes nil.)
  VERIFY:   cd /Users/jojo/workspace/safeslop && go build ./... && echo OK
  EXPECTED: builds (call sites updated in C3).

- [ ] C3. Resolve agent defaults + profile.egress at the call sites
  FILE:     internal/cli/cli.go  (container.Launch @~1049, container.PrepareSession @~657)
  CHANGE:   Compute `egress := append(append([]string{}, policy.AgentEgress(prof.Agent)...), prof.Egress...)`
            and pass it as the new arg to `container.Launch` / `container.PrepareSession`.
            (Leave the `vm.PrepareSession` call unchanged — VM is out of scope.)
  VERIFY:   cd /Users/jojo/workspace/safeslop && go build ./... && go vet ./internal/cli/ && echo OK
  EXPECTED: builds + vet clean.

- [ ] C4. Test the composition
  FILE:     internal/engine/container/launch_test.go (or compose_test.go — wherever
            materializeRun is unit-tested today; add a file if none)
  CHANGE:   materializeRun with `Egress: [".pi.dev", ".anthropic.com"]` (a dup of base)
            writes an allowlist.domains that (a) contains the base infra, (b) contains
            `.pi.dev`, (c) has `.anthropic.com` exactly once (dedupe). With `Egress: nil`
            the file equals the base asset.
  VERIFY:   cd /Users/jojo/workspace/safeslop && go test ./internal/engine/container/ -run Allowlist && echo OK
  EXPECTED: pass.

---

## Phase D — docs + full gates

- [ ] D1. Document the field in the README annotated example
  FILE:     README.md (the safeslop.cue example block near the agent comment)
  CHANGE:   Add a short `egress: [...]` line/comment showing a container profile scoping
            an extra domain. Keep minimal.
  VERIFY:   cd /Users/jojo/workspace/safeslop && fish scripts/slop-sync-help.fish check
  EXPECTED: drift gate passes (no --help change).

- [ ] D2. Full verification sweep + end-to-end
  FILE:     (none)
  CHANGE:   Run all gates and prove the scoping end-to-end.
  VERIFY:   cd /Users/jojo/workspace/safeslop && make check && make build && fish scripts/slop-pinning.fish && fish -n scripts/*.fish
  EXPECTED: all green. Manual proof (document output in the PR): materialize a `pi`
            container profile and a `claude` container profile; the pi run's
            allowlist.domains contains `.deepseek.com`, the claude run's does NOT (only
            base + anthropic + openrouter).

---

## Delivery
One feature branch → PR to **forgejo** (`feat(egress): per-agent + per-profile container
egress allowlist — specs/0046`). Atomic commits: (A) schema+data+lint, (B) base re-home
+ sync, (C) per-run composition + threading, (D) docs. `make check` green before PR.

## Notes
- This partially reverts #24's global widening: pi's providers move from the shared base
  into pi's per-agent default, so they no longer leak into other agents' egress ceilings.
- A per-agent allowlist is a *ceiling*, not a push — it bounds what an agent CAN reach.
- vm tier per-agent egress is a separate future item (proxy model, not this file).
