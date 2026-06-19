# SP7c-3 — secrets + credentials staging parity for cockpit sessions Implementation Plan

**Goal:** Bring `slop serve` cockpit sessions (`OpenSession`/`Attach`) to credential parity with `slop run`: a session's agent gets the profile's resolved secrets and staged credentials, instead of just the inherited host env.

**Architecture:** Today `resolveSession` (cli) passes `secretEnv = nil` and no `Env`, so cockpit agents run with the bare `slop serve` environment (the SP7c-1/2 deferral). `slop run`'s `runProfile` already does the full staging — resolve `secrets`, stage pnpm/aws/gcp/kube/ssh into a per-run dir, deliver per environment. We **extract that staging into a shared `stageProfile` helper** that both `runProfile` and `resolveSession` call, then wire `resolveSession` to stage into a per-session dir and deliver: host/sandbox via the process `Env` (`SessionSpec.Env`), container via the existing `secrets.env` + bind-mounted cred files, vm via the scp'd stage. One credential — **ssh deploy keys** — is deferred in the cockpit (it mints a per-window key keyed off the *workspace* git origin, but `slop serve`'s cwd isn't the workspace); the cockpit rejects it with a clear pointer to `slop run`, mirroring how `slop run` already gates kube/ssh on vm.

**Tech stack:** Go, the existing `secrets`/`creds`/`policy`/`control`/`sandbox`/`container`/`vm` engine packages. No new deps, no `.proto` change.

**Scope:** secrets (`op://`/`env:`) + pnpm + aws + gcp + kube, delivered to all applicable cockpit environments. **ssh deploy-key creds are deferred** in the cockpit (rejected with an error; `slop run` keeps supporting them). vm keeps its existing kube reject. `runProfile`'s observable behaviour is unchanged — its existing tests are the regression guard.

**Base branch:** new feature branch `sp7c-3-cockpit-secrets` off `main` (SP7c-2 merged, `main` @ `39d03da`). **Never push `main`.**

**File structure:**
- `internal/cli/cli.go` (modify) — extract `stageProfile` from `runProfile`; rewrite `resolveSession` to stage + deliver per env; add `chainClose`.
- `internal/cli/cli_stage_test.go` (create) — `stageProfile` resolves an `env:` secret; no creds → empty pathEnv.
- `internal/cli/cli_resolve_test.go` (modify) — secret reaches host `SessionSpec.Env`; ssh-cred profile rejected; the existing host/sandbox + container/vm-absent tests still hold.

---

## Design decisions (read before executing)

1. **Shared `stageProfile`, not duplicated staging.** `runProfile` and `resolveSession` need identical staging; DRY it into one helper. `runProfile` keeps owning its stage-dir lifecycle (the `<ws>/.slop/runtime/<name>` dir, its wipe, its `RevokeSSH` defer, its vm kube/ssh reject) so its behaviour is byte-for-byte unchanged.

2. **Per-session stage dir for all four envs.** `resolveSession` runs inside `OpenSession` before a session id exists, so it creates a unique `os.MkdirTemp(<ws>/.slop/runtime, "cockpit-*")` (also the vm clone name) — preserving the SP7c-1 N-concurrent-sessions guarantee. host/sandbox now get a stage dir too (cheap; needed when pnpm/gcp/kube write files there).

3. **Delivery per env** (mirrors `runProfile`):
   - **host / sandbox:** `SessionSpec.Env = os.Environ() + secretEnv + pathEnv`. `secretEnv` = secrets + aws + gcp (sensitive values); `pathEnv` = `NPM_CONFIG_USERCONFIG` / `KUBECONFIG` host paths into the stage dir.
   - **container:** `secretEnv` → `PrepareSession` → the `secrets.env` the entrypoint sources; pnpm/kube files staged into the dir are bind-mounted (`provision` already `os.Stat`s them).
   - **vm:** `secretEnv` → scp'd `secrets.env`. (kube rejected; pnpm scp'd-but-not-env-wired exactly as `slop run` leaves it.)

4. **ssh deferred in the cockpit.** `StageSSH` runs `git remote get-url origin` in the process cwd to scope the deploy key; `slop serve`'s cwd is not the workspace, and a fresh per-window key (with revoke) is awkward UX. Reject ssh-cred profiles in `resolveSession` with a pointer to `slop run`. `slop run` is unaffected. Lifting this later means giving `StageSSH` an explicit repo dir + deciding the per-window key story.

5. **No `RevokeSSH` in the cockpit.** Because ssh is rejected, no deploy key is ever minted in a cockpit session, so `OnClose` is just stage-dir wipe (host/sandbox) or the existing `PrepareSession` teardown (container/vm). aws/gcp/kube/op tokens are decay-first (no revoke), same as `slop run`.

---

### Task 1: extract `stageProfile` from `runProfile`

**Files:**
- Modify: `internal/cli/cli.go` (`runProfile`, currently ~`406-502`)
- Test: `internal/cli/cli_stage_test.go` (create)

- [ ] **Step 1: Write the failing test** — create `internal/cli/cli_stage_test.go`:

```go
package cli

import (
	"context"
	"slices"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/policy"
)

func TestStageProfileResolvesEnvSecret(t *testing.T) {
	t.Setenv("TEST_SLOP_SECRET", "s3cr3t")
	prof := policy.Profile{Secrets: map[string]string{"FOO": "env:TEST_SLOP_SECRET"}}
	secretEnv, pathEnv, err := stageProfile(context.Background(), prof, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(secretEnv, "FOO=s3cr3t") {
		t.Fatalf("secretEnv missing the resolved secret: %v", secretEnv)
	}
	if len(pathEnv) != 0 {
		t.Fatalf("no credentials → pathEnv must be empty: %v", pathEnv)
	}
}
```

- [ ] **Step 2: Run it, verify it fails**

```bash
go test ./internal/cli/ -run TestStageProfileResolvesEnvSecret -v
```
Expected: FAIL — `stageProfile` undefined.

- [ ] **Step 3: Add `stageProfile` and refactor `runProfile` to use it.** In `internal/cli/cli.go`, add the helper (place it just above `runProfile`):

```go
// stageProfile resolves the profile's secrets and stages its credentials into stageDir. It
// returns secretEnv (sensitive KEY=VAL — the resolved secrets plus aws/gcp env creds, destined
// for the secrets.env channel / the process env) and pathEnv (non-secret NPM_CONFIG_USERCONFIG /
// KUBECONFIG host paths into stageDir, for the host/sandbox process env). The caller owns the
// stageDir lifecycle (creation, the on-exit wipe, and creds.RevokeSSH if it staged an ssh key).
func stageProfile(ctx context.Context, prof policy.Profile, stageDir string) (secretEnv, pathEnv []string, err error) {
	if len(prof.Secrets) > 0 {
		resolved, err := secrets.ResolveMap(ctx, prof.Secrets)
		if err != nil {
			return nil, nil, err
		}
		for k, v := range resolved {
			secretEnv = append(secretEnv, k+"="+v)
		}
	}
	npmrcEnv, err := creds.StagePnpm(ctx, prof.Credentials, stageDir)
	if err != nil {
		return nil, nil, err
	}
	awsEnv, err := creds.StageAWS(ctx, prof.Credentials, stageDir)
	if err != nil {
		return nil, nil, err
	}
	gcpEnv, err := creds.StageGCP(ctx, prof.Credentials, stageDir)
	if err != nil {
		return nil, nil, err
	}
	secretEnv = append(secretEnv, awsEnv...)
	secretEnv = append(secretEnv, gcpEnv...)
	kubeEnv, err := creds.StageKube(ctx, prof.Credentials, stageDir)
	if err != nil {
		return nil, nil, err
	}
	sshEnv, err := creds.StageSSH(ctx, prof.Credentials, stageDir)
	if err != nil {
		return nil, nil, err
	}
	pathEnv = append(pathEnv, npmrcEnv...)
	pathEnv = append(pathEnv, kubeEnv...)
	pathEnv = append(pathEnv, sshEnv...)
	return secretEnv, pathEnv, nil
}
```

Then replace the staging block inside `runProfile` (from the `var secretEnv []string` declaration through the `kubeEnv`/`sshEnv` staging, i.e. the original lines ~427-479) with a single call, and update the `switch` to use `pathEnv`. The resulting `runProfile` body:

```go
func runProfile(name string, prof policy.Profile, argv []string, ws string) (int, error) {
	ctx := context.Background()

	stageDir := filepath.Join(ws, ".slop", "runtime", name)
	defer os.RemoveAll(stageDir) // wipe staged secrets/.npmrc regardless of outcome

	// kube/ssh creds need a file at a boundary-stable path; vm's scp'd stage path
	// (unknown guest $HOME, single-quoted secrets.env) isn't wired yet. Fail fast,
	// before minting any token / registering any deploy key (specs/0010, specs/0011).
	if prof.Environment == "vm" && prof.Credentials != nil {
		if prof.Credentials.Kube != nil {
			return 1, fmt.Errorf("kube credentials are not yet supported with environment:%q — use environment:\"container\" (specs/0010)", prof.Environment)
		}
		if prof.Credentials.Ssh != nil {
			return 1, fmt.Errorf("ssh credentials are not yet supported with environment:%q — use environment:\"container\" (specs/0011)", prof.Environment)
		}
	}

	secretEnv, pathEnv, err := stageProfile(ctx, prof, stageDir)
	if err != nil {
		return 1, err
	}
	if prof.Credentials != nil && prof.Credentials.Ssh != nil {
		defer creds.RevokeSSH(context.Background(), stageDir)
	}

	switch prof.Environment {
	case "sandbox":
		env := append(append(os.Environ(), secretEnv...), pathEnv...)
		return sandbox.Launch(ctx, engexec.LaunchSpec{Argv: argv, Dir: ws, Env: env}, ws, prof.Network)
	case "host":
		env := append(append(os.Environ(), secretEnv...), pathEnv...)
		return engexec.RunInTerminal(ctx, engexec.LaunchSpec{Argv: argv, Dir: ws, Env: env})
	case "container":
		// secrets go in secrets.env (sourced by the entrypoint); .npmrc and kubeconfig
		// are staged in stageDir and reached via the /slop/runtime bind mount.
		return container.Launch(ctx, engexec.LaunchSpec{Argv: argv}, ws, prof.Network, secretEnv, stageDir)
	case "vm":
		// secrets ride secrets.env scp'd into the VM and sourced over ssh; the VM is destroyed on exit.
		tk := ""
		if prof.Toolchain != nil {
			tk = prof.Toolchain.Kind
		}
		return vm.Launch(ctx, argv, prof.Network, secretEnv, stageDir, name, tk)
	default:
		return 1, fmt.Errorf("unknown environment %q", prof.Environment)
	}
}
```

> This preserves `runProfile` exactly: the env was `os.Environ() + secretEnv + npmrcEnv + kubeEnv + sshEnv`; now it's `os.Environ() + secretEnv + pathEnv` where `pathEnv = npmrc + kube + ssh` — identical order and contents. `secrets`, `creds` are already imported by `cli.go`.

- [ ] **Step 4: Run it, verify it passes** (new test + the unchanged `runProfile` cred tests)

```bash
go test ./internal/cli/ -run 'TestStageProfile|Creds|Lint|Toolchain' -v
go build ./...
```
Expected: PASS; build green.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/cli/cli.go internal/cli/cli_stage_test.go
git add internal/cli/cli.go internal/cli/cli_stage_test.go
git commit -m "refactor(cli): extract stageProfile (shared secret/cred staging); runProfile unchanged"
```

---

### Task 2: stage + deliver creds in `resolveSession` (all four environments)

**Files:**
- Modify: `internal/cli/cli.go` (`resolveSession`)
- Test: `internal/cli/cli_resolve_test.go`

- [ ] **Step 1: Write the failing tests** — append to `internal/cli/cli_resolve_test.go` (it already has `package cli` + imports `os`, `path/filepath`, `strings`, `testing`, and `sandbox`; add `"slices"` to its import block):

```go
const secretHostCue = `package slop
slop: {
	version: 1
	profiles: {
		h: {agent: "claude", environment: "host", network: "deny", secrets: {FOO: "env:TEST_SLOP_SECRET"}}
	}
}
`

const sshHostCue = `package slop
slop: {
	version: 1
	profiles: {
		h: {agent: "claude", environment: "host", network: "deny", credentials: {ssh: {}}}
	}
}
`

func TestResolveSessionDeliversSecretToHostEnv(t *testing.T) {
	t.Setenv("TEST_SLOP_SECRET", "s3cr3t")
	dir := t.TempDir()
	path := filepath.Join(dir, "slop.cue")
	if err := os.WriteFile(path, []byte(secretHostCue), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir) // any cockpit-* stage dir lands under a throwaway cwd

	spec, err := resolveSession("h", path)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !slices.Contains(spec.Env, "FOO=s3cr3t") {
		t.Fatalf("secret not delivered to host env: %v", spec.Env)
	}
	if spec.OnClose != nil {
		spec.OnClose() // stage-dir wipe must not panic
	}
}

func TestResolveSessionRejectsSshCreds(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "slop.cue")
	if err := os.WriteFile(path, []byte(sshHostCue), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	if _, err := resolveSession("h", path); err == nil || !strings.Contains(err.Error(), "ssh credentials") {
		t.Fatalf("expected ssh-cred rejection, got %v", err)
	}
}
```

- [ ] **Step 2: Run them, verify they fail**

```bash
go test ./internal/cli/ -run 'TestResolveSessionDeliversSecret|TestResolveSessionRejectsSsh' -v
```
Expected: FAIL — host case sets no `Env` yet (secret absent), and ssh creds are not rejected.

- [ ] **Step 3: Rewrite `resolveSession`.** Replace the whole function (the version from SP7c-2, with the `switch prof.Environment` of host/sandbox/container/vm/default) with:

```go
// resolveSession turns a profile name into a control.SessionSpec: the (optionally toolchain-
// wrapped) agent argv, the workspace, the profile's resolved secrets + staged credentials, and
// the per-environment cleanup as OnClose. Credential parity with `slop run` (SP7c-3), minus ssh
// deploy keys, which are deferred in the cockpit (they key off the workspace git origin).
func resolveSession(profile, configPath string) (control.SessionSpec, error) {
	path, err := findConfig(configPath)
	if err != nil {
		return control.SessionSpec{}, err
	}
	cfg, err := policy.Load(path)
	if err != nil {
		return control.SessionSpec{}, err
	}
	_, prof, err := selectProfile(cfg, profile)
	if err != nil {
		return control.SessionSpec{}, err
	}
	argv, err := agentArgv(prof)
	if err != nil {
		return control.SessionSpec{}, err
	}
	if prof.Toolchain != nil && toolchain.Wraps(prof.Toolchain.Kind) {
		argv = toolchain.Wrap(prof.Toolchain.Kind, prof.Toolchain.Run, argv)
	}
	ws := prof.Workspace
	if ws == "" {
		ws, _ = os.Getwd()
	}

	// Credential gates the cockpit can't satisfy yet (reject before staging anything):
	// ssh mints a per-window deploy key scoped to the *workspace* git origin, but slop serve's
	// cwd isn't the workspace — deferred to `slop run`. vm can't reach kube (mirrors runProfile).
	if prof.Credentials != nil {
		if prof.Credentials.Ssh != nil {
			return control.SessionSpec{}, fmt.Errorf("ssh credentials aren't supported in cockpit sessions yet (the deploy key is scoped to the workspace git origin); use `slop run`")
		}
		if prof.Environment == "vm" && prof.Credentials.Kube != nil {
			return control.SessionSpec{}, fmt.Errorf("kube credentials are not supported with environment:%q; use environment:\"container\" (specs/0010)", prof.Environment)
		}
	}

	// Per-session stage dir (unique → N concurrent sessions don't collide; also the vm clone name).
	base := filepath.Join(ws, ".slop", "runtime")
	if err := os.MkdirAll(base, 0o700); err != nil {
		return control.SessionSpec{}, err
	}
	stageDir, err := os.MkdirTemp(base, "cockpit-*")
	if err != nil {
		return control.SessionSpec{}, err
	}
	secretEnv, pathEnv, err := stageProfile(context.Background(), prof, stageDir)
	if err != nil {
		_ = os.RemoveAll(stageDir)
		return control.SessionSpec{}, err
	}
	wipe := func() { _ = os.RemoveAll(stageDir) }

	switch prof.Environment {
	case "host":
		env := append(append(os.Environ(), secretEnv...), pathEnv...)
		return control.SessionSpec{Argv: argv, Dir: ws, Env: env, OnClose: wipe}, nil
	case "sandbox", "": // sandbox is the default
		wrapped, wrapCleanup, err := sandbox.WrapArgv(argv, ws, prof.Network)
		if err != nil {
			_ = os.RemoveAll(stageDir)
			return control.SessionSpec{}, err
		}
		env := append(append(os.Environ(), secretEnv...), pathEnv...)
		return control.SessionSpec{Argv: wrapped, Dir: ws, Env: env, OnClose: chainClose(wrapCleanup, wipe)}, nil
	case "container":
		cargv, cleanup, err := container.PrepareSession(context.Background(), argv, ws, prof.Network, secretEnv, stageDir)
		if err != nil {
			_ = os.RemoveAll(stageDir)
			return control.SessionSpec{}, err
		}
		return control.SessionSpec{Argv: cargv, Dir: ws, OnClose: cleanup}, nil // cleanup wipes stageDir
	case "vm":
		tk := ""
		if prof.Toolchain != nil {
			tk = prof.Toolchain.Kind
		}
		// stageDir basename is the per-session VM clone name (concurrency isolation).
		vargv, cleanup, err := vm.PrepareSession(context.Background(), argv, prof.Network, secretEnv, stageDir, filepath.Base(stageDir), tk)
		if err != nil {
			_ = os.RemoveAll(stageDir)
			return control.SessionSpec{}, err
		}
		return control.SessionSpec{Argv: vargv, Dir: ws, OnClose: cleanup}, nil // cleanup wipes stageDir
	default:
		_ = os.RemoveAll(stageDir)
		return control.SessionSpec{}, fmt.Errorf("unknown environment %q", prof.Environment)
	}
}

// chainClose returns a cleanup that runs fns in order, skipping nils — used to compose a
// session's OnClose (e.g. a sandbox temp-profile removal followed by the stage-dir wipe).
func chainClose(fns ...func()) func() {
	return func() {
		for _, f := range fns {
			if f != nil {
				f()
			}
		}
	}
}
```

> `secretEnv` now flows to `container.PrepareSession` / `vm.PrepareSession` (was `nil`). For container/vm the `PrepareSession` cleanup already wipes `stageDir`, so it is the whole `OnClose`; host/sandbox add `wipe`. No import changes (`context`, `os`, `path/filepath`, `fmt`, `policy`, `secrets`, `creds`, `sandbox`, `container`, `vm`, `control`, `toolchain` are all already imported).

- [ ] **Step 4: Run them, verify they pass** (new tests + the SP7c-2 resolver tests still hold)

```bash
go test ./internal/cli/ -run TestResolveSession -v
go build ./...
```
Expected: PASS for all four `TestResolveSession*` (host/sandbox happy path, container/vm tooling-absent error, new secret-delivery, new ssh reject); build green.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/cli/cli.go internal/cli/cli_resolve_test.go
git add internal/cli/cli.go internal/cli/cli_resolve_test.go
git commit -m "feat(cli): stage secrets + credentials into cockpit sessions (host/sandbox env, container/vm secrets.env); defer ssh"
```

---

### Task 3: full verification + live secret round-trip + PR

- [ ] **Step 1: Full suite + gates.**

```bash
make check && make build
go test ./internal/engine/control/... -race
fish -n scripts/*.fish
fish tests/run.fish
fish scripts/slop-sync-help.fish check
fish scripts/slop-pinning.fish
```
Expected: all green.

- [ ] **Step 2: Live secret-delivery round-trip** (docker present — OrbStack). Prove a profile `secret` actually reaches the agent inside the container boundary via `secrets.env`. Build a throwaway gRPC probe (as in SP7c-2 verification) or reuse the manual recipe; the key assertion is that a secret resolved from `env:` lands in the container shell's environment:

```bash
# workspace slop.cue: a container/shell profile with secrets: {COCKPIT_SECRET: "env:CS_SRC"}
# start: env CS_SRC=delivered-ok SHELL=/bin/sh ./slop serve &
# drive OpenSession(container-profile) -> Attach -> send: printf 'GOT_%s\n' "$COCKPIT_SECRET"
#   expect: GOT_delivered-ok   (proves the secret crossed into the container via secrets.env)
# then CloseSession; confirm docker ps shows no leftover agent/proxy container.
```
Record the observed result in the PR. If docker is unavailable on the host, say so explicitly rather than claiming a pass.

- [ ] **Step 3: Push + PR.**

```bash
git push -u origin sp7c-3-cockpit-secrets
gh pr create --base main --title "SP7c-3: secrets + credential staging for cockpit sessions" --body "$(cat <<'EOF'
## Summary
Cockpit sessions (\`OpenSession\`/\`Attach\`) now stage the profile's secrets + credentials like \`slop run\`, instead of inheriting the bare \`slop serve\` env. Shared \`stageProfile\` helper drives both \`runProfile\` and \`resolveSession\`.

- \`stageProfile\` extracted from \`runProfile\` (behaviour unchanged; existing cred tests guard it): resolves \`secrets\`, stages pnpm/aws/gcp/kube into the stage dir, returns secretEnv + pathEnv.
- \`resolveSession\` stages into a per-session \`cockpit-*\` dir and delivers per env — host/sandbox via \`SessionSpec.Env\`, container via \`secrets.env\` + bind-mounted cred files, vm via the scp'd stage.
- **ssh deploy keys deferred in the cockpit** (rejected with a pointer to \`slop run\`): they key off the workspace git origin, which \`slop serve\`'s cwd isn't. vm keeps its kube reject.

## Test
\`make check\` + \`make build\` green; control \`-race\` clean; \`stageProfile\` resolves an \`env:\` secret; \`resolveSession\` delivers a secret into the host \`SessionSpec.Env\` and rejects ssh-cred profiles; the SP7c-2 resolver tests still hold; four fish gates green. Live container round-trip: a profile secret reached the in-container shell via \`secrets.env\` (see verification note).

## Deferred
ssh deploy-key staging in the cockpit (workspace-cwd + per-window key churn); the SwiftUI app.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

---

## Verification (what "done" means)

- `make check` + `make build` green; `go test ./internal/engine/control/... -race` clean; four fish gates green.
- `runProfile`'s behaviour is unchanged (its existing cred/lint/toolchain tests pass untouched).
- `stageProfile` resolves `op://`/`env:` secrets and stages pnpm/aws/gcp/kube; `resolveSession` delivers them to host/sandbox via `Env` and to container/vm via `secretEnv`.
- ssh-cred profiles are rejected in the cockpit with a clear message; vm rejects kube.
- Live: a profile secret reaches the agent inside a container cockpit session.

## Deliberately deferred (not here)

- **ssh deploy-key staging** for cockpit sessions — needs an explicit workspace/repo dir for the origin lookup and a per-window key/revoke story.
- **The SwiftUI app** (SwiftTerm + WindowGroup + chrome) — jojo's Xcode track, against the committed `.proto`.
