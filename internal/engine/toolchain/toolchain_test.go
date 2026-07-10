package toolchain

import (
	"strings"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/hostexec"
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

func TestAvailableUsesSanitizedInspectionAndRejectsShadow(t *testing.T) {
	old := hostExecResolver
	hostExecResolver = func() *hostexec.Resolver {
		return hostexec.New(toolchainFakeEnv{path: "/safe/bin:/other/bin", all: map[string][]string{
			"mise": {"/safe/bin/mise", "/other/bin/mise"},
			"nix":  {"/safe/bin/nix"},
		}})
	}
	t.Cleanup(func() { hostExecResolver = old })

	if Available("mise") {
		t.Fatal("shadowed mise should be conservatively unavailable")
	}
	if !Available("nix") {
		t.Fatal("single sanitized nix should be available")
	}
}

type toolchainFakeEnv struct {
	path string
	all  map[string][]string
}

func (f toolchainFakeEnv) PATH() string              { return f.path }
func (f toolchainFakeEnv) Get(string) (string, bool) { return "", false }
func (f toolchainFakeEnv) LookPath(name string) (string, bool) {
	all := f.LookAll(name)
	if len(all) == 0 {
		return "", false
	}
	return all[0], true
}
func (f toolchainFakeEnv) LookAll(name string) []string       { return append([]string(nil), f.all[name]...) }
func (f toolchainFakeEnv) SameFile(a, b string) (bool, error) { return a == b, nil }
