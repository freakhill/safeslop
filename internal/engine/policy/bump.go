package policy

import (
	"bufio"
	"crypto/sha256"
	"fmt"
	"path"
	"strings"
)

// Fetcher is the live-network seam (specs/0059 D2). Production wires net/http; tests
// inject a fixture fetcher so no test ever touches the network (AGENTS.md hermeticity).
type Fetcher interface {
	Get(url string) ([]byte, error)
}

// Bump orchestrates a catalog package bump behind the Fetcher seam (specs/0059 W4). It
// resolves real per-arch digests before mutation, applies the v1 soak proxy from the
// canon, and delegates LAW-A/B/C/D plus the monotonic floor to BumpPackage.
func Bump(cat *Catalog, name, target, lane string, fetcher Fetcher) (*Catalog, PlanSheet, error) {
	if cat == nil {
		return nil, PlanSheet{}, fmt.Errorf("bump: nil catalog")
	}
	pkg, ok := cat.Lookup(name)
	if !ok {
		return nil, PlanSheet{}, fmt.Errorf("bump: unknown package %q", name)
	}
	if pkg.Upstream == nil {
		return nil, PlanSheet{}, fmt.Errorf("bump: package %q has no upstream; cannot resolve digests", name)
	}
	if fetcher == nil {
		return nil, PlanSheet{}, fmt.Errorf("bump: nil fetcher")
	}

	shaByArch, resolved, err := resolveBumpDigests(pkg, target, fetcher)
	if err != nil {
		return nil, PlanSheet{}, err
	}

	magnitude, err := bumpMagnitude(pkg, target)
	if err != nil {
		return nil, PlanSheet{}, err
	}
	soakRequired := bumpSoakRequired(magnitude)
	laneInfo := parseBumpLane(lane)
	soakSatisfied := !soakRequired
	waivedBy := ""
	if soakRequired {
		if laneInfo.Label != "security" {
			return nil, PlanSheet{}, fmt.Errorf("bump: %s bump requires the security lane (--security) or human confirm; soak not satisfied", magnitude)
		}
		soakSatisfied = true
		waivedBy = "security"
	}

	next, diff, err := BumpPackage(cat, name, target, shaByArch, lane)
	if err != nil {
		return nil, PlanSheet{}, err
	}
	sheet := planSheetFromDiff(diff, resolved, pkg.Upstream, laneInfo, soakRequired, soakSatisfied, waivedBy)
	return next, sheet, nil
}

// bumpResolvedDigests is the resolver's non-digest metadata for the plan sheet. The
// per-arch shas are NOT carried here: they flow as the first return value into
// BumpPackage, and the plan sheet reads them back from the post-gate Diff so it always
// reflects what actually landed (specs/0059 W4; canon: the plan sheet is honest about
// the pinned bytes, never a pre-gate copy).
type bumpResolvedDigests struct {
	Origin             string
	VerificationMethod string
}

func resolveBumpDigests(pkg Package, target string, fetcher Fetcher) (map[string]string, bumpResolvedDigests, error) {
	if pkg.Kind != KindBinary {
		origin := pkg.Upstream.URL
		if origin == "" {
			origin = substituteVersion(pkg.Upstream.ManifestURL, target)
		}
		return nil, bumpResolvedDigests{Origin: origin, VerificationMethod: VerificationSelfComputedWeak}, nil
	}
	if pkg.Upstream.ManifestURL != "" {
		return resolveSignedManifestDigests(pkg, target, fetcher)
	}
	return resolveSelfComputedDigests(pkg, target, fetcher)
}

func resolveSignedManifestDigests(pkg Package, target string, fetcher Fetcher) (map[string]string, bumpResolvedDigests, error) {
	manifestURL := substituteVersion(pkg.Upstream.ManifestURL, target)
	manifestBytes, err := fetcher.Get(manifestURL)
	if err != nil {
		return nil, bumpResolvedDigests{}, fmt.Errorf("bump: fetch manifest %q: %w", manifestURL, err)
	}
	manifest := parseSHASUMS256(manifestBytes)
	shaByArch := make(map[string]string, len(buildArches))
	for _, arch := range buildArches {
		assetURL, err := assetURLForArch(pkg, arch, target)
		if err != nil {
			return nil, bumpResolvedDigests{}, err
		}
		filename := path.Base(assetURL)
		sha, ok := manifest[filename]
		if !ok {
			return nil, bumpResolvedDigests{}, fmt.Errorf("law-A: package %q manifest %q is missing %s digest for %s", pkg.Name, manifestURL, arch, filename)
		}
		if !isHex64(sha) || sha == sha256Unresolved {
			return nil, bumpResolvedDigests{}, fmt.Errorf("law-A: package %q manifest %q has non-real %s digest for %s", pkg.Name, manifestURL, arch, filename)
		}
		shaByArch[arch] = sha
	}
	return shaByArch, bumpResolvedDigests{Origin: manifestURL, VerificationMethod: VerificationSignedManifest}, nil
}

func resolveSelfComputedDigests(pkg Package, target string, fetcher Fetcher) (map[string]string, bumpResolvedDigests, error) {
	shaByArch := make(map[string]string, len(buildArches))
	originParts := make([]string, 0, len(buildArches))
	for _, arch := range buildArches {
		assetURL, err := assetURLForArch(pkg, arch, target)
		if err != nil {
			return nil, bumpResolvedDigests{}, err
		}
		body, err := fetcher.Get(assetURL)
		if err != nil {
			return nil, bumpResolvedDigests{}, fmt.Errorf("bump: fetch asset %q: %w", assetURL, err)
		}
		sum := sha256.Sum256(body)
		shaByArch[arch] = fmt.Sprintf("%x", sum[:])
		originParts = append(originParts, fmt.Sprintf("%s=%s", arch, assetURL))
	}
	return shaByArch, bumpResolvedDigests{Origin: strings.Join(originParts, ", "), VerificationMethod: VerificationSelfComputedWeak}, nil
}

func assetURLForArch(pkg Package, arch, target string) (string, error) {
	if pkg.Upstream == nil || pkg.Upstream.Asset == nil {
		return "", fmt.Errorf("law-A: package %q has no upstream asset templates", pkg.Name)
	}
	template, ok := pkg.Upstream.Asset[arch]
	if !ok || strings.TrimSpace(template) == "" {
		return "", fmt.Errorf("law-A: package %q has no upstream asset template for %s", pkg.Name, arch)
	}
	return substituteVersion(template, target), nil
}

func parseSHASUMS256(b []byte) map[string]string {
	out := make(map[string]string)
	s := bufio.NewScanner(strings.NewReader(string(b)))
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "  ", 2)
		if len(parts) != 2 {
			continue
		}
		sha := strings.TrimSpace(parts[0])
		filename := strings.TrimSpace(parts[1])
		if sha == "" || filename == "" {
			continue
		}
		out[filename] = sha
	}
	return out
}

func bumpMagnitude(pkg Package, target string) (MagnitudeKind, error) {
	oldScheme := InferScheme(pkg.Kind, pkg.Version)
	newScheme := InferScheme(pkg.Kind, target)
	oldVersion, err := Parse(pkg.Version, oldScheme)
	if err != nil {
		return MagNone, fmt.Errorf("bump: current version for %q: %w", pkg.Name, err)
	}
	newVersion, err := Parse(target, newScheme)
	if err != nil {
		return MagNone, fmt.Errorf("bump: new version for %q: %w", pkg.Name, err)
	}
	return Magnitude(oldVersion, newVersion), nil
}

func bumpSoakRequired(m MagnitudeKind) bool {
	return m == MagMajor
}

type bumpLaneInfo struct {
	Label string
	CVEID string
}

func parseBumpLane(lane string) bumpLaneInfo {
	trimmed := strings.TrimSpace(lane)
	if trimmed == "" || trimmed == "default" {
		return bumpLaneInfo{Label: "default"}
	}
	label := trimmed
	cveID := ""
	if i := strings.Index(trimmed, ":"); i >= 0 {
		label = strings.TrimSpace(trimmed[:i])
		cveID = strings.TrimSpace(trimmed[i+1:])
	}
	if label == "" {
		label = "default"
	}
	return bumpLaneInfo{Label: label, CVEID: cveID}
}

func substituteVersion(template, version string) string {
	return strings.ReplaceAll(template, "{version}", version)
}

func planSheetFromDiff(diff Diff, resolved bumpResolvedDigests, upstream *Upstream, lane bumpLaneInfo, soakRequired, soakSatisfied bool, waivedBy string) PlanSheet {
	changes := make(map[string]SHA256Change, len(diff.NewSHA256))
	for _, arch := range buildArches {
		newSHA, ok := diff.NewSHA256[arch]
		if !ok {
			continue
		}
		changes[arch] = SHA256Change{Old: diff.OldSHA256[arch], New: newSHA}
	}
	if len(changes) == 0 {
		changes = nil
	}
	return PlanSheet{
		PackageName:        diff.PackageName,
		OldVersion:         diff.OldVersion,
		NewVersion:         diff.NewVersion,
		Magnitude:          diff.Magnitude,
		SHA256:             changes,
		Origin:             resolved.Origin,
		VerificationMethod: resolved.VerificationMethod,
		ChangelogURL:       changelogURL(upstream),
		CVEID:              lane.CVEID,
		BlastRadius:        cloneStringSlice(diff.ReverseClosure),
		Lane:               lane.Label,
		SoakRequired:       soakRequired,
		SoakSatisfied:      soakSatisfied,
		WaivedBy:           waivedBy,
	}
}

func changelogURL(upstream *Upstream) string {
	if upstream == nil {
		return ""
	}
	return upstream.URL
}
