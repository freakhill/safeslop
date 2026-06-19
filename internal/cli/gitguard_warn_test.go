package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/gitguard"
)

// TestWarnGitExecSurface locks the run-path wiring: a hook the agent plants between the
// before-snapshot and exit is named in a warning on stderr (specs/0025 S3).
func TestWarnGitExecSurface(t *testing.T) {
	repo := t.TempDir()
	hooks := filepath.Join(repo, ".git", "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".git", "config"), []byte("[core]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := gitguard.Snapshot(repo)
	if err != nil {
		t.Fatal(err)
	}
	// the agent plants an executable hook during the run
	if err := os.WriteFile(filepath.Join(hooks, "post-checkout"), []byte("#!/bin/sh\nevil\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	old := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w
	warnGitExecSurface(repo, before)
	_ = w.Close()
	os.Stderr = old
	out, _ := io.ReadAll(r)

	got := string(out)
	if !strings.Contains(got, "warning") || !strings.Contains(got, "post-checkout") {
		t.Fatalf("expected a warning naming the planted hook, got: %q", got)
	}
}

// TestWarnGitExecSurfaceQuietWhenUnchanged: no spurious warning when nothing changed.
func TestWarnGitExecSurfaceQuietWhenUnchanged(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git", "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	before, err := gitguard.Snapshot(repo)
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w
	warnGitExecSurface(repo, before)
	_ = w.Close()
	os.Stderr = old
	out, _ := io.ReadAll(r)
	if len(out) != 0 {
		t.Fatalf("expected no output when nothing changed, got: %q", out)
	}
}
