package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/sandbox"
)

const resolverCue = `package slop
slop: {
	version: 1
	profiles: {
		h: {agent: "claude", environment: "host", network: "deny"}
		s: {agent: "claude", environment: "sandbox", network: "deny"}
		c: {agent: "claude", environment: "container", network: "deny"}
		v: {agent: "claude", environment: "vm", network: "allow"}
	}
}
`

func writeResolverCue(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "slop.cue")
	if err := os.WriteFile(path, []byte(resolverCue), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestResolveSessionHostAndSandbox(t *testing.T) {
	path := writeResolverCue(t)

	h, err := resolveSession("h", path)
	if err != nil {
		t.Fatalf("host resolve: %v", err)
	}
	if len(h.Argv) == 0 || h.Argv[0] != "claude" {
		t.Fatalf("host argv = %v, want it to start with claude", h.Argv)
	}
	if h.OnClose != nil {
		t.Fatal("host session needs no cleanup")
	}

	s, err := resolveSession("s", path)
	if err != nil {
		t.Fatalf("sandbox resolve: %v", err)
	}
	if len(s.Argv) == 0 || s.Argv[0] != sandbox.SandboxExecPath {
		t.Fatalf("sandbox argv = %v, want it to start with %s", s.Argv, sandbox.SandboxExecPath)
	}
	if s.OnClose == nil {
		t.Fatal("sandbox session must carry a cleanup (temp profile removal)")
	}
	s.OnClose() // must not panic
}

func TestResolveSessionContainerVMErrorWhenToolingAbsent(t *testing.T) {
	path := writeResolverCue(t)
	t.Chdir(t.TempDir()) // any cockpit-* stage dir lands under a throwaway cwd, not the repo
	t.Setenv("PATH", "") // docker + tart unavailable

	// The error must come from the real provisioning path (PrepareSession -> "docker"/"tart"
	// unavailable), not the pre-SP7c-2 "is SP7c-2" sentinel — that's what makes this fail first.
	if _, err := resolveSession("c", path); err == nil || !strings.Contains(err.Error(), "docker") {
		t.Fatalf("container resolve must reach PrepareSession and fail on docker availability, got %v", err)
	}
	if _, err := resolveSession("v", path); err == nil || !strings.Contains(err.Error(), "tart") {
		t.Fatalf("vm resolve must reach PrepareSession and fail on tart availability, got %v", err)
	}
}
