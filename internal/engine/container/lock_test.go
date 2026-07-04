package container

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWithRepoLockRunsBodyAndReleases(t *testing.T) {
	repo := t.TempDir()
	n := 0
	for i := 0; i < 2; i++ { // the second acquire proves the first released
		if err := withRepoLock(repo, func() error { n++; return nil }); err != nil {
			t.Fatal(err)
		}
	}
	if n != 2 {
		t.Fatalf("body ran %d times, want 2", n)
	}
	if _, err := os.Stat(filepath.Join(repo, ".safeslop", "lock")); err != nil {
		t.Fatalf("lock file missing: %v", err)
	}
}

func TestReconcileSweepsStaleMarkedDirs(t *testing.T) {
	repo := t.TempDir()
	stale := filepath.Join(repo, ".safeslop", "runtime", "old")
	fresh := filepath.Join(repo, ".safeslop", "runtime", "new")
	for _, d := range []string{stale, fresh} {
		os.MkdirAll(d, 0o755)
		os.WriteFile(filepath.Join(d, ".safeslop-stage"), nil, 0o600)
		os.WriteFile(filepath.Join(d, "secrets.env"), []byte("K=v"), 0o600)
	}
	old := time.Now().Add(-2 * time.Hour)
	os.Chtimes(stale, old, old)
	if err := Reconcile(context.Background(), repo, time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("stale staged dir (with secrets) was not swept")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatal("fresh staged dir wrongly swept")
	}
}

func TestWithBuildLockRunsBodyAndReleases(t *testing.T) {
	t.Setenv("SAFESLOP_STATE_DIR", t.TempDir())
	n := 0
	for i := 0; i < 2; i++ { // the second acquire proves the first released
		if err := withBuildLock("local/safeslop-tools:abc123def456", func() error { n++; return nil }); err != nil {
			t.Fatal(err)
		}
	}
	if n != 2 {
		t.Fatalf("body ran %d times, want 2", n)
	}
}

// ensureImage must skip the build when the image already exists — the core of the Bug B fix.
func TestEnsureImageBuildsOnceThenSkips(t *testing.T) {
	t.Setenv("SAFESLOP_STATE_DIR", t.TempDir())
	built := 0
	present := false
	exists := func() bool { return present }
	build := func() error { built++; present = true; return nil }
	if err := ensureImage("local/safeslop-tools:id", exists, build); err != nil {
		t.Fatal(err)
	}
	if err := ensureImage("local/safeslop-tools:id", exists, build); err != nil {
		t.Fatal(err)
	}
	if built != 1 {
		t.Fatalf("ensureImage built %d times, want 1 (second call must skip the existing image)", built)
	}
}
