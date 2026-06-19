# 0023 — honest isolation tier labels Implementation Plan

**Goal:** Stop the tooling from implying the default `sandbox` environment contains a determined
adversary. Surface an honest per-environment **tier label + caveat** in `safeslop run` and
`safeslop doctor`, so users know whether a boundary guards *mistakes* or a *malicious-code escape*
— realizing the ayo's headline actionable H1 (specs/0012 §10.5).

**Architecture:** One source of truth — `policy.EnvTier(env) (tier, note string)` — maps each
environment (`host` / `sandbox`(+default) / `container` / `vm`) to a short tier label and a
one-line honest caveat. `run` prints it (real launch banner + in `--dry-run`'s human/JSON output);
`doctor` prints a tiers legend (human + JSON). The README already carries the honest prose (the
"macOS isolation reality" section); this adds a scannable tier table referencing it.

**Tech stack:** Go stdlib only. No new deps. TDD on the pure `EnvTier` + a `doctorTiers` renderer.

**Honest framing (locked):** host = *none*; sandbox = *mistake-guard* ("guards agent mistakes +
accidental exfil, not a malicious-code escape"); container = *network-enforced*; vm =
*adversary-grade*.

**Scope:** `run` + `doctor` output + a README tier table. **Not** changing behavior — labels only.

**Base branch:** `sp-isolation-tiers` off `main`. **Never push `main`.**

**File structure:**
- `internal/engine/policy/policy.go` (modify) — add `EnvTier`.
- `internal/engine/policy/policy_test.go` (modify) — `EnvTier` table test.
- `internal/cli/cli.go` (modify) — `doctorTiers()` helper; wire into `cmdDoctor` + `cmdRun`.
- `internal/cli/cli_tiers_test.go` (create) — `doctorTiers` shape.
- `README.md` (modify) — a tier table in the isolation section.

---

### Task 1: `policy.EnvTier`

**Files:**
- Modify: `internal/engine/policy/policy.go`
- Test: `internal/engine/policy/policy_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/engine/policy/policy_test.go`:

```go
func TestEnvTier(t *testing.T) {
	cases := map[string]string{
		"host":      "none",
		"sandbox":   "mistake-guard",
		"":          "mistake-guard", // unspecified defaults to sandbox
		"container": "network-enforced",
		"vm":        "adversary-grade",
	}
	for env, wantTier := range cases {
		tier, note := EnvTier(env)
		if tier != wantTier {
			t.Errorf("EnvTier(%q) tier = %q, want %q", env, tier, wantTier)
		}
		if env != "host" && note == "" {
			t.Errorf("EnvTier(%q) must carry an honest note", env)
		}
	}
	if _, note := EnvTier("host"); note == "" {
		t.Error("host tier must still carry a note (no isolation)")
	}
}
```

- [ ] **Step 2: Run it, verify it fails**

```bash
go test ./internal/engine/policy/ -run TestEnvTier -v
```
Expected: FAIL — `undefined: EnvTier`.

- [ ] **Step 3: Write the implementation**

Append to `internal/engine/policy/policy.go`:

```go
// EnvTier returns an honest one-line characterization of an environment's isolation strength so
// run/doctor (and the GUI) never imply the default sandbox contains a determined adversary
// (ayo specs/0012 §10.5 H1). tier is a short label; note is the honest caveat. An empty env is the
// default (sandbox).
func EnvTier(env string) (tier, note string) {
	switch env {
	case "host":
		return "none", "no isolation boundary — the agent runs as you, with your full account"
	case "container":
		return "network-enforced", "container + egress allowlist: real per-URL network control, shared-kernel file isolation"
	case "vm":
		return "adversary-grade", "disposable hardware-virtualized VM: the strongest boundary, heaviest to run"
	default: // "sandbox" and "" (the default)
		return "mistake-guard", "Seatbelt confines files + exec: guards agent mistakes + accidental exfil, not a malicious-code escape"
	}
}
```

- [ ] **Step 4: Run, verify pass**

```bash
go test ./internal/engine/policy/ -run TestEnvTier -v
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/engine/policy/policy.go internal/engine/policy/policy_test.go
git commit -m "feat(policy): EnvTier — honest per-environment isolation tier + caveat (specs/0023)"
```

---

### Task 2: wire tiers into `run` and `doctor`

**Files:**
- Modify: `internal/cli/cli.go`
- Test: `internal/cli/cli_tiers_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/cli/cli_tiers_test.go`:

```go
package cli

import "testing"

func TestDoctorTiers(t *testing.T) {
	tiers := doctorTiers()
	for _, env := range []string{"host", "sandbox", "container", "vm"} {
		row, ok := tiers[env]
		if !ok {
			t.Fatalf("doctorTiers missing %q", env)
		}
		if row["tier"] == "" || row["note"] == "" {
			t.Fatalf("doctorTiers[%q] incomplete: %+v", env, row)
		}
	}
	if tiers["sandbox"]["tier"] != "mistake-guard" {
		t.Fatalf("sandbox tier = %q, want mistake-guard", tiers["sandbox"]["tier"])
	}
}
```

- [ ] **Step 2: Run it, verify it fails**

```bash
go test ./internal/cli/ -run TestDoctorTiers -v
```
Expected: FAIL — `undefined: doctorTiers`.

- [ ] **Step 3: Write the implementation**

Add the helper to `internal/cli/cli.go` (near `doctorReport`):

```go
// doctorTiers renders the per-environment isolation tier legend (shared by doctor's human + JSON).
func doctorTiers() map[string]map[string]string {
	out := map[string]map[string]string{}
	for _, env := range []string{"host", "sandbox", "container", "vm"} {
		tier, note := policy.EnvTier(env)
		out[env] = map[string]string{"tier": tier, "note": note}
	}
	return out
}
```

Wire it into `cmdDoctor`'s `RunE`. In the JSON branch, add `"tiers"` to the emitted map:

```go
			if jsonOut {
				emitJSON(map[string]any{"ok": true, "os": runtime.GOOS, "arch": runtime.GOARCH, "tools": report, "tiers": doctorTiers()})
				return nil
			}
```

And after the human tool loop (just before `return nil` in `cmdDoctor`), print the legend:

```go
			fmt.Println("isolation tiers (what each environment actually protects):")
			for _, env := range []string{"host", "sandbox", "container", "vm"} {
				tier, note := policy.EnvTier(env)
				fmt.Printf("  %-10s %-16s %s\n", env, tier, note)
			}
```

Wire it into `cmdRun`. In the **dry-run** branch, compute the tier once and add it to both
outputs. At the top of the `if dryRun {` block, after the `out` map is built, add the tier to the
JSON map:

```go
				tier, note := policy.EnvTier(prof.Environment)
				out["isolation_tier"] = tier
				out["isolation_note"] = note
```

And in the dry-run human branch, right after the `profile %q: environment=...` printf, add:

```go
					fmt.Printf("  isolation tier: %s — %s\n", tier, note)
```

For the **real launch** (non-dry-run), print a one-line banner before the trust gate /
`runProfile` (non-JSON only) so the user sees what protection they're getting:

```go
			if !jsonOut {
				tier, note := policy.EnvTier(prof.Environment)
				fmt.Printf("isolation tier: %s — %s\n", tier, note)
			}
			if err := enforceTrust(path, trustFlag); err != nil {
				return err
			}
```

- [ ] **Step 4: Run tests + smoke**

```bash
go test ./internal/cli/ -run 'TestDoctorTiers' -v
go build ./cmd/safeslop
./safeslop doctor | sed -n '/isolation tiers/,$p'
./safeslop doctor --json | grep -A2 '"sandbox"'
```
Expected: test PASS; doctor prints the 4-row tier legend; JSON carries `tiers`.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/cli.go internal/cli/cli_tiers_test.go
git commit -m "feat(cli): show isolation tier + caveat in run and doctor (specs/0023)"
```

---

### Task 3: README tier table + close out

**Files:**
- Modify: `README.md` (the "macOS isolation reality in 2026" section, ~line 205)

- [ ] **Step 1: Add a scannable tier table**

Insert immediately under the `### macOS isolation reality in 2026` heading (before the existing
bullets), a table that names each tier honestly:

```markdown
| environment | tier | what it actually protects |
|---|---|---|
| `host` | none | no boundary — the agent runs as you |
| `sandbox` (default) | mistake-guard | Seatbelt confines files + exec; guards mistakes + accidental exfil, **not** a malicious-code escape |
| `container` | network-enforced | container + egress allowlist: real per-URL network control |
| `vm` | adversary-grade | disposable hardware-virtualized VM: strongest, heaviest |

`safeslop doctor` and `safeslop run` print the active tier so the label is never implicit.
```

- [ ] **Step 2: Full gate + drift check + build**

```bash
make check
fish scripts/slop-sync-help.fish check   # README ↔ --help drift gate (prose table must not trip it)
make build
```
Expected: `make check` all ok; the help-sync gate passes (the table is prose, not an AUTOGEN
`--help` block); static binary.

- [ ] **Step 3: Commit**

```bash
git add README.md
git commit -m "docs: honest isolation tier table (ayo H1) (specs/0023)"
```

---

## Gates & done-checklist

```bash
make check
fish scripts/slop-sync-help.fish check
make build
```
Branch + PR (never push `main`):

```bash
git push -u origin sp-isolation-tiers
gh pr create --fill
```

## Deferred (later slices)

- **H7 — strong zero-authoring default**: `run` with no `safeslop.cue` auto-selects a strong
  profile, and a profile declaring secrets/creds defaults to `container` not bare `sandbox`. The
  bigger half of the ayo headline; its own slice.
- Surfacing the tier in the gRPC `Launch` lifecycle events (for the GUI wizard).
