package uninstall

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestTrashMoveRollbackRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	bin := filepath.Join(home, ".local", "bin", "uv")
	writeFile(t, bin, "uv-binary")

	stamp := "20260622T120000Z"
	if _, err := moveToTrash([]string{bin}, stamp); err != nil {
		t.Fatalf("moveToTrash: %v", err)
	}
	if _, err := os.Lstat(bin); !os.IsNotExist(err) {
		t.Fatalf("original should be gone after trashing, stat err=%v", err)
	}

	restored, err := Rollback(stamp)
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if len(restored) != 1 || restored[0] != bin {
		t.Fatalf("rollback should restore the bin path, got %v", restored)
	}
	got, err := os.ReadFile(bin)
	if err != nil || string(got) != "uv-binary" {
		t.Fatalf("restored content mismatch: %q err=%v", got, err)
	}
}

func TestRollbackRefusesToClobber(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	bin := filepath.Join(home, ".local", "bin", "uv")
	writeFile(t, bin, "v1")

	stamp := "20260622T120000Z"
	if _, err := moveToTrash([]string{bin}, stamp); err != nil {
		t.Fatal(err)
	}
	// The path exists again (a reinstall) — rollback must refuse, not overwrite it.
	writeFile(t, bin, "v2-new")
	if _, err := Rollback(stamp); err == nil {
		t.Fatal("rollback should refuse to clobber an existing file")
	}
	if got, _ := os.ReadFile(bin); string(got) != "v2-new" {
		t.Fatalf("existing file must be untouched, got %q", got)
	}
}

func TestPruneByTTL(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	bin := filepath.Join(home, ".local", "bin", "uv")
	writeFile(t, bin, "x")
	stamp := "20260101T000000Z" // old
	if _, err := moveToTrash([]string{bin}, stamp); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 6, 22, 0, 0, 0, 0, time.UTC)
	n, err := Prune(7*24*time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("want 1 pruned stamp, got %d", n)
	}
	base, _ := TrashDir()
	if _, err := os.Stat(filepath.Join(base, stamp)); !os.IsNotExist(err) {
		t.Fatal("pruned stamp dir should be gone")
	}
}
