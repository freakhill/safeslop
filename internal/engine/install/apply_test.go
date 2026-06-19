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

func TestApplySkipsOKActions(t *testing.T) {
	res := Result{Actions: []Action{{Name: "mise", Kind: ActionOK, Format: FormatBinaryTarball}}}
	dirs := Dirs{BinDir: t.TempDir(), AppDir: t.TempDir(), TmpDir: t.TempDir()}
	called := false
	_ = Apply(context.Background(), res, dirs, fakeFetcher{}, func(Event) { called = true })
	if called {
		t.Fatal("ok actions must not fetch or emit work")
	}
}
