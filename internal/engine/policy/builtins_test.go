package policy

import (
	"reflect"
	"sort"
	"testing"
)

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
		if builtin.Profile.Environment != "container" || builtin.Profile.Network != "deny" {
			t.Errorf("builtin %q is not contained deny-by-default: %#v", builtin.Name, builtin.Profile)
		}
		if NormalizeAgent(builtin.Profile.Agent) != builtin.Name {
			t.Errorf("builtin %q has agent %q", builtin.Name, builtin.Profile.Agent)
		}
		if !IsLaunchableAgent(NormalizeAgent(builtin.Profile.Agent)) {
			t.Errorf("builtin %q is not session-launchable", builtin.Name)
		}
		if _, err := Resolve(builtin.Profile); err != nil {
			t.Errorf("builtin %q does not resolve: %v", builtin.Name, err)
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
