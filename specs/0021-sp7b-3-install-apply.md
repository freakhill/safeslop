# SP7b-3 — `safeslop install apply` Implementation Plan

**Goal:** Execute the SP7b-2 install plan — for each pending action, download the pinned
artifact, **verify it fail-closed** (sha256 always; upstream minisign signature when the pin
declares one), and install it **per artifact format** (mise binary → `~/.local/bin`,
tart `.app` → `~/Applications` + a `~/.local/bin` symlink) — and expose the same execution
over the gRPC control plane (a unary `InstallPlan` + a server-streaming `InstallApply`) so
the SwiftUI wizard drives it without parsing CLI text.

**Architecture:** A new pb-free `install.Apply(ctx, Result, Dirs, Fetcher, emit)` does the
work and emits plain `install.Event`s. The CLI (`safeslop install apply`) feeds it an HTTP
`Fetcher` and prints events; the gRPC server feeds it the same core and translates
`install.Event → pb.InstallApplyEvent`. Integrity is the **embedded-pin trust chain**: the
sha256es ship compiled into the notarized binary, so a verified artifact inherits Apple's
code-signing root of trust — *this is the honest justification for not delegating to
Homebrew* (specs/0012 §10.2). Where upstream publishes a verifiable signature (mise's
`SHASUMS256.txt.minisig`), Apply also verifies it (sha + checksum-file + minisign chain),
because provenance ≠ honesty (TUF/SLSA). Extraction is stdlib `archive/tar`+`compress/gzip`;
the only new dep is the pure-Go `aead.dev/minisign` (keeps `CGO_ENABLED=0`).

**Tech stack:** Go; stdlib tar/gzip/sha256/http; `aead.dev/minisign`; the existing
`google.golang.org/grpc` + committed pb. TDD with **in-memory tarball fixtures + a fake
Fetcher** (no network in tests), mirroring the SP7b-2 / cred-provider style.

**Scope:** `install apply` (download + verify + install) for the two manifest formats
(`binary-tarball`, `app-tarball`); the `InstallPlan`/`InstallApply` RPCs; the
notarized-pin trust-chain doc + mise minisign verification. **Out of scope** (later slices):
version-freshness time-delay, behavioral VM-eval (opt-in-first-use only — specs/0012 §10.2),
WARP toolchain cert-env wiring (§10.3), docker/nix install (multi-component), self-update.

**Base branch:** `sp7b-3-install-apply-plan`, already cut off `sp7b-2-install-plan`
(SP7b-2 / PR #30 must merge first; this plan extends that branch's `install` package).
It also assumes specs/0012 §10.2 (PR #31). **Never push `main`.**

**File structure:**
- `internal/engine/install/plan.go` (modify) — add `Format` constants + `Sig` to `Pin`;
  extend `ValidateDesired`; carry `Format`/`Sig` onto `Action`.
- `internal/engine/install/verify.go` (create) — `VerifySHA256`, `VerifyMinisign` (sig→
  checksum-file→artifact chain).
- `internal/engine/install/verify_test.go` (create) — sha + minisign (generated keypair).
- `internal/engine/install/apply.go` (create) — `Fetcher`, `Dirs`, `Event`, `Apply`, the
  per-format installers, `extractTarGz`.
- `internal/engine/install/apply_test.go` (create) — fixture-tarball + fake-Fetcher tests.
- `internal/engine/install/desired.go` (modify) — add `Format` (+ mise `Sig`) to the pins.
- `internal/cli/cli.go` (modify) — `install apply` subcommand + HTTP `Fetcher` + PATH warn.
- `internal/cli/cli_install_apply_test.go` (create) — dry-run / JSON shape.
- `internal/engine/control/control.proto` (modify) — `InstallPlan` + `InstallApply` RPCs.
- `internal/engine/control/pb/*.pb.go` (regen via `make proto`).
- `internal/engine/control/server.go` (modify) — implement both RPCs.
- `internal/engine/control/install_test.go` (create) — server-streaming via a fake stream.

---

### Task 1: extend the `Pin`/`Action` manifest with `Format` + `Sig`

**Files:**
- Modify: `internal/engine/install/plan.go`
- Modify: `internal/engine/install/plan_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/engine/install/plan_test.go`:

```go
func TestValidateDesiredRejectsBadFormat(t *testing.T) {
	p := pin("mise", "toolchain", "2026.6.0")
	p.Format = "zip"
	if err := ValidateDesired([]Pin{p}); err == nil {
		t.Fatal("an unknown format must be rejected")
	}
}

func TestValidateDesiredRejectsMissingFormat(t *testing.T) {
	p := pin("mise", "toolchain", "2026.6.0")
	p.Format = ""
	if err := ValidateDesired([]Pin{p}); err == nil {
		t.Fatal("an empty format must be rejected (fail-closed)")
	}
}

func TestValidateDesiredRejectsIncompleteSig(t *testing.T) {
	p := pin("mise", "toolchain", "2026.6.0")
	p.Sig = &Sig{Scheme: "minisign"} // missing pubkey/urls/artifact
	if err := ValidateDesired([]Pin{p}); err == nil {
		t.Fatal("a partial Sig must be rejected (fail-closed)")
	}
}

func TestPlanCarriesFormatAndSig(t *testing.T) {
	p := pin("mise", "toolchain", "2026.6.0")
	p.Sig = &Sig{Scheme: "minisign", PubKey: "RWQk", SumsURL: "u", SigURL: "u", Artifact: "a"}
	state := State{Toolchains: []Tool{{Name: "mise", Present: false}}}
	res, err := Plan(state, []Pin{p})
	if err != nil {
		t.Fatalf("Plan errored: %v", err)
	}
	if res.Actions[0].Format != "binary-tarball" {
		t.Fatalf("format not carried onto action: %q", res.Actions[0].Format)
	}
	if res.Actions[0].Sig == nil || res.Actions[0].Sig.Scheme != "minisign" {
		t.Fatalf("sig not carried onto action: %+v", res.Actions[0].Sig)
	}
}
```

Update the `pin` helper (it must now produce a valid `Format`) — change its body in
`internal/engine/install/plan_test.go`:

```go
func pin(name, kind, ver string) Pin {
	return Pin{
		Name:    name,
		Kind:    kind,
		Format:  "binary-tarball",
		Version: ver,
		SHA256:  "0000000000000000000000000000000000000000000000000000000000000000",
		URL:     "https://example.test/" + name,
	}
}
```

- [ ] **Step 2: Run the test, verify it fails**

```bash
go test ./internal/engine/install/ -run 'TestValidateDesiredRejectsBadFormat|TestPlanCarriesFormatAndSig' -v
```
Expected: FAIL — `p.Format undefined` / `Sig undefined`.

- [ ] **Step 3: Write the minimal implementation**

In `internal/engine/install/plan.go`, add the format constants + `Sig` type, extend `Pin`
and `Action`, and extend `ValidateDesired` + `Plan`.

Add after the `import` block:

```go
// Artifact formats Apply knows how to install (specs/0021).
const (
	FormatBinaryTarball = "binary-tarball" // tar.gz containing <name>/bin/<name>; install to BinDir
	FormatAppTarball    = "app-tarball"    // tar.gz containing <name>.app; install to AppDir + symlink
)

// Sig is an optional upstream signature over the artifact's checksum file. When present, Apply
// verifies sig -> checksum-file -> artifact-sha (fail-closed). Defends maintainer compromise that
// a copied sha256 cannot (provenance != honesty; specs/0012 §10.2).
type Sig struct {
	Scheme   string `json:"scheme"`   // "minisign"
	PubKey   string `json:"pubkey"`   // minisign public key (the single base64 key line)
	SumsURL  string `json:"sums_url"` // URL of SHASUMS256.txt
	SigURL   string `json:"sig_url"`  // URL of SHASUMS256.txt.minisig
	Artifact string `json:"artifact"` // the artifact's name as it appears in SHASUMS256.txt
}
```

Add the fields to `Pin` (after `URL`):

```go
	Format  string `json:"format"`        // binary-tarball | app-tarball
	Sig     *Sig   `json:"sig,omitempty"` // optional upstream signature
```

Add to `Action` (after `URL`):

```go
	Format  string `json:"format"`
	Sig     *Sig   `json:"sig,omitempty"`
```

In `ValidateDesired`, before the final `}` of the loop body (after the URL check), add:

```go
		if p.Format != FormatBinaryTarball && p.Format != FormatAppTarball {
			return fmt.Errorf("install: pin %q has invalid format %q (want %s|%s)", p.Name, p.Format, FormatBinaryTarball, FormatAppTarball)
		}
		if p.Sig != nil {
			if p.Sig.Scheme != "minisign" {
				return fmt.Errorf("install: pin %q sig scheme %q unsupported (want minisign)", p.Name, p.Sig.Scheme)
			}
			if p.Sig.PubKey == "" || p.Sig.SumsURL == "" || p.Sig.SigURL == "" || p.Sig.Artifact == "" {
				return fmt.Errorf("install: pin %q sig is incomplete (need pubkey, sums_url, sig_url, artifact)", p.Name)
			}
		}
```

In `Plan`, where the `Action` literal is built, carry the new fields:

```go
		a := Action{Name: p.Name, Desired: p.Version, SHA256: p.SHA256, URL: p.URL, Format: p.Format, Sig: p.Sig}
```

- [ ] **Step 4: Run the package tests, verify they pass**

```bash
go test ./internal/engine/install/ -v
```
Expected: PASS — the new Task 1 tests plus all SP7b-2 tests (the `pin` helper change keeps
them valid; `DesiredState()` is still empty-or-valid until Task 7).

Note: this breaks `TestDesiredStateIsFailClosed` only if `desired.go` has populated pins
without a `Format`. It is currently `mise`/`tart` **without** `Format` (SP7b-2 left them so),
so update `desired.go` now to add `Format` (values are known from the verified layouts):
mise → `FormatBinaryTarball`, tart → `FormatAppTarball`. Edit both pin literals in
`internal/engine/install/desired.go` to add the line `Format: FormatBinaryTarball,` (mise)
and `Format: FormatAppTarball,` (tart). Re-run `go test ./internal/engine/install/` — green.

- [ ] **Step 5: Commit**

```bash
git add internal/engine/install/plan.go internal/engine/install/plan_test.go internal/engine/install/desired.go
git commit -m "feat(install): Pin gains Format + optional Sig; carry onto Action (SP7b-3)"
```

---

### Task 2: verification helpers (sha256 + minisign chain)

**Files:**
- Create: `internal/engine/install/verify.go`
- Test: `internal/engine/install/verify_test.go`

- [ ] **Step 1: Add the dependency**

```bash
go get aead.dev/minisign
```

- [ ] **Step 2: Write the failing test**

Create `internal/engine/install/verify_test.go`:

```go
package install

import (
	"testing"

	"aead.dev/minisign"
)

func TestVerifySHA256(t *testing.T) {
	data := []byte("hello safeslop")
	// echo -n 'hello safeslop' | shasum -a 256
	const want = "3a2e1f0b9c..." // REPLACE in step 4 with the real digest printed below
	if err := VerifySHA256(data, want); err != nil {
		t.Fatalf("matching digest must verify: %v", err)
	}
	if err := VerifySHA256(data, "00"+want[2:]); err == nil {
		t.Fatal("a wrong digest must fail closed")
	}
}

func TestVerifyMinisignChain(t *testing.T) {
	pub, priv, err := minisign.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	sums := []byte("084c352a9c5d1a19bd31fef84ba9692952aa04e8d2e3fe666948db35dedfaf95  ./mise.tar.gz\n")
	sig := minisign.Sign(priv, sums)
	// good chain: sig over sums, and the artifact line present
	if err := VerifyMinisign(pub.String(), sums, sig,
		"084c352a9c5d1a19bd31fef84ba9692952aa04e8d2e3fe666948db35dedfaf95", "./mise.tar.gz"); err != nil {
		t.Fatalf("valid chain must verify: %v", err)
	}
	// tampered sums -> signature fails
	if err := VerifyMinisign(pub.String(), append(sums, 'x'), sig,
		"084c352a9c5d1a19bd31fef84ba9692952aa04e8d2e3fe666948db35dedfaf95", "./mise.tar.gz"); err == nil {
		t.Fatal("tampered checksum file must fail the signature")
	}
	// artifact sha not present in the (validly signed) sums -> fail closed
	if err := VerifyMinisign(pub.String(), sums, sig,
		"deadbeef00000000000000000000000000000000000000000000000000000000", "./mise.tar.gz"); err == nil {
		t.Fatal("artifact sha absent from signed checksum file must fail closed")
	}
}
```

- [ ] **Step 3: Run it, verify it fails**

```bash
go test ./internal/engine/install/ -run 'TestVerify' -v
```
Expected: FAIL — `undefined: VerifySHA256` / `VerifyMinisign`.

- [ ] **Step 4: Write the implementation**

Create `internal/engine/install/verify.go`:

```go
package install

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"aead.dev/minisign"
)

// VerifySHA256 fails closed unless sha256(data) hex-equals want (case-insensitive).
func VerifySHA256(data []byte, want string) error {
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("sha256 mismatch: got %s want %s", got, want)
	}
	return nil
}

// VerifyMinisign verifies the upstream signature chain, fail-closed:
//  1. sig is a valid minisign signature over sums (the SHASUMS256.txt bytes), under pubKey;
//  2. a line of sums contains both artifactSHA and artifactName (the pin's artifact is covered).
// This is why a copied sha256 isn't enough: the maintainer's key signs the checksum file, and the
// artifact we fetched must appear inside that signed file (specs/0012 §10.2).
func VerifyMinisign(pubKey string, sums, sig []byte, artifactSHA, artifactName string) error {
	var pk minisign.PublicKey
	if err := pk.UnmarshalText([]byte(pubKey)); err != nil {
		return fmt.Errorf("bad minisign public key: %w", err)
	}
	if !minisign.Verify(pk, sums, sig) {
		return fmt.Errorf("minisign signature does not verify against the checksum file")
	}
	for _, line := range strings.Split(string(sums), "\n") {
		if strings.Contains(strings.ToLower(line), strings.ToLower(artifactSHA)) && strings.Contains(line, artifactName) {
			return nil
		}
	}
	return fmt.Errorf("artifact %q (%s) not found in the signed checksum file", artifactName, artifactSHA)
}
```

Now fill the real digest in the test: run
```bash
printf 'hello safeslop' | shasum -a 256
```
and replace `want` in `TestVerifySHA256` with the printed 64-hex value.

- [ ] **Step 5: Run, verify pass, commit**

```bash
go test ./internal/engine/install/ -run 'TestVerify' -v
git add internal/engine/install/verify.go internal/engine/install/verify_test.go go.mod go.sum
git commit -m "feat(install): sha256 + minisign-chain verification (fail-closed) (SP7b-3)"
```

---

### Task 3: `Apply` core + the `binary-tarball` installer (mise)

**Files:**
- Create: `internal/engine/install/apply.go`
- Test: `internal/engine/install/apply_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/engine/install/apply_test.go`:

```go
package install

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// tgz builds an in-memory .tar.gz from name->content entries (mode 0755).
func tgz(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for name, content := range entries {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	tw.Close()
	gw.Close()
	return buf.Bytes()
}

func sha(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

// fakeFetcher serves canned bytes per URL.
type fakeFetcher map[string][]byte

func (f fakeFetcher) Fetch(_ context.Context, url string) (io.ReadCloser, error) {
	b, ok := f[url]
	if !ok {
		return nil, io.EOF
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func TestApplyInstallsBinaryTarball(t *testing.T) {
	art := tgz(t, map[string]string{"mise/bin/mise": "#!/bin/sh\necho mise\n"})
	url := "https://x/mise.tgz"
	res := Result{Actions: []Action{{
		Name: "mise", Kind: ActionInstall, Desired: "2026.6.11",
		Format: FormatBinaryTarball, SHA256: sha(art), URL: url,
	}}}
	dirs := Dirs{BinDir: t.TempDir(), AppDir: t.TempDir(), TmpDir: t.TempDir()}
	var events []Event
	err := Apply(context.Background(), res, dirs, fakeFetcher{url: art}, func(e Event) { events = append(events, e) })
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	got := filepath.Join(dirs.BinDir, "mise")
	if fi, err := os.Stat(got); err != nil || fi.Mode()&0o111 == 0 {
		t.Fatalf("mise not installed executable at %s (err=%v)", got, err)
	}
	if events[len(events)-1].Kind != EventDone {
		t.Fatalf("expected a final done event, got %+v", events)
	}
}

func TestApplyFailsClosedOnSHAMismatch(t *testing.T) {
	art := tgz(t, map[string]string{"mise/bin/mise": "x"})
	url := "https://x/mise.tgz"
	res := Result{Actions: []Action{{
		Name: "mise", Kind: ActionInstall, Desired: "1", Format: FormatBinaryTarball,
		SHA256: "00000000000000000000000000000000000000000000000000000000deadbeef", URL: url,
	}}}
	dirs := Dirs{BinDir: t.TempDir(), AppDir: t.TempDir(), TmpDir: t.TempDir()}
	err := Apply(context.Background(), res, dirs, fakeFetcher{url: art}, func(Event) {})
	if err == nil {
		t.Fatal("Apply must fail closed on sha mismatch")
	}
	if _, statErr := os.Stat(filepath.Join(dirs.BinDir, "mise")); statErr == nil {
		t.Fatal("nothing must be installed when verification fails")
	}
}

func TestApplySkipsOKActions(t *testing.T) {
	res := Result{Actions: []Action{{Name: "mise", Kind: ActionOK, Format: FormatBinaryTarball}}}
	dirs := Dirs{BinDir: t.TempDir(), AppDir: t.TempDir(), TmpDir: t.TempDir()}
	called := false
	_ = Apply(context.Background(), res, dirs, fakeFetcher{}, func(Event) { called = true })
	if called {
		t.Fatal("ok actions must not fetch or emit work")
	}
}
```

- [ ] **Step 2: Run it, verify it fails**

```bash
go test ./internal/engine/install/ -run TestApply -v
```
Expected: FAIL — `undefined: Apply` / `Dirs` / `Event`.

- [ ] **Step 3: Write the implementation**

Create `internal/engine/install/apply.go`:

```go
package install

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Fetcher fetches an artifact URL. The CLI uses HTTP; tests use a fake.
type Fetcher interface {
	Fetch(ctx context.Context, url string) (io.ReadCloser, error)
}

// Dirs are the install targets. BinDir gets executables (~/.local/bin); AppDir gets .app bundles
// (~/Applications); TmpDir is scratch for download/extract.
type Dirs struct {
	BinDir string
	AppDir string
	TmpDir string
}

// EventKind classifies an Apply progress event.
type EventKind string

const (
	EventStart    EventKind = "start"    // beginning an action
	EventProgress EventKind = "progress" // a step within an action (download/verify/install)
	EventDone     EventKind = "done"     // action completed
	EventError    EventKind = "error"    // action failed (fail-closed abort)
)

// Event is a pb-free progress record so the engine never imports the gRPC types; the CLI prints
// these and the control server translates them to pb.InstallApplyEvent.
type Event struct {
	Kind EventKind `json:"kind"`
	Tool string    `json:"tool"`
	Msg  string    `json:"msg"`
}

// Apply executes the plan's non-ok actions in manifest order: fetch -> verify (sha256, then
// minisign chain when the pin declares a Sig) -> install per Format. Fail-closed: a verify error
// aborts that action (emitting EventError) and Apply returns the first error without installing.
func Apply(ctx context.Context, res Result, dirs Dirs, fetch Fetcher, emit func(Event)) error {
	if emit == nil {
		emit = func(Event) {}
	}
	for _, a := range res.Actions {
		if a.Kind == ActionOK {
			continue
		}
		emit(Event{Kind: EventStart, Tool: a.Name, Msg: string(a.Kind) + " " + a.Desired})
		if err := applyOne(ctx, a, dirs, fetch, emit); err != nil {
			emit(Event{Kind: EventError, Tool: a.Name, Msg: err.Error()})
			return fmt.Errorf("install %s: %w", a.Name, err)
		}
		emit(Event{Kind: EventDone, Tool: a.Name, Msg: a.Desired})
	}
	return nil
}

func applyOne(ctx context.Context, a Action, dirs Dirs, fetch Fetcher, emit func(Event)) error {
	emit(Event{Kind: EventProgress, Tool: a.Name, Msg: "downloading"})
	rc, err := fetch.Fetch(ctx, a.URL)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	data, err := io.ReadAll(rc)
	rc.Close()
	if err != nil {
		return fmt.Errorf("download read: %w", err)
	}
	emit(Event{Kind: EventProgress, Tool: a.Name, Msg: "verifying"})
	if err := VerifySHA256(data, a.SHA256); err != nil {
		return err
	}
	if a.Sig != nil {
		if err := verifySigChain(ctx, a, fetch); err != nil {
			return err
		}
	}
	emit(Event{Kind: EventProgress, Tool: a.Name, Msg: "installing"})
	work, err := os.MkdirTemp(dirs.TmpDir, "safeslop-install-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	if err := extractTarGz(data, work); err != nil {
		return fmt.Errorf("extract: %w", err)
	}
	switch a.Format {
	case FormatBinaryTarball:
		return installBinary(a.Name, work, dirs.BinDir)
	case FormatAppTarball:
		return installApp(a.Name, work, dirs.AppDir, dirs.BinDir)
	default:
		return fmt.Errorf("unknown format %q", a.Format)
	}
}

// verifySigChain is filled in by Task 8 (no-op stub here keeps Task 3 compiling/testable).
func verifySigChain(_ context.Context, _ Action, _ Fetcher) error { return nil }

// installBinary copies <name>/bin/<name> (or the first file named <name>) into binDir, 0755.
func installBinary(name, srcRoot, binDir string) error {
	src := filepath.Join(srcRoot, name, "bin", name)
	if _, err := os.Stat(src); err != nil {
		found, ferr := findFile(srcRoot, name)
		if ferr != nil {
			return fmt.Errorf("binary %q not found in archive: %w", name, err)
		}
		src = found
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return err
	}
	return copyFile(src, filepath.Join(binDir, name), 0o755)
}

// installApp moves <name>.app into appDir and symlinks its inner binary into binDir.
func installApp(name, srcRoot, appDir, binDir string) error {
	app := filepath.Join(srcRoot, name+".app")
	if _, err := os.Stat(app); err != nil {
		return fmt.Errorf("%s.app not found in archive: %w", name, err)
	}
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		return err
	}
	dest := filepath.Join(appDir, name+".app")
	_ = os.RemoveAll(dest)
	if err := os.Rename(app, dest); err != nil {
		if err := copyTree(app, dest); err != nil { // cross-device fallback
			return err
		}
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return err
	}
	link := filepath.Join(binDir, name)
	_ = os.Remove(link)
	return os.Symlink(filepath.Join(dest, "Contents", "MacOS", name), link)
}

func extractTarGz(data []byte, dest string) error {
	gr, err := gzip.NewReader(bytesReader(data))
	if err != nil {
		return err
	}
	defer gr.Close()
	tr := tar.NewReader(gr)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		target, err := safeJoin(dest, h.Name) // reject path traversal / absolute / symlink escape
		if err != nil {
			return err
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(h.Mode)&0o777)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			f.Close()
		case tar.TypeSymlink:
			// app bundles contain internal symlinks; only allow ones that stay inside dest.
			if _, err := safeJoin(filepath.Dir(target), h.Linkname); err != nil {
				return err
			}
			_ = os.Symlink(h.Linkname, target)
		}
	}
}

// safeJoin joins name under root and rejects anything that escapes root (path traversal).
func safeJoin(root, name string) (string, error) {
	clean := filepath.Join(root, name)
	rel, err := filepath.Rel(root, clean)
	if err != nil || rel == ".." || filepathHasDotDotPrefix(rel) {
		return "", fmt.Errorf("unsafe path in archive: %q", name)
	}
	return clean, nil
}
```

Add the small helpers (a new `internal/engine/install/fsutil.go` keeps `apply.go` focused):

```go
package install

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }

func filepathHasDotDotPrefix(rel string) bool {
	return rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func findFile(root, name string) (string, error) {
	var found string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() == name {
			found = p
			return io.EOF // stop early
		}
		return nil
	})
	if found == "" {
		if err == nil {
			err = os.ErrNotExist
		}
		return "", err
	}
	return found, nil
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		return copyFile(p, target, fi.Mode().Perm())
	})
}
```

- [ ] **Step 4: Run the tests, verify they pass**

```bash
go test ./internal/engine/install/ -run TestApply -v
```
Expected: PASS (binary-tarball install, sha-mismatch fail-closed, ok-skip).

- [ ] **Step 5: Commit**

```bash
git add internal/engine/install/apply.go internal/engine/install/fsutil.go internal/engine/install/apply_test.go
git commit -m "feat(install): Apply core + binary-tarball installer, fail-closed (SP7b-3)"
```

---

### Task 4: the `app-tarball` installer (tart)

**Files:**
- Modify: `internal/engine/install/apply_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/engine/install/apply_test.go`:

```go
func TestApplyInstallsAppTarball(t *testing.T) {
	art := tgz(t, map[string]string{
		"tart.app/Contents/MacOS/tart": "#!/bin/sh\necho tart\n",
		"tart.app/Contents/Info.plist": "<plist/>",
	})
	url := "https://x/tart.tgz"
	res := Result{Actions: []Action{{
		Name: "tart", Kind: ActionInstall, Desired: "2.32.1",
		Format: FormatAppTarball, SHA256: sha(art), URL: url,
	}}}
	dirs := Dirs{BinDir: t.TempDir(), AppDir: t.TempDir(), TmpDir: t.TempDir()}
	if err := Apply(context.Background(), res, dirs, fakeFetcher{url: art}, func(Event) {}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dirs.AppDir, "tart.app", "Contents", "MacOS", "tart")); err != nil {
		t.Fatalf("tart.app not installed: %v", err)
	}
	link := filepath.Join(dirs.BinDir, "tart")
	if target, err := os.Readlink(link); err != nil || filepath.Base(target) != "tart" {
		t.Fatalf("expected %s -> .../MacOS/tart symlink, got %q err=%v", link, target, err)
	}
}
```

The `installApp` implementation already exists (Task 3). This task confirms it end-to-end on
an app-tarball fixture. (The implementation may already pass — that is fine; TDD here guards
the format wiring and the symlink target.)

- [ ] **Step 2: Run, verify**

```bash
go test ./internal/engine/install/ -run TestApplyInstallsAppTarball -v
```
Expected: PASS. If it fails on the `os.Rename` cross-device path, the `copyTree` fallback in
`installApp` covers it — confirm the test uses the same filesystem (it does: all under
`t.TempDir()`), so `Rename` succeeds.

- [ ] **Step 3: Commit**

```bash
git add internal/engine/install/apply_test.go
git commit -m "test(install): app-tarball installer end-to-end (tart .app + symlink) (SP7b-3)"
```

---

### Task 5: the `install apply` CLI (HTTP fetcher, --dry-run, --json, PATH warning)

**Files:**
- Modify: `internal/cli/cli.go`
- Test: `internal/cli/cli_install_apply_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/cli/cli_install_apply_test.go`:

```go
package cli

import (
	"encoding/json"
	"testing"
)

func TestInstallApplyDryRunJSONShape(t *testing.T) {
	out, err := renderInstallApplyDryRunJSON("v9.9.9")
	if err != nil {
		t.Fatalf("apply --dry-run --json errored: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, out)
	}
	if _, ok := m["actions"]; !ok {
		t.Fatalf("apply dry-run JSON missing \"actions\": %v", m)
	}
	if _, ok := m["dry_run"]; !ok {
		t.Fatalf("apply dry-run JSON missing \"dry_run\": %v", m)
	}
}
```

- [ ] **Step 2: Run, verify it fails**

```bash
go test ./internal/cli/ -run TestInstallApplyDryRun -v
```
Expected: FAIL — `undefined: renderInstallApplyDryRunJSON`.

- [ ] **Step 3: Write the implementation**

In `internal/cli/cli.go`, register the `apply` subcommand inside `cmdInstall()` (after the
`plan` block, before `return c`):

```go
	c.AddCommand(func() *cobra.Command {
		var dryRun bool
		ac := &cobra.Command{
			Use:   "apply",
			Short: "Download, verify (fail-closed), and install the pinned toolchains + runtimes",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				res, err := installPlanResult(Version)
				if err != nil {
					return err
				}
				if dryRun {
					if jsonOut {
						out, _ := renderInstallApplyDryRunJSON(Version)
						fmt.Println(out)
						return nil
					}
					fmt.Printf("%d change(s) would be applied\n", res.Pending())
					for _, a := range res.Actions {
						if a.Kind != install.ActionOK {
							fmt.Printf("  %-10s %-8s -> %s\n", a.Name, a.Kind, a.Desired)
						}
					}
					return nil
				}
				dirs, err := defaultInstallDirs()
				if err != nil {
					return err
				}
				emit := func(e install.Event) {
					if jsonOut {
						emitJSON(map[string]any{"kind": e.Kind, "tool": e.Tool, "msg": e.Msg})
					} else {
						fmt.Printf("  [%s] %s %s\n", e.Tool, e.Kind, e.Msg)
					}
				}
				if err := install.Apply(cmd.Context(), res, dirs, httpFetcher{}, emit); err != nil {
					return err
				}
				warnIfNotOnPath(dirs.BinDir)
				return nil
			},
		}
		ac.Flags().BoolVar(&dryRun, "dry-run", false, "show what would be installed without doing it")
		return ac
	}())
```

Add the helpers near the other install helpers in `internal/cli/cli.go`:

```go
type httpFetcher struct{}

func (httpFetcher) Fetch(ctx context.Context, url string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req) // system trust store: honors a WARP CA in the keychain
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
	}
	return resp.Body, nil
}

func defaultInstallDirs() (install.Dirs, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return install.Dirs{}, err
	}
	return install.Dirs{
		BinDir: filepath.Join(home, ".local", "bin"),
		AppDir: filepath.Join(home, "Applications"),
		TmpDir: os.TempDir(),
	}, nil
}

func warnIfNotOnPath(binDir string) {
	for _, p := range filepath.SplitList(os.Getenv("PATH")) {
		if p == binDir {
			return
		}
	}
	fmt.Fprintf(os.Stderr, "note: %s is not on your $PATH — add it so installed tools resolve\n", binDir)
}

func renderInstallApplyDryRunJSON(version string) (string, error) {
	res, err := installPlanResult(version)
	if err != nil {
		return "", err
	}
	b, _ := json.MarshalIndent(map[string]any{"dry_run": true, "actions": res.Actions}, "", "  ")
	return string(b), nil
}
```

Add `"io"`, `"net/http"`, `"path/filepath"` to the `cli.go` import block if not already
present (`path/filepath` already is; add `io` and `net/http`).

- [ ] **Step 4: Run tests + a live dry-run**

```bash
go test ./internal/cli/ -run TestInstallApply -v
go build ./cmd/safeslop && ./safeslop install apply --dry-run && ./safeslop install apply --dry-run --json
```
Expected: test PASS; dry-run lists the same pending changes `install plan` shows (e.g.
`mise upgrade -> 2026.6.11`). **Do not run a non-dry-run apply yet** — sig verification
(Task 8) isn't wired and the manifest may still lack a Sig.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/cli.go internal/cli/cli_install_apply_test.go
git commit -m "feat(cli): safeslop install apply (--dry-run/--json, HTTP fetch, PATH warn) (SP7b-3)"
```

---

### Task 6: gRPC `InstallPlan` (unary) + `InstallApply` (server-streaming)

**Files:**
- Modify: `internal/engine/control/control.proto`
- Regen: `internal/engine/control/pb/*.pb.go` (`make proto`)
- Modify: `internal/engine/control/server.go`
- Test: `internal/engine/control/install_test.go`

- [ ] **Step 1: Extend the proto**

In `internal/engine/control/control.proto`, add to the `Control` service:

```proto
  rpc InstallPlan(InstallPlanRequest) returns (InstallPlanResponse);
  rpc InstallApply(InstallApplyRequest) returns (stream InstallApplyEvent);
```

And add the messages (after `CloseSessionResponse`):

```proto
message InstallAction {
  string name = 1;
  string kind = 2;     // install | upgrade | ok
  string current = 3;
  string desired = 4;
}
message InstallPlanRequest {}
message InstallPlanResponse { repeated InstallAction actions = 1; }

message InstallApplyRequest {}
message InstallApplyEvent {
  enum Kind { START = 0; PROGRESS = 1; DONE = 2; ERROR = 3; }
  Kind kind = 1;
  string tool = 2;
  string msg = 3;
}
```

- [ ] **Step 2: Regenerate the Go stubs**

```bash
make proto && go build ./...
```
Expected: regenerates `pb/control.pb.go` + `pb/control_grpc.pb.go` with the new types; builds.
(Requires `protoc` + `protoc-gen-go` + `protoc-gen-go-grpc` locally, per specs/0012 §2. If
absent: `brew install protobuf` and `go install google.golang.org/protobuf/cmd/protoc-gen-go@latest google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest`.)

- [ ] **Step 3: Write the failing test**

Create `internal/engine/control/install_test.go`:

```go
package control

import (
	"context"
	"testing"

	"google.golang.org/grpc"

	"github.com/freakhill/safeslop/internal/engine/control/pb"
)

// fakeApplyStream captures InstallApplyEvents sent by the server-streaming RPC.
type fakeApplyStream struct {
	grpc.ServerStream
	ctx  context.Context
	sent []*pb.InstallApplyEvent
}

func (f *fakeApplyStream) Context() context.Context { return f.ctx }
func (f *fakeApplyStream) Send(e *pb.InstallApplyEvent) error {
	f.sent = append(f.sent, e)
	return nil
}

func TestInstallApplyStreamsEvents(t *testing.T) {
	s := &server{
		version: "vTEST",
		installApplyFn: func(emit func(*pb.InstallApplyEvent)) error {
			emit(&pb.InstallApplyEvent{Kind: pb.InstallApplyEvent_START, Tool: "mise"})
			emit(&pb.InstallApplyEvent{Kind: pb.InstallApplyEvent_DONE, Tool: "mise"})
			return nil
		},
	}
	st := &fakeApplyStream{ctx: context.Background()}
	if err := s.InstallApply(&pb.InstallApplyRequest{}, st); err != nil {
		t.Fatalf("InstallApply: %v", err)
	}
	if len(st.sent) != 2 || st.sent[0].Kind != pb.InstallApplyEvent_START || st.sent[1].Kind != pb.InstallApplyEvent_DONE {
		t.Fatalf("unexpected event stream: %+v", st.sent)
	}
}

func TestInstallPlanReturnsActions(t *testing.T) {
	s := &server{version: "vTEST"}
	resp, err := s.InstallPlan(context.Background(), &pb.InstallPlanRequest{})
	if err != nil {
		t.Fatalf("InstallPlan: %v", err)
	}
	// actions slice is whatever the embedded manifest diffs to (possibly empty) — must not error.
	_ = resp.Actions
}
```

- [ ] **Step 4: Implement the RPCs**

In `internal/engine/control/server.go`, add the field to the `server` struct:

```go
	installApplyFn func(emit func(*pb.InstallApplyEvent)) error
```

Add the import `"github.com/freakhill/safeslop/internal/engine/install"` and the methods:

```go
func (s *server) InstallPlan(_ context.Context, _ *pb.InstallPlanRequest) (*pb.InstallPlanResponse, error) {
	st := install.Status(context.Background(), s.version)
	res, err := install.Plan(st, install.DesiredState())
	if err != nil {
		return nil, err
	}
	out := &pb.InstallPlanResponse{}
	for _, a := range res.Actions {
		out.Actions = append(out.Actions, &pb.InstallAction{
			Name: a.Name, Kind: string(a.Kind), Current: a.Current, Desired: a.Desired,
		})
	}
	return out, nil
}

func (s *server) InstallApply(_ *pb.InstallApplyRequest, stream pb.Control_InstallApplyServer) error {
	emit := func(e *pb.InstallApplyEvent) { _ = stream.Send(e) }
	if s.installApplyFn == nil {
		emit(&pb.InstallApplyEvent{Kind: pb.InstallApplyEvent_ERROR, Msg: "install apply not wired"})
		return nil
	}
	return s.installApplyFn(emit)
}
```

Wire `installApplyFn` in `Serve` (mirror the existing `launchFn` wiring in
`internal/engine/control/serve.go`): build `install.Dirs` from the home dir and adapt
`install.Apply(ctx, plan, dirs, httpFetcher, …)`, translating each `install.Event` to the pb
enum (`EventStart→START`, `EventProgress→PROGRESS`, `EventDone→DONE`, `EventError→ERROR`).
Put the translation in a small `serve.go` helper so the engine stays pb-free:

```go
func installEventToPB(e install.Event) *pb.InstallApplyEvent {
	k := pb.InstallApplyEvent_PROGRESS
	switch e.Kind {
	case install.EventStart:
		k = pb.InstallApplyEvent_START
	case install.EventDone:
		k = pb.InstallApplyEvent_DONE
	case install.EventError:
		k = pb.InstallApplyEvent_ERROR
	}
	return &pb.InstallApplyEvent{Kind: k, Tool: e.Tool, Msg: e.Msg}
}
```

(The `Serve` wiring uses the same `httpFetcher`/`defaultInstallDirs` logic as the CLI; if
that lives in `internal/cli`, lift the tiny HTTP fetcher + dirs resolver into the `install`
package — e.g. `install.HTTPFetcher{}` and `install.DefaultDirs()` — and have both the CLI
and the server use them, so there's one implementation. Do this refactor here.)

- [ ] **Step 5: Run, verify, commit**

```bash
make check
git add internal/engine/control/control.proto internal/engine/control/pb internal/engine/control/server.go internal/engine/control/serve.go internal/engine/control/install_test.go internal/engine/install/apply.go internal/cli/cli.go
git commit -m "feat(control): InstallPlan + InstallApply gRPC (unary + server-streaming) (SP7b-3)"
```

---

### Task 7: populate `Format` (done in Task 1) + real mise `Sig`, and wire signature verification

This is the trust-chain task (specs/0012 §10.2). **Timebox: 20 min.** Network-dependent;
if blocked, leave `mise.Sig` nil (apply stays sha-only, still fail-closed) and land the rest
— the Sig schema + `verifySigChain` are already in place. Do not invent a public key.

**Files:**
- Modify: `internal/engine/install/apply.go` (fill `verifySigChain`)
- Modify: `internal/engine/install/desired.go` (mise `Sig`)
- Test: `internal/engine/install/apply_test.go`

- [ ] **Step 1: Write the failing test** (sig wiring is invoked when a Sig is present)

Append to `internal/engine/install/apply_test.go`:

```go
func TestApplyVerifiesSigWhenPresent(t *testing.T) {
	// A fixture artifact + a SHASUMS file + a real minisign signature over it.
	art := tgz(t, map[string]string{"mise/bin/mise": "x"})
	sumsLine := sha(art) + "  ./mise.tar.gz\n"
	// Generate a keypair, sign the sums; the bad case mutates the sums URL body.
	// (uses aead.dev/minisign as in verify_test.go)
	t.Skip("enable after filling pub/priv generation; guards that a bad sig fails closed")
	_ = sumsLine
}
```

(Keep this as a guard skeleton; the substantive sig-verify logic is already covered by
`TestVerifyMinisignChain` in Task 2. The point of this step is the *wiring* — that
`applyOne` calls `verifySigChain` and that a failing chain aborts. Implement the non-skipped
version if time allows, using a `fakeFetcher` that also serves the sums + sig URLs.)

- [ ] **Step 2: Fetch mise's real minisign public key + the signature URLs**

```bash
# mise publishes its minisign pubkey in its install script / docs. Extract the real key:
curl -fsSL https://mise.run | grep -ioE 'RW[A-Za-z0-9+/]{40,}' | head -1   # the minisign public key line
# The per-release signature + checksum file:
#   sums: https://github.com/jdx/mise/releases/download/v<ver>/SHASUMS256.txt
#   sig:  https://github.com/jdx/mise/releases/download/v<ver>/SHASUMS256.txt.minisig
# Verify locally before trusting:
ver=2026.6.11
curl -fsSL "https://github.com/jdx/mise/releases/download/v${ver}/SHASUMS256.txt" -o /tmp/SUMS
curl -fsSL "https://github.com/jdx/mise/releases/download/v${ver}/SHASUMS256.txt.minisig" -o /tmp/SUMS.minisig
# (optional sanity) brew install minisign && minisign -Vm /tmp/SUMS -P '<the RW... key>'
```

If `mise.run` no longer embeds the key inline, get it from the mise repo
(`jdx/mise` → the bootstrap script or `docs/`), not from a third party. Record the exact
`RW...` key string.

- [ ] **Step 3: Fill `verifySigChain` and the mise `Sig`**

Replace the stub `verifySigChain` in `apply.go`:

```go
func verifySigChain(ctx context.Context, a Action, fetch Fetcher) error {
	sumsRC, err := fetch.Fetch(ctx, a.Sig.SumsURL)
	if err != nil {
		return fmt.Errorf("fetch checksum file: %w", err)
	}
	sums, err := io.ReadAll(sumsRC)
	sumsRC.Close()
	if err != nil {
		return err
	}
	sigRC, err := fetch.Fetch(ctx, a.Sig.SigURL)
	if err != nil {
		return fmt.Errorf("fetch signature: %w", err)
	}
	sig, err := io.ReadAll(sigRC)
	sigRC.Close()
	if err != nil {
		return err
	}
	return VerifyMinisign(a.Sig.PubKey, sums, sig, a.SHA256, a.Sig.Artifact)
}
```

In `internal/engine/install/desired.go`, add the `Sig` to the mise pin (real values from
Step 2):

```go
			Sig: &Sig{
				Scheme:   "minisign",
				PubKey:   "<REAL RW... key from step 2>",
				SumsURL:  "https://github.com/jdx/mise/releases/download/v2026.6.11/SHASUMS256.txt",
				SigURL:   "https://github.com/jdx/mise/releases/download/v2026.6.11/SHASUMS256.txt.minisig",
				Artifact: "./mise-v2026.6.11-macos-arm64.tar.gz",
			},
```

- [ ] **Step 4: Verify end-to-end (real network) + full gate**

```bash
go test ./internal/engine/install/ -v
make check
go build ./cmd/safeslop
# Real apply against this machine (mise is 'upgrade' per SP7b-2): verifies sha + mise minisign.
./safeslop install apply
./safeslop install status   # confirm mise now reports the pinned version
```
Expected: mise downloads, sha + minisign both verify, installs to `~/.local/bin/mise`; tart
is `ok` (no-op). `make check` green. If `mise.Sig` was left nil (network blocked), apply
still runs sha-only and the note in `desired.go` records the follow-up.

- [ ] **Step 5: Commit**

```bash
git add internal/engine/install/apply.go internal/engine/install/desired.go internal/engine/install/apply_test.go
git commit -m "feat(install): verify mise upstream minisign signature on apply (SP7b-3)"
```

---

### Task 8: document the trust chain + close out

**Files:**
- Modify: `internal/engine/install/apply.go` (package/file doc) or `README.md`

- [ ] **Step 1: Document the notarized-pin trust chain**

Add a doc comment at the top of `internal/engine/install/apply.go` (and a short README note
if the README documents Go subcommands) stating the chain explicitly, per specs/0012 §10.2:

> The pinned sha256es ship **compiled into the notarized safeslop binary**, so a downloaded
> artifact that matches its embedded sha inherits Apple's code-signing root of trust —
> tampering with the pin set breaks the binary's signature. This is why safeslop does **not**
> delegate to Homebrew: a GitHub-release download against an advisory README hash would be a
> *weaker* root of trust, not stronger. Where upstream also publishes a verifiable signature
> (mise's minisign), Apply verifies it too, because a faithfully-checksummed artifact from a
> compromised maintainer is still malicious (provenance ≠ honesty).

- [ ] **Step 2: Final full gate + build**

```bash
make check && make build
```
Expected: all `ok`; static binary produced.

- [ ] **Step 3: Commit**

```bash
git add internal/engine/install/apply.go README.md
git commit -m "docs(install): record the notarized-pin trust chain (no-brew rationale) (SP7b-3)"
```

---

## Gates & done-checklist

```bash
make check     # go vet + gofmt + go test ./...  — all ok
make build     # static CGO_ENABLED=0 binary
```
The four fish gates are unaffected (no fish/CUE/README-command surface beyond the optional
trust-chain note). New dep: `aead.dev/minisign` (pure Go — keeps `CGO_ENABLED=0`); verify
`make build` still produces a static binary.

Branch + PR (never push `main`; base on `sp7b-2-install-plan` until PR #30 merges, then
rebase onto `main`):

```bash
git push -u origin sp7b-3-install-apply-plan
gh pr create --fill
```

## Deferred (later slices)

- **Version-freshness time-delay** (prefer artifacts aged > N days — TUF freshness).
- **Behavioral VM-eval**, opt-in first-use only (specs/0012 §10.2; never gate routine updates).
- **WARP toolchain cert-env wiring** for the tools apply installs (specs/0012 §10.3).
- **docker / nix** install (multi-component installers, not single-artifact).
- **`self` self-update** + `app` codesign verification.
- tart upstream signature (sha-only today — its releases don't publish a minisign/PGP sig
  safeslop can pin; revisit if cirruslabs adds one).
