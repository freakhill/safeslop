package policy

import "testing"

// Every bundled preset must be a valid safeslop.cue with a description — they're offered to users as
// known-good starting points, so a broken one is worse than none.
func TestPresetsAreValidAndDescribed(t *testing.T) {
	ps := Presets()
	if len(ps) < 4 {
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
