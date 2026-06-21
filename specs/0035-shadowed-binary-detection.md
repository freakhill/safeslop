# Shadowed-Binary Detection Implementation Plan

**Goal:** Surface when a catalog tool resolves to more than one binary on the reconstructed PATH and flag
the shadowed (lower-priority) install on the Installs tab — because the one earlier in PATH wins and can
silently differ from what the user expects (`/opt/homebrew/bin/docker` vs `/usr/local/bin/docker`), which
can quietly downgrade isolation (ayo Actionable #6 / `[K]/[L] HIGH`; cashes in the hostenv "free
dividend" the ayo flagged: the engine already reconstructs PATH, so it already knows the resolution
order — expose it).

**Architecture:** Add a `which -a` to hostenv (`Env.LookAll`) that mirrors `Env.LookPath` but returns
*every* executable match in PATH order. Thread it into tools detection as a new `probe.lookAll` seam;
`detect()` records the lower-priority matches as `Status.ShadowedPaths` (the winner stays `Path`). Carry
them over a new `ToolStatus.shadowed_paths` proto field; the Installs row shows an amber "shadows N"
badge whose tooltip lists the resolved path + the shadowed ones. Detection logic is unit-tested
(authoritative); the badge is best-effort visual (only appears when the host actually has a shadowed
tool).

**Tech stack:** Go (`internal/engine/hostenv` + `tools` + control plane, `protoc` stubs committed),
SwiftUI + grpc-swift-2 (cockpit), Go `testing` (SwiftProtobuf auto-generates the Swift field).

**Scope:** detection + the Installs-tab badge. The rest of ayo #6 — the "don't inflate the available
counter," the present-incompatible third tool state, and the install-log scroll-anchor/sticky-errors —
are separate items, noted as follow-ons.

**File structure:**
- `internal/engine/hostenv/reconstruct.go` (modify) — add `Env.LookAll`.
- `internal/engine/hostenv/reconstruct_test.go` (modify) — `LookAll` finds shadows / single / miss.
- `internal/engine/tools/tools.go` (modify) — `probe.lookAll` seam; `Status.ShadowedPaths`; `detect`
  records shadows; `probeFromEnv` + `realProbe` wire `LookAll`.
- `internal/engine/tools/tools_test.go` (modify) — `detect` flags a shadowed binary; the
  `probeFromEnv(lp, nil)` call gains the new arg.
- `internal/engine/control/control.proto` + `app/Sources/SafeSlopCockpit/proto/control.proto` (modify) —
  `ToolStatus.shadowed_paths`.
- `internal/engine/control/pb/*.go` (regenerated).
- `internal/engine/control/server.go` (modify) — map `ShadowedPaths` in `ListTools`.
- `app/Sources/SafeSlopCockpit/UI/InstallsTab.swift` (modify) — the "shadows N" badge.

---

### Task 1: hostenv — Env.LookAll (which -a)

**Files:**
- Modify: `internal/engine/hostenv/reconstruct.go` (after `LookPath`, line 69)
- Test: `internal/engine/hostenv/reconstruct_test.go`

- [ ] **Step 1: Write the failing test**

In `internal/engine/hostenv/reconstruct_test.go`, add after `TestEnvLookPath`:

```go
func TestEnvLookAllFindsShadows(t *testing.T) {
	e := &Env{
		vars:   map[string]string{"PATH": "/opt/homebrew/bin:/usr/local/bin:/usr/bin"},
		isExec: func(p string) bool { return p == "/opt/homebrew/bin/docker" || p == "/usr/local/bin/docker" || p == "/usr/bin/git" },
	}
	all := e.LookAll("docker")
	if len(all) != 2 || all[0] != "/opt/homebrew/bin/docker" || all[1] != "/usr/local/bin/docker" {
		t.Errorf("LookAll(docker)=%v, want both matches in PATH order", all)
	}
	if g := e.LookAll("git"); len(g) != 1 || g[0] != "/usr/bin/git" {
		t.Errorf("LookAll(git)=%v, want a single match", g)
	}
	if n := e.LookAll("nonesuch"); n != nil {
		t.Errorf("LookAll(nonesuch)=%v, want nil", n)
	}
}
```

- [ ] **Step 2: Run it, verify it fails**

```bash
go test ./internal/engine/hostenv/ -run TestEnvLookAll 2>&1 | head
```
Expected: build failure — `e.LookAll undefined`.

- [ ] **Step 3: Write the implementation**

In `internal/engine/hostenv/reconstruct.go`, add after `LookPath` (after line 69):

```go
// LookAll resolves file against EVERY directory in the reconstructed PATH, returning all existing
// executable matches in PATH order (like `which -a`). The first is the one LookPath returns (the winner);
// any others are shadowed — a later-PATH install the user may have meant. Flagging this matters because
// picking the wrong binary can silently differ from what the user expects. A slash-containing name is
// checked directly (zero or one result).
func (e *Env) LookAll(file string) []string {
	if strings.Contains(file, "/") {
		if e.isExec(file) {
			return []string{file}
		}
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, dir := range strings.Split(e.PATH(), ":") {
		if dir == "" {
			continue
		}
		full := filepath.Join(dir, file)
		if seen[full] || !e.isExec(full) {
			continue
		}
		out = append(out, full)
		seen[full] = true
	}
	return out
}
```

- [ ] **Step 4: Run the test, verify it passes**

```bash
go test ./internal/engine/hostenv/ -run TestEnvLookAll -v 2>&1 | tail
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/engine/hostenv/reconstruct.go internal/engine/hostenv/reconstruct_test.go
git commit -m "feat(hostenv): Env.LookAll — which -a over the reconstructed PATH (shadow detection)"
```

---

### Task 2: tools — probe.lookAll seam + Status.ShadowedPaths

**Files:**
- Modify: `internal/engine/tools/tools.go` (`probe` struct, `Status`, `detect`, `probeFromEnv`,
  `realProbe`)
- Test: `internal/engine/tools/tools_test.go`

- [ ] **Step 1: Write the failing test**

In `internal/engine/tools/tools_test.go`, add:

```go
func TestDetectFlagsShadowedBinary(t *testing.T) {
	p := probe{
		lookPath: func(b string) (string, bool) { return "/opt/homebrew/bin/docker", b == "docker" },
		lookAll:  func(b string) []string { return []string{"/opt/homebrew/bin/docker", "/usr/local/bin/docker"} },
	}
	s := detect(p, Tool{Name: "Docker", Detect: []string{"docker"}})
	if !s.Present || s.Path != "/opt/homebrew/bin/docker" {
		t.Fatalf("detect = %+v", s)
	}
	if len(s.ShadowedPaths) != 1 || s.ShadowedPaths[0] != "/usr/local/bin/docker" {
		t.Errorf("ShadowedPaths = %v, want the shadowed /usr/local path", s.ShadowedPaths)
	}

	// A single match is not shadowed.
	p2 := probe{
		lookPath: func(b string) (string, bool) { return "/opt/homebrew/bin/uv", b == "uv" },
		lookAll:  func(b string) []string { return []string{"/opt/homebrew/bin/uv"} },
	}
	if s := detect(p2, Tool{Name: "uv", Detect: []string{"uv"}}); len(s.ShadowedPaths) != 0 {
		t.Errorf("single match should not be shadowed: %v", s.ShadowedPaths)
	}

	// A nil lookAll (detection without the seam) degrades to no shadow info, not a crash.
	p3 := probe{lookPath: func(b string) (string, bool) { return "/usr/bin/git", b == "git" }}
	if s := detect(p3, Tool{Name: "git", Detect: []string{"git"}}); len(s.ShadowedPaths) != 0 {
		t.Errorf("nil lookAll should yield no shadows: %v", s.ShadowedPaths)
	}
}
```

- [ ] **Step 2: Run it, verify it fails**

```bash
go test ./internal/engine/tools/ -run TestDetectFlagsShadowed 2>&1 | head
```
Expected: build failure — `unknown field 'lookAll' in struct literal` / `s.ShadowedPaths undefined`.

- [ ] **Step 3: Add the field, seam, and detection logic**

In `internal/engine/tools/tools.go`:

Add to `Status` (after `Path string`, line 71):

```go
	ShadowedPaths []string // other executables of the same name later on PATH (shadowed by Path); nil if none
```

Add to `probe` (after `lookPath`, line 169):

```go
	lookAll    func(string) []string       // all PATH matches for a binary (which -a); nil disables shadow detection
```

In `detect`, record shadows when the tool is found via PATH. Change the PATH branch (lines 178-187):

```go
	for _, bin := range t.Detect {
		if path, ok := p.lookPath(bin); ok {
			src := "standalone"
			if leaf := t.brewLeaf(); leaf != "" && p.formulae[leaf] {
				src = "brew"
			} else if p.brewPrefix != "" && strings.HasPrefix(path, p.brewPrefix) {
				src = "brew"
			}
			st := Status{Tool: t, Present: true, Source: src, Path: path}
			if p.lookAll != nil {
				if all := p.lookAll(bin); len(all) > 1 {
					st.ShadowedPaths = all[1:] // all[0] is the winner (== path); the rest are shadowed
				}
			}
			return st
		}
	}
```

Update `realProbe` (line 205) to pass `LookAll`:

```go
	return probeFromEnv(env.LookPath, env.LookAll, env.Environ())
```

Update `probeFromEnv` (line 212) to accept and store `lookAll`:

```go
func probeFromEnv(lookPath func(string) (string, bool), lookAll func(string) []string, environ []string) probe {
	br := brewRunner{lookPath: lookPath, environ: environ}
	prefix := ""
	if out, err := br.output("--prefix"); err == nil {
		prefix = strings.TrimSpace(out)
	}
	return probe{
		lookPath: lookPath,
		lookAll:  lookAll,
		appExists: func(p string) bool {
			_, err := os.Stat(p)
			return err == nil
		},
		formulae:   br.list("--formula"),
		casks:      br.list("--cask"),
		brewPrefix: prefix,
	}
}
```

- [ ] **Step 4: Fix the existing probeFromEnv test call**

In `internal/engine/tools/tools_test.go` (~line 107), the call `probeFromEnv(lp, nil)` now needs the
extra arg. Change it to:

```go
	p := probeFromEnv(lp, nil, nil)
```

- [ ] **Step 5: Run the tools tests, verify they pass**

```bash
go test ./internal/engine/tools/ -v 2>&1 | tail -5
```
Expected: PASS (the new shadow test + the existing detection tests).

- [ ] **Step 6: Commit**

```bash
git add internal/engine/tools/tools.go internal/engine/tools/tools_test.go
git commit -m "feat(tools): flag shadowed binaries via probe.lookAll (Status.ShadowedPaths)"
```

---

### Task 3: Proto + ListTools mapping

**Files:**
- Modify: `internal/engine/control/control.proto` (`ToolStatus`)
- Modify: `app/Sources/SafeSlopCockpit/proto/control.proto` (copy)
- Regenerate: `internal/engine/control/pb/*.go`
- Modify: `internal/engine/control/server.go` (`ListTools` mapping, line 200-202)

- [ ] **Step 1: Add the field to ToolStatus**

In `internal/engine/control/control.proto`, the `ToolStatus` message ends with `install_hint = 8`. Add:

```proto
  string install_hint = 8;  // the command that would run, e.g. "brew install uv" (display only)
  repeated string shadowed_paths = 9; // other same-name executables later on PATH (shadowed by `path`)
```

- [ ] **Step 2: Sync the Swift copy + regen + build**

```bash
cp internal/engine/control/control.proto app/Sources/SafeSlopCockpit/proto/control.proto
diff internal/engine/control/control.proto app/Sources/SafeSlopCockpit/proto/control.proto && echo IDENTICAL
make proto
grep -l 'ShadowedPaths' internal/engine/control/pb/control.pb.go && go build ./... && echo "BUILD OK"
```
Expected: `IDENTICAL`; `ShadowedPaths` present; `BUILD OK`.

- [ ] **Step 3: Map ShadowedPaths in ListTools**

In `internal/engine/control/server.go`, the `ListTools` `&pb.ToolStatus{...}` (lines 200-202) — add the
field:

```go
		ts := &pb.ToolStatus{
			Name: st.Tool.Name, Category: st.Tool.Category, Note: st.Tool.Note,
			Present: st.Present, Source: st.Source, Path: st.Path, Installable: st.Installable(),
			ShadowedPaths: st.ShadowedPaths,
		}
```

- [ ] **Step 4: Verify the Go check is green**

```bash
make check
```
Expected: all ok (incl. the cockpit-backend smoke, which exercises ListTools).

- [ ] **Step 5: Commit**

```bash
git add internal/engine/control/control.proto app/Sources/SafeSlopCockpit/proto/control.proto \
        internal/engine/control/pb/control.pb.go internal/engine/control/pb/control_grpc.pb.go \
        internal/engine/control/server.go
git commit -m "feat(proto): ToolStatus.shadowed_paths + ListTools mapping (shadow detection)"
```

---

### Task 4: Cockpit — Installs-tab shadow badge

**Files:**
- Modify: `app/Sources/SafeSlopCockpit/UI/InstallsTab.swift` (the present branch of `row(_:)`, ~line 78)

- [ ] **Step 1: Render the shadow badge**

In `app/Sources/SafeSlopCockpit/UI/InstallsTab.swift`, the present branch currently is:

```swift
            } else if t.present {
                Text(t.source).font(.caption2.weight(.semibold))
                    .padding(.horizontal, 6).padding(.vertical, 2)
                    .background(.green.opacity(0.15), in: Capsule()).foregroundStyle(.green)
                    .help(t.path)
            } else if t.installable {
```

Replace it with (adds an amber "shadows N" badge before the source capsule when shadowed):

```swift
            } else if t.present {
                if !t.shadowedPaths.isEmpty {
                    Label("shadows \(t.shadowedPaths.count)", systemImage: "exclamationmark.triangle.fill")
                        .font(.caption2.weight(.semibold)).foregroundStyle(.orange)
                        .help("Resolves to \(t.path)\nAlso on PATH (shadowed): \(t.shadowedPaths.joined(separator: ", "))")
                }
                Text(t.source).font(.caption2.weight(.semibold))
                    .padding(.horizontal, 6).padding(.vertical, 2)
                    .background(.green.opacity(0.15), in: Capsule()).foregroundStyle(.green)
                    .help(t.path)
            } else if t.installable {
```

- [ ] **Step 2: Build + full Swift suite**

```bash
swift build --package-path app 2>&1 | grep -iE 'error:|Build complete'
swift test --package-path app 2>&1 | grep -iE 'Test run with|✘'
```
Expected: `Build complete!`; `Test run with 17 tests ... passed` (no Swift-test change — the field
generates from the proto and the badge is verified by the Go detection test + the screenshot below).

- [ ] **Step 3: Screenshot the Installs tab (no-regression + opportunistic shadow check)**

```bash
make cockpit-shot installs
```
Read `/tmp/safeslop-cockpit-installs.png`: the catalog renders with present/missing states as before. If
the host happens to have a genuinely shadowed tool (same binary in two PATH dirs), its row shows the
amber "shadows N" badge; if not, the absence is correct (nothing to warn about). Either way confirm the
present rows are unregressed.

- [ ] **Step 4: Commit**

```bash
git add app/Sources/SafeSlopCockpit/UI/InstallsTab.swift
git commit -m "feat(cockpit): Installs-tab 'shadows N' badge for shadowed binaries"
```

---

### Task 5: Full verification + handoff

**Files:**
- Modify: `specs/research/2026-06-21-handoff.md` (note ayo #6 shadow detection done)

- [ ] **Step 1: Run every gate**

```bash
make check ; make build
swift build --package-path app && swift test --package-path app 2>&1 | grep -iE 'Test run with|✘'
fish tests/run.fish 2>&1 | tail -3
fish scripts/slop-pinning.fish
```
Expected: all green (Go incl. hostenv + tools + smoke; Swift 17; fish suite; pinning).

- [ ] **Step 2: Update the handoff**

In `specs/research/2026-06-21-handoff.md`, note ayo Actionable #6's shadowed-binary detection shipped in
`specs/0035` on `sp-cockpit-risk-legibility` (hostenv `LookAll` → `tools` shadow flag → Installs badge);
the remaining #6 sub-items (don't-inflate-"available", present-incompatible third state, install-log
scroll-anchor) stay open.

- [ ] **Step 3: Commit + push**

```bash
git add specs/research/2026-06-21-handoff.md
git commit -m "docs(spec): shadowed-binary detection shipped (specs/0035, ayo Actionable #6)"
SSH_AUTH_SOCK="$HOME/Library/Group Containers/2BUA8C4S2C.com.1password/t/agent.sock" \
  git push forgejo sp-cockpit-risk-legibility
```

---

## Self-review notes

- **Spec coverage** (ayo #6 `[K]/[L] HIGH` shadowing): `which -a` over the reconstructed PATH (Task 1,
  reusing the hostenv resolution the ayo called the "free dividend") ✓; shadows recorded engine-side
  (Task 2, unit-tested incl. the nil-seam degradation) ✓; carried over the wire (Task 3) ✓; flagged on
  the Installs row with the resolved + shadowed paths in the tooltip (Task 4) ✓.
- **Placeholder scan:** every step has concrete code/commands; no TBDs.
- **Name consistency:** `LookAll` (hostenv) → `lookAll` (probe seam) → `ShadowedPaths` (Go Status) →
  `shadowed_paths`/`ShadowedPaths` (proto/pb) → `shadowedPaths` (Swift). The winner stays `Path`;
  `ShadowedPaths` is `all[1:]`.
- **Degradation:** a nil `lookAll` (any code path that builds a probe without the seam) yields no shadow
  info rather than a crash — covered by the Task 2 test.
- **Verification honesty:** the detection is unit-tested (authoritative); the badge only appears when the
  host actually has a shadowed tool, so the screenshot is no-regression + opportunistic, not a guaranteed
  visual.
- **Out of scope (intentional, noted):** the "available" counter de-inflation, the present-incompatible
  third tool state, and the install-log scroll-anchor/sticky-errors (the rest of ayo #6).
