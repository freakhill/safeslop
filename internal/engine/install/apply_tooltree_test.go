package install

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/receipt"
)

// TestApplyInstallsToolTree covers FormatToolTree (lima): the WHOLE tarball tree (bin/ + share/ +
// libexec/) must land under LibDir/<name> with bin/<name> symlinked into BinDir — because limactl
// resolves its guest agent + templates relative to its binary, so a bare binary cannot boot a VM.
func TestApplyInstallsToolTree(t *testing.T) {
	art := tgz(t, map[string]string{
		"bin/limactl":                       "#!/bin/sh\necho limactl\n",
		"share/lima/templates/default.yaml": "vmType: vz\n",
		"libexec/lima/lima-guestagent":      "agent\n",
	})
	url := "https://x/lima.tar.gz"
	lib := t.TempDir()
	bin := t.TempDir()
	rcPath := filepath.Join(t.TempDir(), "receipts.json")
	dirs := Dirs{BinDir: bin, AppDir: t.TempDir(), TmpDir: t.TempDir(), CacheDir: t.TempDir(), LibDir: lib, ReceiptPath: rcPath}

	res := Result{Actions: []Action{{
		Name: "limactl", Kind: ActionInstall, Desired: "2.1.3",
		Format: FormatToolTree, SHA256: sha(art), URL: url, Provenance: ProvenanceVendor,
	}}}
	if err := Apply(context.Background(), res, dirs, fakeFetcher{url: art}, nil); err != nil {
		t.Fatalf("Apply tool tree: %v", err)
	}

	// The tree resolves share/ relative to the binary — both must be present under LibDir/limactl.
	treeBin := filepath.Join(lib, "limactl", "bin", "limactl")
	treeShare := filepath.Join(lib, "limactl", "share", "lima", "templates", "default.yaml")
	for _, p := range []string{treeBin, treeShare} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("tool tree missing %s: %v", p, err)
		}
	}
	// BinDir/limactl must be a symlink into the tree (so it's on PATH but resolves ../share).
	link := filepath.Join(bin, "limactl")
	fi, err := os.Lstat(link)
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("BinDir/limactl must be a symlink, got %v (err %v)", fi.Mode(), err)
	}
	if tgt, _ := os.Readlink(link); tgt != treeBin {
		t.Fatalf("symlink target = %q, want %q", tgt, treeBin)
	}

	// Receipt records the tree dir (sha-less, like an .app) + the symlink, so uninstall removes both.
	store, err := receipt.Load(rcPath)
	if err != nil {
		t.Fatal(err)
	}
	e, ok := store.Get("limactl")
	if !ok || len(e.Files) != 2 {
		t.Fatalf("tool-tree receipt must record the tree dir + symlink, got %+v", e)
	}
	var sawDir, sawLink bool
	for _, f := range e.Files {
		if f.Path == filepath.Join(lib, "limactl") && f.SHA256 == "" && !f.Symlink {
			sawDir = true
		}
		if f.Path == link && f.Symlink {
			sawLink = true
		}
	}
	if !sawDir || !sawLink {
		t.Fatalf("receipt files wrong: %+v", e.Files)
	}
}
