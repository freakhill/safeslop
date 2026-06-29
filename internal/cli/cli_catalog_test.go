package cli

import "testing"

func TestCatalogListPackagesEnvelope(t *testing.T) {
	out, err := runRootForTest(t, t.TempDir(), "catalog", "list", "--output", "json")
	if err != nil {
		t.Fatalf("catalog list --output json: %v", err)
	}
	env := parseEnvelopeForTest(t, out)
	if !env.OK {
		t.Fatalf("catalog list returned error envelope: %+v", env.Errors)
	}
	packages, ok := env.Data["packages"].([]any)
	if !ok {
		t.Fatalf("data.packages is not an array: %#v", env.Data)
	}
	if len(packages) == 0 {
		t.Fatal("catalog list returned no packages")
	}
	seen := map[string]bool{}
	for _, raw := range packages {
		pkg, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("package entry is not an object: %#v", raw)
		}
		name, _ := pkg["name"].(string)
		seen[name] = true
		if pkg["version"] == "" || pkg["kind"] == "" {
			t.Fatalf("package %q missing kind/version: %#v", name, pkg)
		}
	}
	for _, want := range []string{"claude-code", "node"} {
		if !seen[want] {
			t.Fatalf("missing package %q in catalog list: %v", want, seen)
		}
	}
}

func TestCatalogListBundlesEnvelope(t *testing.T) {
	out, err := runRootForTest(t, t.TempDir(), "catalog", "list", "--bundles", "--output", "json")
	if err != nil {
		t.Fatalf("catalog list --bundles --output json: %v", err)
	}
	env := parseEnvelopeForTest(t, out)
	if !env.OK {
		t.Fatalf("catalog list --bundles returned error envelope: %+v", env.Errors)
	}
	bundles, ok := env.Data["bundles"].([]any)
	if !ok {
		t.Fatalf("data.bundles is not an array: %#v", env.Data)
	}
	seen := map[string]bool{}
	for _, raw := range bundles {
		bundle, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("bundle entry is not an object: %#v", raw)
		}
		name, _ := bundle["name"].(string)
		seen[name] = true
		if bundle["description"] == "" {
			t.Fatalf("bundle %q missing description: %#v", name, bundle)
		}
		pkgs, ok := bundle["packages"].([]any)
		if !ok || len(pkgs) == 0 {
			t.Fatalf("bundle %q packages malformed: %#v", name, bundle["packages"])
		}
	}
	for _, want := range []string{"claude", "pi", "python"} {
		if !seen[want] {
			t.Fatalf("missing bundle %q in catalog list --bundles: %v", want, seen)
		}
	}
}

func TestCatalogListRequiresOutputJSON(t *testing.T) {
	if _, err := runRootForTest(t, t.TempDir(), "catalog", "list"); err == nil {
		t.Fatal("catalog list without --output json should error")
	}
}
