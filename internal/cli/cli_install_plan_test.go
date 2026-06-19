package cli

import (
	"encoding/json"
	"testing"
)

func TestInstallPlanJSONShape(t *testing.T) {
	out, err := renderInstallPlanJSON("v9.9.9")
	if err != nil {
		t.Fatalf("plan --json errored (manifest must be fail-closed valid): %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("plan --json is not valid JSON: %v\n%s", err, out)
	}
	if _, ok := m["actions"]; !ok {
		t.Fatalf("plan JSON missing \"actions\": %v", m)
	}
}
