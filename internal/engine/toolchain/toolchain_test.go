package toolchain

import (
	"strings"
	"testing"
)

func TestWrap(t *testing.T) {
	agent := []string{"claude"}
	cases := []struct {
		kind, run string
		want      string
	}{
		{"none", "", "claude"},
		{"", "", "claude"},
		{"mise", "", "mise exec -- claude"},
		{"mise", "build", "mise run build"},
		{"nix", "", "nix develop -c claude"},
		{"nix", ".#app", "nix run .#app"},
	}
	for _, c := range cases {
		got := strings.Join(Wrap(c.kind, c.run, agent), " ")
		if got != c.want {
			t.Errorf("Wrap(%q,%q)=%q want %q", c.kind, c.run, got, c.want)
		}
	}
}

func TestWrapPreservesAgentArgs(t *testing.T) {
	got := strings.Join(Wrap("mise", "", []string{"claude", "--flag"}), " ")
	if got != "mise exec -- claude --flag" {
		t.Fatalf("got %q", got)
	}
}

func TestWraps(t *testing.T) {
	if !Wraps("mise") || !Wraps("nix") || Wraps("none") || Wraps("") {
		t.Fatal("Wraps wrong")
	}
}
