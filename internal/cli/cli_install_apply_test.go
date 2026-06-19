package cli

import (
	"encoding/json"
	"testing"
)

func TestInstallApplyDryRunJSONShape(t *testing.T) {
	out, err := renderInstallApplyDryRunJSON("v9.9.9")
	if err != nil {
		t.Fatalf("apply --dry-run --json errored: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, out)
	}
	if _, ok := m["actions"]; !ok {
		t.Fatalf("apply dry-run JSON missing \"actions\": %v", m)
	}
	if _, ok := m["dry_run"]; !ok {
		t.Fatalf("apply dry-run JSON missing \"dry_run\": %v", m)
	}
}
