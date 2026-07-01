package policy

import (
	"reflect"
	"strings"
	"testing"
)

func TestCatalogEditBumpPackage(t *testing.T) {
	t.Run("success updates binary atomically and records blast radius", func(t *testing.T) {
		c := testEditCatalog(t)
		newSHA := map[string]string{"amd64": digest("c"), "arm64": digest("d")}

		next, diff, err := BumpPackage(c, "node", "1.3.0", newSHA, "canary")
		if err != nil {
			t.Fatalf("BumpPackage returned error: %v", err)
		}
		if next == c {
			t.Fatal("BumpPackage returned the input catalog")
		}
		oldNode, _ := c.Lookup("node")
		if got := oldNode.Version; got != "1.2.3" {
			t.Fatalf("input catalog mutated: old version = %q", got)
		}
		if got := oldNode.SHA256["amd64"]; got != digest("a") {
			t.Fatalf("input catalog sha mutated: got %q", got)
		}
		node, _ := next.Lookup("node")
		if node.Version != "1.3.0" {
			t.Errorf("new version = %q, want 1.3.0", node.Version)
		}
		if !reflect.DeepEqual(node.SHA256, newSHA) {
			t.Errorf("new sha = %v, want %v", node.SHA256, newSHA)
		}
		if diff.PackageName != "node" || diff.OldVersion != "1.2.3" || diff.NewVersion != "1.3.0" {
			t.Errorf("diff version fields = %#v", diff)
		}
		if diff.Magnitude != MagMinor {
			t.Errorf("magnitude = %q, want %q", diff.Magnitude, MagMinor)
		}
		if diff.Lane != "canary" {
			t.Errorf("lane = %q, want canary", diff.Lane)
		}
		wantClosure := []string{"plugin", "sidecar", "tool"}
		if !reflect.DeepEqual(diff.ReverseClosure, wantClosure) {
			t.Errorf("reverse closure = %v, want %v", diff.ReverseClosure, wantClosure)
		}
		newSHA["amd64"] = digest("e")
		if got := node.SHA256["amd64"]; got != digest("c") {
			t.Errorf("catalog reused caller sha map: got %q", got)
		}
	})

	t.Run("law-A rejects single arch sha", func(t *testing.T) {
		c := testEditCatalog(t)
		_, _, err := BumpPackage(c, "node", "1.3.0", map[string]string{"amd64": digest("c")}, "canary")
		assertErrContains(t, err, "law-A")
		assertErrContains(t, err, "missing arm64")
	})

	t.Run("law-A rejects unresolved sha", func(t *testing.T) {
		c := testEditCatalog(t)
		_, _, err := BumpPackage(c, "node", "1.3.0", map[string]string{"amd64": sha256Unresolved, "arm64": digest("d")}, "canary")
		assertErrContains(t, err, "law-A")
		assertErrContains(t, err, "unresolved amd64")
	})

	t.Run("law-B rejects unstable channel", func(t *testing.T) {
		c := testEditCatalog(t)
		_, _, err := BumpPackage(c, "tool", "1.3.0-rc1", nil, "canary")
		assertErrContains(t, err, "law-B")
	})

	t.Run("law-C gates apt bumps", func(t *testing.T) {
		c := testEditCatalog(t)
		_, _, err := BumpPackage(c, "aptlib", "1.3", nil, "canary")
		assertErrContains(t, err, "law-C: apt bumps require the coordinated Debian-snapshot timestamp (two-part) — not yet wired")
	})

	t.Run("monotonic floor rejects lower version", func(t *testing.T) {
		c := testEditCatalog(t)
		_, _, err := BumpPackage(c, "tool", "0.9.0", nil, "canary")
		assertErrContains(t, err, "monotonic floor")
	})

	t.Run("equal version unchanged is no change", func(t *testing.T) {
		c := testEditCatalog(t)
		_, _, err := BumpPackage(c, "node", "1.2.3", map[string]string{"amd64": digest("a"), "arm64": digest("b")}, "canary")
		assertErrContains(t, err, "bump: no change")
	})

	t.Run("equal version changed sha requires revision", func(t *testing.T) {
		c := testEditCatalog(t)
		_, _, err := BumpPackage(c, "node", "1.2.3", map[string]string{"amd64": digest("c"), "arm64": digest("d")}, "canary")
		assertErrContains(t, err, "bump: same version requires Revision (v1.1)")
	})

	t.Run("non-binary ignores sha map", func(t *testing.T) {
		c := testEditCatalog(t)
		next, diff, err := BumpPackage(c, "tool", "1.0.1", map[string]string{"amd64": "not-a-sha"}, "stable")
		if err != nil {
			t.Fatalf("BumpPackage returned error: %v", err)
		}
		tool, _ := next.Lookup("tool")
		if tool.Version != "1.0.1" {
			t.Errorf("tool version = %q, want 1.0.1", tool.Version)
		}
		if diff.Magnitude != MagPatch {
			t.Errorf("magnitude = %q, want %q", diff.Magnitude, MagPatch)
		}
		if diff.NewSHA256 != nil {
			t.Errorf("non-binary diff NewSHA256 = %v, want nil", diff.NewSHA256)
		}
	})

	// A cross-grammar bump (semver -> calver) must surface as an error, never a panic
	// inside version.Compare (which panics on a scheme mismatch, by design). InferScheme
	// sniffs a year-leading version as calver, so bumping a semver binary to "2026.x"
	// flips the inferred grammar; BumpPackage must catch that before Compare or the
	// monotonic floor cannot be enforced (specs/0059 W1; canon: a bump never rolls back).
	t.Run("scheme change is rejected not panicked", func(t *testing.T) {
		c := testEditCatalog(t)
		_, _, err := BumpPackage(c, "node", "2026.1.1", map[string]string{"amd64": digest("c"), "arm64": digest("d")}, "canary")
		assertErrContains(t, err, "scheme change")
	})

	// An unknown root is a caller bug, not an empty edit: BumpPackage must refuse it
	// rather than silently no-op the mutation (LAW-D relies on the name resolving).
	t.Run("unknown package is rejected", func(t *testing.T) {
		c := testEditCatalog(t)
		_, _, err := BumpPackage(c, "ghost", "1.0.0", nil, "canary")
		assertErrContains(t, err, "unknown package")
	})

	// LAW-A is all-or-none: a nil sha map for a binary is the degenerate "no digests"
	// case and must be rejected exactly like a partial map, so no sha256Unresolved or
	// missing arch ever survives a binary bump (specs/0059 D5).
	t.Run("law-A rejects nil sha for binary", func(t *testing.T) {
		c := testEditCatalog(t)
		_, _, err := BumpPackage(c, "node", "1.3.0", nil, "canary")
		assertErrContains(t, err, "law-A")
		assertErrContains(t, err, "every build arch")
	})
}

func TestCatalogEditAddPackage(t *testing.T) {
	t.Run("adds validated package without mutating input", func(t *testing.T) {
		c := testEditCatalog(t)
		next, diff, err := AddPackage(c, Package{Name: "newpkg", Kind: KindPip, Version: "2.0.0", Requires: []string{"tool"}})
		if err != nil {
			t.Fatalf("AddPackage returned error: %v", err)
		}
		if _, ok := c.Lookup("newpkg"); ok {
			t.Fatal("input catalog mutated with new package")
		}
		if _, ok := next.Lookup("newpkg"); !ok {
			t.Fatal("new catalog missing added package")
		}
		if diff.Operation != "add-package" || diff.PackageName != "newpkg" || diff.NewVersion != "2.0.0" {
			t.Errorf("diff = %#v", diff)
		}
	})

	t.Run("law-D rejects duplicate package", func(t *testing.T) {
		c := testEditCatalog(t)
		_, _, err := AddPackage(c, Package{Name: "tool", Kind: KindNpm, Version: "2.0.0"})
		assertErrContains(t, err, "law-D")
	})

	t.Run("rejects invalid kind before mutation", func(t *testing.T) {
		c := testEditCatalog(t)
		_, _, err := AddPackage(c, Package{Name: "scripted", Kind: "script", Version: "1"})
		assertErrContains(t, err, "invalid kind")
		if _, ok := c.Lookup("scripted"); ok {
			t.Fatal("input catalog mutated after invalid add")
		}
	})

	t.Run("re-validates references", func(t *testing.T) {
		c := testEditCatalog(t)
		_, _, err := AddPackage(c, Package{Name: "broken", Kind: KindPip, Version: "1.0.0", Requires: []string{"ghost"}})
		assertErrContains(t, err, "requires unknown package")
	})
}

func TestCatalogEditBundleAddRemove(t *testing.T) {
	t.Run("bundle add validates package references and is pure", func(t *testing.T) {
		c := testEditCatalog(t)
		next, diff, err := BundleAdd(c, "base", "sidecar")
		if err != nil {
			t.Fatalf("BundleAdd returned error: %v", err)
		}
		oldBundle := c.bndIdx["base"]
		if !reflect.DeepEqual(oldBundle.Packages, []string{"tool"}) {
			t.Fatalf("input bundle mutated: %v", oldBundle.Packages)
		}
		newBundle := next.bndIdx["base"]
		if !reflect.DeepEqual(newBundle.Packages, []string{"tool", "sidecar"}) {
			t.Errorf("new bundle packages = %v, want [tool sidecar]", newBundle.Packages)
		}
		if diff.BundleName != "base" || !reflect.DeepEqual(diff.AddedPackages, []string{"sidecar"}) {
			t.Errorf("diff = %#v", diff)
		}
	})

	t.Run("bundle add rejects unknown bundle", func(t *testing.T) {
		c := testEditCatalog(t)
		_, _, err := BundleAdd(c, "ghost", "tool")
		assertErrContains(t, err, "unknown bundle")
	})

	t.Run("bundle add rejects unknown package", func(t *testing.T) {
		c := testEditCatalog(t)
		_, _, err := BundleAdd(c, "base", "ghost")
		assertErrContains(t, err, "unknown package")
	})

	t.Run("bundle remove is pure and revalidates result", func(t *testing.T) {
		c := testEditCatalog(t)
		next, diff, err := BundleRemove(c, "base", "tool")
		if err != nil {
			t.Fatalf("BundleRemove returned error: %v", err)
		}
		oldBundle := c.bndIdx["base"]
		if !reflect.DeepEqual(oldBundle.Packages, []string{"tool"}) {
			t.Fatalf("input bundle mutated: %v", oldBundle.Packages)
		}
		newBundle := next.bndIdx["base"]
		if len(newBundle.Packages) != 0 {
			t.Errorf("new bundle packages = %v, want empty", newBundle.Packages)
		}
		if diff.BundleName != "base" || !reflect.DeepEqual(diff.RemovedPackages, []string{"tool"}) {
			t.Errorf("diff = %#v", diff)
		}
	})

	t.Run("bundle remove rejects unknown bundle", func(t *testing.T) {
		c := testEditCatalog(t)
		_, _, err := BundleRemove(c, "ghost", "tool")
		assertErrContains(t, err, "unknown bundle")
	})
}

func TestReverseClosure(t *testing.T) {
	t.Run("returns sorted transitive dependents without root", func(t *testing.T) {
		c := testEditCatalog(t)
		got, err := ReverseClosureErr(c, "node")
		if err != nil {
			t.Fatalf("ReverseClosureErr returned error: %v", err)
		}
		want := []string{"plugin", "sidecar", "tool"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("ReverseClosureErr(node) = %v, want %v", got, want)
		}
		if legacy := ReverseClosure(c, "node"); !reflect.DeepEqual(legacy, want) {
			t.Errorf("ReverseClosure(node) = %v, want %v", legacy, want)
		}
	})

	t.Run("unknown package returns error", func(t *testing.T) {
		c := testEditCatalog(t)
		got, err := ReverseClosureErr(c, "ghost")
		assertErrContains(t, err, "unknown package")
		if got != nil {
			t.Errorf("ReverseClosureErr unknown got %v, want nil", got)
		}
	})
}

func testEditCatalog(t *testing.T) *Catalog {
	t.Helper()
	c := newCatalog([]Package{
		{Name: "node", Kind: KindBinary, Version: "1.2.3", SHA256: map[string]string{"amd64": digest("a"), "arm64": digest("b")}},
		{Name: "tool", Kind: KindNpm, Version: "1.0.0", Requires: []string{"node"}},
		{Name: "plugin", Kind: KindPip, Version: "1.0.0", Requires: []string{"tool"}},
		{Name: "sidecar", Kind: KindNpm, Version: "1.0.0", Requires: []string{"node"}},
		{Name: "aptlib", Kind: KindApt, Version: "1.2"},
	}, []Bundle{{Name: "base", Packages: []string{"tool"}}}, nil)
	if err := c.Validate(); err != nil {
		t.Fatalf("test catalog invalid: %v", err)
	}
	return c
}

func digest(s string) string { return strings.Repeat(s, 64) }

func assertErrContains(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error containing %q, got nil", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q does not contain %q", err.Error(), want)
	}
}
