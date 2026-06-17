package container

import (
	"context"
	"os"
	"path/filepath"
	"time"
)

// Reconcile reclaims state a crashed prior run may have left: staged dirs older than maxAge
// carrying the .slop-stage marker (wiping any leftover secrets.env). Safe to call on every
// run; idempotent. repo is the workspace root. Orphaned-squid reaping rides the Docker path
// (Up/Down) added in a later task.
func Reconcile(ctx context.Context, repo string, maxAge time.Duration) error {
	_ = ctx
	runtimeRoot := filepath.Join(repo, ".slop", "runtime")
	entries, _ := os.ReadDir(runtimeRoot)
	for _, e := range entries {
		dir := filepath.Join(runtimeRoot, e.Name())
		if _, err := os.Stat(filepath.Join(dir, ".slop-stage")); err != nil {
			continue // not a slop staging dir
		}
		if fi, err := os.Stat(dir); err == nil && time.Since(fi.ModTime()) > maxAge {
			_ = os.RemoveAll(dir) // wipes any leftover secrets.env
		}
	}
	return nil
}
