package install

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/receipt"
)

// TestApplyWritesPathAReceipt drives Apply for a raw-binary tool and asserts it records a verifiable
// Path A receipt: the placed binary's path + a sha matching the bytes, plus the carried Provenance and
// SelfUpdating flags. Uses the simplest installable format (raw binary) to avoid archive fixtures.
func TestApplyWritesPathAReceipt(t *testing.T) {
	art := []byte("#!/bin/sh\necho claude\n")
	url := "https://x/claude"
	res := Result{Actions: []Action{{
		Name: "claude", Kind: ActionInstall, Desired: "2.1.176",
		Format: FormatRawBinary, SHA256: sha(art), URL: url,
		Provenance: ProvenanceVendor, SelfUpdating: true,
	}}}
	rcPath := filepath.Join(t.TempDir(), "receipts.json")
	dirs := Dirs{BinDir: t.TempDir(), AppDir: t.TempDir(), TmpDir: t.TempDir(), ReceiptPath: rcPath}

	if err := Apply(context.Background(), res, dirs, fakeFetcher{url: art}, nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	store, err := receipt.Load(rcPath)
	if err != nil {
		t.Fatalf("load receipt: %v", err)
	}
	e, ok := store.Get("claude")
	if !ok {
		t.Fatal("no receipt recorded for claude")
	}
	if e.Path != "A" {
		t.Fatalf("path want A, got %q", e.Path)
	}
	if e.Version != "2.1.176" || e.Provenance != ProvenanceVendor || !e.SelfUpdating {
		t.Fatalf("receipt metadata mismatch: %+v", e)
	}
	if len(e.Files) != 1 {
		t.Fatalf("want 1 placed file, got %d (%+v)", len(e.Files), e.Files)
	}
	wantPath := filepath.Join(dirs.BinDir, "claude")
	if e.Files[0].Path != wantPath {
		t.Fatalf("placed path want %s, got %s", wantPath, e.Files[0].Path)
	}
	if e.Files[0].SHA256 != sha(art) {
		t.Fatalf("placed sha want %s, got %s", sha(art), e.Files[0].SHA256)
	}
}

// TestApplyOKActionWritesNoReceipt confirms an already-satisfied (ActionOK) tool is skipped — not
// recorded — so the receipt only reflects what Apply actually placed.
func TestApplyOKActionWritesNoReceipt(t *testing.T) {
	rcPath := filepath.Join(t.TempDir(), "receipts.json")
	dirs := Dirs{BinDir: t.TempDir(), AppDir: t.TempDir(), TmpDir: t.TempDir(), ReceiptPath: rcPath}
	res := Result{Actions: []Action{{Name: "mise", Kind: ActionOK, Desired: "2026.6.11"}}}

	if err := Apply(context.Background(), res, dirs, fakeFetcher{}, nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	store, err := receipt.Load(rcPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Get("mise"); ok {
		t.Fatal("ActionOK tool should not be recorded in the receipt")
	}
}
