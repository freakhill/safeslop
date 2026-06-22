package tools

import (
	"testing"

	"github.com/freakhill/safeslop/internal/engine/receipt"
)

// TestRecordVerifiedInstallWritesPathBReceipt asserts a verified-installer (Path B) install records a
// receipt carrying the tool's OWN designated uninstaller argv, so uninstall delegates rather than
// hand-rolling teardown. HOME is redirected so the store lands in a temp dir, not the real ~/.config.
func TestRecordVerifiedInstallWritesPathBReceipt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	nix := catalogTool(t, "nix")
	if err := recordVerifiedInstall(nix); err != nil {
		t.Fatalf("recordVerifiedInstall(nix): %v", err)
	}
	rust := catalogTool(t, "Rust")
	if err := recordVerifiedInstall(rust); err != nil {
		t.Fatalf("recordVerifiedInstall(Rust): %v", err)
	}

	rcPath, err := receipt.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	store, err := receipt.Load(rcPath)
	if err != nil {
		t.Fatal(err)
	}

	gotNix, ok := store.Get("nix")
	if !ok {
		t.Fatal("nix receipt not recorded")
	}
	if gotNix.Path != "B" {
		t.Fatalf("nix path want B, got %q", gotNix.Path)
	}
	if want := nix.Installer.Uninstall; len(gotNix.Uninstall) != len(want) || gotNix.Uninstall[0] != "/nix/nix-installer" {
		t.Fatalf("nix uninstall argv mismatch: got %v want %v", gotNix.Uninstall, want)
	}
	if gotNix.Version != "3.21.2" || gotNix.InstallerVersion != "3.21.2" {
		t.Fatalf("nix version mismatch: %+v", gotNix)
	}

	gotRust, ok := store.Get("Rust")
	if !ok {
		t.Fatal("Rust receipt not recorded")
	}
	if gotRust.Path != "B" || len(gotRust.Uninstall) != 4 || gotRust.Uninstall[1] != "self" {
		t.Fatalf("Rust uninstall argv mismatch: %+v", gotRust)
	}
}

// TestNoteUnmanagedRecordsForeignTool exercises the negative-provenance audit trail: a tool present on
// the box but NOT installed by safeslop (Docker via brew) is recorded as unmanaged, so a later uninstall
// can explain why it left it untouched.
func TestNoteUnmanagedRecordsForeignTool(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	rcPath, err := receipt.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	store, err := receipt.Load(rcPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.NoteUnmanaged("docker", "/opt/homebrew/bin/docker"); err != nil {
		t.Fatal(err)
	}
	reloaded, err := receipt.Load(rcPath)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Unmanaged()["docker"] != "/opt/homebrew/bin/docker" {
		t.Fatalf("docker not recorded as unmanaged: %v", reloaded.Unmanaged())
	}
}
