package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProfilePresetsEnvelope(t *testing.T) {
	out, err := runRootForTest(t, t.TempDir(), "profile", "presets", "--output", "json")
	if err != nil {
		t.Fatalf("profile presets --output json: %v", err)
	}
	env := parseEnvelopeForTest(t, out)
	if !env.OK {
		t.Fatalf("presets returned error envelope: %+v", env.Errors)
	}
	presets, ok := env.Data["presets"].([]any)
	if !ok {
		t.Fatalf("data.presets is not an array: %#v", env.Data)
	}
	if len(presets) != 5 {
		t.Fatalf("got %d presets, want 5", len(presets))
	}
	seen := map[string]bool{}
	var names []string
	for _, p := range presets {
		m, ok := p.(map[string]any)
		if !ok {
			t.Fatalf("preset entry is not an object: %#v", p)
		}
		name, ok := m["name"].(string)
		if !ok || name == "" {
			t.Fatalf("preset carries empty/non-string name: %#v", m)
		}
		if seen[name] {
			t.Fatalf("duplicate preset name %q", name)
		}
		seen[name] = true
		names = append(names, name)
		if m["cue"] == "" {
			t.Fatalf("preset %v carries empty cue", m["name"])
		}
		if m["description"] == "" {
			t.Fatalf("preset %v carries empty description", m["name"])
		}
	}
	wantNames := []string{
		"claude-container-allowlist",
		"claude-host-unconfined",
		"claude-subscription-container",
		"pi-container-allowlist",
		"shell-container",
	}
	if len(names) != len(wantNames) {
		t.Fatalf("got names %v, want %v", names, wantNames)
	}
	for i, want := range wantNames {
		if names[i] != want {
			t.Fatalf("preset names/order = %v, want %v", names, wantNames)
		}
	}
}

func TestProfileListEnvelope(t *testing.T) {
	dir := t.TempDir()
	cue := "package safeslop\n\nsafeslop: {\n\tversion: 1\n\tprofiles: {\n\t\treview: {agent: \"claude\", environment: \"container\", network: \"deny\"}\n\t}\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "safeslop.cue"), []byte(cue), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runRootForTest(t, dir, "profile", "list", "--output", "json")
	if err != nil {
		t.Fatalf("profile list --output json: %v", err)
	}
	env := parseEnvelopeForTest(t, out)
	if !env.OK {
		t.Fatalf("list returned error envelope: %+v", env.Errors)
	}
	profiles, ok := env.Data["profiles"].(map[string]any)
	if !ok {
		t.Fatalf("data.profiles is not an object: %#v", env.Data)
	}
	review, ok := profiles["review"].(map[string]any)
	if !ok {
		t.Fatalf("profile 'review' missing: %#v", profiles)
	}
	if review["agent"] != "claude" || review["environment"] != "container" || review["network"] != "deny" {
		t.Fatalf("review profile fields wrong: %#v", review)
	}
}

func TestProfileListRequiresOutputJSON(t *testing.T) {
	// The --output guard fires before any config lookup, so no safeslop.cue is needed.
	if _, err := runRootForTest(t, t.TempDir(), "profile", "list"); err == nil {
		t.Fatal("profile list without --output json should error")
	}
}
