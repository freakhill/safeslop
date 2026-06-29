package cli

import "testing"

func TestIW3CommandsRegisteredInRootHelp(t *testing.T) {
	root := newRoot()
	for _, want := range []string{"catalog", "profile", "lock"} {
		if _, _, err := root.Find([]string{want}); err != nil {
			t.Fatalf("root command %q not registered: %v", want, err)
		}
	}
}

func TestProfileIW3SubcommandsRegistered(t *testing.T) {
	root := newRoot()
	profile, _, err := root.Find([]string{"profile"})
	if err != nil {
		t.Fatalf("profile command missing: %v", err)
	}
	for _, want := range []string{"list", "presets", "show", "create"} {
		var found bool
		for _, c := range profile.Commands() {
			if c.Name() == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("profile subcommand %q not registered", want)
		}
	}
}
