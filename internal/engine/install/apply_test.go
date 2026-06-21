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

	"aead.dev/minisign"
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
	if len(events) == 0 || events[len(events)-1].Kind != EventDone {
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

// sigFixture builds a fixture artifact plus a minisign-signed SHASUMS file covering it, and a
// fakeFetcher wired for all three URLs. badSHA replaces the artifact's sha in the sums (so the
// signature is valid but the artifact isn't covered → fail closed).
func sigFixture(t *testing.T, badSHA bool) (Action, fakeFetcher, []byte) {
	t.Helper()
	pub, priv, err := minisign.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	art := tgz(t, map[string]string{"mise/bin/mise": "#!/bin/sh\necho mise\n"})
	sumSHA := sha(art)
	if badSHA {
		sumSHA = "deadbeef00000000000000000000000000000000000000000000000000000000"
	}
	sums := []byte(sumSHA + "  ./mise.tar.gz\n")
	sig := minisign.Sign(priv, sums)
	const artURL, sumsURL, sigURL = "https://x/mise.tgz", "https://x/sums", "https://x/sig"
	a := Action{
		Name: "mise", Kind: ActionInstall, Desired: "2026.6.11", Format: FormatBinaryTarball,
		SHA256: sha(art), URL: artURL,
		Sig: &Sig{Scheme: "minisign", PubKey: pub.String(), SumsURL: sumsURL, SigURL: sigURL, Artifact: "./mise.tar.gz"},
	}
	return a, fakeFetcher{artURL: art, sumsURL: sums, sigURL: sig}, art
}

func TestApplyWithValidSigInstalls(t *testing.T) {
	a, ff, _ := sigFixture(t, false)
	dirs := Dirs{BinDir: t.TempDir(), AppDir: t.TempDir(), TmpDir: t.TempDir()}
	if err := Apply(context.Background(), Result{Actions: []Action{a}}, dirs, ff, nil); err != nil {
		t.Fatalf("valid sig chain should install: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dirs.BinDir, "mise")); err != nil {
		t.Fatalf("mise should be installed after a verified sig: %v", err)
	}
}

func TestApplyFailsClosedOnBadSig(t *testing.T) {
	a, ff, _ := sigFixture(t, true) // artifact sha absent from the signed sums
	dirs := Dirs{BinDir: t.TempDir(), AppDir: t.TempDir(), TmpDir: t.TempDir()}
	if err := Apply(context.Background(), Result{Actions: []Action{a}}, dirs, ff, nil); err == nil {
		t.Fatal("Apply must fail closed when the artifact isn't covered by the signed checksum file")
	}
	if _, err := os.Stat(filepath.Join(dirs.BinDir, "mise")); err == nil {
		t.Fatal("nothing must be installed when sig verification fails")
	}
}

// TestInstallAppUpgradeKeepsBackup verifies an .app upgrade is non-destructive: the new version lands
// at dest and the PRIOR version is preserved at dest+".bak" (the rollback copy) — the old code did
// os.RemoveAll(dest) BEFORE the rename, so any failure left the user with no app.
func TestInstallAppUpgradeKeepsBackup(t *testing.T) {
	dirs := Dirs{BinDir: t.TempDir(), AppDir: t.TempDir(), TmpDir: t.TempDir()}
	mk := func(body string) []byte {
		return tgz(t, map[string]string{
			"tart.app/Contents/MacOS/tart": body,
			"tart.app/Contents/Info.plist": "<plist/>",
		})
	}
	install := func(art []byte) {
		url := "https://x/tart.tgz"
		res := Result{Actions: []Action{{Name: "tart", Kind: ActionInstall, Desired: "v", Format: FormatAppTarball, SHA256: sha(art), URL: url}}}
		if err := Apply(context.Background(), res, dirs, fakeFetcher{url: art}, nil); err != nil {
			t.Fatalf("Apply: %v", err)
		}
	}
	install(mk("#!/bin/sh\necho v1\n"))
	install(mk("#!/bin/sh\necho v2\n")) // upgrade over the existing app

	got, err := os.ReadFile(filepath.Join(dirs.AppDir, "tart.app", "Contents", "MacOS", "tart"))
	if err != nil || string(got) != "#!/bin/sh\necho v2\n" {
		t.Fatalf("dest must hold the new v2, got %q err=%v", got, err)
	}
	bak, err := os.ReadFile(filepath.Join(dirs.AppDir, "tart.app.bak", "Contents", "MacOS", "tart"))
	if err != nil || string(bak) != "#!/bin/sh\necho v1\n" {
		t.Fatalf(".bak must preserve the prior v1 for rollback, got %q err=%v", bak, err)
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
