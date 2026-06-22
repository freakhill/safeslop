package uninstall

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/install"
	"github.com/freakhill/safeslop/internal/engine/receipt"
)

func shaOf(s string) string { sum := sha256.Sum256([]byte(s)); return hex.EncodeToString(sum[:]) }

// engineWithHome builds an Engine whose BinDir is under an isolated HOME so trash + prefix checks resolve
// inside the test sandbox.
func engineWithHome(t *testing.T) (*Engine, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	bin := filepath.Join(home, ".local", "bin")
	app := filepath.Join(home, "Applications")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	return NewEngine(install.Dirs{BinDir: bin, AppDir: app}), bin
}

func TestPathACleanRemoval(t *testing.T) {
	e, bin := engineWithHome(t)
	p := filepath.Join(bin, "uv")
	writeFile(t, p, "uv-body")
	item := Item{Tool: "uv", Kind: RemovePathA, Files: []receipt.File{{Path: p, SHA256: shaOf("uv-body")}}}

	res, err := e.applyPathA(item, false)
	if err != nil {
		t.Fatalf("applyPathA: %v", err)
	}
	if len(res.Trashed) != 1 || res.Trashed[0] != p {
		t.Fatalf("expected uv trashed, got %+v", res.Trashed)
	}
	if _, err := os.Lstat(p); !os.IsNotExist(err) {
		t.Fatal("uv should be gone from BinDir")
	}
}

func TestPathAHashMismatchAbortsAtomically(t *testing.T) {
	e, bin := engineWithHome(t)
	good := filepath.Join(bin, "good")
	bad := filepath.Join(bin, "bad")
	writeFile(t, good, "good-body")
	writeFile(t, bad, "EDITED") // on-disk differs from receipt
	item := Item{Tool: "x", Kind: RemovePathA, Files: []receipt.File{
		{Path: good, SHA256: shaOf("good-body")},
		{Path: bad, SHA256: shaOf("original-body")},
	}}

	_, err := e.applyPathA(item, false)
	if err == nil {
		t.Fatal("expected abort on hash mismatch")
	}
	// Atomic batch: nothing moved, including the file that DID match.
	if _, err := os.Lstat(good); err != nil {
		t.Fatalf("matching file must NOT be trashed when the batch aborts: %v", err)
	}
	if _, err := os.Lstat(bad); err != nil {
		t.Fatalf("mismatching file must be left in place: %v", err)
	}
}

func TestPathASelfUpdatingNeedsConfirm(t *testing.T) {
	e, bin := engineWithHome(t)
	p := filepath.Join(bin, "claude")
	writeFile(t, p, "v2-self-updated")
	item := Item{Tool: "claude", Kind: RemovePathA, SelfUpdating: true,
		Files: []receipt.File{{Path: p, SHA256: shaOf("v1-original")}}}

	// Without confirmation: signals ErrNeedsConfirm, file untouched.
	_, err := e.applyPathA(item, false)
	if !errors.Is(err, ErrNeedsConfirm) {
		t.Fatalf("self-updating mismatch should need confirm, got %v", err)
	}
	if _, err := os.Lstat(p); err != nil {
		t.Fatal("claude must be untouched before confirmation")
	}
	// With confirmation: removed despite the drift.
	res, err := e.applyPathA(item, true)
	if err != nil {
		t.Fatalf("confirmed removal: %v", err)
	}
	if len(res.Trashed) != 1 {
		t.Fatalf("confirmed self-updated removal should trash the binary, got %+v", res.Trashed)
	}
}

func TestPathAMissingFileIsSuccess(t *testing.T) {
	e, bin := engineWithHome(t)
	p := filepath.Join(bin, "gone")
	item := Item{Tool: "gone", Kind: RemovePathA, Files: []receipt.File{{Path: p, SHA256: shaOf("whatever")}}}
	res, err := e.applyPathA(item, false)
	if err != nil {
		t.Fatalf("already-gone file should be success (rm -f), got %v", err)
	}
	if len(res.Trashed) != 0 || len(res.Skipped) != 1 {
		t.Fatalf("missing file should be skipped, not trashed: %+v", res)
	}
}

func TestPathAExternalSymlinkSkipped(t *testing.T) {
	e, bin := engineWithHome(t)
	// A brew-style target OUTSIDE the prefix.
	outside := filepath.Join(t.TempDir(), "brew-uv")
	writeFile(t, outside, "brew-binary")
	link := filepath.Join(bin, "uv")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	// Receipt thought uv was a regular file we placed; it's now an external symlink.
	item := Item{Tool: "uv", Kind: RemovePathA, Files: []receipt.File{{Path: link, SHA256: shaOf("uv-body")}}}

	res, err := e.applyPathA(item, false)
	if err != nil {
		t.Fatalf("external symlink should be skipped, not error: %v", err)
	}
	if len(res.Trashed) != 0 || len(res.Skipped) != 1 {
		t.Fatalf("external symlink must be skipped: %+v", res)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatal("the external target must remain intact — never follow the symlink out of the prefix")
	}
}
