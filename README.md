# Appropriate footwear for Agentic workflows

[![Tests](https://github.com/freakhill/agentic_tactical_boots/actions/workflows/tests.yml/badge.svg?branch=main)](https://github.com/freakhill/agentic_tactical_boots/actions/workflows/tests.yml?query=branch%3Amain+is%3Asuccess)

The badge shows the current state of the test suite on `main`. Click it to see
the list of successful runs on `main` — the topmost entry is the last commit
that passed CI.

Local scripts and docs for running agent workflows with stronger isolation defaults:

- container sandbox helpers (`slop-agent-sandbox`, `slop-agent-sandbox-tools`)
- VM-backed Homebrew evaluation (`slop-brew-vm`)
- strict installer wrappers (`slop-safe-npm`, `slop-safe-uv`)
- ephemeral key/identity lifecycle helpers (`slop-gh-key`, `slop-forgejo-key`, `slop-radicle`)
- unified isolation policy compiler (`slop-isolate`: one CUE file → per-tool configs for sandbox-exec, docker-compose, envoy, claude-code, opencode, …)
- host-side agent launchers with bundled defaults (`slop-agents claude`, `slop-agents opencode`)
- a single Textual TUI hub (`slop`) wrapping every action above

## Quick start

After installing the shims (see *Install fish command shims* below), every
`slop-*` command is on PATH in any new fish shell:

```fish
scripts/slop-sandboxctl.fish help    # hub: lists every tool and what it does
```

For an interactive launcher across every tool in this repo:

```fish
slop          # Textual TUI; auto-installs Textual on first run via uv + PEP-723.
slop --check  # diagnose the first-run install path (useful behind TLS-intercepting proxies).
```

For the most common workflow (managing deploy keys for the current repo) you
can skip the menu entirely with the repo-aware shortcuts:

```fish
slop-gh-key here create-pair    # RO+RW pair for cwd's git origin, 24h, ssh-config installed
slop-gh-key here list           # list keys for current repo
slop-gh-key here revoke 12345   # revoke by id
slop-gh-key here cleanup        # revoke-expired --yes
slop-gh-key here revoke-all     # revoke-by-title '^llm-agent:' --yes (destructive)
slop-gh-key tui                 # per-tool TUI; soft-deps on [gum](https://github.com/charmbracelet/gum) (the global slop is [Textual](https://textual.textualize.io), no gum needed)
```

To launch Claude Code or OpenCode with the repo's bundled defaults applied:

```fish
slop-agents seed all            # one-time: write .claude/settings.json + opencode.json at repo root
slop-agents claude              # drop into Claude Code from the seeded cwd
slop-agents opencode            # drop into OpenCode from the seeded cwd
```

## Install fish command shims

One-shot install — cleans up legacy artifacts, writes the conf.d snippet, then execs into a fresh fish so every `slop-*` command is immediately on PATH:

```fish
./install
```

Variants: `./install --no-exec` (write snippet, do not replace shell), `./install --dry-run` (preview, no writes), `./install --help`. The bootstrap pre-flights `fish`/`uv`/`cue`; missing `uv`/`cue` warn and continue.

Or invoke the inner installer directly (does not exec fish):

```fish
scripts/slop-install.fish install
```

What this does:

- Generates `~/.config/fish/conf.d/agentic_tactical_boots.fish`. Fish sources
  it on every shell startup, so commands like `slop`, `slop-sandboxctl`,
  `slop-gh-key`, `slop-agent-sandbox`, `slop-brew-vm`, etc. are available in every new
  fish session — no `~/.local/bin` shims, no `PATH` manipulation.
- The snippet `source`s "module" scripts that define functions, and wraps
  "standalone" scripts (those with top-level code) as thin functions that
  exec them with `command fish`.
- Auto-loads completion files from `scripts/completions/`.
- Bakes the absolute repo root into the snippet so it is self-contained.
- Re-runs are idempotent — re-execute any time to regenerate the snippet.

After install, run `exec fish` (or open a new shell) to load the snippet.

Use a custom snippet directory:

```fish
scripts/slop-install.fish install --conf-dir /path/to/conf.d
```

Verify install state:

```fish
scripts/slop-install.fish status
```

Uninstall:

```fish
scripts/slop-install.fish uninstall
```

### Cleanup of legacy installs

If you previously used the older bin-shim/stow installer, the new install
detects and removes its artifacts on first run: `~/.local/bin/<our-tools>`,
`~/.local/share/fish/vendor_conf.d/agentic_tactical_boots.fish`,
`~/.local/share/fish/vendor_completions.d/<our-tools>.fish`,
`~/.config/agentic_tactical_boots/fish-tools.env`, and any
`~/.local/.local` tree-fold symlink left behind by stow. Pass
`--no-cleanup` to opt out.

## Contributor policy (important)

Before edits, read:

1. `CONTRIBUTING.md`
2. `agents.md`
3. `scripts/CONVENTIONS.md`

When changing `scripts/*.fish`, keep docs, skills, **and tests** synchronized in the same change:

- `README.md`
- affected `skills/*/SKILL.md`
- `skills/README.md` when usage/install guidance changes
- `tests/test_<script>.fish` for new/changed subcommands, flags, or error paths
- `scripts/_py/<helper>.py` and `tests/test_py_helpers.fish` when the Python helper contract changes

CI enforces this via `.github/workflows/script-doc-sync-check.yml`.

Run the test suite locally with:

```fish
fish tests/run.fish
```

### Python helpers run via `uv`

The `scripts/llm-*.fish` wrappers delegate JSON, datetime, and state-file work
to small Python helpers in `scripts/_py/llm_*.py`. Each helper carries
PEP-723 inline metadata (`requires-python`, `dependencies`) and is invoked as
`uv run --script "$HELPER_PY" <subcommand> ...` from fish. This keeps the
Python interpreter version pinned per helper and avoids relying on whatever
`python3` happens to be on `$PATH`. **`uv` is therefore a hard dependency of
the `llm-*` workflows; `python3` is not.** Any new Python work in this repo
must follow the same pattern (no bare `python3 -c '...'`).

## LLM Agent Sandboxing on macOS (fish shell)

This guide is written for macOS and fish users. It follows the Diataxis model:

- Explanation: why these controls matter and what macOS can/cannot do
- Reference: capability matrix and copy/paste config snippets
- How-to: task-oriented procedures
- Tutorials: end-to-end walkthroughs

---

## Explanation

### Threat model for coding agents

When an LLM can call tools, the main risks are:

- Prompt injection: hostile text in docs/issues/tests tricks the agent into dangerous commands
- Data exfiltration: reading secrets (`~/.ssh`, cloud creds, `.env`) then sending them over the network
- Supply chain compromise: package installs run untrusted scripts
- Persistence: shell startup files or git hooks get modified to survive beyond one session

For your use case, enforce three boundaries at all times:

1. File boundary: only mount/expose a dedicated workspace
2. Network boundary: explicit URL/domain allowlist
3. Installer boundary: strict package install policy (`npm`, `uv/pip`, `brew`)

### macOS isolation reality in 2026

- `sandbox-exec` is deprecated and not a future-proof foundation
- macOS does not provide Linux-like namespaces/cgroups for arbitrary CLI processes
- The most reliable modern boundary is virtualization/containerization ([VZ.framework](https://developer.apple.com/documentation/virtualization) via [OrbStack](https://orbstack.dev), [Lima](https://lima-vm.io), or [Tart](https://tart.run))
- Per-process outbound controls are best done with a Network Extension firewall ([LuLu](https://objective-see.org/products/lulu.html))

Practical consequence: for untrusted agent actions, run them in containers/VMs and keep host-level network controls as defense-in-depth.

Optional exception: if you need a lighter local control layer, you can use `scripts/slop-macos-sandbox.fish` (`slop-sandboxctl local ...`) on systems that still provide `sandbox-exec`. Treat it as defense-in-depth only, not as a substitute for container/VM isolation.

### Package installer threat model

- `npm`: lifecycle scripts (`preinstall`, `install`, `postinstall`) can execute arbitrary shell commands
- `uv/pip`: source builds or malicious build backends can execute arbitrary code at install time
- `brew`: formula Ruby can execute arbitrary commands during source build; bottles are prebuilt binaries you still trust by provenance

Default stance: no installer network except approved registries, no installer scripts unless necessary, immutable lockfiles/hashes.

---

## Reference

### Capability matrix (macOS)

Columns are ordered from "what the agent can see/touch on disk" through to
"what enforces the policy". Two columns are deliberately distinct:

- **URL restrictions** — HTTP/HTTPS-layer allowlist applied to the agent's
  fetch tools (webfetch/websearch/etc.).
- **Network restrictions** — broader socket-/DNS-/firewall-level egress
  control. URL allowlists do not stop a `bash -c "curl ..."` or a raw
  socket; only network-layer controls (sandbox-exec `(deny network*)`,
  Docker network namespace + proxy, host firewall like [LuLu](https://objective-see.org/products/lulu.html))
  do.
- **Process visibility limits** — whether the framework prevents the
  agent from enumerating, inspecting, or signaling other processes on the
  host (`ps`, `/proc/*/cmdline`, `lsof`, `kill`, etc.). Agents that can
  read other processes can scrape secrets out of `argv`, environment
  variables, or open file handles.

| Framework | File restrictions | SSH key restrictions | URL restrictions | Network restrictions | Process visibility limits | Installer restrictions | Enforcement level |
|---|---|---|---|---|---|---|---|
| Claude Code | Yes (`/sandbox` filesystem policy) | Yes (`denyRead` on `~/.ssh`) | Yes (managed domain filtering/proxy) | Yes when `/sandbox` profile uses `(deny network*)`; otherwise relies on app-layer allowlist | Configurable via sandbox-exec (`(deny process-info*)`, `(deny mach-lookup)`); not enforced by default profile | Indirect via command policy + environment | OS-level sandbox + app policy |
| OpenCode | Yes (permission rules, app-level) | Yes (pattern deny, app-level) | Partial (`webfetch/websearch`; bash needs external controls) | Not built-in (bash escape route); rely on Docker netns + proxy | Not built-in; rely on Docker PID namespace | Via command allow/deny + external sandbox | App policy (plus Docker if added) |
| CrewAI | Not built-in | Not built-in | Not built-in | Not built-in | Not built-in | Not built-in | External controls required |
| PydanticAI | Strong in Code Mode (Monty); otherwise not built-in | Strong in Code Mode; otherwise not built-in | Strong in Code Mode; otherwise not built-in | Strong in Code Mode (Monty isolates network); otherwise not built-in | Strong in Code Mode (Monty has no host process access); otherwise not built-in | Policy in your tool wrappers | Rust sandbox (Monty) + your controls |
| AG2 | Yes with Docker executor (`work_dir` mount) | Yes if keys never mounted | Via Docker networking/proxy | Via Docker network policy + proxy ACL | Yes via Docker PID namespace (default); broken if `--pid=host` is set | Via container policy/wrappers | Container boundary |
| OpenClaw | Partial (workspace defaults to `~/.openclaw/workspace`; broad by default — must be confined to a project mount) | Not built-in (requires explicit deny via container/sandbox; OpenClaw will read whatever the host process can read) | Partial (per-tool/channel allow/deny in `SOUL.md` and config; not a full HTTP allowlist) | Not built-in (messaging-channel egress is intentional; rely on container netns + proxy ACL) | Not built-in (rely on Docker PID namespace) | Via tool allowlist + external sandbox | App policy (plus Docker if added) |
| ZeroClaw | Yes (workspace boundary in runtime; supervised autonomy denies escape by default) | Yes if keys never mounted; runtime does not auto-expose host creds | Partial (per-tool policy; HTTP tool can be gated) | Partial (OS sandbox layer: Landlock / Bubblewrap / Seatbelt / Docker — depends on host); rely on container netns + proxy ACL for full enforcement | Yes when run under Bubblewrap/Landlock/Docker; not enforced by the binary alone | Yes (medium-risk requires approval, high-risk blocked; cryptographic tool receipts) | Rust runtime policy + OS sandbox |

For frameworks marked "Not built-in" or "Configurable", the practical
defense remains the container/VM boundary plus a host firewall:

- **Network**: route the agent through a proxy (`library/layer/container/squid.conf`)
  inside a Docker network with no direct internet route, then keep a
  host-level deny-by-default firewall ([LuLu](https://objective-see.org/products/lulu.html) or [pf](https://www.openbsd.org/faq/pf/)).
- **Process visibility**: prefer Docker / Tart so the agent runs in its
  own PID namespace. Avoid `--pid=host`, `docker run --privileged`, or
  mounting `/proc`. On macOS `sandbox-exec`, add `(deny process-info*)`
  and `(deny mach-lookup)` to the profile.

### Default best-practice recommendations per framework

These are the defaults this repo recommends. They turn each row of the
matrix into concrete configuration. Treat them as the floor: weaken
only with a written reason and a compensating control.

**All frameworks (cross-cutting):**

- Run the agent inside a container (OrbStack / Lima / Docker Desktop) or
  a disposable VM (Tart for macOS). No host home mount; mount only the
  project directory at a fixed path (e.g. `/workspace`).
- Force outbound traffic through a proxy with a deny-by-default
  allowlist. Start from `library/layer/container/allowlist.domains` and
  `library/layer/container/squid.conf`. Add a host-level firewall ([LuLu](https://objective-see.org/products/lulu.html)
  or [pf](https://www.openbsd.org/faq/pf/)) as defense-in-depth.
- Never mount `~/.ssh`, `~/.aws`, `~/.config/gcloud`, or other
  credential directories. Use ephemeral, scope-limited credentials
  generated by the helpers under `scripts/slop-*.fish`.
- Pin all installed tools to exact versions (`library/layer/container/agent-tools.env`,
  lockfiles checked in, `npm ci` / `uv sync --frozen`). CI gates this
  via `scripts/slop-pinning.fish`.
- Default network policy: `strict-egress`. Document any exceptions.
- Do not pass `--pid=host`, `--network=host`, `--privileged`, or mount
  `/var/run/docker.sock` into the agent container.

**Claude Code:** start from `library/layer/policy/claude-code.settings.json`.
Enable `/sandbox`, deny-list `~/.ssh`, `~/.aws`, `~/.config/gcloud`,
and shell rc files. Sandbox profile must include `(deny network*)`,
`(deny process-info*)`, and `(deny mach-lookup)`; allow only the
specific subpaths the session needs to write.

**OpenCode:** load `library/layer/policy/opencode.restrictive.json` via
`OPENCODE_CONFIG`, then run OpenCode inside the `agent` container from
`library/layer/container/docker-compose.yml`. App-level URL allowlist is not enough on
its own — bash escapes it; the container network namespace + proxy is
what enforces network policy.

**CrewAI:** treat the framework as having no built-in controls. Run
the crew runtime inside a container, expose only the project mount,
keep credentials in short-lived env vars or secret mounts, and route
all egress through the proxy. For tools that execute generated code,
prefer external sandbox services (E2B / Modal) over local execution.

**PydanticAI:** for any LLM-written code, use Code Mode with Monty so
execution happens in the Rust sandbox (no host process access, no host
filesystem, no host network). For ordinary tools, enforce path/network
allowlists in each tool wrapper, set `requires_approval=True` on tools
that mutate state or can exfiltrate, and cap runs with `UsageLimits`
(`request_limit`, `tool_calls_limit`, token caps).

**AG2:** use `DockerCommandLineCodeExecutor`, never the local executor.
Mount only a per-session `work_dir`; keep the container's root
filesystem read-only where possible; set `network_mode` to use the
proxy network, not `host`. Destroy the execution container after each
session.

**OpenClaw:** start from `library/task/restrictive-flows/openclaw.md`. Because
OpenClaw is a *messaging gateway* — by design it bridges messaging
platforms (WhatsApp, Telegram, Slack, Discord, …) into an LLM agent —
its blast radius is broader than a coding-only agent. Defaults:
override the workspace from `~/.openclaw/workspace` to a per-session
project mount; opt in to channels explicitly, never enable all
channels at once; never run on the host with access to host
credential directories — always run inside the `agent` container
from `library/layer/container/docker-compose.yml`; treat `SOUL.md` and any persona
file as untrusted input (prompt-injection vector); keep the proxy
allowlist narrow and add only the messaging-platform endpoints you
actually intend to bridge.

**ZeroClaw:** start from `library/task/restrictive-flows/zeroclaw.md`. ZeroClaw
ships with security-relevant defaults (workspace boundary, supervised
autonomy where medium-risk operations require approval and high-risk
are blocked, cryptographic tool receipts, optional OS sandbox layer
via [Landlock](https://landlock.io/) / [Bubblewrap](https://github.com/containers/bubblewrap) / [Seatbelt](https://reverse.put.as/wp-content/uploads/2011/09/Apple-Sandbox-Guide-v1.0.pdf) / Docker). Keep them on. On
macOS the OS sandbox layer reduces to Seatbelt — defense-in-depth
only; still run ZeroClaw inside the `agent` container so the network
namespace + proxy enforces egress. Pin the binary by checksum, not
by `latest` tag. Disable the shell tool unless a specific task needs
it; if you enable it, route it through the same proxy.

**macOS host (defense-in-depth):** for risky one-off package installs,
prefer `scripts/slop-brew-vm.fish` over the host. `scripts/slop-macos-sandbox.fish`
(`slop-sandboxctl local ...`) is acceptable as a lighter local layer for
trusted tasks but not as a substitute for container/VM isolation.

### OpenCode deep dive (requested focus)

OpenCode's permission model is useful but not sufficient by itself for untrusted execution. Use it with a container boundary.

Recommended layering:

1. Run OpenCode in Docker (no host home mount)
2. Mount only project directory
3. Route all egress through proxy allowlist
4. Use restrictive `opencode.json`

Reference config: `library/layer/policy/opencode.restrictive.json`

### OpenClaw deep dive

OpenClaw's threat surface is wider than a code-only agent because it
is, by design, a multi-channel messaging gateway: every connected
channel (WhatsApp, Telegram, Slack, Discord, Signal, iMessage, email,
…) is both an input vector for prompt injection and an output vector
for exfiltration. The host process reads workspace memory from
plain Markdown files (`SOUL.md`, notes, scratch files) — those files
are agent-controlled and must be treated as untrusted text.

Recommended layering:

1. Run OpenClaw in the `agent` container, never directly on the host.
2. Override `OPENCLAW_WORKSPACE` (or equivalent) to a per-session
   project subdirectory mounted at `/workspace`; do not let it
   default to `~/.openclaw/workspace`.
3. Enable channels one at a time, behind explicit credentials with
   the smallest possible scope (single chat, single inbox).
4. Route all egress through the proxy, and add only the messaging
   platform's API endpoints you actually need to the allowlist.
5. Treat `SOUL.md` as configuration, not as a memory bank. Review it
   like you would a system prompt.
6. Never mount host credential directories. Use ephemeral keys from
   `scripts/llm-*.fish` for any source-control identity OpenClaw
   needs.

Reference policy: `library/task/restrictive-flows/openclaw.md`.

### ZeroClaw deep dive

ZeroClaw is a single Rust binary with a trait-driven runtime: model
providers, channels, memory backends, and tools are pluggable. The
positive side is that the runtime itself ships security-relevant
defaults that this repo wants to keep enforced:

- workspace boundary (operations confined to a configured root)
- supervised autonomy (medium-risk operations require approval,
  high-risk operations are blocked)
- optional OS sandbox layer (Landlock on Linux, Seatbelt on macOS,
  Bubblewrap, or Docker)
- cryptographic tool receipts (signed audit log of every tool call)

Keep all four on. The runtime defaults are necessary but not
sufficient: the OS sandbox layer on macOS reduces to Seatbelt, which
is deprecated as a primary boundary. Treat ZeroClaw's local sandbox
as defense-in-depth and still run the binary inside the `agent`
container so the Docker network namespace + proxy enforces egress.

Recommended layering:

1. Pin the binary by SHA-256 checksum; do not fetch `latest`.
2. Run inside the `agent` container, mounting only the project
   directory.
3. Keep supervised autonomy enabled. Do not raise the auto-approve
   threshold for the agent identity.
4. Enable the shell tool only when needed; when enabled, ensure the
   container's `HTTP_PROXY`/`HTTPS_PROXY` is set so any subprocess
   inherits the allowlist.
5. Persist tool receipts to a path under `/workspace` so the audit
   log survives container teardown.

Reference policy: `library/task/restrictive-flows/zeroclaw.md`.

### Claude Code sandbox reference

Use `/sandbox` in Claude Code and keep filesystem/network policy strict. Example file: `library/layer/policy/claude-code.settings.json`.

Key points:

- Explicitly deny `~/.ssh`, cloud credentials, and shell rc files
- Restrict write access to project/work directories
- Keep domain allowlist narrow (registries + source control only)

### CrewAI reference

CrewAI has no native process sandbox for arbitrary tools. Use:

- Docker wrapper for crew runtime
- Proxy-enforced egress allowlist
- Tool wrappers for sensitive actions
- Optional external sandbox services for code execution (E2B/Modal)

Minimal hardening checklist:

- run CrewAI process in container/VM
- expose only a project mount (never host home)
- keep credentials in short-lived env vars or secret mounts
- route outbound network through allowlist proxy

### PydanticAI reference

- For LLM-written code: prefer Code Mode + Monty
- For normal tools: each tool is your code, so enforce path/network rules in tool wrappers
- Add `requires_approval=True` to high-risk tools

Minimal hardening checklist:

- use Monty for generated code execution
- guard filesystem/network tools with explicit allowlists
- enforce `UsageLimits` and approval for sensitive tools

### AG2 reference

Use `DockerCommandLineCodeExecutor` for untrusted code. Keep:

- `work_dir` as only mounted path
- read-only root fs where possible
- no host credential mounts
- proxy-enforced egress rules

Minimal hardening checklist:

- prefer `DockerCommandLineCodeExecutor` over local executor
- isolate `work_dir` per session
- avoid mounting host sockets (`/var/run/docker.sock`) unless unavoidable

### Homebrew reference (sandboxing suspicious installs)

Facts that matter:

- Homebrew still uses sandboxing for source builds on macOS, but bottles are prebuilt and skip build sandboxing
- Separate Homebrew prefixes can coexist but are not a security boundary
- Strongest option for suspicious brew installs is a disposable macOS VM (Tart)

Use `scripts/slop-brew-vm.fish` for VM-backed isolation. `scripts/brew-sandbox.fish` is only prefix separation and should not be treated as sandboxing.

### Package manager hardening reference

`npm`

- Prefer `npm ci`
- Default to `--ignore-scripts`
- Use lockfile only, no ad hoc install in agent runs
- Pin CLI packages to exact versions (no `latest` in production)

`uv/pip`

- Prefer wheels only: `--only-binary :all:`
- Pin exact versions/hashes where possible
- Use `uv sync --frozen` for project sync
- Keep pinned framework versions in `library/layer/container/agent-tools.env`

`brew`

- Audit formula first (`brew cat`, `brew info`, `brew install --dry-run`)
- Prefer official taps only
- For unknown packages, install in disposable VM first

Recommended registry/domain allowlist baseline:

- npm: `registry.npmjs.org`
- Python: `pypi.org`, `files.pythonhosted.org`
- Git source: `github.com`, `raw.githubusercontent.com`

### Artifact pinning and attestation reference

Use pinned versions by default:

- `library/layer/container/agent-tools.env`
- `library/layer/container/agent-tools.env.example`

Useful verification commands:

```fish
npm view @anthropic-ai/claude-code@2.1.121 dist.integrity
npm view opencode-ai@1.14.28 dist.integrity
"/opt/homebrew/bin/python3" -m pip index versions crewai
"/opt/homebrew/bin/python3" -m pip index versions pydantic-ai
"/opt/homebrew/bin/python3" -m pip index versions ag2
./scripts/slop-pinning.fish
```

For project dependencies:

- npm: commit `package-lock.json` and use `npm ci`
- uv: commit `uv.lock` and use `uv sync --frozen`
- Homebrew: prefer explicit formula names, official taps, and dry-run/audit before install

---

## How-to

### Unified isolation config

`slop-isolate` compiles a single [CUE](https://cuelang.org/) policy to per-tool configs (sandbox-exec, docker-compose, [squid](https://www.squid-cache.org/), [envoy](https://www.envoyproxy.io/) + [coredns](https://coredns.io/) + notifier, [lulu](https://objective-see.org/products/lulu.html), [pf](https://www.openbsd.org/faq/pf/), claude-code-settings, opencode-settings, ag2-executor, [tart](https://tart.run), [orbstack](https://orbstack.dev)). Ten presets ship: `any-agent`, `claude-code`, `opencode`, `crewai`, `pydantic-ai`, `ag2`, `openclaw`, `zeroclaw`, `nous-hermes-local`, `nous-hermes-remote`. Authors keep one `isolation.cue`; the compiler emits the config every adapter actually understands. Where an adapter cannot enforce a primitive (e.g. pf cannot match by domain), the emitted output records the gap as a comment (or fails with `--strict`).

The [Envoy](https://www.envoyproxy.io/) adapter additionally compiles a runnable docker-compose stack ([Envoy](https://www.envoyproxy.io/) + [CoreDNS](https://coredns.io/) + a notifier sidecar). It runs SNI-only by default, opts into MITM with `--mitm`, surfaces blocked flows as macOS notifications via [terminal-notifier](https://github.com/julienXX/terminal-notifier) or [alerter](https://github.com/vjeantet/alerter), and accepts `slop-isolate approve --once` (10-min TTL) or `--always` (logs the approval; user adds it to `extras.allow-domains`).

1. Install dependencies:

```fish
brew install cue-lang/tap/cue
brew install terminal-notifier   # optional: macOS deny notifications
```

2. Pick a preset and validate it:

```fish
source scripts/slop-isolate.fish
slop-isolate presets list
slop-isolate presets show claude-code
```

3. Author your config (extend a preset via the `extras` struct):

```fish
cat > .isolation.cue <<'CUE'
package isolation
import "slop.dev/isolation/presets"
isolation: presets.#ClaudeCode & {
    extras: "allow-domains": ["github.example.internal"]
    tool: pf: "domain-fallback": "fail"
}
CUE
slop-isolate validate .isolation.cue
```

4. Compile to one or every adapter:

```fish
slop-isolate compile .isolation.cue --adapter sandbox-exec --out ./out
slop-isolate compile .isolation.cue --adapter envoy --out ./out
slop-isolate compile .isolation.cue   # uses adapters.enabled list
```

5. Apply (bounded — never touches sudo/pf/lulu):

```fish
slop-isolate apply .isolation.cue --yes
```

6. Boot the interactive proxy and approve flows on the fly:

```fish
slop-isolate proxy start
slop-isolate approve --once api.example.com
slop-isolate denials --since 10m
slop-isolate proxy stop
```

### How to run any agent behind Docker + URL allowlist proxy

1. Create stack files from this repo:
   - `library/layer/container/Dockerfile.agent`
   - `library/layer/container/Dockerfile.agent.tools`
   - `library/layer/container/docker-compose.yml`
   - `library/layer/container/squid.conf`
   - `library/layer/container/agent-tools.env.example`
2. Start the proxy:

```fish
docker compose -f library/layer/container/docker-compose.yml build agent
docker compose -f library/layer/container/docker-compose.yml up -d proxy
```

3. Run agent container through proxy:

```fish
docker compose -f library/layer/container/docker-compose.yml run --rm agent
```

4. Verify blocking:

```fish
docker compose -f library/layer/container/docker-compose.yml run --rm agent sh -lc 'curl -I https://example.com'
```

Expected: denied unless domain is allowlisted.

### How to run with preinstalled CLIs/frameworks

1. Copy env template and pin versions:

```fish
cp library/layer/container/agent-tools.env.example library/layer/container/agent-tools.env
```

2. Edit `library/layer/container/agent-tools.env` and enable only the stacks you need
3. Keep versions pinned; avoid `latest` in automation
4. Build and run the tools image:

```fish
docker compose --env-file library/layer/container/agent-tools.env -f library/layer/container/docker-compose.yml build agent-tools
docker compose --env-file library/layer/container/agent-tools.env -f library/layer/container/docker-compose.yml run --rm agent-tools
```

5. Optional convenience wrapper:

```fish
source scripts/slop-agent-sandbox-tools.fish
slop-agent-sandbox-tools shell
```

or use hub command:

```fish
scripts/slop-sandboxctl.fish docker-tools shell
```

### Launch agents with defaults

`slop-agents` drops you into Claude Code or OpenCode in the right cwd,
applying the bundled defaults the first time you opt in. The bundled
JSON is the compile output of the matching CUE presets in
`library/layer/policy/presets/`, mirrored as fixtures.

1. Source the helper:

```fish
source scripts/slop-agents.fish
```

2. One-time, write secure defaults to the repo root:

```fish
slop-agents seed all
```

3. Drop into Claude Code with those defaults applied:

```fish
slop-agents claude
```

4. Drop into OpenCode with those defaults applied:

```fish
slop-agents opencode
```

`seed` writes `<repo-root>/.claude/settings.json` and
`<repo-root>/opencode.json`. It never overwrites an existing override
file — edit the resulting JSON to take control. Settings precedence,
first hit wins:

1. `<cwd>/.claude/settings.json` (or `<cwd>/opencode.json`)
2. `<repo_root>/.claude/settings.json` (or `<repo_root>/opencode.json`)
3. user-level `~/.claude/settings.json` if present, else nothing

`slop` (the Textual TUI) exposes the same flow under key `a` ("Agents").

For container-side use, drop into the agent-tools shell first and type
`claude` or `opencode` once inside (`slop-agent-sandbox-tools shell`).

### How to lock down OpenCode on macOS

1. Use restrictive config:

```fish
set -x OPENCODE_CONFIG (pwd)/library/layer/policy/opencode.restrictive.json
```

2. Run OpenCode inside the `agent` container from `library/layer/container/docker-compose.yml`
3. Do not mount host home, only mount repo workspace
4. Keep proxy allowlist minimal and add domains only when required

### How to lock down OpenClaw

1. Read `library/task/restrictive-flows/openclaw.md` and apply its policy to
   your local OpenClaw config.
2. Run OpenClaw inside the `agent` container from
   `library/layer/container/docker-compose.yml`. Do not run it on the host.
3. Override the workspace location so it does not default to the
   user's home directory:

```fish
docker compose -f library/layer/container/docker-compose.yml run --rm \
    -e OPENCLAW_WORKSPACE=/workspace/.openclaw \
    agent fish
```

4. Enable one channel at a time. For each channel, scope its
   credential to the smallest possible target (one chat, one inbox).
5. Add the channel's API host to `library/layer/container/allowlist.domains` and
   restart the proxy. Remove it when the session ends.
6. Treat `SOUL.md` as configuration, not memory. Review it on every
   session start.

### How to lock down ZeroClaw

1. Read `library/task/restrictive-flows/zeroclaw.md` and apply its policy to
   your local ZeroClaw config.
2. Pin the binary by SHA-256 and verify before running.
3. Run ZeroClaw inside the `agent` container so the Docker network
   namespace + proxy enforces egress, even though ZeroClaw also has
   its own OS sandbox layer (Seatbelt on macOS, Landlock /
   Bubblewrap on Linux).
4. Keep supervised autonomy at the default threshold (medium-risk
   asks for approval; high-risk is blocked).
5. Persist tool receipts under `/workspace` so the audit log
   survives container teardown.
6. Enable the shell tool only on demand. When enabled, confirm the
   container's `HTTP_PROXY` / `HTTPS_PROXY` are set so any
   subprocess egress goes through the allowlist.

### How to use optional local `sandbox-exec` layer on macOS

Use this only when full container/VM flows are not practical.

1. Load helper:

```fish
source scripts/slop-macos-sandbox.fish
```

2. Run command with default `cwd` scope and strict egress deny:

```fish
slop-macos-sandbox run -- /bin/pwd
```

3. Run command with repository-root scope (alternative to default `cwd` scope):

```fish
slop-macos-sandbox run --repo-root-access -- /usr/bin/env ls
slop-macos-sandbox run --path-scope repo-root -- /usr/bin/env ls
```

4. Use through the unified hub:

```fish
scripts/slop-sandboxctl.fish local run --repo-root-access -- /bin/pwd
```

5. Add explicit additional paths only when needed:

```fish
slop-macos-sandbox run --allow-read ~/.config --allow-write ./tmp -- /usr/bin/env ls
```

Notes:

- `--repo-root-access` is an alias for `--path-scope repo-root`
- `--network-policy strict-egress` (default) denies outbound network in profile
- Prefer Docker/VM workflows for untrusted execution

### How to lock down Claude Code

1. Enable `/sandbox`
2. Apply settings similar to `library/layer/policy/claude-code.settings.json`
3. Add deny rules for sensitive paths (`~/.ssh`, `~/.aws`, `~/.config/gcloud`)
4. Keep network allowlist to registries + git hosts only

### How to run CrewAI with container boundaries

1. Run your CrewAI app inside the `agent` service (or a custom image)
2. Mount only workspace paths needed by tasks
3. Keep outbound traffic proxy-only (`HTTP_PROXY`/`HTTPS_PROXY` set)
4. For code execution tools, use external sandbox providers where possible

### How to run PydanticAI safely

1. Use Code Mode + Monty for generated code
2. Wrap filesystem/network tools in allowlist checks
3. Add approval gates to mutation/exfiltration-capable tools
4. Enforce run limits (`request_limit`, `tool_calls_limit`, token limits)

### How to run AG2 safely

1. Use Docker executor classes, not local execution
2. Mount only a session directory as `work_dir`
3. Keep egress constrained by proxy ACLs
4. Destroy execution container after each session

### How to add host-level process egress controls (LuLu)

1. Install [LuLu](https://objective-see.org/products/lulu.html) (free, open source)
2. Create deny-by-default outbound policy for agent binaries
3. Allow only explicit domains/ports needed for registries and git
4. Keep this as defense-in-depth even when using containers

### How to sandbox `npm` and `uv` installs

Use helper scripts:

- `scripts/slop-safe-npm.fish`
- `scripts/slop-safe-uv.fish`

These enforce strict defaults and are designed to run inside containerized agent sessions.

### How to sandbox `brew` with disposable Tart VMs

1. Load VM helper:

```fish
source scripts/slop-brew-vm.fish
```

2. Create base template once:

```fish
slop-brew-vm create-base
```

3. Install formula in disposable VM session:

```fish
set -x BREW_VM_PROXY_URL http://<proxy-host>:3128
slop-brew-vm install --network-policy strict-egress <formula>
```

4. Optional: inspect manually in VM shell:

```fish
set -x BREW_VM_KEEP_SESSION true
slop-brew-vm install <formula>
slop-brew-vm shell
slop-brew-vm destroy
```

5. Share files explicitly with host:

```fish
slop-brew-vm copy-in ./local-file.txt /tmp/llm-share/local-file.txt
slop-brew-vm copy-out /tmp/llm-share/result.txt ./result.txt
```

6. Verify policy enforcement:

```fish
slop-brew-vm verify-network
```

Reference: `library/task/evaluate-formulae/README.md`

### How to strengthen network limiting

1. Keep default script mode as `strict-egress`
2. Maintain outbound allowlist in `library/layer/container/allowlist.domains`
3. Use internal Docker network path (`agent`/`agent-tools` -> `proxy` only)
4. For VM sessions, set `BREW_VM_PROXY_URL` and run `slop-brew-vm verify-network`
5. Keep host firewall egress rules ([LuLu](https://objective-see.org/products/lulu.html) or [pf](https://www.openbsd.org/faq/pf/)) as defense in depth

### How to manage ephemeral GitHub SSH deploy keys

1. Load helper:

```fish
source scripts/slop-gh-key.fish
```

2. Create RO + RW key pair for one repo (default TTL 24h):

```fish
slop-gh-key create-pair --repo <owner>/<repo> --name session-1 --ttl 24h
```

Optional: append SSH aliases to `~/.ssh/config` while creating:

```fish
slop-gh-key create-pair --repo <owner>/<repo> --name session-1 --ttl 24h --install-ssh-config --host-prefix github-llm
```

What this actually writes — the launcher appends a marker-fenced block
to `~/.ssh/config` so a later `uninstall-ssh-config` can remove it
cleanly:

```ssh-config
# BEGIN slop-gh-key:<owner>-<repo>:session-1:<utc-stamp>
Host github-llm-ro
  HostName github.com
  User git
  IdentityFile ~/.ssh/llm_agent_github_ro_session-1_<utc-stamp>
  IdentitiesOnly yes

Host github-llm-rw
  HostName github.com
  User git
  IdentityFile ~/.ssh/llm_agent_github_rw_session-1_<utc-stamp>
  IdentitiesOnly yes
# END slop-gh-key:<owner>-<repo>:session-1:<utc-stamp>
```

`Host github-llm-ro` is just a [name your local SSH client recognizes](https://www.man7.org/linux/man-pages/man5/ssh_config.5.html) —
the `HostName` line points it at the real `github.com`. `IdentitiesOnly yes`
forces ssh to use *only* the listed `IdentityFile`, never your
`~/.ssh/id_*`, so the ephemeral deploy key is the only credential
offered. With that block in place, you swap the alias into any git
URL targeting that one repo:

```fish
# Read-only: clone, fetch, pull
git clone git@github-llm-ro:<owner>/<repo>.git
git -C <repo> remote set-url origin git@github-llm-ro:<owner>/<repo>.git

# Read-write: same repo, different alias → gets the RW key
git -C <repo> remote set-url --push origin git@github-llm-rw:<owner>/<repo>.git
git -C <repo> push
```

The aliases are scoped per repo by their marker block, but the
`Host github-llm-ro` / `github-llm-rw` *names* are global per
`--host-prefix`. If you juggle multiple repos under one user account,
pass distinct `--host-prefix github-llm-<repo-slug>` values so the
aliases don't shadow each other in `~/.ssh/config`. See
`scripts/slop-gh-key.fish help` and `slop-gh-key uninstall-ssh-config`
for the full lifecycle.

3. List deploy keys on repo:

```fish
slop-gh-key list --repo <owner>/<repo>
```

4. Revoke one key by id:

```fish
slop-gh-key revoke --repo <owner>/<repo> --id <key-id>
```

5. Revoke keys by title pattern or expiration:

```fish
slop-gh-key revoke-by-title --repo <owner>/<repo> --match '^llm-agent:'
slop-gh-key revoke-expired --repo <owner>/<repo>
```

6. Generate or install SSH config aliases manually:

```fish
slop-gh-key print-ssh-config --ro-key ~/.ssh/llm_agent_github_ro_<stamp> --rw-key ~/.ssh/llm_agent_github_rw_<stamp>
slop-gh-key install-ssh-config --repo <owner>/<repo> --name session-1 --ro-key ~/.ssh/llm_agent_github_ro_<stamp> --rw-key ~/.ssh/llm_agent_github_rw_<stamp>
```

7. Remove old alias blocks from `~/.ssh/config`:

```fish
slop-gh-key uninstall-ssh-config --repo <owner>/<repo> --name session-1 --yes
slop-gh-key uninstall-ssh-config --marker '^slop-gh-key:<owner>-<repo>:' --yes
```

Notes:

- Prefer deploy keys (repo-scoped) over account-level SSH keys for agent identities
- Keep RO and RW keys separate and short-lived
- Enforce branch protections/rulesets for RW keys

### How to manage ephemeral Forgejo deploy keys (multi-instance)

1. Load helper:

```fish
source scripts/slop-forgejo-key.fish
```

Optional bootstrap to copy starter config locally:

```fish
slop-forgejo-key bootstrap-config
```

2. Save Forgejo instance profile once:

```fish
slop-forgejo-key instance-set --name main --url https://forgejo.example.com --token-env FORGEJO_TOKEN_MAIN
set -x FORGEJO_TOKEN_MAIN <token-with-repo-admin>
```

Reference template: `library/layer/policy/forgejo-instances.example.json`

3. Create RO + RW deploy key pair for one repository:

```fish
slop-forgejo-key create-pair --instance main --repo <owner>/<repo> --name session-1 --ttl 24h --install-ssh-config
```

4. List and revoke:

```fish
slop-forgejo-key list --instance main --repo <owner>/<repo>
slop-forgejo-key revoke --instance main --repo <owner>/<repo> --id <key-id>
slop-forgejo-key revoke-expired --instance main --repo <owner>/<repo> --yes
```

5. Remove old SSH alias blocks:

```fish
slop-forgejo-key uninstall-ssh-config --repo <owner>/<repo> --name session-1 --yes
```

### How to manage ephemeral Radicle identities across many repos

1. Load helper:

```fish
source scripts/slop-radicle.fish
```

Optional bootstrap to copy starter policy file locally:

```fish
slop-radicle bootstrap-config
```

2. Create short-lived identity:

```fish
slop-radicle create-identity --name session-1 --ttl 24h
```

3. Bind identity to current/future repositories by RID:

```fish
slop-radicle bind-repo --rid <rad:...> --identity-id <identity-id> --access ro
slop-radicle bind-repo --rid <rad:...> --identity-id <identity-id> --access rw --note "maintainer tasks"
```

4. Inspect and retire:

```fish
slop-radicle list-identities
slop-radicle list-bindings --all
slop-radicle retire-expired --yes
slop-radicle unbind-repo --rid <rad:...> --yes
```

5. Print shell export for active identity key:

```fish
slop-radicle print-env --identity-id <identity-id>
```

Reference state format: `library/layer/policy/radicle-access-policy.example.json`

---

## Tutorials

### Tutorial: first sandboxed OpenCode session

Goal: run OpenCode with file, SSH, and URL constraints in <10 minutes.

1. Start proxy and agent container:

```fish
docker compose -f library/layer/container/docker-compose.yml build agent
docker compose -f library/layer/container/docker-compose.yml up -d proxy
docker compose -f library/layer/container/docker-compose.yml run --rm agent
```

2. Inside container, set OpenCode config and start:

```fish
set -x OPENCODE_CONFIG /workspace/library/layer/policy/opencode.restrictive.json
# Install your OpenCode binary/package in this image first,
# then run it with the restrictive config.
```

3. Validate file isolation:
   - Attempt read of `/root/.ssh/id_rsa` (should fail or not exist)
   - Attempt write outside `/workspace` (should fail)

4. Validate URL isolation:
   - `curl https://registry.npmjs.org` should succeed
   - `curl https://example.com` should fail by proxy ACL

5. Tear down:

```fish
docker compose -f library/layer/container/docker-compose.yml down
```

### Tutorial: evaluate a suspicious formula safely

1. Load VM helper and create base template:

```fish
source scripts/slop-brew-vm.fish
slop-brew-vm create-base
```

2. Review formula first:

```fish
set -x BREW_VM_PROXY_URL http://<proxy-host>:3128
slop-brew-vm run --network-policy strict-egress brew cat <formula>
slop-brew-vm run --network-policy strict-egress brew info <formula>
slop-brew-vm run --network-policy strict-egress brew install --dry-run <formula>
```

3. Install in disposable VM:

```fish
slop-brew-vm install --network-policy strict-egress <formula>
```

4. Verify teardown:

```fish
slop-brew-vm destroy
```

Host remains unchanged after VM deletion.

---

## Example scripts and configs

- `scripts/slop-agent-sandbox.fish`: convenience runner for Docker sandbox
- `scripts/slop-agent-sandbox-tools.fish`: runner for tool-preinstalled sandbox image
- `scripts/slop-macos-sandbox.fish`: optional local `sandbox-exec` wrapper (defense-in-depth)
- `scripts/slop-sandboxctl.fish`: unified command hub for sandbox scripts and tutorials
- `scripts/slop-brew-vm.fish`: disposable Tart VM wrapper for Homebrew installs
- `library/task/evaluate-formulae/README.md`: VM template assumptions for `slop-brew-vm`
- `scripts/brew-sandbox.fish`: legacy isolated-prefix helper (not a sandbox)
- `scripts/slop-gh-key.fish`: generate/revoke ephemeral GitHub deploy keys
- `scripts/slop-forgejo-key.fish`: generate/revoke ephemeral Forgejo deploy keys (multi-instance)
- `scripts/slop-radicle.fish`: manage ephemeral Radicle identities and RID bindings
- `scripts/_py/llm_*.py`: pinned-Python helpers for the three `llm-*.fish` scripts (run via `uv run --script`, PEP-723 inline metadata)
- `scripts/slop-skills-install.fish`: install repo-versioned skills into local runtime
- `scripts/slop-install.fish`: install fish command shims (stow preferred, direct fallback)
- `stow/fish-tools`: stow package for tool command shims under `.local/{bin,lib}`
- `skills/agent-sandbox-ops/SKILL.md`: operating workflow for sandbox + network controls
- `skills/agent-key-lifecycle/SKILL.md`: operating workflow for key and identity lifecycle
- `library/layer/policy/forgejo-instances.example.json`: sample multi-instance Forgejo profile file
- `library/layer/policy/radicle-access-policy.example.json`: sample Radicle identity/binding state format
- `scripts/slop-pinning.fish`: CI/local gate for pinned tool versions
- `scripts/slop-safe-npm.fish`: strict npm install wrapper
- `scripts/slop-safe-uv.fish`: strict uv/pip install wrapper
- `scripts/CONVENTIONS.md`: script UX/comment/safety standards for maintainers
- `scripts/script-template.fish`: starter template for new fish scripts
- `CONTRIBUTING.md`: contributor workflow and sync requirements
- `agents.md`: agent operating contract and mandatory read order
- `library/layer/container/docker-compose.yml`: reusable agent + proxy stack
- `library/layer/container/allowlist.domains`: central outbound domain allowlist used by proxy
- `library/layer/container/Dockerfile.agent`: custom agent image with fish + uv + Python + Node
- `library/layer/container/Dockerfile.agent.tools`: optional layer with preinstalled agent stacks
- `library/layer/container/agent-tools.env.example`: pinned package/version template
- `library/layer/container/squid.conf`: URL allowlist rules
- `library/layer/policy/opencode.restrictive.json`: restrictive OpenCode permissions
- `library/layer/policy/claude-code.settings.json`: restrictive Claude Code sandbox settings
- `library/task/restrictive-flows/openclaw.md`: restrictive OpenClaw policy (channels, workspace, SOUL.md handling)
- `library/task/restrictive-flows/zeroclaw.md`: restrictive ZeroClaw policy (workspace boundary, supervised autonomy, tool receipts)

---

## Recommended baseline (short version)

If you want one default setup that works well on macOS:

1. Run agent in container (OrbStack/Lima/Docker Desktop)
2. Mount only the project directory
3. Never mount host credential directories (`~/.ssh`, cloud configs)
4. Force egress through allowlist proxy
5. Keep allowlist in `library/layer/container/allowlist.domains` and verify denials regularly
6. For risky brew installs, use disposable Tart VM first

---

## Verification checklist

Use these checks after setup to prove controls are active.

1. Build and start components:

```fish
docker compose -f library/layer/container/docker-compose.yml build agent
docker compose -f library/layer/container/docker-compose.yml up -d proxy
```

Optional tools image verification:

```fish
cp library/layer/container/agent-tools.env.example library/layer/container/agent-tools.env
docker compose --env-file library/layer/container/agent-tools.env -f library/layer/container/docker-compose.yml build agent-tools
docker compose --env-file library/layer/container/agent-tools.env -f library/layer/container/docker-compose.yml run --rm agent-tools sh -lc 'python3 --version && node --version && uv --version'
docker compose --env-file library/layer/container/agent-tools.env -f library/layer/container/docker-compose.yml run --rm agent-tools sh -lc 'python3 -m pip show crewai pydantic-ai pydantic-ai-harness ag2'
```

2. Confirm proxy policy blocks non-allowlisted URLs:

```fish
docker compose -f library/layer/container/docker-compose.yml run --rm agent \
    sh -lc 'curl -I https://example.com || true'
```

3. Confirm allowlisted registries still work:

```fish
docker compose -f library/layer/container/docker-compose.yml run --rm agent \
    sh -lc 'curl -I https://registry.npmjs.org'
docker compose -f library/layer/container/docker-compose.yml run --rm agent \
    sh -lc 'curl -I https://pypi.org/simple/'
```

4. Confirm SSH keys are not present in container:

```fish
docker compose -f library/layer/container/docker-compose.yml run --rm agent \
    sh -lc 'ls -la /root/.ssh || true'
```

5. Confirm `npm` strict mode wrapper behavior:

```fish
source scripts/slop-safe-npm.fish
slop-safe-npm
```

6. Confirm `uv` strict mode wrapper behavior:

```fish
source scripts/slop-safe-uv.fish
slop-safe-uv sync
slop-safe-uv pip-install requests==2.32.3
```

7. Confirm pinning gate passes:

```fish
./scripts/slop-pinning.fish
```

8. Confirm `brew` VM workflow is disposable:

```fish
source scripts/slop-brew-vm.fish
set -x BREW_VM_PROXY_URL http://<proxy-host>:3128
slop-brew-vm create-base
slop-brew-vm install --network-policy strict-egress wget
tart list | grep brew-sandbox-session
```

Expected: no `brew-sandbox-session` VM remains unless `BREW_VM_KEEP_SESSION=true`.

9. Confirm CI enforces pinning on PRs:

- Workflow file: `.github/workflows/pinning-check.yml`
- Trigger: pull requests and pushes to `main`

10. Confirm CI can build sandbox images:

- Workflow file: `.github/workflows/sandbox-images-check.yml`
- Builds both `agent` and `agent-tools` services

11. Confirm CI enforces script/docs/skills/tests synchronization:

- Workflow file: `.github/workflows/script-doc-sync-check.yml`
- Rule: when `scripts/*.fish` **or** `scripts/_py/*.py` changes, corresponding updates must include:
  - `README.md`
  - `skills/*/SKILL.md` or `skills/README.md`
  - `tests/*.fish`

12. Confirm CI runs the test suite:

- Workflow file: `.github/workflows/tests.yml`
- Runs `fish tests/run.fish` on Ubuntu for every PR and push to `main`
