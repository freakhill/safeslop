package install

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// fakeTool writes a stub executable that prints `out` (so `<tool> --version` is probeable).
func fakeTool(t *testing.T, dir, name, out string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\necho '"+out+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestStatusProbesToolsAndSelf(t *testing.T) {
	bin := t.TempDir()
	fakeTool(t, bin, "mise", "2026.6.0")
	fakeTool(t, bin, "docker", "Docker version 29.4.0")
	fakeTool(t, bin, "safeslop", "safeslop version test")
	t.Setenv("PATH", bin) // only our stubs are visible

	st := Status(context.Background(), "v1.2.3")

	if st.Self.Version != "v1.2.3" {
		t.Fatalf("self version = %q, want v1.2.3", st.Self.Version)
	}
	if !st.Self.OnPath {
		t.Fatal("self should be detected on PATH (safeslop stub present)")
	}
	mise := find(st.Toolchains, "mise")
	if mise == nil || !mise.Present || mise.Version == "" {
		t.Fatalf("mise should be present with a version: %+v", mise)
	}
	docker := find(st.Runtimes, "docker")
	if docker == nil || !docker.Present {
		t.Fatalf("docker runtime should be present: %+v", docker)
	}
	if nix := find(st.Toolchains, "nix"); nix == nil || nix.Present {
		t.Fatalf("nix should be absent (no stub): %+v", nix)
	}
}

func find(tools []Tool, name string) *Tool {
	for i := range tools {
		if tools[i].Name == name {
			return &tools[i]
		}
	}
	return nil
}
