# SP7b-2 — `safeslop install plan` Implementation Plan

**Goal:** Add a read-only `safeslop install plan` command that diffs the live install inventory (`install.Status`) against an embedded, **pinned + checksummed** desired-state manifest and emits the ordered install/upgrade actions to reconcile it — the second installer slice after SP7b-1 (design `specs/0012` §5).

**Architecture:** Extend `internal/engine/install` with a pure diff: `Plan(state State, desired []Pin) (Result, error)`. A `Pin` is one tool's fully-specified desired state (exact version, sha256, source url). `ValidateDesired` enforces the design's **fail-closed** contract — no `latest`, every artifact carries a sha256 — and `Plan` refuses to emit a partial plan from an invalid manifest. The embedded `DesiredState()` manifest is the seed (mise + tart, the two single-binary darwin-arm64 artifacts). `internal/cli` adds a `plan` subcommand (+ `--json`) under the existing `cmdInstall()`. No side effects, no downloads, no gRPC — those are `apply` (SP7b-3).

**Tech stack:** Go only. No new deps, no `.proto` change. Mirrors the SP7b-1 (`Status`) + cred-provider TDD style (synthetic-input table tests; real artifact values fetched once in the final task).

**Scope:** `install plan` for the **toolchains + runtimes** in the manifest (mise, nix-class tools, docker, tart — seeded with mise + tart). Diffs only tools present in the manifest; tools probed by `Status` but absent from the manifest (docker, nix until their multi-component installers land) are reported by `status` but are **not yet plan-managed**, disclosed in the render. The **self** (safeslop self-update) and **app** (codesign verification) diffs are explicitly out of scope here — same deferral shape as SP7b-1 deferring signing. Actual downloading/installing + gRPC streaming is `apply` (SP7b-3).

**Base branch:** new feature branch `sp7b-2-install-plan` off `main` (`52d6cd0`). **Never push `main`.**

**File structure:**
- `internal/engine/install/plan.go` (create) — `Pin`/`Action`/`ActionKind`/`Result` types, `ValidateDesired`, `Plan`, `extractVersion`.
- `internal/engine/install/plan_test.go` (create) — synthetic-pin tests for the validator + diff classification.
- `internal/engine/install/desired.go` (create) — `DesiredState() []Pin`, the embedded pinned manifest.
- `internal/engine/install/desired_test.go` (create) — asserts the embedded manifest is fail-closed valid.
- `internal/cli/cli.go` (modify) — add the `plan` subcommand to `cmdInstall()` (`internal/cli/cli.go:420-450`) + `installPlanResult` / `renderInstallPlanJSON` helpers.
- `internal/cli/cli_install_plan_test.go` (create) — the `--json` render shape.

---

### Task 1: `Pin`/`Action`/`Result` types + the fail-closed `ValidateDesired`

**Files:**
- Create: `internal/engine/install/plan.go`
- Test: `internal/engine/install/plan_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/engine/install/plan_test.go`:

```go
package install

import "testing"

// pin builds a fully-pinned (valid) Pin with a stub sha256/url, for validator + diff tests.
func pin(name, kind, ver string) Pin {
	return Pin{
		Name:    name,
		Kind:    kind,
		Version: ver,
		SHA256:  "0000000000000000000000000000000000000000000000000000000000000000",
		URL:     "https://example.test/" + name,
	}
}

func TestValidateDesiredAcceptsFullyPinned(t *testing.T) {
	if err := ValidateDesired([]Pin{pin("mise", "toolchain", "2026.6.0")}); err != nil {
		t.Fatalf("fully pinned manifest should validate: %v", err)
	}
}

func TestValidateDesiredEmptyOK(t *testing.T) {
	if err := ValidateDesired(nil); err != nil {
		t.Fatalf("empty manifest should validate vacuously: %v", err)
	}
}

func TestValidateDesiredRejectsLatest(t *testing.T) {
	if err := ValidateDesired([]Pin{pin("mise", "toolchain", "latest")}); err == nil {
		t.Fatal("a 'latest' version must be rejected (fail-closed)")
	}
}

func TestValidateDesiredRejectsMissingSHA(t *testing.T) {
	p := pin("mise", "toolchain", "2026.6.0")
	p.SHA256 = ""
	if err := ValidateDesired([]Pin{p}); err == nil {
		t.Fatal("a missing sha256 must be rejected (fail-closed)")
	}
}

func TestValidateDesiredRejectsShortSHA(t *testing.T) {
	p := pin("mise", "toolchain", "2026.6.0")
	p.SHA256 = "abc123"
	if err := ValidateDesired([]Pin{p}); err == nil {
		t.Fatal("a non-64-hex sha256 must be rejected")
	}
}

func TestValidateDesiredRejectsBadKind(t *testing.T) {
	if err := ValidateDesired([]Pin{pin("mise", "wat", "2026.6.0")}); err == nil {
		t.Fatal("an invalid kind must be rejected")
	}
}

func TestValidateDesiredRejectsDuplicate(t *testing.T) {
	ps := []Pin{pin("mise", "toolchain", "2026.6.0"), pin("mise", "toolchain", "2026.7.0")}
	if err := ValidateDesired(ps); err == nil {
		t.Fatal("duplicate tool names must be rejected")
	}
}
```

- [ ] **Step 2: Run the test, verify it fails**

```bash
go test ./internal/engine/install/ -run TestValidateDesired -v
```
Expected: FAIL — `undefined: Pin` / `undefined: ValidateDesired`.

- [ ] **Step 3: Write the minimal implementation**

Create `internal/engine/install/plan.go`:

```go
package install

import (
	"fmt"
	"regexp"
)

// Pin is one tool's pinned desired-state entry. Plan diffs the live Status against these; apply
// (SP7b-3) downloads URL, verifies SHA256, installs Version. The manifest is fail-closed: every
// field is mandatory and Version is never "latest" (specs/0012 §5).
type Pin struct {
	Name    string `json:"name"`    // matches Tool.Name from Status (e.g. "mise", "tart")
	Kind    string `json:"kind"`    // "toolchain" | "runtime" — informs apply's provisioner
	Version string `json:"version"` // exact pinned version, never "latest"
	SHA256  string `json:"sha256"`  // sha256 of the darwin-arm64 artifact (provenance)
	URL     string `json:"url"`     // download source for that artifact
}

var sha256Re = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ValidateDesired enforces the fail-closed contract: every pin is fully specified and exact. An
// invalid manifest is an error, never a silent skip (specs/0012 §5: "fails closed").
func ValidateDesired(pins []Pin) error {
	seen := map[string]bool{}
	for _, p := range pins {
		if p.Name == "" {
			return fmt.Errorf("install: pin with empty name")
		}
		if seen[p.Name] {
			return fmt.Errorf("install: duplicate pin %q", p.Name)
		}
		seen[p.Name] = true
		if p.Kind != "toolchain" && p.Kind != "runtime" {
			return fmt.Errorf("install: pin %q has invalid kind %q (want toolchain|runtime)", p.Name, p.Kind)
		}
		if p.Version == "" || p.Version == "latest" {
			return fmt.Errorf("install: pin %q must declare an exact version, got %q", p.Name, p.Version)
		}
		if !sha256Re.MatchString(p.SHA256) {
			return fmt.Errorf("install: pin %q must declare a 64-hex sha256", p.Name)
		}
		if p.URL == "" {
			return fmt.Errorf("install: pin %q must declare a source url", p.Name)
		}
	}
	return nil
}
```

- [ ] **Step 4: Run the test, verify it passes**

```bash
go test ./internal/engine/install/ -run TestValidateDesired -v
```
Expected: PASS (all seven `TestValidateDesired*`).

- [ ] **Step 5: Commit**

```bash
git add internal/engine/install/plan.go internal/engine/install/plan_test.go
git commit -m "feat(install): Pin manifest type + fail-closed ValidateDesired (SP7b-2)"
```

---

### Task 2: the `Plan` diff engine

**Files:**
- Modify: `internal/engine/install/plan.go`
- Test: `internal/engine/install/plan_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/engine/install/plan_test.go`:

```go
func TestPlanClassifiesInstallUpgradeOK(t *testing.T) {
	state := State{
		Toolchains: []Tool{{Name: "mise", Present: true, Version: "mise 2026.6.0 macos-arm64"}},
		Runtimes:   []Tool{{Name: "tart", Present: false}},
	}
	desired := []Pin{
		pin("mise", "toolchain", "2026.6.0"), // present at exact version -> ok
		pin("tart", "runtime", "2.0.0"),       // absent -> install
	}
	res, err := Plan(state, desired)
	if err != nil {
		t.Fatalf("Plan errored: %v", err)
	}
	if len(res.Actions) != 2 {
		t.Fatalf("want 2 actions in manifest order, got %d", len(res.Actions))
	}
	if res.Actions[0].Kind != ActionOK {
		t.Fatalf("mise should be ok, got %s", res.Actions[0].Kind)
	}
	if res.Actions[1].Kind != ActionInstall {
		t.Fatalf("tart should be install, got %s", res.Actions[1].Kind)
	}
	if res.Pending() != 1 {
		t.Fatalf("pending want 1, got %d", res.Pending())
	}
}

func TestPlanDetectsUpgrade(t *testing.T) {
	state := State{Toolchains: []Tool{{Name: "mise", Present: true, Version: "2026.5.0"}}}
	res, err := Plan(state, []Pin{pin("mise", "toolchain", "2026.6.0")})
	if err != nil {
		t.Fatalf("Plan errored: %v", err)
	}
	if res.Actions[0].Kind != ActionUpgrade {
		t.Fatalf("want upgrade, got %s", res.Actions[0].Kind)
	}
	if res.Actions[0].Current != "2026.5.0" {
		t.Fatalf("current want 2026.5.0, got %q", res.Actions[0].Current)
	}
	if res.Actions[0].Desired != "2026.6.0" {
		t.Fatalf("desired want 2026.6.0, got %q", res.Actions[0].Desired)
	}
}

func TestPlanFailsClosedOnBadManifest(t *testing.T) {
	bad := []Pin{{Name: "mise", Kind: "toolchain", Version: "latest", SHA256: "x", URL: "u"}}
	if _, err := Plan(State{}, bad); err == nil {
		t.Fatal("Plan must fail closed on an invalid manifest")
	}
}

func TestExtractVersion(t *testing.T) {
	cases := map[string]string{
		"mise 2026.6.0 macos-arm64":     "2026.6.0",
		"tart version: 2.0.0 (build 7)": "2.0.0",
		"no version here":               "",
		"v1.2":                          "1.2",
	}
	for in, want := range cases {
		if got := extractVersion(in); got != want {
			t.Errorf("extractVersion(%q) = %q want %q", in, got, want)
		}
	}
}
```

- [ ] **Step 2: Run the test, verify it fails**

```bash
go test ./internal/engine/install/ -run 'TestPlan|TestExtractVersion' -v
```
Expected: FAIL — `undefined: Plan` / `undefined: ActionOK` / `undefined: extractVersion`.

- [ ] **Step 3: Write the minimal implementation**

Append to `internal/engine/install/plan.go`:

```go
// ActionKind is what apply must do to one tool to reach the pinned state.
type ActionKind string

const (
	ActionInstall ActionKind = "install" // tool absent -> fetch + install
	ActionUpgrade ActionKind = "upgrade" // present but not the pinned version -> replace
	ActionOK      ActionKind = "ok"      // present at the pinned version -> no-op
)

// Action is the planned outcome for one pinned tool.
type Action struct {
	Name    string     `json:"name"`
	Kind    ActionKind `json:"kind"`
	Current string     `json:"current,omitempty"` // probed version ("" if absent)
	Desired string     `json:"desired"`           // pinned version
	SHA256  string     `json:"sha256"`            // carried through for apply
	URL     string     `json:"url"`
}

// Result is the ordered plan: one Action per pinned tool, in manifest order.
type Result struct {
	Actions []Action `json:"actions"`
}

// Pending counts the non-ok actions (install + upgrade) — the "N changes" headline.
func (r Result) Pending() int {
	n := 0
	for _, a := range r.Actions {
		if a.Kind != ActionOK {
			n++
		}
	}
	return n
}

var versionRe = regexp.MustCompile(`\d+(?:\.\d+)+`)

// Plan diffs the live install state against the pinned desired manifest and returns the ordered
// actions to reconcile it. It fails closed: an invalid manifest is an error, never a partial plan.
func Plan(state State, desired []Pin) (Result, error) {
	if err := ValidateDesired(desired); err != nil {
		return Result{}, err
	}
	index := map[string]Tool{}
	for _, t := range state.Toolchains {
		index[t.Name] = t
	}
	for _, t := range state.Runtimes {
		index[t.Name] = t
	}
	var res Result
	for _, p := range desired {
		a := Action{Name: p.Name, Desired: p.Version, SHA256: p.SHA256, URL: p.URL}
		tool, found := index[p.Name]
		cur := extractVersion(tool.Version)
		switch {
		case !found || !tool.Present:
			a.Kind = ActionInstall
		case cur == p.Version:
			a.Kind = ActionOK
			a.Current = cur
		default:
			a.Kind = ActionUpgrade
			a.Current = cur
		}
		res.Actions = append(res.Actions, a)
	}
	return res, nil
}

// extractVersion pulls the first dotted-numeric token out of a `--version` line so a pinned
// "2.0.0" matches probe output like "tart version: 2.0.0 (build 7)". Returns "" if none.
func extractVersion(s string) string {
	return versionRe.FindString(s)
}
```

- [ ] **Step 4: Run the test, verify the whole package passes**

```bash
go test ./internal/engine/install/ -v
```
Expected: PASS (Task 1 + Task 2 tests, plus the pre-existing `TestStatusProbesToolsAndSelf`).

- [ ] **Step 5: Commit**

```bash
git add internal/engine/install/plan.go internal/engine/install/plan_test.go
git commit -m "feat(install): Plan() diff -> ordered install/upgrade/ok actions (SP7b-2)"
```

---

### Task 3: the embedded `DesiredState()` manifest (empty seed)

This task lands a valid-but-empty manifest so the CLI (Task 4) ships a working `plan` even before real artifact values are fetched (Task 5). An empty manifest is fail-closed valid (vacuously) and renders "0 change(s) pending".

**Files:**
- Create: `internal/engine/install/desired.go`
- Test: `internal/engine/install/desired_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/engine/install/desired_test.go`:

```go
package install

import "testing"

// The embedded manifest must always satisfy the fail-closed contract, empty or populated. This is
// the Go-manifest equivalent of the slop-pinning gate (which only scans *.cue): a bad pin breaks
// the build, never ships a "latest" or an unchecksummed artifact.
func TestDesiredStateIsFailClosed(t *testing.T) {
	if err := ValidateDesired(DesiredState()); err != nil {
		t.Fatalf("the embedded desired-state manifest must be fail-closed valid: %v", err)
	}
}
```

- [ ] **Step 2: Run the test, verify it fails**

```bash
go test ./internal/engine/install/ -run TestDesiredStateIsFailClosed -v
```
Expected: FAIL — `undefined: DesiredState`.

- [ ] **Step 3: Write the minimal implementation**

Create `internal/engine/install/desired.go`:

```go
package install

// DesiredState is the embedded, pinned + checksummed install manifest for darwin-arm64 — the only
// platform SafeSlop targets (specs/0012). It is the desired-state half of `install plan`; apply
// (SP7b-3) consumes URL + SHA256. Bump entries as data edits; TestDesiredStateIsFailClosed +
// ValidateDesired guarantee every entry stays fully pinned. Tools probed by Status but absent here
// (docker, nix) are not yet installer-managed — their multi-component installers are a later slice.
//
// Seeded empty; Task 5 of specs/0020 populates the real mise + tart darwin-arm64 artifacts.
func DesiredState() []Pin {
	return nil
}
```

- [ ] **Step 4: Run the test, verify it passes**

```bash
go test ./internal/engine/install/ -run TestDesiredStateIsFailClosed -v
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/engine/install/desired.go internal/engine/install/desired_test.go
git commit -m "feat(install): embedded DesiredState() manifest (empty seed, fail-closed) (SP7b-2)"
```

---

### Task 4: the `install plan` CLI subcommand

**Files:**
- Modify: `internal/cli/cli.go` (the `cmdInstall()` function at `internal/cli/cli.go:420-450`)
- Test: `internal/cli/cli_install_plan_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/cli/cli_install_plan_test.go`:

```go
package cli

import (
	"encoding/json"
	"testing"
)

func TestInstallPlanJSONShape(t *testing.T) {
	out, err := renderInstallPlanJSON("v9.9.9")
	if err != nil {
		t.Fatalf("plan --json errored (manifest must be fail-closed valid): %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("plan --json is not valid JSON: %v\n%s", err, out)
	}
	if _, ok := m["actions"]; !ok {
		t.Fatalf("plan JSON missing \"actions\": %v", m)
	}
}
```

- [ ] **Step 2: Run the test, verify it fails**

```bash
go test ./internal/cli/ -run TestInstallPlanJSONShape -v
```
Expected: FAIL — `undefined: renderInstallPlanJSON`.

- [ ] **Step 3: Write the minimal implementation**

In `internal/cli/cli.go`, register the `plan` subcommand inside `cmdInstall()`. Add it immediately after the `status` `c.AddCommand({...})` block and before `return c` (around `internal/cli/cli.go:448`):

```go
	c.AddCommand(&cobra.Command{
		Use:   "plan",
		Short: "Show the pinned actions needed to install/upgrade toolchains + runtimes",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if jsonOut {
				out, err := renderInstallPlanJSON(Version)
				if err != nil {
					return err
				}
				fmt.Println(out)
				return nil
			}
			res, err := installPlanResult(Version)
			if err != nil {
				return err // fail closed: a bad manifest is an error, not an empty plan
			}
			fmt.Printf("%d change(s) pending\n", res.Pending())
			for _, a := range res.Actions {
				cur := a.Current
				if cur == "" {
					cur = "-"
				}
				fmt.Printf("  %-10s %-8s %s -> %s\n", a.Name, a.Kind, cur, a.Desired)
			}
			if len(res.Actions) == 0 {
				fmt.Println("  (desired-state manifest is empty)")
			}
			return nil
		},
	})
```

Then add the two helpers next to `renderInstallStatusJSON` (after `internal/cli/cli.go:456`):

```go
func installPlanResult(version string) (install.Result, error) {
	st := install.Status(context.Background(), version)
	return install.Plan(st, install.DesiredState())
}

func renderInstallPlanJSON(version string) (string, error) {
	res, err := installPlanResult(version)
	if err != nil {
		return "", err
	}
	b, _ := json.MarshalIndent(res, "", "  ")
	return string(b), nil
}
```

- [ ] **Step 4: Run the tests, verify they pass**

```bash
go test ./internal/cli/ -run TestInstallPlan -v
go build ./cmd/safeslop && ./safeslop install plan && ./safeslop install plan --json
```
Expected: test PASS; the human run prints `0 change(s) pending` + `(desired-state manifest is empty)`; the `--json` run prints `{ "actions": null }`. (Both honest while the seed is empty — Task 5 fills it.)

- [ ] **Step 5: Commit**

```bash
git add internal/cli/cli.go internal/cli/cli_install_plan_test.go
git commit -m "feat(cli): safeslop install plan (human + --json desired-state diff) (SP7b-2)"
```

---

### Task 5: populate the real pinned manifest (mise + tart, darwin-arm64)

Fetch real artifact URLs + sha256 for one pinned version of each tool and fill `DesiredState()`. This is the only task that touches the network. **Timebox: 15 min.** If downloads are blocked (corporate WARP / Zscaler TLS interception), retry the `curl` with `--cacert /etc/ssl/cert.pem`; if still blocked, **leave `DesiredState()` returning `nil` and skip this task** — Tasks 1–4 already ship a working (empty) `plan`, and manifest population becomes a clean follow-up. Do not invent sha256 values.

**Files:**
- Modify: `internal/engine/install/desired.go`

- [ ] **Step 1: Pick a stable version of each and fetch its darwin-arm64 artifact + sha256**

mise (single binary tarball on GitHub releases):
```bash
# Pick the current stable tag from https://github.com/jdx/mise/releases
ver=2026.6.0
url="https://github.com/jdx/mise/releases/download/v${ver}/mise-v${ver}-macos-arm64.tar.gz"
curl -fsSL "$url" -o /tmp/mise.tgz && shasum -a 256 /tmp/mise.tgz
# -> record the 64-hex digest and confirm the URL resolved (HTTP 200, non-empty file)
```

tart (single tarball on GitHub releases):
```bash
# Pick the current stable tag from https://github.com/cirruslabs/tart/releases
ver=2.0.0
url="https://github.com/cirruslabs/tart/releases/download/${ver}/tart-arm64.tar.gz"
curl -fsSL "$url" -o /tmp/tart.tgz && shasum -a 256 /tmp/tart.tgz
# -> record the 64-hex digest and confirm the URL resolved
```

If a release asset is named differently than above, open the release page and use the actual darwin/arm64 asset name — the command shape (download → `shasum -a 256`) is what matters.

- [ ] **Step 2: Fill `DesiredState()` with the real values**

Replace the `return nil` in `internal/engine/install/desired.go` with the fetched values (substitute the real `<...>` you recorded):

```go
func DesiredState() []Pin {
	return []Pin{
		{
			Name:    "mise",
			Kind:    "toolchain",
			Version: "<MISE_VER>",            // e.g. 2026.6.0 (no leading v)
			SHA256:  "<MISE_SHA256_64HEX>",
			URL:     "<MISE_URL>",
		},
		{
			Name:    "tart",
			Kind:    "runtime",
			Version: "<TART_VER>",
			SHA256:  "<TART_SHA256_64HEX>",
			URL:     "<TART_URL>",
		},
	}
}
```

- [ ] **Step 3: Verify the manifest validates and the plan reflects the live machine**

```bash
go test ./internal/engine/install/ -run TestDesiredStateIsFailClosed -v
go build ./cmd/safeslop && ./safeslop install plan
```
Expected: `TestDesiredStateIsFailClosed` PASS (now exercising real values); `install plan` lists `mise` and `tart` as `ok` / `upgrade` / `install` matching what's actually on this machine (cross-check against `./safeslop install status`).

- [ ] **Step 4: Run the full Go gate**

```bash
make check
```
Expected: all packages `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/engine/install/desired.go
git commit -m "feat(install): pin mise + tart darwin-arm64 in DesiredState() manifest (SP7b-2)"
```

---

## Gates & done-checklist

Before opening the PR (per `AGENTS.md` + `CLAUDE.md`):

```bash
make check          # go vet + gofmt + go test ./...  — must be all ok
make build          # static CGO_ENABLED=0 binary -> ./safeslop
```

The four fish gates (`fish -n scripts/*.fish`, `fish tests/run.fish`, `slop-sync-help check`, `slop-pinning`) are unaffected — this slice touches no fish/CUE/README surface. The Go manifest's pinning is guarded by `TestDesiredStateIsFailClosed`, not `slop-pinning` (which only scans `*.cue`).

Then branch + PR (never push `main`):

```bash
git push -u origin sp7b-2-install-plan
gh pr create --fill
```

## Deferred (SP7b-3 and later)

- `install apply` — execute the plan: download each `Action.URL`, verify `Action.SHA256` (fail closed), install `Action.Desired`, **stream progress over gRPC** (the app's wizard consumes the stream).
- Manifest entries for docker + nix (multi-component installers, not single signed binaries).
- `self` self-update diff and `app` codesign verification (both reported by `status` today, neither plan-managed yet).
- Optional behavioral VM-eval of a candidate install step (design `specs/0012` §5/§6).
