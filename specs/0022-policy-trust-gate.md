# 0022 — policy trust gate (`safeslop trust`) Implementation Plan

**Goal:** Stop a sandboxed agent from escalating by rewriting its own `safeslop.cue`, and stop a
freshly-cloned repo from silently being honored, by gating `safeslop run` on a **host-recorded,
fail-closed approval of the policy file's exact bytes** — realizing the ayo HIGH actionable H2
(specs/0012 §10.5).

**Architecture:** A new host-side trust store at `~/.config/safeslop/trust.json` (outside any
agent-writable workspace, mirroring `internal/engine/userconfig`) maps each policy's absolute path
to the sha256 of its approved bytes. `safeslop run` reads the policy bytes, hashes them, and
**blocks the launch** unless the hash matches a recorded approval — distinguishing *untrusted* (no
record, e.g. a cloned repo) from *changed* (recorded but bytes differ, e.g. an agent edited it). A
new `safeslop trust [path]` command records approval; a `--trust` flag on `run` approves-and-runs.
`validate`, `list`, and `run --dry-run` stay **ungated** (you review an untrusted policy before
trusting it). The policy is read once before launch (cli.go:190), so this is a load-time gate.

**Tech stack:** Go stdlib only (`crypto/sha256`, `encoding/json`). No new deps. TDD with a temp
`HOME` so tests never touch the real `~/.config/safeslop`.

**Decisions (locked with jojo):** fail-closed by default (untrusted OR changed blocks `run`);
explicit `safeslop trust` command + `--trust` convenience flag; `validate`/`list`/`--dry-run`
ungated.

**Scope:** the CLI `run` chokepoint. The gRPC `Launch`/`OpenSession` cockpit path (cli.go:319) is
the *other* launch chokepoint the GUI uses — gating it with the same `trust.Store.Check` is a
fast-follow noted in Deferred, not built here (keeps this slice tight; the CLI is the shipped
power-user surface).

**Base branch:** `sp-policy-trust-gate` off `main`. **Never push `main`.**

**File structure:**
- `internal/engine/trust/trust.go` (create) — `Store`, `Status`, `DefaultPath`, `Load`, `Hash`,
  `Check`, `Approve`.
- `internal/engine/trust/trust_test.go` (create) — untrusted → approve → trusted → changed.
- `internal/cli/cli.go` (modify) — `enforceTrust` helper, `cmdTrust`, register it, `run --trust`
  gate.
- `internal/cli/cli_trust_test.go` (create) — the gate's untrusted/trusted/changed/approve flow.
- `specs/0012-sp7-gui-design.md` (modify) — mark §10.5 H2 realized.

---

### Task 1: the `trust` package

**Files:**
- Create: `internal/engine/trust/trust.go`
- Test: `internal/engine/trust/trust_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/engine/trust/trust_test.go`:

```go
package trust

import (
	"path/filepath"
	"testing"
)

func TestCheckUntrustedThenApproveThenChanged(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "trust.json")
	pol := "/repo/safeslop.cue"
	v1 := []byte("profiles: { dev: { agent: \"claude\" } }")

	s, err := Load(storePath) // missing file -> empty store
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Check(pol, v1); got != Untrusted {
		t.Fatalf("fresh policy should be Untrusted, got %v", got)
	}
	if err := s.Approve(pol, v1); err != nil {
		t.Fatal(err)
	}

	// reload from disk: approval must persist
	s2, err := Load(storePath)
	if err != nil {
		t.Fatal(err)
	}
	if got := s2.Check(pol, v1); got != Trusted {
		t.Fatalf("approved policy should be Trusted, got %v", got)
	}
	// the agent edits the policy -> bytes differ -> Changed (not silently honored)
	if got := s2.Check(pol, []byte("profiles: { dev: { network: \"allow\" } }")); got != Changed {
		t.Fatalf("edited policy should be Changed, got %v", got)
	}
}

func TestHashStable(t *testing.T) {
	if Hash([]byte("x")) != Hash([]byte("x")) || Hash([]byte("x")) == Hash([]byte("y")) {
		t.Fatal("Hash must be deterministic and content-sensitive")
	}
}
```

- [ ] **Step 2: Run it, verify it fails**

```bash
go test ./internal/engine/trust/ -v
```
Expected: FAIL — `undefined: Load` / `Untrusted` / `Hash`.

- [ ] **Step 3: Write the implementation**

Create `internal/engine/trust/trust.go`:

```go
// Package trust is a host-side approval store for per-repo safeslop.cue policies. The policy file
// lives inside the agent-writable workspace, so a sandboxed agent could rewrite its own policy and
// a cloned repo ships its own — therefore `safeslop run` is gated on an explicit, host-recorded
// approval of the policy's exact bytes (specs/0022; ayo specs/0012 §10.5 H2). The store lives in
// ~/.config/safeslop/ (outside any workspace, agent-unreachable), mirroring internal/engine/userconfig.
package trust

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
)

// Status is a policy's trust state relative to the store.
type Status int

const (
	Trusted   Status = iota // recorded, and the bytes still hash to the approved value
	Untrusted               // no record for this path (never approved)
	Changed                 // recorded, but the bytes hash differs (edited since approval)
)

func (s Status) String() string {
	switch s {
	case Trusted:
		return "trusted"
	case Changed:
		return "changed"
	default:
		return "untrusted"
	}
}

const storeVersion = 1

type storeFile struct {
	Version int               `json:"version"`
	Entries map[string]string `json:"entries"` // absolute policy path -> approved sha256 hex
}

// Store is an in-memory view of the trust file plus its on-disk path.
type Store struct {
	path    string
	entries map[string]string
}

// DefaultPath is ~/.config/safeslop/trust.json (host-side, agent-unreachable).
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "safeslop", "trust.json"), nil
}

// Load reads the store at path; a missing file is an empty store (not an error).
func Load(path string) (*Store, error) {
	s := &Store{path: path, entries: map[string]string{}}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	var f storeFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, err
	}
	if f.Entries != nil {
		s.entries = f.Entries
	}
	return s, nil
}

// Hash is the sha256 hex of the policy bytes — the approval token.
func Hash(policyBytes []byte) string {
	sum := sha256.Sum256(policyBytes)
	return hex.EncodeToString(sum[:])
}

// Check reports the trust status of policyBytes for the policy at absPath.
func (s *Store) Check(absPath string, policyBytes []byte) Status {
	want, ok := s.entries[absPath]
	if !ok {
		return Untrusted
	}
	if want != Hash(policyBytes) {
		return Changed
	}
	return Trusted
}

// Approve records policyBytes' hash for absPath and persists the store (0700 dir, 0600 file).
func (s *Store) Approve(absPath string, policyBytes []byte) error {
	s.entries[absPath] = Hash(policyBytes)
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(storeFile{Version: storeVersion, Entries: s.entries}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, b, 0o600)
}
```

- [ ] **Step 4: Run, verify pass**

```bash
go test ./internal/engine/trust/ -v
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/engine/trust/
git commit -m "feat(trust): host-side policy approval store (sha256 of safeslop.cue bytes) (specs/0022)"
```

---

### Task 2: `enforceTrust` helper, `safeslop trust` command, and the `run` gate

**Files:**
- Modify: `internal/cli/cli.go`
- Test: `internal/cli/cli_trust_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/cli/cli_trust_test.go`:

```go
package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnforceTrustGate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home) // trust store -> {home}/.config/safeslop/trust.json
	pol := filepath.Join(t.TempDir(), "safeslop.cue")
	if err := os.WriteFile(pol, []byte("profiles: { dev: { agent: \"claude\" } }"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 1. fresh policy is untrusted -> blocked
	if err := enforceTrust(pol, false); err == nil {
		t.Fatal("untrusted policy must block run (fail-closed)")
	}
	// 2. --trust approves and proceeds
	if err := enforceTrust(pol, true); err != nil {
		t.Fatalf("--trust must approve: %v", err)
	}
	// 3. now trusted -> proceeds
	if err := enforceTrust(pol, false); err != nil {
		t.Fatalf("approved policy must pass: %v", err)
	}
	// 4. policy changes -> blocked again (agent-rewrite case)
	if err := os.WriteFile(pol, []byte("profiles: { dev: { network: \"allow\" } }"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := enforceTrust(pol, false); err == nil {
		t.Fatal("a changed policy must block run until re-trusted")
	}
}
```

- [ ] **Step 2: Run it, verify it fails**

```bash
go test ./internal/cli/ -run TestEnforceTrustGate -v
```
Expected: FAIL — `undefined: enforceTrust`.

- [ ] **Step 3: Write the implementation**

In `internal/cli/cli.go`, add the import `"github.com/freakhill/safeslop/internal/engine/trust"`
to the engine-import group, and `"path/filepath"` is already present.

Register the command in `newRoot()` (cli.go:60) — add `cmdTrust()`:

```go
	root.AddCommand(cmdValidate(), cmdList(), cmdDoctor(), cmdRun(), cmdTrust(), cmdDown(), cmdServe(), cmdLaunch(), cmdInstall())
```

Add the helper + command (place near `cmdRun`, e.g. after `runProfile`):

```go
// enforceTrust gates `run` on a host-recorded approval of the policy's exact bytes. With allowTrust
// it records approval and proceeds; otherwise an untrusted or changed policy is a fail-closed error.
// The store is host-side (~/.config/safeslop/trust.json), outside the agent-writable workspace.
func enforceTrust(policyPath string, allowTrust bool) error {
	abs, err := filepath.Abs(policyPath)
	if err != nil {
		return err
	}
	bytes, err := os.ReadFile(abs)
	if err != nil {
		return err
	}
	storePath, err := trust.DefaultPath()
	if err != nil {
		return err
	}
	store, err := trust.Load(storePath)
	if err != nil {
		return err
	}
	if allowTrust {
		return store.Approve(abs, bytes)
	}
	switch store.Check(abs, bytes) {
	case trust.Trusted:
		return nil
	case trust.Changed:
		return fmt.Errorf("safeslop.cue at %s changed since you trusted it (an agent or edit may have modified it).\n  review it, then run:  safeslop trust %s", abs, abs)
	default: // Untrusted
		return fmt.Errorf("safeslop.cue at %s is not trusted (a policy can grant network and secret access).\n  review it, then run:  safeslop trust %s", abs, abs)
	}
}

func cmdTrust() *cobra.Command {
	return &cobra.Command{
		Use:   "trust [safeslop.cue]",
		Short: "Record approval of a repo's safeslop.cue so `safeslop run` will honor it",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			path, err := findConfig(arg0(args))
			if err != nil {
				return err
			}
			if err := enforceTrust(path, true); err != nil {
				return err
			}
			abs, _ := filepath.Abs(path)
			if jsonOut {
				emitJSON(map[string]any{"ok": true, "trusted": abs})
			} else {
				fmt.Printf("trusted: %s\n", abs)
			}
			return nil
		},
	}
}
```

Wire the gate into `cmdRun`: add the flag and the call. Add the flag var next to `dryRun`
(cli.go:180):

```go
	var trustFlag bool
```

Register it alongside the dry-run flag (cli.go:258):

```go
	c.Flags().BoolVar(&trustFlag, "trust", false, "approve this safeslop.cue, then run it")
```

Insert the gate **after** the `dry-run` early-return block and **before** `runProfile`
(cli.go:250) — so `--dry-run` stays ungated (it's inspection, like `validate`):

```go
			if err := enforceTrust(path, trustFlag); err != nil {
				return err
			}
			code, err := runProfile(name, prof, argv, ws)
```

- [ ] **Step 4: Run the tests + a live smoke**

```bash
go test ./internal/cli/ -run TestEnforceTrustGate -v
go build ./cmd/safeslop
# smoke: an untrusted policy blocks; trust then dry-run works.
mkdir -p /tmp/trustsmoke && printf 'profiles: { dev: { agent: "claude", environment: "sandbox" } }\n' > /tmp/trustsmoke/safeslop.cue
( cd /tmp/trustsmoke && /Users/jojo/workspace/safeslop/safeslop run dev 2>&1 | head -2 )   # expect: not trusted + the `safeslop trust` hint
( cd /tmp/trustsmoke && /Users/jojo/workspace/safeslop/safeslop trust && /Users/jojo/workspace/safeslop/safeslop run dev --dry-run 2>&1 | head -2 )  # expect: trusted, then dry-run prints the resolved launch
```
Expected: first `run` errors fail-closed with the trust hint; after `trust`, `run --dry-run`
proceeds. (`--dry-run` would also work before trust — it's ungated; the smoke just shows the order.)

- [ ] **Step 5: Commit**

```bash
git add internal/cli/cli.go internal/cli/cli_trust_test.go
git commit -m "feat(cli): fail-closed policy trust gate on run + safeslop trust (specs/0022)"
```

---

### Task 3: mark the ayo actionable realized + close out

**Files:**
- Modify: `specs/0012-sp7-gui-design.md` (§10.5 H2 line)

- [ ] **Step 1: Update the design record**

In `specs/0012-sp7-gui-design.md` §10.5, append to the **Policy integrity** bullet:

```markdown
  **(Realized: specs/0022 — host-side `~/.config/safeslop/trust.json`, `safeslop trust`, fail-closed
  `run` gate on the policy's sha256; gRPC `Launch` path is the remaining fast-follow.)**
```

- [ ] **Step 2: Full gate + build**

```bash
make check && make build
```
Expected: all `ok`; static binary.

- [ ] **Step 3: Commit**

```bash
git add specs/0012-sp7-gui-design.md
git commit -m "docs(spec): mark ayo H2 (policy integrity) realized by specs/0022"
```

---

## Gates & done-checklist

```bash
make check     # go vet + gofmt + go test ./...
make build     # static CGO_ENABLED=0 binary
```
The four fish gates are unaffected (no fish/CUE/README-command surface). Branch + PR (never push
`main`):

```bash
git push -u origin sp-policy-trust-gate
gh pr create --fill
```

## Deferred (later slices)

- **Gate the gRPC `Launch`/`OpenSession` cockpit path** with the same `trust.Store.Check` — the GUI
  is the audience that most needs it; the SwiftUI wizard surfaces the approval (the engine returns a
  trust error, the app shows a "review & trust" screen). Distinct chokepoint (cli.go:319), small.
- **TOFU edge:** if a user sets their workspace to `~` itself, `~/.config/safeslop/` would be inside
  the agent's writable tree. Document as an unsupported configuration; consider a sub-store outside
  `$HOME` if it ever matters.
- Repo-move re-approval (trust keys on absolute path) is intentional, not a bug.
