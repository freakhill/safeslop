package receipt

import (
	"os"
	"path/filepath"
	"testing"
)

// storePath returns a receipts.json path under a temp dir whose parent does NOT yet exist, so the test
// also exercises persist()'s MkdirAll + permission setting.
func storePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "safeslop", "receipts.json")
}

func TestLoadMissingFileIsEmptyStore(t *testing.T) {
	s, err := Load(storePath(t))
	if err != nil {
		t.Fatalf("Load of missing file should be empty store, got err: %v", err)
	}
	if len(s.All()) != 0 {
		t.Fatalf("expected empty store, got %d entries", len(s.All()))
	}
	if _, ok := s.Get("nope"); ok {
		t.Fatal("Get on empty store should report not-found")
	}
}

func TestRecordLoadRoundTrip(t *testing.T) {
	path := storePath(t)
	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	pathA := Entry{
		Tool: "uv", Path: "A", Version: "0.11.23", Provenance: "vendor", SelfUpdating: false,
		Files: []File{
			{Path: "/u/.local/bin/uv", SHA256: "abc123"},
			{Path: "/u/.local/bin/uvx", Symlink: true},
		},
	}
	pathB := Entry{
		Tool: "nix", Path: "B", Version: "3.21.2", InstallerVersion: "3.21.2",
		Uninstall: []string{"/nix/nix-installer", "uninstall", "--no-confirm"},
	}
	if err := s.Record(pathA); err != nil {
		t.Fatal(err)
	}
	if err := s.Record(pathB); err != nil {
		t.Fatal(err)
	}

	// Reload from disk to prove persistence, not just in-memory state.
	s2, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := s2.Get("uv")
	if !ok {
		t.Fatal("uv entry missing after reload")
	}
	if got.Path != "A" || got.Version != "0.11.23" || got.Provenance != "vendor" {
		t.Fatalf("uv entry round-trip mismatch: %+v", got)
	}
	if len(got.Files) != 2 || got.Files[0].SHA256 != "abc123" || !got.Files[1].Symlink {
		t.Fatalf("uv files round-trip mismatch: %+v", got.Files)
	}
	gotB, ok := s2.Get("nix")
	if !ok {
		t.Fatal("nix entry missing after reload")
	}
	if gotB.Path != "B" || len(gotB.Uninstall) != 3 || gotB.Uninstall[0] != "/nix/nix-installer" {
		t.Fatalf("nix entry round-trip mismatch: %+v", gotB)
	}
	if all := s2.All(); len(all) != 2 || all[0].Tool != "nix" || all[1].Tool != "uv" {
		t.Fatalf("All() should be sorted by tool name, got %v", toolNames(all))
	}
}

func TestRecordUpserts(t *testing.T) {
	path := storePath(t)
	s, _ := Load(path)
	if err := s.Record(Entry{Tool: "uv", Path: "A", Version: "0.11.23"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Record(Entry{Tool: "uv", Path: "A", Version: "0.12.0"}); err != nil {
		t.Fatal(err)
	}
	s2, _ := Load(path)
	all := s2.All()
	if len(all) != 1 {
		t.Fatalf("Record of same tool should upsert, got %d entries", len(all))
	}
	if all[0].Version != "0.12.0" {
		t.Fatalf("upsert should keep the latest version, got %q", all[0].Version)
	}
}

func TestRemove(t *testing.T) {
	path := storePath(t)
	s, _ := Load(path)
	if err := s.Remove("absent"); err != nil {
		t.Fatalf("Remove of absent tool should be nil, got %v", err)
	}
	if err := s.Record(Entry{Tool: "uv", Path: "A"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove("uv"); err != nil {
		t.Fatal(err)
	}
	s2, _ := Load(path)
	if _, ok := s2.Get("uv"); ok {
		t.Fatal("uv should be gone after Remove + reload")
	}
}

func TestFilePermissions(t *testing.T) {
	path := storePath(t)
	s, _ := Load(path)
	if err := s.Record(Entry{Tool: "uv", Path: "A"}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("receipt file should be 0600, got %o", perm)
	}
	di, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if perm := di.Mode().Perm(); perm != 0o700 {
		t.Fatalf("receipt dir should be 0700, got %o", perm)
	}
}

func TestNoteUnmanagedRoundTrip(t *testing.T) {
	path := storePath(t)
	s, _ := Load(path)
	if err := s.NoteUnmanaged("docker", "/opt/homebrew/bin/docker"); err != nil {
		t.Fatal(err)
	}
	s2, _ := Load(path)
	um := s2.Unmanaged()
	if um["docker"] != "/opt/homebrew/bin/docker" {
		t.Fatalf("unmanaged round-trip mismatch: %v", um)
	}
	// Unmanaged() returns a copy — mutating it must not affect the store.
	um["docker"] = "tampered"
	if s2.Unmanaged()["docker"] != "/opt/homebrew/bin/docker" {
		t.Fatal("Unmanaged() should return a defensive copy")
	}
}

func toolNames(es []Entry) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Tool
	}
	return out
}
