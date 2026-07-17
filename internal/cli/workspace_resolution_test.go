package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRunDryRunResolvesRelativeWorkspaceFromPolicyDirectory(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "project")
	invocation := filepath.Join(root, "nested", "caller")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(invocation, 0o700); err != nil {
		t.Fatal(err)
	}
	policy := `package safeslop

safeslop: {
	version: 1
	profiles: {
		review: {
			agent: "fish"
			environment: "container"
			workspace: "project"
		}
	}
}
`
	if err := os.WriteFile(filepath.Join(root, "safeslop.cue"), []byte(policy), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := runRootForTest(t, invocation, "--json", "run", "review", "--dry-run")
	if err != nil {
		t.Fatalf("run --dry-run: %v\n%s", err, out)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode dry-run: %v\n%s", err, out)
	}
	canonicalWorkspace, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if got["workspace"] != canonicalWorkspace {
		t.Fatalf("dry-run workspace = %#v, want canonical policy-relative %q", got["workspace"], canonicalWorkspace)
	}
}

func TestRunDryRunRejectsMissingWorkspace(t *testing.T) {
	root := t.TempDir()
	policy := `package safeslop
safeslop: {version: 1, profiles: {review: {agent: "fish", environment: "container", workspace: "missing"}}}
`
	if err := os.WriteFile(filepath.Join(root, "safeslop.cue"), []byte(policy), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := runRootForTest(t, root, "--json", "run", "review", "--dry-run"); err == nil {
		t.Fatalf("run --dry-run accepted a missing workspace: %s", out)
	}
}

func TestSessionCreateProfileStoresCanonicalSymlinkResolvedWorkspace(t *testing.T) {
	root := t.TempDir()
	realWorkspace := filepath.Join(root, "real-workspace")
	if err := os.Mkdir(realWorkspace, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real-workspace", filepath.Join(root, "workspace-link")); err != nil {
		t.Fatal(err)
	}
	policy := `package safeslop
safeslop: {version: 1, profiles: {review: {agent: "fish", environment: "container", workspace: "workspace-link"}}}
`
	if err := os.WriteFile(filepath.Join(root, "safeslop.cue"), []byte(policy), 0o600); err != nil {
		t.Fatal(err)
	}
	trustFixtureForTest(t, root)
	out, err := runRootForTest(t, root, "session", "create", "--profile", "review", "--output", "json")
	if err != nil {
		t.Fatalf("session create: %v\n%s", err, out)
	}
	var envelope struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatalf("decode session create: %v\n%s", err, out)
	}
	canonicalWorkspace, err := filepath.EvalSymlinks(realWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	if envelope.Data["workspace"] != canonicalWorkspace {
		t.Fatalf("stored workspace = %#v, want symlink-resolved %q", envelope.Data["workspace"], canonicalWorkspace)
	}
}
