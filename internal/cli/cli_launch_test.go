package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLaunchWorkspaceFromConfigDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "safeslop.cue"), []byte("package safeslop\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// passing the directory resolves to that directory (canonicalized).
	ws, err := launchWorkspace(dir)
	if err != nil {
		t.Fatalf("launchWorkspace(dir): %v", err)
	}
	real, _ := filepath.EvalSymlinks(dir)
	if ws != real {
		t.Errorf("ws = %q, want canonical %q", ws, real)
	}
}

func TestLaunchWorkspaceFromConfigFile(t *testing.T) {
	dir := t.TempDir()
	cue := filepath.Join(dir, "safeslop.cue")
	if err := os.WriteFile(cue, []byte("package safeslop\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// passing the file resolves to its parent directory.
	ws, err := launchWorkspace(cue)
	if err != nil {
		t.Fatalf("launchWorkspace(file): %v", err)
	}
	real, _ := filepath.EvalSymlinks(dir)
	if ws != real {
		t.Errorf("ws = %q, want %q", ws, real)
	}
}

func TestLaunchWorkspaceMissingConfigErrors(t *testing.T) {
	if _, err := launchWorkspace(filepath.Join(t.TempDir(), "no-such-dir")); err == nil {
		t.Fatal("launchWorkspace must error when no safeslop.cue is found")
	}
}

func TestLaunchWorkspaceEmptyUsesCwd(t *testing.T) {
	ws, err := launchWorkspace("")
	if err != nil {
		t.Fatalf("launchWorkspace(\"\"): %v", err)
	}
	cwd, _ := os.Getwd()
	if ws != cwd {
		t.Errorf("empty config: ws = %q, want cwd %q", ws, cwd)
	}
}
