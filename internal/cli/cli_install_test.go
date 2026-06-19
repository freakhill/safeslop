package cli

import (
	"encoding/json"
	"testing"
)

func TestInstallStatusJSONShape(t *testing.T) {
	out := renderInstallStatusJSON("v9.9.9")
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("status --json is not valid JSON: %v\n%s", err, out)
	}
	self, ok := m["self"].(map[string]any)
	if !ok {
		t.Fatalf("missing self object: %v", m)
	}
	if self["version"] != "v9.9.9" {
		t.Fatalf("self.version = %v, want v9.9.9", self["version"])
	}
	for _, k := range []string{"app", "toolchains", "runtimes"} {
		if _, ok := m[k]; !ok {
			t.Fatalf("status JSON missing %q: %v", k, m)
		}
	}
}
