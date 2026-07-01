package cli

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/policy"
)

type fixtureFetcher map[string][]byte

func (f fixtureFetcher) Get(url string) ([]byte, error) {
	b, ok := f[url]
	if !ok {
		return nil, fmt.Errorf("fixture fetcher: missing %s", url)
	}
	return append([]byte(nil), b...), nil
}

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

func TestCatalogBumpOutputJSONWritesCatalogArtifacts(t *testing.T) {
	dir := tempCatalogDirForTest(t)
	target := "22.23.2"
	amd64SHA := cliDigest("node-amd64-22.23.2")
	arm64SHA := cliDigest("node-arm64-22.23.2")
	withCatalogFetcher(t, fixtureFetcher{
		nodeManifestURLForTest(t, target): nodeManifestForTest(t, target, amd64SHA, arm64SHA),
	})

	beforeCue := mustReadFileForTest(t, filepath.Join(dir, "catalog.cue"))
	out, err := runRootForTest(t, dir, "catalog", "bump", "node", "--to", target, "--catalog-dir", dir, "--output", "json")
	if err != nil {
		t.Fatalf("catalog bump --output json: %v\nout=%s", err, out)
	}
	env := parseEnvelopeForTest(t, out)
	if !env.OK {
		t.Fatalf("catalog bump returned error envelope: %+v", env.Errors)
	}
	if _, ok := env.Data["plan_sheet"].(map[string]any); !ok {
		t.Fatalf("data.plan_sheet missing or not object: %#v", env.Data)
	}
	written, ok := env.Data["written"].([]any)
	if !ok || len(written) != 2 {
		t.Fatalf("data.written malformed: %#v", env.Data["written"])
	}

	cat, err := policy.LoadCatalogFile(filepath.Join(dir, "catalog.json"))
	if err != nil {
		t.Fatalf("reload mutated catalog: %v", err)
	}
	node, ok := cat.Lookup("node")
	if !ok {
		t.Fatal("mutated catalog missing node")
	}
	if node.Version != target {
		t.Fatalf("node version = %q, want %q", node.Version, target)
	}
	if node.SHA256["amd64"] != amd64SHA || node.SHA256["arm64"] != arm64SHA {
		t.Fatalf("node sha = %v, want amd64=%s arm64=%s", node.SHA256, amd64SHA, arm64SHA)
	}
	afterCue := mustReadFileForTest(t, filepath.Join(dir, "catalog.cue"))
	if string(afterCue) == string(beforeCue) || !strings.Contains(string(afterCue), target) {
		t.Fatalf("catalog.cue was not rewritten with target %s", target)
	}
}

func TestCatalogBumpOlderVersionReturnsErrorEnvelopeAndDoesNotWrite(t *testing.T) {
	dir := tempCatalogDirForTest(t)
	target := "22.23.0"
	withCatalogFetcher(t, fixtureFetcher{
		nodeManifestURLForTest(t, target): nodeManifestForTest(t, target, cliDigest("old-amd64"), cliDigest("old-arm64")),
	})
	jsonPath := filepath.Join(dir, "catalog.json")
	before := mustReadFileForTest(t, jsonPath)

	out, err := runRootForTest(t, dir, "catalog", "bump", "node", "--to", target, "--catalog-dir", dir, "--output", "json")
	if err == nil {
		t.Fatalf("catalog bump to older version unexpectedly succeeded: %s", out)
	}
	env := parseEnvelopeForTest(t, out)
	if env.OK || len(env.Errors) == 0 || !strings.Contains(env.Errors[0].Message, "monotonic floor") {
		t.Fatalf("wrong error envelope for older bump: %+v", env)
	}
	after := mustReadFileForTest(t, jsonPath)
	if string(after) != string(before) {
		t.Fatal("catalog.json changed after refused older bump")
	}
}

func TestCatalogProposeVersionOutputJSON(t *testing.T) {
	dir := tempCatalogDirForTest(t)
	target := "22.23.2"
	fixtures := defaultCatalogFetchFixtures(t)
	fixtures[nodeUpstreamURLForTest(t)] = []byte(`[{"version":"v22.23.1"},{"version":"v22.23.2"}]`)
	fixtures[nodeManifestURLForTest(t, target)] = nodeManifestForTest(t, target, cliDigest("propose-amd64"), cliDigest("propose-arm64"))
	withCatalogFetcher(t, fixtures)

	before := mustReadFileForTest(t, filepath.Join(dir, "catalog.json"))
	out, err := runRootForTest(t, dir, "catalog", "propose-version", "node", "--catalog-dir", dir, "--output", "json")
	if err != nil {
		t.Fatalf("catalog propose-version --output json: %v\nout=%s", err, out)
	}
	env := parseEnvelopeForTest(t, out)
	if !env.OK {
		t.Fatalf("catalog propose-version returned error envelope: %+v", env.Errors)
	}
	candidates, ok := env.Data["candidates"].([]any)
	if !ok || len(candidates) == 0 {
		t.Fatalf("data.candidates malformed: %#v", env.Data)
	}
	if after := mustReadFileForTest(t, filepath.Join(dir, "catalog.json")); string(after) != string(before) {
		t.Fatal("catalog propose-version wrote to catalog.json")
	}
}

func TestCatalogAuditOutputJSON(t *testing.T) {
	dir := tempCatalogDirForTest(t)
	withCatalogFetcher(t, defaultCatalogFetchFixtures(t))

	before := mustReadFileForTest(t, filepath.Join(dir, "catalog.json"))
	out, err := runRootForTest(t, dir, "catalog", "audit", "--catalog-dir", dir, "--output", "json")
	if err != nil {
		t.Fatalf("catalog audit --output json: %v\nout=%s", err, out)
	}
	env := parseEnvelopeForTest(t, out)
	if !env.OK {
		t.Fatalf("catalog audit returned error envelope: %+v", env.Errors)
	}
	report, ok := env.Data["report"].(map[string]any)
	if !ok || report["Rows"] == nil || report["Summary"] == nil {
		t.Fatalf("data.report malformed: %#v", env.Data["report"])
	}
	if after := mustReadFileForTest(t, filepath.Join(dir, "catalog.json")); string(after) != string(before) {
		t.Fatal("catalog audit wrote to catalog.json")
	}
}

func TestCatalogAddOutputJSONWritesPackage(t *testing.T) {
	dir := tempCatalogDirForTest(t)
	out, err := runRootForTest(t, dir, "catalog", "add", "cli-test-npm", "--kind", "npm", "--version", "1.0.0", "--catalog-dir", dir, "--output", "json")
	if err != nil {
		t.Fatalf("catalog add --output json: %v\nout=%s", err, out)
	}
	env := parseEnvelopeForTest(t, out)
	if !env.OK || env.Data["package"] != "cli-test-npm" || env.Data["added"] != true {
		t.Fatalf("wrong catalog add envelope: %+v", env)
	}
	cat, err := policy.LoadCatalogFile(filepath.Join(dir, "catalog.json"))
	if err != nil {
		t.Fatalf("reload catalog: %v", err)
	}
	pkg, ok := cat.Lookup("cli-test-npm")
	if !ok {
		t.Fatal("catalog add did not persist cli-test-npm")
	}
	if pkg.Kind != policy.KindNpm || pkg.Version != "1.0.0" {
		t.Fatalf("cli-test-npm = %#v, want npm 1.0.0", pkg)
	}
}

func tempCatalogDirForTest(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"catalog.json", "catalog.cue"} {
		src := filepath.Join("..", "engine", "policy", name)
		b, err := os.ReadFile(src)
		if err != nil {
			t.Fatalf("read %s: %v", src, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatalf("write temp %s: %v", name, err)
		}
	}
	return dir
}

func withCatalogFetcher(t *testing.T, fetcher policy.Fetcher) {
	t.Helper()
	old := catalogFetcher
	catalogFetcher = fetcher
	t.Cleanup(func() { catalogFetcher = old })
}

func defaultCatalogFetchFixtures(t *testing.T) fixtureFetcher {
	t.Helper()
	fixtures := fixtureFetcher{policy.AdvisoryURL: []byte(`{"packages":{}}`)}
	for _, pkg := range policy.DefaultCatalog().Packages() {
		if pkg.Upstream == nil || pkg.Upstream.URL == "" {
			continue
		}
		switch pkg.Upstream.Kind {
		case "github-releases":
			fixtures[pkg.Upstream.URL] = []byte(fmt.Sprintf(`[{"tag_name":"v%s"}]`, pkg.Version))
		case "npm-registry":
			fixtures[pkg.Upstream.URL] = []byte(fmt.Sprintf(`{"versions":{%q:{}}}`, pkg.Version))
		case "node-dist":
			fixtures[pkg.Upstream.URL] = []byte(fmt.Sprintf(`[{"version":"v%s"}]`, pkg.Version))
		case "pypi":
			fixtures[pkg.Upstream.URL] = []byte(fmt.Sprintf(`{"info":{"version":%q},"releases":{%q:[]}}`, pkg.Version, pkg.Version))
		case "debian-snapshot":
			fixtures[pkg.Upstream.URL] = []byte(fmt.Sprintf("20260701T000000Z %s\n", pkg.Version))
		case "url-regex":
			fixtures[pkg.Upstream.URL] = []byte(fmt.Sprintf("release %s\n", pkg.Version))
		default:
			t.Fatalf("unhandled upstream kind %q for %s", pkg.Upstream.Kind, pkg.Name)
		}
	}
	return fixtures
}

func nodeUpstreamURLForTest(t *testing.T) string {
	t.Helper()
	return nodePackageForTest(t).Upstream.URL
}

func nodeManifestURLForTest(t *testing.T, target string) string {
	t.Helper()
	node := nodePackageForTest(t)
	return strings.ReplaceAll(node.Upstream.ManifestURL, "{version}", target)
}

func nodeManifestForTest(t *testing.T, target, amd64SHA, arm64SHA string) []byte {
	t.Helper()
	node := nodePackageForTest(t)
	entries := map[string]string{}
	for arch, digest := range map[string]string{"amd64": amd64SHA, "arm64": arm64SHA} {
		asset := node.Upstream.Asset[arch]
		asset = strings.ReplaceAll(asset, "{version}", target)
		asset = strings.ReplaceAll(asset, "{name}", node.Name)
		entries[path.Base(asset)] = digest
	}
	return []byte(cliManifestFixture(entries))
}

func nodePackageForTest(t *testing.T) policy.Package {
	t.Helper()
	node, ok := policy.DefaultCatalog().Lookup("node")
	if !ok || node.Upstream == nil || node.Upstream.ManifestURL == "" {
		t.Fatal("default catalog node package must have a manifest upstream")
	}
	return node
}

func cliManifestFixture(entries map[string]string) string {
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, name := range names {
		fmt.Fprintf(&b, "%s  %s\n", entries[name], name)
	}
	return b.String()
}

func cliDigest(label string) string {
	sum := sha256.Sum256([]byte(label))
	return fmt.Sprintf("%x", sum[:])
}

func mustReadFileForTest(t *testing.T, file string) []byte {
	t.Helper()
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read %s: %v", file, err)
	}
	return b
}
