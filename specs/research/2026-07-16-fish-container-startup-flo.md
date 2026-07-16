# 2026-07-16 — Builtin Fish startup decision

Status: decision landed
Score: **94.25 / 100** (C1 9.5×35%, C2 9.5×25%, C3 9.5×25%, C4 9.0×15%; all deterministic laws pass)

## Verdict

The signed-binary builtin `fish` projects exactly two optional physical-regular globs:

- `~/.config/fish/functions/*.fish` (`fish-functions`)
- `~/.config/fish/completions/*.fish` (`fish-completions`)

It does **not** project, resolve, snapshot, mount, copy, transform, or create absent manifest rows for `~/.config/fish/config.fish` or `~/.config/fish/conf.d/*.fish`. Those files execute eagerly and cannot be assumed portable from a macOS host to the contained Debian/tool environment.

Launch argv remains exactly `fish`. Container/image-owned normal startup stays intact; retained functions and completions remain available through Fish's normal demand-loading. Host/ad-hoc Fish behavior, project-profile precedence, deny-by-default networking, package closure, and specs 0107/0108 snapshot laws remain unchanged. No `--no-config` bridge, startup parser, conditional source, command shim, package install, egress expansion, or host path is added.

## Identity and migration

Builtin identity is explicitly SHA-256 over the exact embedded CUE bytes; engine-owned projection is attached after CUE parsing and is not otherwise represented in that hash. Therefore `fish.cue` gains the exact marker:

```cue
// Builtin contract: fish-projection-v2-functions-completions-only.
```

This intentionally changes the Fish hash from `sha256:4154b2100c9e8a65f11c1d3a3c5cae98de9a5755dd44b68fb119002439957814` to `sha256:92da9d4ef90abd8f84031d9578650c319f22e3a7a7776ae34d33ed1e26e9a85e`. Tests pin both the exact projection and new hash. Existing created builtin Fish records retain the old bytes and fail the existing fidelity check; operators create a fresh session. Running sessions are not retrofitted.

## Laws

1. No eager host Fish startup file crosses the builtin boundary or appears as a skipped/absent candidate.
2. Only physical regular function/completion matches enter private snapshots; optional non-regular matches remain aggregate-omitted.
3. Normal `fish` argv and container-owned startup remain; host/ad-hoc/project profile behavior is unchanged.
4. No parsing, rewriting, shimming, dependency installation, network widening, or live host mount compensates for omitted startup code.
5. The exact-byte builtin hash transition is explicit and fail-closed.

## Rejected alternatives

- **Keep files plus `--no-config`/init paths:** adds launch machinery and disables normal user autoload paths.
- **Sanitize, shim, or install:** arbitrary shell is not safely transformable and the authority expansion is unbounded.
- **Require host guards:** does not make existing defaults deterministic and preserves automatic host-code execution.

## Verification trace

- Policy tests pin exactly two ordered optional globs, absence of eager candidates, normal package/network posture, and the new hash.
- Container tests seed eager sentinels plus function/completion fixtures and prove only demand-loaded assets publish.
- CLI tests retain exact `fish` argv for host/container and fail old builtin hashes.
- A real isolated-home Docker smoke proves no eager transcript, function/completion demand-loading, snapshot-only mounts, and cleanup.

## Method

Expansion used main 7b034e9, the live running Fish session, builtin/hash/argv code, and specs 0107/0108. AYO mined Fish and Dev Container semantics. An isolated worker selected omission over three alternatives; a Kimi evaluator found no fatal flaw. Its score is decision-quality only; implementation gates remain required by spec 0109.
