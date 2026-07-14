package cli

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/policy"
	"github.com/freakhill/safeslop/internal/jsoncontract"
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

var personalPackagesForBuiltinTest = []string{
	"bat", "eza", "fd", "fzf", "go", "hyperfine", "node", "pnpm", "python3",
	"ripgrep", "ruff", "rust", "sccache", "tokei", "uv", "yq", "zoxide",
}

func TestProfileShowFallsBackToBuiltinWithProvenance(t *testing.T) {
	for _, name := range []string{"claude", "fish", "pi", "zsh"} {
		t.Run(name, func(t *testing.T) {
			out, err := runRootForTest(t, t.TempDir(), "profile", "show", name, "--output", "json")
			if err != nil {
				t.Fatalf("profile show builtin %s: %v", name, err)
			}
			env := parseEnvelopeForTest(t, out)
			if !env.OK {
				t.Fatalf("profile show builtin returned error envelope: %+v", env.Errors)
			}
			if env.Data["profile_source"] != "builtin" || env.Data["profile_name"] != name || env.Data["policy_path"] != "builtin:"+name {
				t.Fatalf("builtin provenance = %#v", env.Data)
			}
			if hash, _ := env.Data["policy_hash"].(string); hash == "" {
				t.Fatalf("builtin policy hash missing: %#v", env.Data)
			}
			profile, ok := env.Data["profile"].(map[string]any)
			if !ok || profile["environment"] != "container" || profile["network"] != "deny" {
				t.Fatalf("builtin is not contained deny-by-default: %#v", env.Data["profile"])
			}
			bundles, _ := profile["bundles"].([]any)
			projection, _ := profile["projection"].(map[string]any)
			if !stringSliceAnyContains(bundles, "personal") || projection["enabled"] != true {
				t.Fatalf("builtin lacks personal tools or host projection: %#v", profile)
			}
			resolved, ok := env.Data["resolved"].(map[string]any)
			if !ok {
				t.Fatalf("builtin resolved closure missing: %#v", env.Data)
			}
			identity, _ := resolved["identitySet"].([]any)
			for _, pkg := range personalPackagesForBuiltinTest {
				if !stringSliceAnyContains(identity, pkg) {
					t.Errorf("builtin %s identity missing personal package %q: %#v", name, pkg, identity)
				}
			}
			if recipeID, _ := env.Data["recipeID"].(string); len(recipeID) != 12 {
				t.Errorf("builtin recipeID = %q, want 12 chars", recipeID)
			}
			if image, _ := env.Data["image"].(string); !strings.HasPrefix(image, "local/safeslop-tools:") {
				t.Errorf("builtin image = %q", image)
			}
		})
	}
}

func TestProfileShowInvalidProjectConfigBlocksBuiltinFallback(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "safeslop.cue"), []byte("this is not CUE"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runRootForTest(t, dir, "profile", "show", "pi", "--output", "json")
	if !errors.Is(err, errOutputEmitted) {
		t.Fatalf("profile show invalid project: err=%v, out=%s", err, out)
	}
	env := parseEnvelopeForTest(t, out)
	if env.OK || len(env.Errors) != 1 || env.Errors[0].Code != jsoncontract.CodeSchemaViolation {
		t.Fatalf("invalid project should block fallback with schema error: %#v", env)
	}
}

func TestProfileDefaultsEnvelope(t *testing.T) {
	out, err := runRootForTest(t, t.TempDir(), "profile", "defaults", "--output", "json")
	if err != nil {
		t.Fatalf("profile defaults: %v", err)
	}
	env := parseEnvelopeForTest(t, out)
	profiles, ok := env.Data["profiles"].([]any)
	if !env.OK || !ok || len(profiles) != 4 {
		t.Fatalf("defaults envelope = %#v", env)
	}
	for _, value := range profiles {
		profile := value.(map[string]any)
		if profile["profile_source"] != "builtin" || profile["policy_path"] == "" || profile["policy_hash"] == "" {
			t.Fatalf("default provenance = %#v", profile)
		}
		body, _ := profile["profile"].(map[string]any)
		bundles, _ := body["bundles"].([]any)
		projection, _ := body["projection"].(map[string]any)
		if body["environment"] != "container" || body["network"] != "deny" || !stringSliceAnyContains(bundles, "personal") || projection["enabled"] != true {
			t.Fatalf("builtin default is not contained-hybrid with personal tools: %#v", profile)
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

func TestProfileShowProjectExactByteEvaluation(t *testing.T) {
	fixed := withProfileEvaluationLocalPass(t)
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	path := filepath.Join(dir, "safeslop.cue")
	cue := []byte(`package safeslop

safeslop: {
	version: 1
	profiles: review: {agent: "fish", environment: "host", network: "deny", workspace: "."}
}
`)
	if err := os.WriteFile(path, cue, 0o644); err != nil {
		t.Fatal(err)
	}

	showTrust := func() map[string]any {
		t.Helper()
		out, err := runRootForTest(t, dir, "profile", "show", "review", "--output", "json")
		if err != nil {
			t.Fatalf("profile show: %v\nout=%s", err, out)
		}
		env := parseEnvelopeForTest(t, out)
		evaluation, ok := env.Data["evaluation"].(map[string]any)
		if !ok {
			t.Fatalf("evaluation missing: %#v", env.Data)
		}
		return evaluation["trust"].(map[string]any)
	}

	untrusted := showTrust()
	if untrusted["state"] != policy.TrustStateUntrusted || untrusted["basis"] != policy.TrustBasisProjectExactBytes || untrusted["checked_at"] != fixed.Format("2006-01-02T15:04:05Z07:00") {
		t.Fatalf("untrusted project evaluation = %#v", untrusted)
	}
	if err := approvePolicyBytes(canonicalPolicyPath(path), cue); err != nil {
		t.Fatalf("approve policy: %v", err)
	}
	if trusted := showTrust(); trusted["state"] != policy.TrustStateTrusted || trusted["basis"] != policy.TrustBasisProjectExactBytes {
		t.Fatalf("trusted project evaluation = %#v", trusted)
	}

	changedBytes := append(append([]byte(nil), cue...), []byte("// exact bytes changed\n")...)
	if err := os.WriteFile(path, changedBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	changed := showTrust()
	if changed["state"] != policy.TrustStateChanged || changed["basis"] != policy.TrustBasisProjectExactBytes {
		t.Fatalf("changed project evaluation = %#v", changed)
	}
}

func TestProfileShowBuiltinEvaluation(t *testing.T) {
	fixed := withProfileEvaluationLocalPass(t)
	out, err := runRootForTest(t, t.TempDir(), "profile", "show", "pi", "--output", "json")
	if err != nil {
		t.Fatalf("profile show builtin: %v\nout=%s", err, out)
	}
	env := parseEnvelopeForTest(t, out)
	evaluation := env.Data["evaluation"].(map[string]any)
	trustSection := evaluation["trust"].(map[string]any)
	if trustSection["state"] != policy.TrustStateTrusted || trustSection["basis"] != policy.TrustBasisEmbeddedBuiltin || trustSection["checked_at"] != fixed.Format("2006-01-02T15:04:05Z07:00") {
		t.Fatalf("builtin trust evaluation = %#v", trustSection)
	}
	if evaluation["readiness"].(map[string]any)["state"] != policy.ReadinessStateReady {
		t.Fatalf("builtin readiness = %#v", evaluation["readiness"])
	}
	if env.Data["risk"] == nil || env.Data["risk_axes"] == nil || env.Data["profile"] == nil || env.Data["resolved"] == nil {
		t.Fatalf("evaluation replaced existing show keys: %#v", env.Data)
	}
}

func TestProfileShowAndCreateDryRunEvaluationAuthorityEqual(t *testing.T) {
	withProfileEvaluationLocalPass(t)
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	cue := `package safeslop

safeslop: {
	version: 1
	profiles: review: {agent: "fish", environment: "host", network: "deny", workspace: "."}
}
`
	if err := os.WriteFile(filepath.Join(dir, "safeslop.cue"), []byte(cue), 0o644); err != nil {
		t.Fatal(err)
	}
	showOut, err := runRootForTest(t, dir, "profile", "show", "review", "--output", "json")
	if err != nil {
		t.Fatalf("profile show: %v\nout=%s", err, showOut)
	}
	dryOut, err := runRootForTest(t, dir,
		"profile", "create", "--dry-run", "--name", "review", "--agent", "fish",
		"--environment", "host", "--network", "deny", "--workspace", ".", "--output", "json",
	)
	if err != nil {
		t.Fatalf("profile create dry-run: %v\nout=%s", err, dryOut)
	}
	showEval := parseEnvelopeForTest(t, showOut).Data["evaluation"].(map[string]any)
	dryEval := parseEnvelopeForTest(t, dryOut).Data["evaluation"].(map[string]any)
	if !reflect.DeepEqual(showEval["authority"], dryEval["authority"]) {
		t.Fatalf("static authority drifted across show/dry-run:\nshow=%#v\n dry=%#v", showEval["authority"], dryEval["authority"])
	}
	if showEval["trust"].(map[string]any)["state"] != policy.TrustStateUntrusted || dryEval["trust"].(map[string]any)["state"] != policy.TrustStateNotApplicable {
		t.Fatalf("show/dry-run trust contexts were not separate: show=%#v dry=%#v", showEval["trust"], dryEval["trust"])
	}
}

func TestProfileListRequiresOutputJSON(t *testing.T) {
	// The --output guard fires before any config lookup, so no safeslop.cue is needed.
	if _, err := runRootForTest(t, t.TempDir(), "profile", "list"); err == nil {
		t.Fatal("profile list without --output json should error")
	}
}
