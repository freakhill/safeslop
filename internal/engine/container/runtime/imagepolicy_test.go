package runtime

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/install"
	"github.com/freakhill/safeslop/internal/engine/receipt"
)

func TestRejectsMutableTag(t *testing.T) {
	mutable := []string{"node:latest", "ubuntu", "python:3.12", "registry.io/foo:v1"}
	for _, r := range mutable {
		if !rejectsMutableTag(r) {
			t.Errorf("%q is a mutable tag and must be rejected", r)
		}
		if _, err := RewriteOrReject(r); err == nil {
			t.Errorf("RewriteOrReject(%q) must error", r)
		}
	}
	pinned := "ubuntu@sha256:" + "abc123def456"
	if rejectsMutableTag(pinned) {
		t.Errorf("%q is digest-pinned and must pass", pinned)
	}
	if got, err := RewriteOrReject(pinned); err != nil || got != pinned {
		t.Errorf("RewriteOrReject(%q) = (%q,%v), want it to pass", pinned, got, err)
	}
}

func TestCosignPolicyDefaultsReject(t *testing.T) {
	b, err := cosignPolicyJSON([]string{"docker.io"})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("policy.json must be valid JSON: %v", err)
	}
	def, ok := m["default"].([]any)
	if !ok || len(def) == 0 {
		t.Fatalf("policy.json missing a default rule: %v", m)
	}
	rule := def[0].(map[string]any)
	if rule["type"] != "reject" {
		t.Fatalf("default rule must be reject (fail-closed), got %v", rule["type"])
	}
}

func TestUnmanagedRuntimesRecorded(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	st := install.State{Runtimes: []install.Tool{
		{Name: "docker", Present: true, Path: "/opt/homebrew/bin/docker"},
		{Name: "tart", Present: true},
	}}
	um := unmanagedRuntimes(st)
	if um["docker"] != "/opt/homebrew/bin/docker" {
		t.Fatalf("docker must be reported unmanaged, got %v", um)
	}
	if _, isTart := um["tart"]; isTart {
		t.Fatal("tart is safeslop-managed and must NOT be in the unmanaged set")
	}

	rcPath := filepath.Join(t.TempDir(), "r.json")
	store, _ := receipt.Load(rcPath)
	if err := noteUnmanaged(store, st); err != nil {
		t.Fatal(err)
	}
	reloaded, _ := receipt.Load(rcPath)
	if reloaded.Unmanaged()["docker"] != "/opt/homebrew/bin/docker" {
		t.Fatalf("docker not recorded as unmanaged: %v", reloaded.Unmanaged())
	}
}
