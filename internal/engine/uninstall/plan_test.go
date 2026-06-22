package uninstall

import (
	"path/filepath"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/install"
	"github.com/freakhill/safeslop/internal/engine/receipt"
)

func seedStore(t *testing.T, entries ...receipt.Entry) *receipt.Store {
	t.Helper()
	s, err := receipt.Load(filepath.Join(t.TempDir(), "receipts.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if err := s.Record(e); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func TestBuildClassifiesAndListsUntouched(t *testing.T) {
	store := seedStore(t,
		receipt.Entry{Tool: "uv", Path: "A", Version: "0.11.23", Files: []receipt.File{{Path: "/u/.local/bin/uv", SHA256: "ab"}}},
		receipt.Entry{Tool: "nix", Path: "B", Version: "3.21.2", Uninstall: []string{"/nix/nix-installer", "uninstall"}, UninstallVerify: []string{"/usr/sbin/diskutil", "apfs", "list"}},
	)
	st := install.State{
		Toolchains: []install.Tool{{Name: "uv", Present: true}, {Name: "nix", Present: true}},
		Runtimes:   []install.Tool{{Name: "docker", Present: true, Path: "/opt/homebrew/bin/docker"}},
	}

	p, err := Build(store, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Items) != 2 {
		t.Fatalf("want 2 items, got %d", len(p.Items))
	}
	byTool := map[string]Item{}
	for _, it := range p.Items {
		byTool[it.Tool] = it
	}
	if byTool["uv"].Kind != RemovePathA || !byTool["uv"].Reversible {
		t.Fatalf("uv should be reversible Path A: %+v", byTool["uv"])
	}
	if byTool["nix"].Kind != DelegatePathB || byTool["nix"].Reversible {
		t.Fatalf("nix should be irreversible Path B: %+v", byTool["nix"])
	}
	if len(byTool["nix"].Delegate) == 0 || byTool["nix"].Delegate[0] != "/nix/nix-installer" {
		t.Fatalf("nix delegate not carried: %+v", byTool["nix"].Delegate)
	}
	if !p.HasIrreversible() {
		t.Fatal("plan with a Path B item must report HasIrreversible")
	}
	// docker is present but unreceipted → must be Untouched, never an Item.
	var dockerUntouched bool
	for _, u := range p.Untouched {
		if u.Tool == "docker" {
			dockerUntouched = true
		}
	}
	if !dockerUntouched {
		t.Fatalf("docker should be listed untouched, got %+v", p.Untouched)
	}
	if _, isItem := byTool["docker"]; isItem {
		t.Fatal("docker must never be an uninstall Item")
	}
}

func TestBuildNamedToolWithoutReceiptIsUntouched(t *testing.T) {
	store := seedStore(t, receipt.Entry{Tool: "uv", Path: "A"})
	p, err := Build(store, install.State{}, []string{"ripgrep"})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Items) != 0 {
		t.Fatalf("ripgrep has no receipt — nothing to remove, got %d items", len(p.Items))
	}
	if len(p.Untouched) != 1 || p.Untouched[0].Tool != "ripgrep" {
		t.Fatalf("ripgrep should be reported untouched, got %+v", p.Untouched)
	}
}

func TestBuildAllReversibleWhenOnlyPathA(t *testing.T) {
	store := seedStore(t, receipt.Entry{Tool: "uv", Path: "A"}, receipt.Entry{Tool: "bun", Path: "A"})
	p, err := Build(store, install.State{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Reversible() {
		t.Fatal("a plan of only Path A items must be fully reversible")
	}
}
