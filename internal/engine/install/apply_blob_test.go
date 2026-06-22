package install

import (
	"context"
	"crypto/sha512"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/receipt"
)

func sha512of(b []byte) string { s := sha512.Sum512(b); return hex.EncodeToString(s[:]) }

// TestApplyVerifiesSHA512OnlyBlob covers a pin that publishes ONLY a sha512 sidecar (alpine-lima's VM
// image): Apply must verify by sha512 and place the blob; a wrong sha512 must fail closed.
func TestApplyVerifiesSHA512OnlyBlob(t *testing.T) {
	art := []byte("a vm image with only a sha512 sidecar\n")
	url := "https://x/guest.iso"
	cache := t.TempDir()
	dirs := Dirs{BinDir: t.TempDir(), AppDir: t.TempDir(), TmpDir: t.TempDir(), CacheDir: cache, ReceiptPath: filepath.Join(t.TempDir(), "r.json")}

	good := Result{Actions: []Action{{Name: "guest", Kind: ActionInstall, Desired: "1", Format: FormatBlob, SHA512: sha512of(art), URL: url, Provenance: ProvenanceVendor}}}
	if err := Apply(context.Background(), good, dirs, fakeFetcher{url: art}, nil); err != nil {
		t.Fatalf("sha512-only blob should verify+place: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cache, "guest")); err != nil {
		t.Fatalf("blob not placed: %v", err)
	}

	bad := Result{Actions: []Action{{Name: "guest2", Kind: ActionInstall, Desired: "1", Format: FormatBlob, SHA512: sha512of([]byte("different")), URL: url, Provenance: ProvenanceVendor}}}
	if err := Apply(context.Background(), bad, Dirs{BinDir: t.TempDir(), AppDir: t.TempDir(), TmpDir: t.TempDir(), CacheDir: t.TempDir(), ReceiptPath: filepath.Join(t.TempDir(), "r2.json")}, fakeFetcher{url: art}, nil); err == nil {
		t.Fatal("a sha512 mismatch must fail closed")
	}
}

// TestApplyPlacesBlobNonExecutable drives Apply for a FormatBlob pin and asserts the artifact lands in
// CacheDir as a 0644 (NON-executable) file and is receipted with its sha256 — the new capability the
// container runtime's VM image + engine tarballs need (specs/0044).
func TestApplyPlacesBlobNonExecutable(t *testing.T) {
	art := []byte("not-an-executable: a pinned VM image blob\n")
	url := "https://x/lima-guest-image"
	res := Result{Actions: []Action{{
		Name: "lima-guest-image", Kind: ActionInstall, Desired: "24.04",
		Format: FormatBlob, SHA256: sha(art), URL: url, Provenance: ProvenanceVendor,
	}}}
	cache := t.TempDir()
	rcPath := filepath.Join(t.TempDir(), "receipts.json")
	dirs := Dirs{BinDir: t.TempDir(), AppDir: t.TempDir(), TmpDir: t.TempDir(), CacheDir: cache, ReceiptPath: rcPath}

	if err := Apply(context.Background(), res, dirs, fakeFetcher{url: art}, nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	dest := filepath.Join(cache, "lima-guest-image")
	fi, err := os.Stat(dest)
	if err != nil {
		t.Fatalf("blob not placed at %s: %v", dest, err)
	}
	if fi.Mode().Perm() != 0o644 {
		t.Fatalf("blob must be 0644 (non-executable), got %o", fi.Mode().Perm())
	}
	if fi.Mode()&0o111 != 0 {
		t.Fatal("blob must NOT have any execute bit")
	}

	store, err := receipt.Load(rcPath)
	if err != nil {
		t.Fatal(err)
	}
	e, ok := store.Get("lima-guest-image")
	if !ok || e.Path != "A" {
		t.Fatalf("blob not receipted as Path A: %+v", e)
	}
	if len(e.Files) != 1 || e.Files[0].Path != dest || e.Files[0].SHA256 != sha(art) {
		t.Fatalf("blob receipt File mismatch: %+v", e.Files)
	}
}
