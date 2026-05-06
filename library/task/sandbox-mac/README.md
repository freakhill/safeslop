# task/sandbox-mac/

Wrap an agent (or any command) in a [`sandbox-exec`](https://reverse.put.as/wp-content/uploads/2011/09/Apple-Sandbox-Guide-v1.0.pdf) profile on macOS. Defense-in-depth — `sandbox-exec` is deprecated and won't survive forever, so use this *alongside* a container/VM boundary, not as a substitute.

## What it composes

- [`../../layer/policy/fixtures/<adapter>/<adapter>.sb`](../../layer/policy/fixtures/) — pre-compiled `sandbox-exec` profiles per preset.
- [`../../layer/policy/presets/`](../../layer/policy/presets/) — CUE source if you want to edit before compiling.
- [`../../layer/policy/schema/schema.cue`](../../layer/policy/schema/schema.cue) — the CUE schema if you want to author your own.

## Recipe

Open a sandboxed shell with the default profile (cwd-only, strict-egress):

```fish
source scripts/slop-macos-sandbox.fish
slop-macos-sandbox shell
```

Run a single command:

```fish
slop-macos-sandbox run -- /bin/pwd
slop-macos-sandbox run --network-policy strict-egress --path-scope repo-root -- npm test
```

Inspect the profile that would be applied without running anything:

```fish
slop-macos-sandbox print-profile --network-policy strict-egress --path-scope cwd
```

## Compose your own profile via CUE

```fish
source scripts/slop-isolate.fish

cat > .isolation.cue <<'CUE'
package isolation
import "slop.dev/isolation/presets"
isolation: presets.#ClaudeCode & {
    extras: "allow-domains": ["github.example.internal"]
}
CUE

slop-isolate validate .isolation.cue
slop-isolate compile .isolation.cue --adapter sandbox-exec --out ./out
sandbox-exec -f ./out/claude-code.sb /bin/zsh
```

## Failure modes

- `sandbox-exec: ...` errors usually mean the profile references a path that doesn't exist on this host. Check `(allow file-read*)` clauses for absolute paths. The compiler keeps the profile minimal so this is rare.
- macOS may print a deprecation warning on each `sandbox-exec` invocation; ignore it. There is currently no supported replacement for arbitrary processes (Apple's framework requires app-level integration).

## Cleanup

`sandbox-exec` runs as a child process under your shell — exiting the sandboxed shell or letting the run command finish is the cleanup. No host-side state is left behind.
