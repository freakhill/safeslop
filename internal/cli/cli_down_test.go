package cli

import "testing"

func TestDownCommandRegistered(t *testing.T) {
	var found bool
	for _, c := range newRoot().Commands() {
		if c.Name() == "down" {
			found = true
		}
	}
	if !found {
		t.Fatal("down command not registered")
	}
}
