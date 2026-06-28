package policy

import (
	"reflect"
	"strings"
	"testing"
)

func TestResolveDefaultBundle(t *testing.T) {
	// agent claude, nothing declared -> exactly the claude default bundle.
	r, err := Resolve(Profile{Agent: "claude", Environment: "container"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if want := []string{"claude-code", "node"}; !reflect.DeepEqual(r.IdentitySet, want) {
		t.Errorf("IdentitySet = %v, want %v", r.IdentitySet, want)
	}
	// node must install before claude-code (it requires node).
	if want := []string{"node", "claude-code"}; !reflect.DeepEqual(r.Packages, want) {
		t.Errorf("Packages (install order) = %v, want %v", r.Packages, want)
	}
	if want := []string{".anthropic.com"}; !reflect.DeepEqual(r.RuntimeEgress, want) {
		t.Errorf("RuntimeEgress = %v, want %v", r.RuntimeEgress, want)
	}
}

// A legacy/shell profile with no packages and no default bundle resolves to nothing —
// the migration guarantee (specs/0058 N0): empty profile == agent default (here none).
func TestResolveShellAgentEmpty(t *testing.T) {
	r, err := Resolve(Profile{Agent: "fish", Environment: "container"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(r.Packages) != 0 || len(r.IdentitySet) != 0 || len(r.RuntimeEgress) != 0 {
		t.Errorf("fish should resolve to nothing, got %+v", r)
	}
}

// The agent default bundle is additive: à-la-carte packages add to it, never replace it.
func TestResolveAgentDefaultIsAdditive(t *testing.T) {
	r, err := Resolve(Profile{Agent: "claude", Environment: "container", Packages: []string{"uv"}})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if want := []string{"claude-code", "node", "uv"}; !reflect.DeepEqual(r.IdentitySet, want) {
		t.Errorf("IdentitySet = %v, want %v", r.IdentitySet, want)
	}
}

// Identity is order-independent and deduped across the default bundle, a declared
// bundle, and a duplicate à-la-carte package.
func TestResolveDedup(t *testing.T) {
	r, err := Resolve(Profile{Agent: "claude", Environment: "container",
		Bundles: []string{"node"}, Packages: []string{"node"}})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if want := []string{"bun", "claude-code", "node", "pnpm"}; !reflect.DeepEqual(r.IdentitySet, want) {
		t.Errorf("IdentitySet = %v, want %v (deduped)", r.IdentitySet, want)
	}
}

func TestResolveUnknownNames(t *testing.T) {
	if _, err := Resolve(Profile{Agent: "fish", Packages: []string{"ghost"}}); err == nil ||
		!strings.Contains(err.Error(), "unknown package") {
		t.Errorf("unknown package: got %v", err)
	}
	if _, err := Resolve(Profile{Agent: "fish", Bundles: []string{"ghost"}}); err == nil ||
		!strings.Contains(err.Error(), "unknown bundle") {
		t.Errorf("unknown bundle: got %v", err)
	}
}

// Synthetic catalogs exercise the resolver's structural error paths independently of
// the real catalog's contents.
func TestResolveTopologicalOrderFromClosure(t *testing.T) {
	c := newCatalog([]Package{
		{Name: "cc", Kind: KindNpm, Version: "1", Requires: []string{"node"}},
		{Name: "node", Kind: KindApt, Version: "1"},
	}, nil, nil)
	// declare only the dependent; the closure must pull in node, ordered first.
	r, err := c.Resolve(Profile{Agent: "fish", Packages: []string{"cc"}})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if want := []string{"node", "cc"}; !reflect.DeepEqual(r.Packages, want) {
		t.Errorf("install order = %v, want %v", r.Packages, want)
	}
	if want := []string{"cc", "node"}; !reflect.DeepEqual(r.IdentitySet, want) {
		t.Errorf("identity = %v, want %v", r.IdentitySet, want)
	}
}

func TestResolveConflict(t *testing.T) {
	c := newCatalog([]Package{
		{Name: "a", Kind: KindApt, Version: "1", Conflicts: []string{"b"}},
		{Name: "b", Kind: KindApt, Version: "1"},
	}, nil, nil)
	_, err := c.Resolve(Profile{Agent: "fish", Packages: []string{"a", "b"}})
	if err == nil || !strings.Contains(err.Error(), "conflict") {
		t.Fatalf("expected conflict error, got %v", err)
	}
}

func TestResolveCycle(t *testing.T) {
	c := newCatalog([]Package{
		{Name: "a", Kind: KindApt, Version: "1", Requires: []string{"b"}},
		{Name: "b", Kind: KindApt, Version: "1", Requires: []string{"a"}},
	}, nil, nil)
	_, err := c.Resolve(Profile{Agent: "fish", Packages: []string{"a"}})
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected cycle error, got %v", err)
	}
}

func TestResolveRuntimeEgressUnion(t *testing.T) {
	c := newCatalog([]Package{
		{Name: "a", Kind: KindApt, Version: "1", RuntimeEgress: []string{".x.com", "api.y.com"}},
		{Name: "b", Kind: KindApt, Version: "1", RuntimeEgress: []string{".x.com", ".z.com"}},
	}, nil, nil)
	r, err := c.Resolve(Profile{Agent: "fish", Packages: []string{"a", "b"}})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if want := []string{".x.com", ".z.com", "api.y.com"}; !reflect.DeepEqual(r.RuntimeEgress, want) {
		t.Errorf("egress union = %v, want %v", r.RuntimeEgress, want)
	}
}

// The schema accepts bundles/packages on a profile and they decode onto policy.Profile.
func TestLoadDecodesPackagesAndBundles(t *testing.T) {
	cfg, err := loadStr(t, `package safeslop
safeslop: profiles: dev: {agent: "claude", environment: "container", bundles: ["python"], packages: ["ripgrep"]}`)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	dev := cfg.Profiles["dev"]
	if want := []string{"python"}; !reflect.DeepEqual(dev.Bundles, want) {
		t.Errorf("dev.Bundles = %v, want %v", dev.Bundles, want)
	}
	if want := []string{"ripgrep"}; !reflect.DeepEqual(dev.Packages, want) {
		t.Errorf("dev.Packages = %v, want %v", dev.Packages, want)
	}
	// and the declared names resolve against the real catalog.
	if _, err := Resolve(dev); err != nil {
		t.Errorf("Resolve(dev): %v", err)
	}
}
