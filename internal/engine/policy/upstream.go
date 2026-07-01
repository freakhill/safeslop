package policy

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
)

// AdvisoryURL is the optional v1 advisory feed hook for Audit/ProposeVersions. A
// Fetcher may return JSON shaped as:
//
//	{"packages":{"pkg":{"yanked":["1.2.3"],"unmaintained":true,"cve":true}}}
//
// The pseudo-URL keeps specs/0059 W5 behind D2's Fetcher seam without pretending the
// future signed feed exists: fixtures populate it; production fetchers may return an
// error, which means "no advisory data supplied".
const AdvisoryURL = "safeslop://catalog-advisories/v1"

// Candidate is one upstream version discovered for a catalog package. It is read-only
// proposal data: bump remains the mutating LAW-A/B/C/D gate (specs/0059 W4/W5).
type Candidate struct {
	Version              string
	Magnitude            MagnitudeKind
	WouldBeSHA256        map[string]string
	RequiresHumanConfirm bool
	Source               string
	IsYanked             bool
}

// Report is the read-only catalog audit output (specs/0059 W5; canon: staleness,
// yanked/revoked, unmaintained, and lane assignment are audit tiers, not mutations).
type Report struct {
	Rows    []ReportRow
	Summary ReportSummary
}

// ReportRow is one package's audit result. Note carries v1-gate skips such as
// "no upstream (audit skipped)" without overloading Latest/VersionsBehind.
type ReportRow struct {
	Name                 string
	Current              string
	Latest               string
	VersionsBehind       int
	MagnitudeToLatest    MagnitudeKind
	RequiresHumanConfirm bool
	Unmaintained         bool
	IsYanked             bool
	SuggestedLane        string
	BlastRadius          []string
	Note                 string
}

// ReportSummary aggregates the package rows for human review dashboards.
type ReportSummary struct {
	TotalPackages int
	Behind        int
	Yanked        int
	Unmaintained  int
}

type upstreamStrategy func([]byte) ([]string, error)

var upstreamStrategies = map[string]upstreamStrategy{
	"github-releases": parseGitHubReleaseVersions,
	"npm-registry":    parseNPMRegistryVersions,
	"node-dist":       parseNodeDistVersions,
	"pypi":            parsePyPIVersions,
	"debian-snapshot": parseDebianSnapshotVersions,
	"url-regex":       parseURLRegexVersions,
}

// ProposeVersions discovers newer upstream candidates for name, newest first. The v1
// gate is intentional: without Upstream metadata there is no mechanical discovery
// source, so propose is a no-op/error rather than guessing from package names.
func ProposeVersions(cat *Catalog, name string, fetcher Fetcher) ([]Candidate, error) {
	if cat == nil {
		return nil, fmt.Errorf("propose: nil catalog")
	}
	pkg, ok := cat.Lookup(name)
	if !ok {
		return nil, fmt.Errorf("propose: unknown package %q", name)
	}
	if pkg.Upstream == nil {
		return nil, fmt.Errorf("propose: package %q has no upstream", name)
	}
	if fetcher == nil {
		return nil, fmt.Errorf("propose: nil fetcher")
	}

	versions, err := orderedUpstreamVersions(pkg, fetcher)
	if err != nil {
		return nil, err
	}
	current, err := parsePackageVersion(pkg, pkg.Version)
	if err != nil {
		return nil, fmt.Errorf("propose: current version for %q: %w", name, err)
	}
	advisories, err := loadAdvisories(fetcher)
	if err != nil {
		return nil, err
	}
	adv := advisories.Packages[pkg.Name]

	out := make([]Candidate, 0, len(versions))
	for _, v := range versions {
		if Compare(v.parsed, current) <= 0 {
			continue
		}
		sha, err := resolveWouldBeSHA256(pkg, v.raw, fetcher)
		if err != nil {
			return nil, err
		}
		out = append(out, Candidate{
			Version:              v.raw,
			Magnitude:            Magnitude(current, v.parsed),
			WouldBeSHA256:        sha,
			RequiresHumanConfirm: requiresHumanConfirm(pkg, v.raw),
			Source:               pkg.Upstream.Kind,
			IsYanked:             adv.yanks(v.raw),
		})
	}
	return out, nil
}

// Audit reports catalog staleness and advisory tiers without mutating catalog data. A
// package without Upstream gets an explicit skipped row, honoring the canon's v1-gate:
// no upstream discovery means no invented freshness signal.
func Audit(cat *Catalog, fetcher Fetcher) (*Report, error) {
	if cat == nil {
		return nil, fmt.Errorf("audit: nil catalog")
	}
	if fetcher == nil {
		return nil, fmt.Errorf("audit: nil fetcher")
	}
	advisories, err := loadAdvisories(fetcher)
	if err != nil {
		return nil, err
	}

	report := &Report{}
	for _, pkg := range cat.Packages() {
		row := ReportRow{
			Name:              pkg.Name,
			Current:           pkg.Version,
			MagnitudeToLatest: MagNone,
			SuggestedLane:     "default",
			BlastRadius:       ReverseClosure(cat, pkg.Name),
		}
		adv := advisories.Packages[pkg.Name]
		row.Unmaintained = adv.Unmaintained
		row.IsYanked = adv.yanks(pkg.Version)
		if row.IsYanked || adv.hasCVE() {
			row.SuggestedLane = "security"
		}
		if pkg.Upstream == nil {
			row.Note = "no upstream (audit skipped)"
			report.addRow(row)
			continue
		}

		versions, err := orderedUpstreamVersions(pkg, fetcher)
		if err != nil {
			return nil, err
		}
		current, err := parsePackageVersion(pkg, pkg.Version)
		if err != nil {
			return nil, fmt.Errorf("audit: current version for %q: %w", pkg.Name, err)
		}
		if len(versions) == 0 {
			row.Note = "no upstream candidates (audit skipped)"
			report.addRow(row)
			continue
		}

		row.Latest = versions[0].raw
		row.RequiresHumanConfirm = requiresHumanConfirm(pkg, row.Latest)
		for _, v := range versions {
			if Compare(v.parsed, current) > 0 {
				row.VersionsBehind++
			}
		}
		if latest := versions[0].parsed; Compare(latest, current) > 0 {
			row.MagnitudeToLatest = Magnitude(current, latest)
		}
		report.addRow(row)
	}
	return report, nil
}

func (r *Report) addRow(row ReportRow) {
	r.Rows = append(r.Rows, row)
	r.Summary.TotalPackages++
	if row.VersionsBehind > 0 {
		r.Summary.Behind++
	}
	if row.IsYanked {
		r.Summary.Yanked++
	}
	if row.Unmaintained {
		r.Summary.Unmaintained++
	}
}

type orderedVersion struct {
	raw    string
	parsed Version
}

func orderedUpstreamVersions(pkg Package, fetcher Fetcher) ([]orderedVersion, error) {
	raw, err := listUpstreamVersions(pkg.Upstream, fetcher)
	if err != nil {
		return nil, fmt.Errorf("upstream: list versions for %q: %w", pkg.Name, err)
	}
	current, err := parsePackageVersion(pkg, pkg.Version)
	if err != nil {
		return nil, err
	}
	out := make([]orderedVersion, 0, len(raw))
	for _, s := range raw {
		parsed, err := parsePackageVersion(pkg, s)
		if err != nil || parsed.Scheme != current.Scheme {
			continue
		}
		out = append(out, orderedVersion{raw: s, parsed: parsed})
	}
	sort.Slice(out, func(i, j int) bool {
		cmp := Compare(out[i].parsed, out[j].parsed)
		if cmp == 0 {
			return out[i].raw > out[j].raw
		}
		return cmp > 0
	})
	return out, nil
}

func parsePackageVersion(pkg Package, s string) (Version, error) {
	scheme := InferScheme(pkg.Kind, s)
	return Parse(s, scheme)
}

func requiresHumanConfirm(pkg Package, version string) bool {
	return InferScheme(pkg.Kind, version) != SchemeSemver || !IsStableChannel(version)
}

func listUpstreamVersions(upstream *Upstream, fetcher Fetcher) ([]string, error) {
	if upstream == nil {
		return nil, nil
	}
	kind := strings.TrimSpace(upstream.Kind)
	strategy, ok := upstreamStrategies[kind]
	if kind == "" || !ok {
		return nil, nil
	}
	if strings.TrimSpace(upstream.URL) == "" {
		return nil, nil
	}
	if fetcher == nil {
		return nil, fmt.Errorf("nil fetcher")
	}
	body, err := fetcher.Get(upstream.URL)
	if err != nil {
		return nil, fmt.Errorf("fetch %s %q: %w", kind, upstream.URL, err)
	}
	versions, err := strategy(body)
	if err != nil {
		return nil, err
	}
	return uniqueNormalizedVersions(versions), nil
}

func parseGitHubReleaseVersions(body []byte) ([]string, error) {
	var releases []struct {
		TagName string `json:"tag_name"`
	}
	if err := json.Unmarshal(body, &releases); err == nil {
		out := make([]string, 0, len(releases))
		for _, r := range releases {
			out = append(out, r.TagName)
		}
		return out, nil
	}
	var page struct {
		Payload struct {
			Preload struct {
				Releases struct {
					Edges []struct {
						Node struct {
							TagName string `json:"tagName"`
						} `json:"node"`
					} `json:"edges"`
				} `json:"releases"`
			} `json:"preload"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		return nil, fmt.Errorf("github-releases: decode releases JSON: %w", err)
	}
	out := make([]string, 0, len(page.Payload.Preload.Releases.Edges))
	for _, e := range page.Payload.Preload.Releases.Edges {
		out = append(out, e.Node.TagName)
	}
	return out, nil
}

func parseNPMRegistryVersions(body []byte) ([]string, error) {
	var registry struct {
		Versions map[string]json.RawMessage `json:"versions"`
	}
	if err := json.Unmarshal(body, &registry); err != nil {
		return nil, fmt.Errorf("npm-registry: decode registry JSON: %w", err)
	}
	out := make([]string, 0, len(registry.Versions))
	for v := range registry.Versions {
		out = append(out, v)
	}
	return out, nil
}

func parseNodeDistVersions(body []byte) ([]string, error) {
	var entries []struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(body, &entries); err != nil {
		return nil, fmt.Errorf("node-dist: decode index JSON: %w", err)
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Version)
	}
	return out, nil
}

func parsePyPIVersions(body []byte) ([]string, error) {
	var api struct {
		Info struct {
			Version string `json:"version"`
		} `json:"info"`
		Releases map[string]json.RawMessage `json:"releases"`
	}
	if err := json.Unmarshal(body, &api); err != nil {
		return nil, fmt.Errorf("pypi: decode API JSON: %w", err)
	}
	out := make([]string, 0, len(api.Releases)+1)
	for v := range api.Releases {
		out = append(out, v)
	}
	if api.Info.Version != "" {
		out = append(out, api.Info.Version)
	}
	return out, nil
}

func parseDebianSnapshotVersions(body []byte) ([]string, error) {
	var api struct {
		Result []struct {
			Version string `json:"version"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &api); err == nil && len(api.Result) > 0 {
		out := make([]string, 0, len(api.Result))
		for _, r := range api.Result {
			out = append(out, r.Version)
		}
		return out, nil
	}

	// v1 fixture contract (specs/0059 W5): text lines are either
	//   <timestamp> <version>
	// or a bare <version>; blank lines and # comments are ignored. The timestamp is
	// audit provenance for future LAW-C wiring, not part of the Version field here.
	var out []string
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 1 {
			out = append(out, fields[0])
			continue
		}
		out = append(out, fields[1])
	}
	return out, nil
}

var urlVersionTokenRE = regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9])(?:v|go)?([0-9]+(?::[0-9]+)?(?:\.[0-9]+){1,3}(?:(?:-(?:[0-9][0-9A-Za-z.+~_-]*))|(?:[-.]?(?:rc|beta|alpha|pre|dev|nightly)[0-9A-Za-z.+~_-]*))?)`)

func parseURLRegexVersions(body []byte) ([]string, error) {
	// v1 scrape regex (specs/0059 W5): find tokens with optional v/go prefix, 2-4
	// dotted numeric components, optional Debian numeric revision, or an unstable
	// channel suffix. Asset/platform suffixes such as "-linux-x64" are deliberately
	// not captured; this is livecheck-only discovery, not proof of compatibility.
	matches := urlVersionTokenRE.FindAllStringSubmatch(string(body), -1)
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		if len(m) > 1 {
			out = append(out, m[1])
		}
	}
	return out, nil
}

func uniqueNormalizedVersions(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, raw := range in {
		v := normalizeVersionToken(raw)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

func normalizeVersionToken(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.Trim(s, `"'`)
	s = strings.TrimPrefix(s, "refs/tags/")
	if m := urlVersionTokenRE.FindStringSubmatch(s); len(m) > 1 {
		return m[1]
	}
	lower := strings.ToLower(s)
	if strings.HasPrefix(lower, "go") && len(s) > 2 && isASCIIDigit(s[2]) {
		return s[2:]
	}
	if strings.HasPrefix(lower, "v") && len(s) > 1 && isASCIIDigit(s[1]) {
		return s[1:]
	}
	return s
}

func isASCIIDigit(b byte) bool { return b >= '0' && b <= '9' }

func substituteManifestTemplate(template, version string, pkg Package) string {
	out := substituteVersion(template, version)
	return strings.ReplaceAll(out, "{name}", pkg.Name)
}

func upstreamAssetURLForArch(pkg Package, arch, target string) (string, error) {
	if pkg.Upstream == nil || pkg.Upstream.Asset == nil {
		return "", fmt.Errorf("law-A: package %q has no upstream asset templates", pkg.Name)
	}
	template, ok := pkg.Upstream.Asset[arch]
	if !ok || strings.TrimSpace(template) == "" {
		return "", fmt.Errorf("law-A: package %q has no upstream asset template for %s", pkg.Name, arch)
	}
	out := substituteVersion(template, target)
	out = strings.ReplaceAll(out, "{name}", pkg.Name)
	return out, nil
}

func resolveWouldBeSHA256(pkg Package, version string, fetcher Fetcher) (map[string]string, error) {
	if pkg.Kind != KindBinary || pkg.Upstream == nil || pkg.Upstream.ManifestURL == "" || len(pkg.Upstream.Asset) == 0 || fetcher == nil {
		return nil, nil
	}
	manifestURL := substituteManifestTemplate(pkg.Upstream.ManifestURL, version, pkg)
	body, err := fetcher.Get(manifestURL)
	if err != nil {
		return nil, nil
	}
	manifest := parseChecksumManifest(body)
	if len(manifest) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(buildArches))
	for _, arch := range buildArches {
		assetURL, err := upstreamAssetURLForArch(pkg, arch, version)
		if err != nil {
			return nil, nil
		}
		sha, ok := manifest[path.Base(assetURL)]
		if !ok || !isHex64(sha) || sha == sha256Unresolved {
			return nil, nil
		}
		out[arch] = sha
	}
	return out, nil
}

type advisoryDocument struct {
	Packages map[string]packageAdvisory `json:"packages"`
}

type packageAdvisory struct {
	Yanked       []string `json:"yanked"`
	Unmaintained bool     `json:"unmaintained"`
	CVE          bool     `json:"cve"`
	CVEs         []string `json:"cves"`
}

func loadAdvisories(fetcher Fetcher) (advisoryDocument, error) {
	doc := advisoryDocument{Packages: map[string]packageAdvisory{}}
	if fetcher == nil {
		return doc, nil
	}
	body, err := fetcher.Get(AdvisoryURL)
	if err != nil {
		return doc, nil
	}
	if strings.TrimSpace(string(body)) == "" {
		return doc, nil
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		// Advisories are optional enrichment; any failure degrades to no-advisory (specs/0059 W5).
		return advisoryDocument{Packages: map[string]packageAdvisory{}}, nil
	}
	if doc.Packages == nil {
		doc.Packages = map[string]packageAdvisory{}
	}
	return doc, nil
}

func (a packageAdvisory) yanks(version string) bool {
	for _, y := range a.Yanked {
		if y == "*" || normalizeVersionToken(y) == normalizeVersionToken(version) {
			return true
		}
	}
	return false
}

func (a packageAdvisory) hasCVE() bool { return a.CVE || len(a.CVEs) > 0 }
