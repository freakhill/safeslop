package policy

import "testing"

// Every bundled preset must be a valid safeslop.cue with a description — they're offered to users as
// known-good starting points, so a broken one is worse than none.
func TestPresetsAreValidAndDescribed(t *testing.T) {
	ps := Presets()
	if len(ps) < 3 {
		t.Fatalf("expected several presets, got %d", len(ps))
	}
	for _, p := range ps {
		if p.Name == "" || p.Description == "" || p.CUE == "" {
			t.Errorf("preset %q missing name/description/cue", p.Name)
		}
		cfg, err := LoadBytes([]byte(p.CUE))
		if err != nil {
			t.Errorf("preset %q does not validate:\n%v", p.Name, err)
			continue
		}
		if len(cfg.Profiles) == 0 {
			t.Errorf("preset %q has no profiles", p.Name)
		}
	}
}

// A preset exists to be opened as a profile-backed session from Emacs, so every profile it ships
// must use an agent that the profile-backed session path accepts. Ad-hoc `session create --agent
// shell` remains unsupported, but an existing profile may still use the legacy "shell" value.
func TestPresetProfilesAreSessionCreatable(t *testing.T) {
	for _, p := range Presets() {
		cfg, err := LoadBytes([]byte(p.CUE))
		if err != nil {
			t.Errorf("preset %q does not validate: %v", p.Name, err)
			continue
		}
		for name, prof := range cfg.Profiles {
			agent := NormalizeAgent(prof.Agent)
			if !IsLaunchableAgent(agent) && agent != "shell" {
				t.Errorf("preset %q profile %q uses non-session-creatable agent %q", p.Name, name, prof.Agent)
			}
		}
	}
}

// Each shipped preset must also resolve its package set against the catalog — a starter whose profile
// names a bogus bundle/package or trips a requires-cycle/conflict would be offered as "known-good" yet
// fail the moment it is built, so resolution is part of the preset contract.
func TestPresetProfilesResolve(t *testing.T) {
	for _, p := range Presets() {
		cfg, err := LoadBytes([]byte(p.CUE))
		if err != nil {
			t.Errorf("preset %q does not validate: %v", p.Name, err)
			continue
		}
		for name, prof := range cfg.Profiles {
			if _, err := Resolve(prof); err != nil {
				t.Errorf("preset %q profile %q does not resolve: %v", p.Name, name, err)
			}
		}
	}
}
