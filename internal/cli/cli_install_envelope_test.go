package cli

import "testing"

// The enveloped `--output json` path is additive: the legacy raw `--json` output (pinned by
// TestInstallStatusJSONShape and friends) is untouched. These assert the contract envelope shape
// the Emacs install surface parses (specs/0052 E1).

func TestInstallStatusOutputJSONEnvelope(t *testing.T) {
	out, err := runRootForTest(t, t.TempDir(), "install", "status", "--output", "json")
	if err != nil {
		t.Fatalf("install status --output json: %v", err)
	}
	env := parseEnvelopeForTest(t, out)
	if !env.OK {
		t.Fatalf("error envelope: %+v", env.Errors)
	}
	for _, k := range []string{"self", "toolchains", "runtimes"} {
		if _, ok := env.Data[k]; !ok {
			t.Fatalf("data missing %q: %#v", k, env.Data)
		}
	}
	self, ok := env.Data["self"].(map[string]any)
	if !ok || self["version"] == nil {
		t.Fatalf("data.self.version missing: %#v", env.Data)
	}
}

func TestInstallPlanOutputJSONEnvelope(t *testing.T) {
	out, err := runRootForTest(t, t.TempDir(), "install", "plan", "--output", "json")
	if err != nil {
		t.Fatalf("install plan --output json: %v", err)
	}
	env := parseEnvelopeForTest(t, out)
	if !env.OK {
		t.Fatalf("error envelope: %+v", env.Errors)
	}
	if _, ok := env.Data["actions"]; !ok {
		t.Fatalf("data.actions missing: %#v", env.Data)
	}
	if _, ok := env.Data["pending"]; !ok {
		t.Fatalf("data.pending missing: %#v", env.Data)
	}
}

func TestInstallApplyDryRunOutputJSONEnvelope(t *testing.T) {
	out, err := runRootForTest(t, t.TempDir(), "install", "apply", "--dry-run", "--output", "json")
	if err != nil {
		t.Fatalf("install apply --dry-run --output json: %v", err)
	}
	env := parseEnvelopeForTest(t, out)
	if !env.OK {
		t.Fatalf("error envelope: %+v", env.Errors)
	}
	if env.Data["dry_run"] != true {
		t.Fatalf("data.dry_run not true: %#v", env.Data)
	}
	if _, ok := env.Data["actions"]; !ok {
		t.Fatalf("data.actions missing: %#v", env.Data)
	}
}
