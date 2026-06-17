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
	if _, err := os.Stat(filepath.Join(repo, ".slop", "lock")); err != nil {
		t.Fatalf("lock file missing: %v", err)
	}
}

func TestReconcileSweepsStaleMarkedDirs(t *testing.T) {
	repo := t.TempDir()
	stale := filepath.Join(repo, ".slop", "runtime", "old")
	fresh := filepath.Join(repo, ".slop", "runtime", "new")
	for _, d := range []string{stale, fresh} {
		os.MkdirAll(d, 0o755)
		os.WriteFile(filepath.Join(d, ".slop-stage"), nil, 0o600)
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
