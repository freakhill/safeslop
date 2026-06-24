# 0045 — Add the `pi` coding agent to the Go engine

## Problem (one line)
Register **Pi** (pi.dev — Mario Zechner / earendil-works; binary `pi`, BYOK,
`npm i -g --ignore-scripts @earendil-works/pi-coding-agent`) as a first-class
launchable agent in the **Go engine**, alongside `claude`/`opencode`/`shell`.

## Success criteria
- `safeslop run <profile>` with `agent: "pi"` launches `pi` interactively on the
  **host** and **sandbox** tiers (resolves `pi` off the host PATH).
- The **container** tier bakes a pinned `pi` into the agent-tools image and lets it
  egress to the approved ZDR-clean provider set through the squid allowlist.
- `safeslop doctor` detects `pi` and prints the install hint when absent.
- `make check` green; `fish scripts/slop-pinning.fish` clean (pi pinned, no `latest`).

## Scope / off-limits
- **Go engine ONLY.** Do NOT touch the fish/Python stack: `scripts/slop-agents.fish`,
  `scripts/_py/slop_orchestrator.py`, `library/layer/policy/**` (presets/fixtures/
  schema), or `library/layer/container/**`. opencode already lives in both stacks; pi
  intentionally does not (per sign-off 2026-06-24).
- **No per-agent allowlist mechanism.** The Go engine's egress allowlist is a single
  global file; pi's providers are added there (widens egress for all container agents —
  accepted tradeoff). A per-agent allowlist is a separate future feature, not this spec.
- **vm tier deferred.** Same posture as every agent: `pi` must be pre-baked into the
  tart base template; not wired here.
- Additive only — no renames/refactors of existing agent plumbing.

## Pins (load-bearing)
- npm package: `@earendil-works/pi-coding-agent` (the `@mariozechner/*` alias lags).
- Version: **0.80.2** (current as of 2026-06-24). The pin that takes effect in Go builds
  is the `ARG PI_VERSION` default in the engine Dockerfile (buildImages does not pass a
  version build-arg, same as claude/opencode).
- Install flag: `--ignore-scripts` (suppresses npm lifecycle scripts — supply-chain
  hygiene, matches pi's own official install command).

---

## Phase A — Spine (delivers host + sandbox; pure Go)

- [ ] A1. Add `pi` to the engine `#Agent` union
  FILE:     internal/engine/policy/schema/schema.cue
  CHANGE:   Line 14 — `#Agent: "claude" | "shell" | "opencode" | "pi"`
  VERIFY:   cd /Users/jojo/workspace/safeslop && go build ./... && echo OK
  EXPECTED: builds; the embedded schema now accepts `agent: "pi"`.

- [ ] A2. Map `pi` to its launch argv
  FILE:     internal/cli/cli.go  (func agentArgv, ~line 1169)
  CHANGE:   Add `case "pi":` returning `[]string{"pi"}, nil` (interactive, same shape as
            the `claude`/`opencode` cases — no special flags).
  VERIFY:   cd /Users/jojo/workspace/safeslop && go vet ./internal/cli/ && echo OK
  EXPECTED: vet clean.

- [ ] A3. Add the human display label
  FILE:     internal/engine/policy/risk.go  (func agentLabel, ~line 126)
  CHANGE:   Add `case "pi": return "Pi"` before the default.
  VERIFY:   cd /Users/jojo/workspace/safeslop && go vet ./internal/engine/policy/ && echo OK
  EXPECTED: vet clean.

- [ ] A4. Make `safeslop doctor` probe for `pi`
  FILE:     internal/cli/cli.go  (func doctorReport, ~line 140)
  CHANGE:   Add `"pi"` to the `tools := []string{...}` probe list (after `"opencode"`).
  VERIFY:   cd /Users/jojo/workspace/safeslop && go build ./... && ./safeslop doctor --json 2>/dev/null | grep -o '"pi"' | head -1
  EXPECTED: prints `"pi"` (key present in the doctor tools report).

- [ ] A5. Add `pi` to the tools catalog (Installs-tab + doctor install hint)
  FILE:     internal/engine/tools/tools.go  (Catalog, "// Agents" block, ~line 286)
  CHANGE:   After the opencode entry add:
            `{Name: "Pi", Category: CatAgents, Detect: []string{"pi"},`
            `  Script: "npm install -g --ignore-scripts @earendil-works/pi-coding-agent",`
            `  Note: "the Pi coding agent (pi.dev, BYOK)"}`
            (Mirror the `Codex` entry's `Script:`-based npm install shape.)
  VERIFY:   cd /Users/jojo/workspace/safeslop && go test ./internal/engine/tools/ && echo OK
  EXPECTED: tools tests pass; catalog includes a Pi/agents entry.

- [ ] A6. Unit-test the spine
  FILE:     internal/cli/cli_agentargv_test.go ; internal/engine/policy/risk_test.go ;
            internal/engine/tools/tools_test.go
  CHANGE:   (a) New test `TestAgentArgvPi`: `agentArgv(policy.Profile{Agent:"pi"})` →
            `[]string{"pi"}`, no error. (b) Assert `agentLabel("pi") == "Pi"` (mirror the
            existing opencode label assertion if present, else add one). (c) Assert the
            catalog contains a tool whose `Detect` includes `"pi"` in `CatAgents`.
  VERIFY:   cd /Users/jojo/workspace/safeslop && go test ./internal/cli/ ./internal/engine/policy/ ./internal/engine/tools/ && echo OK
  EXPECTED: all three packages pass, including the new pi assertions.

- [ ] A7. Confirm `agent: "pi"` resolves end-to-end (schema acceptance, no new test if a
          resolve harness already exists)
  FILE:     (test only) internal/cli/cli_resolve_test.go
  CHANGE:   If the file has a "resolve a minimal slop.cue" helper, add a case with
            `agent: "pi"` that resolves without error. If adding a case is awkward, skip
            and rely on A1's build + a manual check.
  VERIFY:   cd /Users/jojo/workspace/safeslop && printf 'safeslop: { profiles: { p: { agent: "pi" } } }\n' > /tmp/ss-pi.cue && ./safeslop list --config /tmp/ss-pi.cue 2>&1 | grep -i 'agent=pi'
  EXPECTED: `list` shows `agent=pi` (the engine parsed+validated a pi profile).
            (Adjust the `--config` flag to the actual flag name if it differs.)

---

## Phase B — Container tier (pinned image + egress allowlist)

- [ ] B1. Bake a pinned `pi` into the engine agent-tools image
  FILE:     internal/engine/container/assets/Dockerfile.agent.tools
  CHANGE:   Add alongside the existing agent ARGs:
            `ARG ENABLE_PI=false`
            `ARG PI_NPM_PACKAGE=@earendil-works/pi-coding-agent`
            `ARG PI_VERSION=0.80.2`
            and, next to the opencode RUN block, a conditional install:
            `RUN if [ "$ENABLE_PI" = "true" ]; then \`
            `      npm install -g --ignore-scripts "${PI_NPM_PACKAGE}@${PI_VERSION}"; \`
            `    fi`
  VERIFY:   cd /Users/jojo/workspace/safeslop && grep -q 'ENABLE_PI' internal/engine/container/assets/Dockerfile.agent.tools && grep -q 'ignore-scripts' internal/engine/container/assets/Dockerfile.agent.tools && echo OK
  EXPECTED: prints OK (pinned, --ignore-scripts present).

- [ ] B2. Enable pi when the Go backend builds the tools image
  FILE:     internal/engine/container/container.go  (func buildImages, ~line 86)
  CHANGE:   Append `"--build-arg", "ENABLE_PI=true",` to the tools-image build args
            (after the ENABLE_OPENCODE build-arg).
  VERIFY:   cd /Users/jojo/workspace/safeslop && go build ./... && grep -q 'ENABLE_PI=true' internal/engine/container/container.go && echo OK
  EXPECTED: builds; build-arg present.

- [ ] B3. Mirror the pin into the engine env template (consistency + pinning gate)
  FILE:     internal/engine/container/assets/agent-tools.env
  CHANGE:   Add `ENABLE_PI=false` near the other `ENABLE_*` lines and a pin block:
            `PI_NPM_PACKAGE=@earendil-works/pi-coding-agent`
            `PI_VERSION=0.80.2`
  VERIFY:   cd /Users/jojo/workspace/safeslop && grep -q 'PI_VERSION=0.80.2' internal/engine/container/assets/agent-tools.env && echo OK
  EXPECTED: prints OK.

- [ ] B4. Add pi's providers to the global container egress allowlist
  FILE:     internal/engine/container/assets/allowlist.domains
  CHANGE:   Append (leading-dot suffix entries; `.anthropic.com` + `.openrouter.ai`
            already present — do not duplicate):
            `.pi.dev`
            `.generativelanguage.googleapis.com`   # Gemini
            `.moonshot.ai`                          # Kimi (verify .ai vs .cn)
            `.z.ai`                                 # GLM / z.ai (verify vs .bigmodel.cn)
            `.deepseek.com`
            `.mistral.ai`
            `.sakana.ai`
            `.exa.ai`
            VERIFY each hostname against the provider's API base URL (and jojo's
            ai-router config under ~/dotfiles/claude/plugins/ai-router for the ones it
            routes) before committing — a wrong host silently breaks egress.
  VERIFY:   cd /Users/jojo/workspace/safeslop && grep -q '.pi.dev' internal/engine/container/assets/allowlist.domains && go test ./internal/engine/container/ && echo OK
  EXPECTED: container package tests pass (embed/asset checks still green) and `.pi.dev` present.

---

## Phase C — Docs + full gates

- [ ] C1. Mention pi where the engine enumerates agents for users (if such a list exists)
  FILE:     README.md (and/or specs/0001 agent enumeration) — only if an agent list is present
  CHANGE:   Add `pi` to the supported-agents enumeration. Keep minimal; do NOT invent new
            docs sections. Skip if no canonical agent list exists in the Go-engine docs.
  VERIFY:   cd /Users/jojo/workspace/safeslop && fish scripts/slop-sync-help.fish check
  EXPECTED: README ↔ --help drift gate passes (we add no new --help text, so this is a
            no-op confirmation).

- [ ] C2. Full verification sweep
  FILE:     (none)
  CHANGE:   Run the complete gate set.
  VERIFY:   cd /Users/jojo/workspace/safeslop && make check && make build && fish scripts/slop-pinning.fish && fish -n scripts/*.fish
  EXPECTED: `make check` (vet+gofmt+go test) green; binary builds; pinning gate finds no
            `latest` (pi pinned to 0.80.2); fish syntax clean.

---

## Delivery
One feature branch → PR to **forgejo** (`feat(agents): register the pi coding agent
(Go engine) — specs/0045`). Atomic commits: (1) spine A1–A6, (2) container B1–B4,
(3) docs/gates C. `make check` green before PR; merge keeps main green.

## Notes / known edges
- BYOK posture: pi reads `ANTHROPIC_API_KEY` (or `/login`). The allowlist is a *ceiling*,
  not a push — Claude Code still dials only anthropic; the added provider hosts only
  matter when pi is actually configured for them. OpenAI/xAI are intentionally **not**
  in the set (privacy hard line).
- vm tier and per-agent allowlist remain future work (out of scope here).
