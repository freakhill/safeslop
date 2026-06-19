# SP7b-1 — `safeslop install status` Implementation Plan

**Goal:** Add a read-only `safeslop install status` command that inventories whether safeslop itself (the binary, the app, its toolchains + runtimes) is installed and current — the foundation the installer's `plan`/`apply` build on (design `specs/0012` §5).

**Architecture:** A new `internal/engine/install` package exposes `Status(ctx, version) State` — a pure-probe function that reports the safeslop **binary** (path / version / on-PATH), the **SafeSlop.app**, the **toolchains** (mise, nix), and the **container/vm runtimes** (docker, tart) with their versions. `internal/cli` adds `cmdInstall()` with a `status` subcommand (+ `--json`) that renders it. No side effects, no gRPC — this is the inventory half of SP7b; `plan` (diff vs pinned desired-state) and `apply` (execute + stream over gRPC) are SP7b-2/3.

**Tech stack:** Go, the existing `os/exec` probe pattern (mirrors `creds`' fake-binary tests + `doctorReport`). No new deps, no `.proto` change.

**Scope:** `install status` only — a richer, install-focused inventory than `doctor` (which only answers "is tool X on PATH right now"). It adds the **self** question (where/what-version is *safeslop*, is it on PATH) + the **app** + tool **versions**. Pinning/outdated comparison is `plan` (SP7b-2); signing verification + actual installation are later slices.

**Base branch:** new feature branch `sp7b-1-install-status` off `main` (`5c49648`). **Never push `main`.**

**File structure:**
- `internal/engine/install/install.go` (create) — `State`/`Tool`/`Self`/`App` types + `Status(ctx, version) State`.
- `internal/engine/install/install_test.go` (create) — fake-binary probe tests.
- `internal/cli/cli.go` (modify) — `cmdInstall()` (with `status`), registered in `newRoot()`.
- `internal/cli/cli_install_test.go` (create) — the JSON render shape.

---

### Task 1: the `install` package — `Status()` probe

**Files:**
- Create: `internal/engine/install/install.go`
- Test: `internal/engine/install/install_test.go`

- [ ] **Step 1: Write the failing test** — create `internal/engine/install/install_test.go`:

```go
package install

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// fakeTool writes a stub executable that prints `out` (so `<tool> --version` is probeable).
func fakeTool(t *testing.T, dir, name, out string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\necho '"+out+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestStatusProbesToolsAndSelf(t *testing.T) {
	bin := t.TempDir()
	fakeTool(t, bin, "mise", "2026.6.0")
	fakeTool(t, bin, "docker", "Docker version 29.4.0")
	fakeTool(t, bin, "safeslop", "safeslop version test")
	t.Setenv("PATH", bin) // only our stubs are visible

	st := Status(context.Background(), "v1.2.3")

	if st.Self.Version != "v1.2.3" {
		t.Fatalf("self version = %q, want v1.2.3", st.Self.Version)
	}
	if !st.Self.OnPath {
		t.Fatal("self should be detected on PATH (safeslop stub present)")
	}
	mise := find(st.Toolchains, "mise")
	if mise == nil || !mise.Present || mise.Version == "" {
		t.Fatalf("mise should be present with a version: %+v", mise)
	}
	docker := find(st.Runtimes, "docker")
	if docker == nil || !docker.Present {
		t.Fatalf("docker runtime should be present: %+v", docker)
	}
	if nix := find(st.Toolchains, "nix"); nix == nil || nix.Present {
		t.Fatalf("nix should be absent (no stub): %+v", nix)
	}
}

func find(tools []Tool, name string) *Tool {
	for i := range tools {
		if tools[i].Name == name {
			return &tools[i]
		}
	}
	return nil
}
```

- [ ] **Step 2: Run it, verify it fails**

```bash
go test ./internal/engine/install/ -run TestStatusProbes -v
```
Expected: FAIL — package/`Status` undefined.

- [ ] **Step 3: Write the package** — create `internal/engine/install/install.go`:

```go
// Package install inventories whether safeslop itself (binary, app, toolchains, runtimes) is
// installed and current — the read-only half of the installer (specs/0012 §5). No side effects.
package install

import (
	"context"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
)

// Tool is one external dependency's install state.
type Tool struct {
	Name    string `json:"name"`
	Present bool   `json:"present"`
	Path    string `json:"path,omitempty"`
	Version string `json:"version,omitempty"`
}

// Self is the running safeslop binary's own install state.
type Self struct {
	Version string `json:"version"`
	Path    string `json:"path,omitempty"`   // os.Executable()
	OnPath  bool   `json:"on_path"`          // a `safeslop` resolves on PATH
}

// App is the SafeSlop.app presence (signing verification is a later slice).
type App struct {
	Present bool   `json:"present"`
	Path    string `json:"path,omitempty"`
}

// State is the full install inventory.
type State struct {
	Self       Self   `json:"self"`
	App        App    `json:"app"`
	Toolchains []Tool `json:"toolchains"`
	Runtimes   []Tool `json:"runtimes"`
}

// Status probes the environment. version is the running binary's version (from cli.Version).
func Status(ctx context.Context, version string) State {
	exe, _ := os.Executable()
	_, lookErr := osexec.LookPath("safeslop")
	st := State{
		Self: Self{Version: version, Path: exe, OnPath: lookErr == nil},
		App:  detectApp(),
		Toolchains: []Tool{
			probe(ctx, "mise", "--version"),
			probe(ctx, "nix", "--version"),
		},
		Runtimes: []Tool{
			probe(ctx, "docker", "--version"),
			probe(ctx, "tart", "--version"),
		},
	}
	return st
}

// probe reports a tool's presence + first-line version output (best-effort).
func probe(ctx context.Context, name string, versionArgs ...string) Tool {
	path, err := osexec.LookPath(name)
	if err != nil {
		return Tool{Name: name, Present: false}
	}
	t := Tool{Name: name, Present: true, Path: path}
	if out, verr := osexec.CommandContext(ctx, name, versionArgs...).Output(); verr == nil {
		t.Version = strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	}
	return t
}

// detectApp looks for SafeSlop.app in the standard install locations.
func detectApp() App {
	candidates := []string{"/Applications/SafeSlop.app"}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, "Applications", "SafeSlop.app"))
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return App{Present: true, Path: p}
		}
	}
	return App{Present: false}
}
```

- [ ] **Step 4: Run it, verify it passes**

```bash
go test ./internal/engine/install/ -run TestStatusProbes -v
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/engine/install/install.go internal/engine/install/install_test.go
git add internal/engine/install/
git commit -m "feat(install): Status() install-state inventory (binary self, app, toolchains, runtimes)"
```

---

### Task 2: wire `safeslop install status` into the CLI

**Files:**
- Modify: `internal/cli/cli.go` (add `cmdInstall()`; register in `newRoot()`; import the package)
- Test: `internal/cli/cli_install_test.go` (create)

- [ ] **Step 1: Write the failing test** — create `internal/cli/cli_install_test.go`:

```go
package cli

import (
	"encoding/json"
	"testing"
)

func TestInstallStatusJSONShape(t *testing.T) {
	out := renderInstallStatusJSON("v9.9.9")
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("status --json is not valid JSON: %v\n%s", err, out)
	}
	self, ok := m["self"].(map[string]any)
	if !ok {
		t.Fatalf("missing self object: %v", m)
	}
	if self["version"] != "v9.9.9" {
		t.Fatalf("self.version = %v, want v9.9.9", self["version"])
	}
	for _, k := range []string{"app", "toolchains", "runtimes"} {
		if _, ok := m[k]; !ok {
			t.Fatalf("status JSON missing %q: %v", k, m)
		}
	}
}
```

- [ ] **Step 2: Run it, verify it fails**

```bash
go test ./internal/cli/ -run TestInstallStatusJSONShape -v
```
Expected: FAIL — `renderInstallStatusJSON` undefined.

- [ ] **Step 3: Add the command.** In `internal/cli/cli.go`, add the import `"github.com/freakhill/safeslop/internal/engine/install"`, register the command in `newRoot()` (the `root.AddCommand(...)` line), and add `cmdInstall` + a small render helper:

```go
func cmdInstall() *cobra.Command {
	c := &cobra.Command{
		Use:   "install",
		Short: "Inventory and (later) provision the safeslop toolchain",
	}
	c.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Report whether safeslop, its app, toolchains, and runtimes are installed",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if jsonOut {
				fmt.Println(renderInstallStatusJSON(Version))
				return nil
			}
			st := install.Status(context.Background(), Version)
			fmt.Printf("safeslop %s  (on PATH: %v)\n", st.Self.Version, st.Self.OnPath)
			if st.Self.Path != "" {
				fmt.Printf("  binary: %s\n", st.Self.Path)
			}
			app := "not installed"
			if st.App.Present {
				app = st.App.Path
			}
			fmt.Printf("  app:    %s\n", app)
			printTools("toolchains", st.Toolchains)
			printTools("runtimes", st.Runtimes)
			return nil
		},
	})
	return c
}

func renderInstallStatusJSON(version string) string {
	st := install.Status(context.Background(), version)
	b, _ := json.MarshalIndent(st, "", "  ")
	return string(b)
}

func printTools(label string, tools []install.Tool) {
	fmt.Printf("  %s:\n", label)
	for _, t := range tools {
		mark := "no"
		if t.Present {
			mark = "yes"
		}
		v := t.Version
		if v == "" {
			v = "-"
		}
		fmt.Printf("    %-10s %-4s %s\n", t.Name, mark, v)
	}
}
```

and register it:

```go
	root.AddCommand(cmdValidate(), cmdList(), cmdDoctor(), cmdRun(), cmdDown(), cmdServe(), cmdLaunch(), cmdInstall())
```

> `context`, `encoding/json`, `fmt` are already imported by `cli.go`.

- [ ] **Step 4: Run it, verify it passes + smoke the command**

```bash
go test ./internal/cli/ -run TestInstallStatusJSONShape -v
go build ./... && ./safeslop install status && ./safeslop install status --json | head -6
```
Expected: PASS; human + JSON output render (the real environment's tools).

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/cli/cli.go internal/cli/cli_install_test.go
git add internal/cli/cli.go internal/cli/cli_install_test.go
git commit -m "feat(cli): safeslop install status (human + --json install inventory)"
```

---

### Task 3: full verification + PR

- [ ] **Step 1: Full suite + gates.**

```bash
make check && make build
fish -n scripts/*.fish
fish tests/run.fish
fish scripts/slop-sync-help.fish check
fish scripts/slop-pinning.fish
./safeslop install status
```
Expected: all green; `install status` reports a sane inventory.

- [ ] **Step 2: Push + PR.**

```bash
git push -u origin sp7b-1-install-status
gh pr create --base main --title "SP7b-1: safeslop install status (install-state inventory)" --body "$(cat <<'EOF'
## Summary
First slice of the installer (design specs/0012 §5): a read-only `safeslop install status` that inventories whether safeslop itself is installed and current — the safeslop **binary** (path / version / on-PATH), the **SafeSlop.app**, the **toolchains** (mise/nix) and **container/vm runtimes** (docker/tart) with versions. Human table + `--json`.

- `internal/engine/install`: `Status(ctx, version) State` — pure probes (no side effects), mirrors the `creds` fake-binary test pattern.
- cli: `safeslop install status` (+ `--json`). Distinct from `doctor` — it adds the *self* (is safeslop itself installed / on PATH) + app + tool versions.

## Deferred (SP7b-2/3)
`install plan` (diff vs pinned + checksummed desired state, fail-closed), `install apply` (execute + stream progress over gRPC), app codesign verification, optional VM-eval (specs/0012 §5/§6).

## Test
`make check` + `make build` green; fake-binary probe tests for `Status()`; JSON-shape test for the command; four fish gates green; `safeslop install status` smoke output.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

---

## Verification (what "done" means)

- `make check` + `make build` green; four fish gates green.
- `Status()` reports the self binary (version + on-PATH), app presence, and tool presence+version; absent tools report `present: false`.
- `safeslop install status` renders a human table; `--json` is valid JSON with `self`/`app`/`toolchains`/`runtimes`.

## Deliberately deferred (not here)

- **`install plan`** — the pinned + checksummed desired-state diff (no `latest`; sha256 per artifact; fail-closed), SP7b-2.
- **`install apply`** — execute the plan + stream progress over gRPC, SP7b-3.
- **App codesign / notarization verification** and **VM-eval** behavioral diffing (specs/0012 §5/§6).
