package policy

import (
	"reflect"
	"strings"
	"testing"
)

func TestProposeVersionsRequiresUpstreamGate(t *testing.T) {
	cat := newCatalog([]Package{{Name: "local", Kind: KindNpm, Version: "1.0.0"}}, nil, nil)
	if err := cat.Validate(); err != nil {
		t.Fatalf("test catalog invalid: %v", err)
	}
	got, err := ProposeVersions(cat, "local", fixtureFetcher{})
	if err == nil || !strings.Contains(err.Error(), `propose: package "local" has no upstream`) {
		t.Fatalf("ProposeVersions error = %v, want no-upstream gate", err)
	}
	if got != nil {
		t.Fatalf("candidates = %#v, want nil", got)
	}
}

func TestProposeVersionsListsNewestFirstWithSignedManifestSHA(t *testing.T) {
	cat := newCatalog([]Package{
		{
			Name:    "node",
			Kind:    KindBinary,
			Version: "1.2.3",
			SHA256:  map[string]string{"amd64": bumpDigest("a"), "arm64": bumpDigest("b")},
			Upstream: &Upstream{
				Kind:        "node-dist",
				URL:         "https://node.example.invalid/index.json",
				ManifestURL: "https://node.example.invalid/v{version}/SHASUMS256.txt",
				Asset: map[string]string{
					"amd64": "https://node.example.invalid/v{version}/node-v{version}-linux-x64.tar.xz",
					"arm64": "https://node.example.invalid/v{version}/node-v{version}-linux-arm64.tar.xz",
				},
			},
		},
	}, nil, nil)
	if err := cat.Validate(); err != nil {
		t.Fatalf("test catalog invalid: %v", err)
	}

	sha130 := map[string]string{"amd64": bumpDigest("c"), "arm64": bumpDigest("d")}
	sha124 := map[string]string{"amd64": bumpDigest("e"), "arm64": bumpDigest("f")}
	fetcher := fixtureFetcher{
		"https://node.example.invalid/index.json": []byte(`[
			{"version":"v1.2.3"},
			{"version":"v1.2.4"},
			{"version":"v1.3.0"}
		]`),
		"https://node.example.invalid/v1.3.0/SHASUMS256.txt": []byte(manifestFixture(map[string]string{
			"node-v1.3.0-linux-x64.tar.xz":   sha130["amd64"],
			"node-v1.3.0-linux-arm64.tar.xz": sha130["arm64"],
		})),
		"https://node.example.invalid/v1.2.4/SHASUMS256.txt": []byte(manifestFixture(map[string]string{
			"node-v1.2.4-linux-x64.tar.xz":   sha124["amd64"],
			"node-v1.2.4-linux-arm64.tar.xz": sha124["arm64"],
		})),
	}

	got, err := ProposeVersions(cat, "node", fetcher)
	if err != nil {
		t.Fatalf("ProposeVersions returned error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("candidate count = %d, want 2: %#v", len(got), got)
	}
	if got[0].Version != "1.3.0" || got[1].Version != "1.2.4" {
		t.Fatalf("versions = [%s %s], want [1.3.0 1.2.4]", got[0].Version, got[1].Version)
	}
	if got[0].Magnitude != MagMinor || got[1].Magnitude != MagPatch {
		t.Fatalf("magnitudes = [%s %s], want [minor patch]", got[0].Magnitude, got[1].Magnitude)
	}
	if got[0].RequiresHumanConfirm || got[1].RequiresHumanConfirm {
		t.Fatalf("semver stable candidates unexpectedly require human confirm: %#v", got)
	}
	if got[0].Source != "node-dist" {
		t.Fatalf("source = %q, want node-dist", got[0].Source)
	}
	if !reflect.DeepEqual(got[0].WouldBeSHA256, sha130) {
		t.Fatalf("1.3.0 sha = %v, want %v", got[0].WouldBeSHA256, sha130)
	}
}

func TestProposeVersionsListsGitHubAndNPMCandidates(t *testing.T) {
	cat := newCatalog([]Package{
		{
			Name:    "tool",
			Kind:    KindBinary,
			Version: "1.0.0",
			SHA256:  map[string]string{"amd64": bumpDigest("a"), "arm64": bumpDigest("b")},
			Upstream: &Upstream{
				Kind:        "github-releases",
				URL:         "https://api.example.invalid/repos/acme/tool/releases",
				ManifestURL: "https://downloads.example.invalid/tool/v{version}/SHASUMS256.txt",
				Asset: map[string]string{
					"amd64": "https://downloads.example.invalid/tool/v{version}/tool-v{version}-linux-amd64.tar.gz",
					"arm64": "https://downloads.example.invalid/tool/v{version}/tool-v{version}-linux-arm64.tar.gz",
				},
			},
		},
		{
			Name:    "cli",
			Kind:    KindNpm,
			Version: "2.0.0",
			Upstream: &Upstream{
				Kind: "npm-registry",
				URL:  "https://registry.example.invalid/cli",
			},
		},
	}, nil, nil)
	if err := cat.Validate(); err != nil {
		t.Fatalf("test catalog invalid: %v", err)
	}
	sha := map[string]string{"amd64": bumpDigest("c"), "arm64": bumpDigest("d")}
	fetcher := fixtureFetcher{
		"https://api.example.invalid/repos/acme/tool/releases": []byte(`[{"tag_name":"v1.0.1"},{"tag_name":"v1.1.0"}]`),
		"https://downloads.example.invalid/tool/v1.1.0/SHASUMS256.txt": []byte(manifestFixture(map[string]string{
			"tool-v1.1.0-linux-amd64.tar.gz": sha["amd64"],
			"tool-v1.1.0-linux-arm64.tar.gz": sha["arm64"],
		})),
		"https://downloads.example.invalid/tool/v1.0.1/SHASUMS256.txt": []byte(manifestFixture(map[string]string{
			"tool-v1.0.1-linux-amd64.tar.gz": bumpDigest("e"),
			"tool-v1.0.1-linux-arm64.tar.gz": bumpDigest("f"),
		})),
		"https://registry.example.invalid/cli": []byte(`{"versions":{"2.0.0":{},"2.0.1":{},"2.1.0":{}}}`),
	}

	github, err := ProposeVersions(cat, "tool", fetcher)
	if err != nil {
		t.Fatalf("ProposeVersions github returned error: %v", err)
	}
	if len(github) != 2 || github[0].Version != "1.1.0" || github[0].Magnitude != MagMinor || !reflect.DeepEqual(github[0].WouldBeSHA256, sha) {
		t.Fatalf("github candidates = %#v, want 1.1.0 first with sha %v", github, sha)
	}
	npm, err := ProposeVersions(cat, "cli", fetcher)
	if err != nil {
		t.Fatalf("ProposeVersions npm returned error: %v", err)
	}
	if len(npm) != 2 || npm[0].Version != "2.1.0" || npm[1].Version != "2.0.1" || npm[0].WouldBeSHA256 != nil {
		t.Fatalf("npm candidates = %#v, want [2.1.0 2.0.1] without sha", npm)
	}
}

func TestProposeVersionsFlagsDebianRequiresHumanConfirm(t *testing.T) {
	cat := newCatalog([]Package{
		{
			Name:    "python3",
			Kind:    KindApt,
			Version: "3.11.2-6+deb12u1",
			Upstream: &Upstream{
				Kind: "debian-snapshot",
				URL:  "https://snapshot.example.invalid/mr/package/python3.11/",
			},
		},
	}, nil, nil)
	if err := cat.Validate(); err != nil {
		t.Fatalf("test catalog invalid: %v", err)
	}
	fetcher := fixtureFetcher{
		"https://snapshot.example.invalid/mr/package/python3.11/": []byte("# timestamp version\n20260630T000000Z 3.11.2-6+deb12u2\n20260701T000000Z 3.12.0-1\n"),
	}

	got, err := ProposeVersions(cat, "python3", fetcher)
	if err != nil {
		t.Fatalf("ProposeVersions returned error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("candidate count = %d, want 2: %#v", len(got), got)
	}
	for _, c := range got {
		if !c.RequiresHumanConfirm {
			t.Fatalf("Debian candidate %#v should require human confirm", c)
		}
		if c.Source != "debian-snapshot" {
			t.Fatalf("source = %q, want debian-snapshot", c.Source)
		}
	}
}

func TestProposeVersionsFlagsPrereleaseChannelRequiresHumanConfirm(t *testing.T) {
	// LAW-B channel ban applies to propose, not just audit: an unstable semver
	// channel (e.g. 1.2.3-rc1) must surface as RequiresHumanConfirm.
	cat := newCatalog([]Package{
		{
			Name:    "tool",
			Kind:    KindNpm,
			Version: "1.0.0",
			Upstream: &Upstream{
				Kind: "npm-registry",
				URL:  "https://prerelease.example.invalid/tool",
			},
		},
	}, nil, nil)
	if err := cat.Validate(); err != nil {
		t.Fatalf("test catalog invalid: %v", err)
	}
	fetcher := fixtureFetcher{
		"https://prerelease.example.invalid/tool": []byte(`{"versions":{"1.0.0":{},"1.2.3-rc1":{}}}`),
	}

	got, err := ProposeVersions(cat, "tool", fetcher)
	if err != nil {
		t.Fatalf("ProposeVersions returned error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("candidate count = %d, want 1: %#v", len(got), got)
	}
	if got[0].Version != "1.2.3-rc1" {
		t.Fatalf("version = %q, want 1.2.3-rc1", got[0].Version)
	}
	if !got[0].RequiresHumanConfirm {
		t.Fatalf("prerelease candidate %#v should require human confirm (LAW-B channel ban)", got[0])
	}
}

func TestLoadAdvisoriesDegradesOnMalformedJSON(t *testing.T) {
	// Regression guard: advisories are optional enrichment, so a malformed feed
	// must degrade to no-advisory (nil error), never abort propose/audit.
	fetcher := fixtureFetcher{
		AdvisoryURL: []byte(`{"packages": {"plugin": {"yanked": [ this is not json`),
	}
	doc, err := loadAdvisories(fetcher)
	if err != nil {
		t.Fatalf("loadAdvisories on malformed JSON returned error %v, want graceful nil", err)
	}
	if len(doc.Packages) != 0 {
		t.Fatalf("malformed advisory doc = %#v, want empty packages", doc.Packages)
	}
	if doc.Packages == nil {
		t.Fatalf("advisory doc Packages map is nil, want non-nil empty map")
	}
}

func TestUpstreamVersionListingStrategies(t *testing.T) {
	fetcher := fixtureFetcher{
		"https://example.invalid/github": []byte(`[{"tag_name":"v2.0.0"},{"tag_name":"tool-v1.5.0"}]`),
		"https://example.invalid/npm":    []byte(`{"versions":{"1.0.0":{},"1.1.0":{}}}`),
		"https://example.invalid/node":   []byte(`[{"version":"v22.23.1"},{"version":"v22.24.0"}]`),
		"https://example.invalid/pypi":   []byte(`{"info":{"version":"1.2.0"},"releases":{"1.0.0":[],"1.1.0":[]}}`),
		"https://example.invalid/debian": []byte("20260630T000000Z 1:3.11.2-6\n20260701T000000Z 1:3.11.2-7\n"),
		"https://example.invalid/html":   []byte(`<a href="/download/v3.4.5/tool.tar.gz">v3.4.5</a><span>go1.22.3</span>`),
	}
	tests := []struct {
		name string
		up   *Upstream
		want []string
	}{
		{"github", &Upstream{Kind: "github-releases", URL: "https://example.invalid/github"}, []string{"2.0.0", "1.5.0"}},
		{"npm", &Upstream{Kind: "npm-registry", URL: "https://example.invalid/npm"}, []string{"1.0.0", "1.1.0"}},
		{"node", &Upstream{Kind: "node-dist", URL: "https://example.invalid/node"}, []string{"22.23.1", "22.24.0"}},
		{"pypi", &Upstream{Kind: "pypi", URL: "https://example.invalid/pypi"}, []string{"1.0.0", "1.1.0", "1.2.0"}},
		{"debian", &Upstream{Kind: "debian-snapshot", URL: "https://example.invalid/debian"}, []string{"1:3.11.2-6", "1:3.11.2-7"}},
		{"url-regex", &Upstream{Kind: "url-regex", URL: "https://example.invalid/html"}, []string{"3.4.5", "1.22.3"}},
		{"unknown", &Upstream{Kind: "unknown", URL: "https://example.invalid/missing"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := listUpstreamVersions(tt.up, fetcher)
			if err != nil {
				t.Fatalf("listUpstreamVersions returned error: %v", err)
			}
			if !sameStringSet(got, tt.want) {
				t.Fatalf("versions = %v, want set %v", got, tt.want)
			}
		})
	}
}

func TestAuditReportsStaleYankedUnmaintainedAndSkippedRows(t *testing.T) {
	cat := newCatalog([]Package{
		{
			Name:    "node",
			Kind:    KindBinary,
			Version: "1.2.3",
			SHA256:  map[string]string{"amd64": bumpDigest("a"), "arm64": bumpDigest("b")},
			Upstream: &Upstream{
				Kind: "node-dist",
				URL:  "https://audit.example.invalid/node.json",
			},
		},
		{
			Name:     "plugin",
			Kind:     KindNpm,
			Version:  "1.0.0",
			Requires: []string{"node"},
			Upstream: &Upstream{
				Kind: "npm-registry",
				URL:  "https://audit.example.invalid/plugin",
			},
		},
		{
			Name:    "oldlib",
			Kind:    KindPip,
			Version: "0.1.0",
			Upstream: &Upstream{
				Kind: "pypi",
				URL:  "https://audit.example.invalid/oldlib",
			},
		},
		{Name: "local-only", Kind: KindNpm, Version: "1.0.0"},
	}, nil, nil)
	if err := cat.Validate(); err != nil {
		t.Fatalf("test catalog invalid: %v", err)
	}
	fetcher := fixtureFetcher{
		AdvisoryURL: []byte(`{"packages":{"plugin":{"yanked":["1.0.0"],"cve":true},"oldlib":{"unmaintained":true}}}`),
		"https://audit.example.invalid/node.json": []byte(`[{"version":"v1.2.3"},{"version":"v1.2.4"},{"version":"v1.3.0"}]`),
		"https://audit.example.invalid/plugin":    []byte(`{"versions":{"1.0.0":{},"1.0.1":{}}}`),
		"https://audit.example.invalid/oldlib":    []byte(`{"info":{"version":"0.2.0"},"releases":{"0.1.0":[],"0.2.0":[]}}`),
	}

	report, err := Audit(cat, fetcher)
	if err != nil {
		t.Fatalf("Audit returned error: %v", err)
	}
	if report.Summary.TotalPackages != 4 || report.Summary.Behind != 3 || report.Summary.Yanked != 1 || report.Summary.Unmaintained != 1 {
		t.Fatalf("summary = %#v, want total=4 behind=3 yanked=1 unmaintained=1", report.Summary)
	}
	node := findAuditRow(t, report, "node")
	if node.Latest != "1.3.0" || node.VersionsBehind != 2 || node.MagnitudeToLatest != MagMinor {
		t.Fatalf("node row = %#v, want latest 1.3.0 behind 2 minor", node)
	}
	if !reflect.DeepEqual(node.BlastRadius, []string{"plugin"}) {
		t.Fatalf("node blast radius = %v, want [plugin]", node.BlastRadius)
	}
	plugin := findAuditRow(t, report, "plugin")
	if !plugin.IsYanked || plugin.SuggestedLane != "security" || plugin.VersionsBehind != 1 {
		t.Fatalf("plugin row = %#v, want yanked security behind 1", plugin)
	}
	oldlib := findAuditRow(t, report, "oldlib")
	if !oldlib.Unmaintained {
		t.Fatalf("oldlib row = %#v, want unmaintained", oldlib)
	}
	skipped := findAuditRow(t, report, "local-only")
	if !strings.Contains(skipped.Note, "no upstream") || skipped.Latest != "" || skipped.VersionsBehind != 0 {
		t.Fatalf("skipped row = %#v, want no-upstream skipped row", skipped)
	}
}

func findAuditRow(t *testing.T, report *Report, name string) ReportRow {
	t.Helper()
	for _, row := range report.Rows {
		if row.Name == name {
			return row
		}
	}
	t.Fatalf("missing row %q in %#v", name, report.Rows)
	return ReportRow{}
}

func sameStringSet(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := make(map[string]int, len(got))
	for _, s := range got {
		seen[s]++
	}
	for _, s := range want {
		if seen[s] == 0 {
			return false
		}
		seen[s]--
	}
	return true
}
