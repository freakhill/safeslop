package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/freakhill/agentic_tactical_boots/internal/engine/exec"
)

func TestProfileContainsExpectedDirectives(t *testing.T) {
	p := Profile("/Users/x/repo", "deny")
	for _, want := range []string{
		"(version 1)",
		`(import "system.sb")`,
		`(allow file-read* (subpath "/Users/x/repo"))`,
		`(allow file-write* (subpath "/Users/x/repo"))`,
		`(allow file-write* (subpath "/private/tmp"))`,
		"(deny network*)",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("profile missing %q\n---\n%s", want, p)
		}
	}
}

func TestProfileNetworkAllow(t *testing.T) {
	p := Profile("/w", "allow")
	if !strings.Contains(p, "(allow network*)") {
		t.Errorf("network=allow profile missing (allow network*)")
	}
	if strings.Contains(p, "(deny network*)") {
		t.Errorf("network=allow profile should not contain (deny network*)")
	}
}

func TestProfileEscapesQuotes(t *testing.T) {
	p := Profile(`/tmp/a"b\c`, "deny")
	if !strings.Contains(p, `/tmp/a\"b\\c`) {
		t.Errorf("profile did not escape quotes/backslashes in workspace path:\n%s", p)
	}
}

// --- darwin-only launch behavior (skipped elsewhere; the Go CI runs on macOS) ---

func TestLaunchRunsCommandOnDarwin(t *testing.T) {
	if !Available() {
		t.Skip("sandbox-exec unavailable (not macOS)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	code, err := Launch(ctx, exec.LaunchSpec{
		Argv:   []string{"/usr/bin/true"},
		Stdout: &strings.Builder{},
	}, t.TempDir(), "deny")
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (profile rejected or command failed)", code)
	}
}

func TestLaunchAllowsWorkspaceWriteOnDarwin(t *testing.T) {
	if !Available() {
		t.Skip("sandbox-exec unavailable (not macOS)")
	}
	ws := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var out strings.Builder
	code, err := Launch(ctx, exec.LaunchSpec{
		Argv:   []string{"/bin/sh", "-c", "echo ok > " + filepath.Join(ws, "probe")},
		Stdout: &out,
		Stderr: &out,
	}, ws, "deny")
	if err != nil || code != 0 {
		t.Fatalf("workspace write failed: code=%d err=%v out=%q", code, err, out.String())
	}
	if _, err := os.Stat(filepath.Join(ws, "probe")); err != nil {
		t.Fatalf("expected probe file written inside workspace: %v", err)
	}
}

func TestLaunchDeniesWriteOutsideWorkspaceOnDarwin(t *testing.T) {
	if !Available() {
		t.Skip("sandbox-exec unavailable (not macOS)")
	}
	ws := t.TempDir()
	// A path outside workspace and outside the allowed temp dirs: a sibling of
	// the workspace under the same parent.
	outside := filepath.Join(filepath.Dir(ws), "slop_outside_probe")
	defer os.Remove(outside)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	code, _ := Launch(ctx, exec.LaunchSpec{
		Argv:   []string{"/bin/sh", "-c", "echo x > " + outside},
		Stdout: &strings.Builder{},
		Stderr: &strings.Builder{},
	}, ws, "deny")
	if code == 0 {
		t.Fatalf("write outside workspace unexpectedly succeeded (confinement broken)")
	}
	if _, err := os.Stat(outside); err == nil {
		t.Fatalf("file was written outside the workspace — confinement failed")
	}
}
