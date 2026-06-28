package policy

import (
	"sort"
	"strings"
	"testing"
)

func TestDefaultCatalogIsWellFormed(t *testing.T) {
	if err := DefaultCatalog().Validate(); err != nil {
		t.Fatalf("the in-tree catalog must validate: %v", err)
	}
}

// The v1 catalog ships binary digests as the IW2 sentinel; this both documents that
// IW2 owes the real digests and guards that we never silently mark them resolved.
func TestDefaultCatalogBinaryDigestsPendingIW2(t *testing.T) {
	pending := DefaultCatalog().BuildReady()
	if len(pending) == 0 {
		t.Fatal("expected unresolved binary digests pending IW2; got none (were real digests added without lifting the sentinel guard?)")
	}
	for _, name := range pending {
		p, ok := DefaultCatalog().pkgIdx[name]
		if !ok || p.Kind != KindBinary || p.SHA256 != sha256Unresolved {
			t.Errorf("BuildReady returned %q which is not a sentinel binary", name)
		}
	}
}

func TestCatalogAccessorsSorted(t *testing.T) {
	pkgs := DefaultCatalog().Packages()
	if !sort.SliceIsSorted(pkgs, func(i, j int) bool { return pkgs[i].Name < pkgs[j].Name }) {
		t.Error("Packages() not sorted by name")
	}
	bnd := DefaultCatalog().Bundles()
	if !sort.SliceIsSorted(bnd, func(i, j int) bool { return bnd[i].Name < bnd[j].Name }) {
		t.Error("Bundles() not sorted by name")
	}
}

func TestDefaultBundle(t *testing.T) {
	c := DefaultCatalog()
	cases := map[string]string{
		"claude":      "claude",
		"claude-code": "claude", // normalized alias
		"pi":          "pi",
		"fish":        "",
		"zsh":         "",
		"shell":       "",
	}
	for agent, want := range cases {
		if got := c.DefaultBundle(agent); got != want {
			t.Errorf("DefaultBundle(%q) = %q, want %q", agent, got, want)
		}
	}
}

func TestValidateCatchesMalformedCatalogs(t *testing.T) {
	good := sha256Unresolved // a structurally valid 64-hex
	cases := []struct {
		name     string
		pkgs     []Package
		bundles  []Bundle
		defaults map[string]string
		want     string
	}{
		{"dup-package", []Package{{Name: "a", Kind: KindApt, Version: "1"}, {Name: "a", Kind: KindApt, Version: "1"}}, nil, nil, "duplicate package"},
		{"empty-name", []Package{{Name: "", Kind: KindApt, Version: "1"}}, nil, nil, "empty name"},
		{"bad-kind", []Package{{Name: "a", Kind: "script", Version: "1"}}, nil, nil, "invalid kind"},
		{"no-version", []Package{{Name: "a", Kind: KindApt}}, nil, nil, "no pinned version"},
		{"binary-no-sha", []Package{{Name: "a", Kind: KindBinary, Version: "1"}}, nil, nil, "needs a 64-hex sha256"},
		{"requires-unknown", []Package{{Name: "a", Kind: KindApt, Version: "1", Requires: []string{"ghost"}}}, nil, nil, "requires unknown package"},
		{"conflict-unknown", []Package{{Name: "a", Kind: KindApt, Version: "1", Conflicts: []string{"ghost"}}}, nil, nil, "conflicts with unknown package"},
		{"over-wide-egress", []Package{{Name: "a", Kind: KindApt, Version: "1", RuntimeEgress: []string{"*"}}}, nil, nil, "over-wide egress"},
		{"single-label-egress", []Package{{Name: "a", Kind: KindApt, Version: "1", RuntimeEgress: []string{"localhost"}}}, nil, nil, "over-wide egress"},
		{"bundle-unknown-pkg", []Package{{Name: "a", Kind: KindApt, Version: "1"}}, []Bundle{{Name: "b", Packages: []string{"ghost"}}}, nil, "references unknown package"},
		{"dup-bundle", []Package{{Name: "a", Kind: KindApt, Version: "1"}}, []Bundle{{Name: "b", Packages: []string{"a"}}, {Name: "b", Packages: []string{"a"}}}, nil, "duplicate bundle"},
		{"default-missing", []Package{{Name: "a", Kind: KindApt, Version: "1"}}, nil, map[string]string{"claude": "ghost"}, "default bundle"},
		{"requires-cycle", []Package{{Name: "a", Kind: KindApt, Version: "1", Requires: []string{"b"}}, {Name: "b", Kind: KindApt, Version: "1", Requires: []string{"a"}}}, nil, nil, "cycle"},
		{"good-binary-ok", []Package{{Name: "a", Kind: KindBinary, Version: "1", SHA256: good}}, nil, nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := newCatalog(tc.pkgs, tc.bundles, tc.defaults).Validate()
			if tc.want == "" {
				if err != nil {
					t.Fatalf("expected valid, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.want)
			}
		})
	}
}

func TestEgressTooWide(t *testing.T) {
	wide := []string{"", "*", "localhost", "anthropic"}
	ok := []string{".anthropic.com", "anthropic.com", "api.x.io", ".a.b.c"}
	for _, d := range wide {
		if !egressTooWide(d) {
			t.Errorf("egressTooWide(%q) = false, want true", d)
		}
	}
	for _, d := range ok {
		if egressTooWide(d) {
			t.Errorf("egressTooWide(%q) = true, want false", d)
		}
	}
}
