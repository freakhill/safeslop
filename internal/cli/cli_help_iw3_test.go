package cli

import "testing"

func TestIW3CommandsRegisteredInRootHelp(t *testing.T) {
	root := newRoot()
	for _, want := range []string{"catalog", "profile", "lock", "untrust"} {
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

func TestSessionEgressSubcommandsRegistered(t *testing.T) {
	root := newRoot()
	egress, _, err := root.Find([]string{"session", "egress"})
	if err != nil {
		t.Fatalf("session egress command missing: %v", err)
	}
	for _, want := range []string{"observations", "grants", "grant", "revoke"} {
		var found bool
		for _, c := range egress.Commands() {
			if c.Name() == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("session egress subcommand %q not registered", want)
		}
	}
}
