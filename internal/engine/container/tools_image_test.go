package container

import (
	"strings"
	"testing"
)

func TestNodePackageInstallsXZHelperOnlyWhenNodeEnabled(t *testing.T) {
	b, err := readAsset("Dockerfile.agent.tools")
	if err != nil {
		t.Fatal(err)
	}
	df := string(b)
	// Node ships official .tar.xz archives. The golden base intentionally excludes xz,
	// so the guarded node block must install xz-utils before tar -xJf. Keep it in the
	// package block (not the universal base), so shell-only profiles stay lean.
	idx := strings.Index(df, "ARG ENABLE_NODE=false")
	if idx < 0 {
		t.Fatal("missing node package block")
	}
	next := strings.Index(df[idx+1:], "# --- claude-code")
	if next < 0 {
		t.Fatal("missing claude-code block after node block")
	}
	nodeBlock := df[idx : idx+1+next]
	for _, want := range []string{"apt-get update", "xz-utils", "tar -xJf"} {
		if !strings.Contains(nodeBlock, want) {
			t.Fatalf("node block missing %q needed for .tar.xz install", want)
		}
	}
}
