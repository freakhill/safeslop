package cli

import (
	"path/filepath"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/policy"
)

func TestBundleListOutputJSON(t *testing.T) {
	out, err := runRootForTest(t, t.TempDir(), "bundle", "list", "--output", "json")
	if err != nil {
		t.Fatalf("bundle list --output json: %v", err)
	}
	env := parseEnvelopeForTest(t, out)
	if !env.OK {
		t.Fatalf("bundle list returned error envelope: %+v", env.Errors)
	}
	bundles, ok := env.Data["bundles"].([]any)
	if !ok || len(bundles) == 0 {
		t.Fatalf("data.bundles malformed: %#v", env.Data)
	}
}

func TestBundleAddOutputJSONWritesMembership(t *testing.T) {
	dir := tempCatalogDirForTest(t)
	before := bundlePackageCountForTest(t, dir, "claude")
	out, err := runRootForTest(t, dir, "bundle", "add", "claude", "pnpm", "--catalog-dir", dir, "--output", "json")
	if err != nil {
		t.Fatalf("bundle add --output json: %v\nout=%s", err, out)
	}
	env := parseEnvelopeForTest(t, out)
	if !env.OK {
		t.Fatalf("bundle add returned error envelope: %+v", env.Errors)
	}
	if count := bundlePackageCountForTest(t, dir, "claude"); count != before+1 {
		t.Fatalf("claude bundle count = %d, want %d", count, before+1)
	}
	if !bundleHasPackageForTest(t, dir, "claude", "pnpm") {
		t.Fatal("claude bundle missing added pnpm")
	}
}

func TestBundleRemoveOutputJSONWritesMembership(t *testing.T) {
	dir := tempCatalogDirForTest(t)
	before := bundlePackageCountForTest(t, dir, "claude")
	out, err := runRootForTest(t, dir, "bundle", "remove", "claude", "claude-code", "--catalog-dir", dir, "--output", "json")
	if err != nil {
		t.Fatalf("bundle remove --output json: %v\nout=%s", err, out)
	}
	env := parseEnvelopeForTest(t, out)
	if !env.OK {
		t.Fatalf("bundle remove returned error envelope: %+v", env.Errors)
	}
	if count := bundlePackageCountForTest(t, dir, "claude"); count != before-1 {
		t.Fatalf("claude bundle count = %d, want %d", count, before-1)
	}
	if bundleHasPackageForTest(t, dir, "claude", "claude-code") {
		t.Fatal("claude bundle still contains removed claude-code")
	}
}

func TestBundleAddUnknownPackageReturnsErrorEnvelope(t *testing.T) {
	dir := tempCatalogDirForTest(t)
	out, err := runRootForTest(t, dir, "bundle", "add", "claude", "not-a-real-package", "--catalog-dir", dir, "--output", "json")
	if err == nil {
		t.Fatalf("bundle add unknown package unexpectedly succeeded: %s", out)
	}
	env := parseEnvelopeForTest(t, out)
	if env.OK || len(env.Errors) == 0 {
		t.Fatalf("expected error envelope, got %+v", env)
	}
}

func bundlePackageCountForTest(t *testing.T, dir, name string) int {
	t.Helper()
	for _, bundle := range loadCatalogForBundleTest(t, dir).Bundles() {
		if bundle.Name == name {
			return len(bundle.Packages)
		}
	}
	t.Fatalf("bundle %q not found", name)
	return 0
}

func bundleHasPackageForTest(t *testing.T, dir, bundleName, packageName string) bool {
	t.Helper()
	for _, bundle := range loadCatalogForBundleTest(t, dir).Bundles() {
		if bundle.Name != bundleName {
			continue
		}
		for _, name := range bundle.Packages {
			if name == packageName {
				return true
			}
		}
		return false
	}
	t.Fatalf("bundle %q not found", bundleName)
	return false
}

func loadCatalogForBundleTest(t *testing.T, dir string) *policy.Catalog {
	t.Helper()
	cat, err := policy.LoadCatalogFile(filepath.Join(dir, "catalog.json"))
	if err != nil {
		t.Fatalf("load temp catalog: %v", err)
	}
	return cat
}
