package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLockWritesRootLockfile(t *testing.T) {
	dir := t.TempDir()
	cue := `package safeslop

safeslop: {
	version: 1
	profiles: {
		review: {agent: "claude", environment: "container", network: "deny", packages: ["pnpm"]}
	}
}
`
	if err := os.WriteFile(filepath.Join(dir, "safeslop.cue"), []byte(cue), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runRootForTest(t, dir, "lock", "review", "--output", "json")
	if err != nil {
		t.Fatalf("lock review --output json: %v", err)
	}
	env := parseEnvelopeForTest(t, out)
	if !env.OK {
		t.Fatalf("lock returned error envelope: %+v", env.Errors)
	}
	path, _ := env.Data["path"].(string)
	wantPath, err := filepath.EvalSymlinks(filepath.Join(dir, "safeslop.lock.json"))
	if err != nil {
		wantPath = filepath.Join(dir, "safeslop.lock.json")
	}
	gotPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		gotPath = path
	}
	if gotPath != wantPath {
		t.Fatalf("lock path = %q, want repo root safeslop.lock.json", path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("lockfile missing: %v", err)
	}
	var lock map[string]any
	if err := json.Unmarshal(b, &lock); err != nil {
		t.Fatalf("lockfile JSON invalid: %v\n%s", err, b)
	}
	if rid, _ := lock["recipeID"].(string); len(rid) != 12 {
		t.Fatalf("recipeID = %q, want 12 hex chars", rid)
	}
	if lock["agent"] != "claude" || lock["base"] == "" {
		t.Fatalf("lockfile provenance wrong: %#v", lock)
	}
	packages, ok := lock["packages"].([]any)
	if !ok {
		t.Fatalf("lock packages malformed: %#v", lock["packages"])
	}
	for _, want := range []string{"claude-code", "node", "pnpm"} {
		if !stringSliceAnyContains(packages, want) {
			t.Fatalf("lock packages missing %q: %#v", want, packages)
		}
	}
	versions, ok := lock["versions"].(map[string]any)
	if !ok || versions["node"] == "" || versions["claude-code"] == "" || versions["pnpm"] == "" {
		t.Fatalf("lock versions wrong: %#v", lock["versions"])
	}
	if env.Data["recipeID"] != lock["recipeID"] {
		t.Fatalf("envelope recipeID %v != lock recipeID %v", env.Data["recipeID"], lock["recipeID"])
	}
}

func TestLockSelectsDefaultProfile(t *testing.T) {
	dir := t.TempDir()
	cue := `package safeslop

safeslop: profiles: default: {agent: "fish", environment: "container", network: "deny"}
`
	if err := os.WriteFile(filepath.Join(dir, "safeslop.cue"), []byte(cue), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runRootForTest(t, dir, "lock", "--output", "json"); err != nil {
		t.Fatalf("lock default profile: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "safeslop.lock.json")); err != nil {
		t.Fatalf("lockfile missing: %v", err)
	}
}

func TestLockRequiresOutputJSON(t *testing.T) {
	if _, err := runRootForTest(t, t.TempDir(), "lock"); err == nil {
		t.Fatal("lock without --output json should error")
	}
}
