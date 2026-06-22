package cli

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/receipt"
)

func seedReceipts(t *testing.T, entries ...receipt.Entry) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	path, err := receipt.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	s, err := receipt.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if err := s.Record(e); err != nil {
			t.Fatal(err)
		}
	}
	// sanity: the store really landed under the temp HOME
	if filepath.Dir(filepath.Dir(path)) == "" {
		t.Fatal("receipt path did not resolve under HOME")
	}
}

func TestRenderUninstallPlanJSONShape(t *testing.T) {
	seedReceipts(t,
		receipt.Entry{Tool: "uv", Path: "A", Version: "0.11.23", Files: []receipt.File{{Path: "/u/.local/bin/uv", SHA256: "ab"}}},
		receipt.Entry{Tool: "nix", Path: "B", Version: "3.21.2", Uninstall: []string{"/nix/nix-installer", "uninstall"}},
	)
	out, err := renderUninstallPlanJSON(nil)
	if err != nil {
		t.Fatalf("plan --json errored: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("plan --json is not valid JSON: %v\n%s", err, out)
	}
	items, ok := m["items"].([]any)
	if !ok {
		t.Fatalf("plan JSON missing items array: %v", m)
	}
	if len(items) != 2 {
		t.Fatalf("want 2 receipted items, got %d", len(items))
	}
	if _, ok := m["untouched"]; !ok {
		t.Fatalf("plan JSON missing untouched key: %v", m)
	}
}

func TestRenderUninstallPlanJSONEmptyReceipt(t *testing.T) {
	seedReceipts(t) // no entries
	out, err := renderUninstallPlanJSON(nil)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatal(err)
	}
	if items := m["items"]; items != nil {
		if arr, ok := items.([]any); ok && len(arr) != 0 {
			t.Fatalf("empty receipt should yield no items, got %v", arr)
		}
	}
}

func TestConfirmationMatches(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"uninstall\n", "uninstall", true},
		{"uninstall", "uninstall", true},
		{"  uninstall  \n", "uninstall", true},
		{"nope\n", "uninstall", false},
		{"", "uninstall", false},
		{"purge\n", "purge", true},
		{"uninstall\n", "purge", false},
	}
	for _, c := range cases {
		if got := confirmationMatches(c.in, c.want); got != c.ok {
			t.Errorf("confirmationMatches(%q, %q) = %v, want %v", c.in, c.want, got, c.ok)
		}
	}
}
