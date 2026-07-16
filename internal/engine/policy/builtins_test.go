package policy

import (
	"reflect"
	"sort"
	"testing"
)

func TestBuiltinFishProjectsDemandLoadedOnly(t *testing.T) {
	builtin, ok := BuiltinProfileByName("fish")
	if !ok {
		t.Fatal("fish builtin missing")
	}
	want := []ProjectionItem{
		{Source: "~/.config/fish/functions/*.fish", Kind: "glob", Label: "fish-functions", Optional: boolPtrForBuiltinTest(true)},
		{Source: "~/.config/fish/completions/*.fish", Kind: "glob", Label: "fish-completions", Optional: boolPtrForBuiltinTest(true)},
	}
	if builtin.Profile.Projection == nil || !reflect.DeepEqual(builtin.Profile.Projection.Items, want) {
		t.Fatalf("fish builtin projection = %#v, want %#v", builtin.Profile.Projection, want)
	}
}

func boolPtrForBuiltinTest(v bool) *bool { return &v }

func TestBuiltinProfilesAreLaunchableAndDeterministic(t *testing.T) {
	builtins := BuiltinProfiles()
	if len(builtins) != 4 {
		t.Fatalf("expected four builtin profiles, got %d", len(builtins))
	}

	names := make([]string, len(builtins))
	for i, builtin := range builtins {
		names[i] = builtin.Name
		if builtin.Description == "" || builtin.Hash == "" {
			t.Errorf("builtin %q is missing description or hash", builtin.Name)
		}
		if builtin.Profile.Environment != "container" || builtin.Profile.Network != "deny" || len(builtin.Profile.Bundles) != 1 || builtin.Profile.Bundles[0] != "personal" {
			t.Errorf("builtin %q is not contained deny-by-default with personal tools: %#v", builtin.Name, builtin.Profile)
		}
		if builtin.Profile.BareAgent || builtin.Profile.Projection == nil || !builtin.Profile.Projection.Enabled || len(builtin.Profile.Projection.Items) == 0 {
			t.Errorf("builtin %q lacks its safe host projection: %#v", builtin.Name, builtin.Profile)
		}
		if NormalizeAgent(builtin.Profile.Agent) != builtin.Name {
			t.Errorf("builtin %q has agent %q", builtin.Name, builtin.Profile.Agent)
		}
		if !IsLaunchableAgent(NormalizeAgent(builtin.Profile.Agent)) {
			t.Errorf("builtin %q is not session-launchable", builtin.Name)
		}
		resolved, err := Resolve(builtin.Profile)
		if err != nil {
			t.Errorf("builtin %q does not resolve: %v", builtin.Name, err)
			continue
		}
		if pending := DefaultCatalog().BuildReadyFor(resolved.IdentitySet); len(pending) > 0 {
			t.Errorf("builtin %q resolves unbuildable packages: %v", builtin.Name, pending)
		}
	}

	if !sort.StringsAreSorted(names) {
		t.Errorf("builtin profiles are not sorted: %v", names)
	}
	if want := []string{"claude", "fish", "pi", "zsh"}; !reflect.DeepEqual(names, want) {
		t.Errorf("builtin profile names = %v, want %v", names, want)
	}
	for _, name := range names {
		builtin, ok := BuiltinProfileByName(name)
		if !ok || builtin.Name != name {
			t.Errorf("BuiltinProfileByName(%q) = %#v, %v", name, builtin, ok)
		}
	}
	if _, ok := BuiltinProfileByName("missing"); ok {
		t.Error("BuiltinProfileByName(missing) unexpectedly resolved")
	}
	if !reflect.DeepEqual(builtins, BuiltinProfiles()) {
		t.Error("BuiltinProfiles returned non-deterministic output")
	}
}
