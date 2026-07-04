package policy

import (
	"crypto/sha256"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

type fixtureFetcher map[string][]byte

func (f fixtureFetcher) Get(url string) ([]byte, error) {
	b, ok := f[url]
	if !ok {
		return nil, fmt.Errorf("fixture fetcher: missing %s", url)
	}
	return append([]byte(nil), b...), nil
}

func TestBumpSignedManifestMutatesCatalogAndPlanSheet(t *testing.T) {
	cat := testBumpCatalog(t, signedBumpUpstream())
	amd64SHA := bumpDigest("c")
	arm64SHA := bumpDigest("d")
	fetcher := fixtureFetcher{
		"https://downloads.example.invalid/node/v1.2.4/SHASUMS256.txt": []byte(manifestFixture(map[string]string{
			"node-v1.2.4-linux-amd64.tar.gz": amd64SHA,
			"node-v1.2.4-linux-arm64.tar.gz": arm64SHA,
		})),
	}

	next, sheet, err := Bump(cat, "node", "1.2.4", "", fetcher)
	if err != nil {
		t.Fatalf("Bump returned error: %v", err)
	}
	if next == cat {
		t.Fatal("Bump returned the input catalog")
	}
	oldNode, _ := cat.Lookup("node")
	if oldNode.Version != "1.2.3" || oldNode.SHA256["amd64"] != bumpDigest("a") {
		t.Fatalf("input catalog mutated: %#v", oldNode)
	}
	newNode, _ := next.Lookup("node")
	if newNode.Version != "1.2.4" {
		t.Errorf("new version = %q, want 1.2.4", newNode.Version)
	}
	wantSHA := map[string]string{"amd64": amd64SHA, "arm64": arm64SHA}
	if !reflect.DeepEqual(newNode.SHA256, wantSHA) {
		t.Errorf("new sha = %v, want %v", newNode.SHA256, wantSHA)
	}

	if sheet.PackageName != "node" || sheet.OldVersion != "1.2.3" || sheet.NewVersion != "1.2.4" {
		t.Errorf("plan sheet version fields = %#v", sheet)
	}
	if sheet.Magnitude != MagPatch {
		t.Errorf("magnitude = %q, want %q", sheet.Magnitude, MagPatch)
	}
	if sheet.VerificationMethod != VerificationSignedManifest {
		t.Errorf("verification = %q, want %q", sheet.VerificationMethod, VerificationSignedManifest)
	}
	if got := sheet.SHA256["arm64"].New; got != arm64SHA {
		t.Errorf("arm64 new sha = %q, want %q", got, arm64SHA)
	}
	if got := sheet.SHA256["amd64"].Old; got != bumpDigest("a") {
		t.Errorf("amd64 old sha = %q, want %q", got, bumpDigest("a"))
	}
	if !reflect.DeepEqual(sheet.BlastRadius, []string{"plugin", "tool"}) {
		t.Errorf("blast radius = %v, want [plugin tool]", sheet.BlastRadius)
	}
	if sheet.Lane != "default" {
		t.Errorf("lane = %q, want default", sheet.Lane)
	}
	if sheet.SoakRequired || !sheet.SoakSatisfied || sheet.WaivedBy != "" {
		t.Errorf("soak state = required:%v satisfied:%v waivedBy:%q, want false/true/empty", sheet.SoakRequired, sheet.SoakSatisfied, sheet.WaivedBy)
	}
	if !strings.Contains(sheet.String(), "Verification: signed-manifest") {
		t.Errorf("plan sheet render missing verification label:\n%s", sheet.String())
	}
}

func TestBumpRefusesMissingManifestArchLAWAGate(t *testing.T) {
	cat := testBumpCatalog(t, signedBumpUpstream())
	fetcher := fixtureFetcher{
		"https://downloads.example.invalid/node/v1.2.4/SHASUMS256.txt": []byte(manifestFixture(map[string]string{
			"node-v1.2.4-linux-amd64.tar.gz": bumpDigest("c"),
		})),
	}

	_, _, err := Bump(cat, "node", "1.2.4", "security", fetcher)
	requireErrContains(t, err, "law-A")
	requireErrContains(t, err, "arm64")
	oldNode, _ := cat.Lookup("node")
	if oldNode.Version != "1.2.3" || oldNode.SHA256["arm64"] != bumpDigest("b") {
		t.Fatalf("input catalog mutated after LAW-A failure: %#v", oldNode)
	}
}

func TestBumpRefusesRollbackTargetMonotonicFloor(t *testing.T) {
	cat := testBumpCatalog(t, signedBumpUpstream())
	fetcher := fixtureFetcher{
		"https://downloads.example.invalid/node/v1.2.2/SHASUMS256.txt": []byte(manifestFixture(map[string]string{
			"node-v1.2.2-linux-amd64.tar.gz": bumpDigest("c"),
			"node-v1.2.2-linux-arm64.tar.gz": bumpDigest("d"),
		})),
	}

	_, _, err := Bump(cat, "node", "1.2.2", "", fetcher)
	requireErrContains(t, err, "monotonic floor")
	oldNode, _ := cat.Lookup("node")
	if oldNode.Version != "1.2.3" {
		t.Fatalf("input catalog mutated after rollback refusal: %#v", oldNode)
	}
}

func TestBumpSecurityLaneWaivesMajorSoakOnly(t *testing.T) {
	t.Run("default lane blocks major bump", func(t *testing.T) {
		cat := testBumpCatalog(t, signedBumpUpstream())
		fetcher := fixtureFetcher{
			"https://downloads.example.invalid/node/v2.0.0/SHASUMS256.txt": []byte(manifestFixture(map[string]string{
				"node-v2.0.0-linux-amd64.tar.gz": bumpDigest("c"),
				"node-v2.0.0-linux-arm64.tar.gz": bumpDigest("d"),
			})),
		}

		_, _, err := Bump(cat, "node", "2.0.0", "default", fetcher)
		requireErrContains(t, err, "bump: major bump requires the security lane (--security) or human confirm; soak not satisfied")
		oldNode, _ := cat.Lookup("node")
		if oldNode.Version != "1.2.3" {
			t.Fatalf("input catalog mutated after soak failure: %#v", oldNode)
		}
	})

	t.Run("security lane waives soak", func(t *testing.T) {
		cat := testBumpCatalog(t, signedBumpUpstream())
		fetcher := fixtureFetcher{
			"https://downloads.example.invalid/node/v2.0.0/SHASUMS256.txt": []byte(manifestFixture(map[string]string{
				"node-v2.0.0-linux-amd64.tar.gz": bumpDigest("c"),
				"node-v2.0.0-linux-arm64.tar.gz": bumpDigest("d"),
			})),
		}

		next, sheet, err := Bump(cat, "node", "2.0.0", "security:CVE-2026-12345", fetcher)
		if err != nil {
			t.Fatalf("Bump security returned error: %v", err)
		}
		node, _ := next.Lookup("node")
		if node.Version != "2.0.0" {
			t.Errorf("new version = %q, want 2.0.0", node.Version)
		}
		if sheet.Magnitude != MagMajor {
			t.Errorf("magnitude = %q, want %q", sheet.Magnitude, MagMajor)
		}
		if !sheet.SoakRequired || !sheet.SoakSatisfied || sheet.WaivedBy != "security" {
			t.Errorf("soak state = required:%v satisfied:%v waivedBy:%q, want true/true/security", sheet.SoakRequired, sheet.SoakSatisfied, sheet.WaivedBy)
		}
		if sheet.Lane != "security" || sheet.CVEID != "CVE-2026-12345" {
			t.Errorf("lane/CVE = %q/%q, want security/CVE-2026-12345", sheet.Lane, sheet.CVEID)
		}
	})

	t.Run("security lane still refuses LAW-A", func(t *testing.T) {
		cat := testBumpCatalog(t, signedBumpUpstream())
		fetcher := fixtureFetcher{
			"https://downloads.example.invalid/node/v2.0.0/SHASUMS256.txt": []byte(manifestFixture(map[string]string{
				"node-v2.0.0-linux-amd64.tar.gz": bumpDigest("c"),
			})),
		}

		_, _, err := Bump(cat, "node", "2.0.0", "security", fetcher)
		requireErrContains(t, err, "law-A")
		requireErrContains(t, err, "arm64")
	})
}

func TestBumpSelfComputedWeakFallback(t *testing.T) {
	cat := testBumpCatalog(t, &Upstream{
		Kind: "url-regex",
		URL:  "https://downloads.example.invalid/node/releases",
		Asset: map[string]string{
			"amd64": "https://downloads.example.invalid/node-v{version}-linux-amd64.tar.gz",
			"arm64": "https://downloads.example.invalid/node-v{version}-linux-arm64.tar.gz",
		},
	})
	amd64Bytes := []byte("amd64 artifact bytes")
	arm64Bytes := []byte("arm64 artifact bytes")
	fetcher := fixtureFetcher{
		"https://downloads.example.invalid/node-v1.2.4-linux-amd64.tar.gz": amd64Bytes,
		"https://downloads.example.invalid/node-v1.2.4-linux-arm64.tar.gz": arm64Bytes,
	}

	next, sheet, err := Bump(cat, "node", "1.2.4", "", fetcher)
	if err != nil {
		t.Fatalf("Bump self-computed returned error: %v", err)
	}
	amd64SHA := sha256Hex(amd64Bytes)
	arm64SHA := sha256Hex(arm64Bytes)
	node, _ := next.Lookup("node")
	if got := node.SHA256["amd64"]; got != amd64SHA {
		t.Errorf("amd64 sha = %q, want %q", got, amd64SHA)
	}
	if got := node.SHA256["arm64"]; got != arm64SHA {
		t.Errorf("arm64 sha = %q, want %q", got, arm64SHA)
	}
	if sheet.VerificationMethod != VerificationSelfComputedWeak {
		t.Errorf("verification = %q, want %q", sheet.VerificationMethod, VerificationSelfComputedWeak)
	}
}

func TestBumpRequiresUpstream(t *testing.T) {
	cat := testBumpCatalog(t, nil)
	_, _, err := Bump(cat, "node", "1.2.4", "", fixtureFetcher{})
	requireErrContains(t, err, "bump: package \"node\" has no upstream; cannot resolve digests")
}

func TestPlanSheetString(t *testing.T) {
	sheet := PlanSheet{
		PackageName:        "node",
		OldVersion:         "1.2.3",
		NewVersion:         "1.2.4",
		Magnitude:          MagPatch,
		SHA256:             map[string]SHA256Change{"amd64": {Old: bumpDigest("a"), New: bumpDigest("c")}},
		Origin:             "https://downloads.example.invalid/node/v1.2.4/SHASUMS256.txt",
		VerificationMethod: VerificationSignedManifest,
		ChangelogURL:       "https://downloads.example.invalid/node/releases",
		CVEID:              "CVE-2026-12345",
		BlastRadius:        []string{"plugin", "tool"},
		Lane:               "security",
		SoakRequired:       true,
		SoakSatisfied:      true,
		WaivedBy:           "security",
	}

	rendered := sheet.String()
	for _, want := range []string{
		"Package: node",
		"Version: 1.2.3 -> 1.2.4",
		"Magnitude: patch",
		"Verification: signed-manifest",
		"CVE: CVE-2026-12345",
		"amd64:",
		"Blast radius:",
		"- plugin",
		"Waived by: security",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("plan sheet render missing %q:\n%s", want, rendered)
		}
	}
}

// The following cases lock behaviors specs/0059 W4 requires of Bump ("enforce
// LAW-A/B/C/D + monotonic floor + soak") but that the initial suite exercised only
// through BumpPackage, or not at all. Without them a regression in Bump's ORDERING
// (e.g. a soak check that swallows a hard-gate error, or a fetch on the non-binary
// path) would go uncaught. Canon: specs/research/2026-06-30-version-policy-flo.md —
// the LAWs are hard overrides; soak is the soft gate waived only by --security.

// LAW-B must fire even when upstream published a signed manifest for the pre-release,
// so the bump reaches BumpPackage on the happy-fetch path and is still refused — a
// pre-release digest never lands, and the input catalog is untouched.
func TestBumpEnforcesChannelBanLAWB(t *testing.T) {
	cat := testBumpCatalog(t, signedBumpUpstream())
	fetcher := fixtureFetcher{
		"https://downloads.example.invalid/node/v1.2.4-rc1/SHASUMS256.txt": []byte(manifestFixture(map[string]string{
			"node-v1.2.4-rc1-linux-amd64.tar.gz": bumpDigest("c"),
			"node-v1.2.4-rc1-linux-arm64.tar.gz": bumpDigest("d"),
		})),
	}
	_, _, err := Bump(cat, "node", "1.2.4-rc1", "", fetcher)
	requireErrContains(t, err, "law-B")
	if oldNode, _ := cat.Lookup("node"); oldNode.Version != "1.2.3" {
		t.Fatalf("input catalog mutated after LAW-B refusal: %#v", oldNode)
	}
}

// LAW-C: an apt bump has no per-arch binary digest (resolveBumpDigests takes the
// non-binary path and never fetches), so LAW-C must still refuse it inside Bump.
func TestBumpEnforcesAptLAWC(t *testing.T) {
	cat := newCatalog([]Package{
		{Name: "aptlib", Kind: KindApt, Version: "1.2", Upstream: signedBumpUpstream()},
	}, nil, nil)
	if err := cat.Validate(); err != nil {
		t.Fatalf("test catalog invalid: %v", err)
	}
	_, _, err := Bump(cat, "aptlib", "1.3", "", fixtureFetcher{})
	requireErrContains(t, err, "law-C")
}

// Soak-proxy boundary: a MINOR bump is not a major step, so it proceeds in the DEFAULT
// lane with soak not required — the guard that soak fires ONLY on a major step
// (canon: patch < minor < major; only major is blocked absent a waiver).
func TestBumpMinorProceedsInDefaultLane(t *testing.T) {
	cat := testBumpCatalog(t, signedBumpUpstream())
	fetcher := fixtureFetcher{
		"https://downloads.example.invalid/node/v1.3.0/SHASUMS256.txt": []byte(manifestFixture(map[string]string{
			"node-v1.3.0-linux-amd64.tar.gz": bumpDigest("c"),
			"node-v1.3.0-linux-arm64.tar.gz": bumpDigest("d"),
		})),
	}
	_, sheet, err := Bump(cat, "node", "1.3.0", "", fetcher)
	if err != nil {
		t.Fatalf("minor bump in default lane returned error: %v", err)
	}
	if sheet.Magnitude != MagMinor {
		t.Errorf("magnitude = %q, want %q", sheet.Magnitude, MagMinor)
	}
	if sheet.SoakRequired || !sheet.SoakSatisfied || sheet.WaivedBy != "" {
		t.Errorf("soak state = required:%v satisfied:%v waivedBy:%q, want false/true/empty", sheet.SoakRequired, sheet.SoakSatisfied, sheet.WaivedBy)
	}
}

// LAW-A on the self-computed (no-ManifestURL) path: all-arch-or-none must also hold
// when digests are computed from downloads, not read from a manifest. A missing arch
// asset aborts before any mutation (defense-in-depth with the manifest LAW-A path).
func TestBumpSelfComputedAllArchOrNone(t *testing.T) {
	cat := testBumpCatalog(t, &Upstream{
		Kind: "url-regex",
		URL:  "https://downloads.example.invalid/node/releases",
		Asset: map[string]string{
			"amd64": "https://downloads.example.invalid/node-v{version}-linux-amd64.tar.gz",
			"arm64": "https://downloads.example.invalid/node-v{version}-linux-arm64.tar.gz",
		},
	})
	// Only amd64 is fetchable; the arm64 download is absent.
	fetcher := fixtureFetcher{
		"https://downloads.example.invalid/node-v1.2.4-linux-amd64.tar.gz": []byte("amd64 only"),
	}
	_, _, err := Bump(cat, "node", "1.2.4", "", fetcher)
	requireErrContains(t, err, "arm64")
	if oldNode, _ := cat.Lookup("node"); oldNode.Version != "1.2.3" {
		t.Fatalf("input catalog mutated after self-computed all-arch failure: %#v", oldNode)
	}
}

// Non-binary (npm) bump: the canon scopes per-arch sha256 to KindBinary, so an npm bump
// resolves no digests and never fetches (an empty fetcher proves it); it labels the
// plan sheet self-computed-WEAK (the weaker verification method) and carries no SHA
// diff, while the version still moves under the monotonic floor.
func TestBumpNonBinaryResolvesNoDigests(t *testing.T) {
	up := &Upstream{Kind: "npm-registry", URL: "https://registry.example.invalid/pkg"}
	cat := newCatalog([]Package{
		{Name: "tool", Kind: KindNpm, Version: "1.0.0", Upstream: up},
	}, nil, nil)
	if err := cat.Validate(); err != nil {
		t.Fatalf("test catalog invalid: %v", err)
	}
	next, sheet, err := Bump(cat, "tool", "1.1.0", "", fixtureFetcher{})
	if err != nil {
		t.Fatalf("non-binary bump returned error: %v", err)
	}
	tool, _ := next.Lookup("tool")
	if tool.Version != "1.1.0" {
		t.Errorf("new version = %q, want 1.1.0", tool.Version)
	}
	if sheet.VerificationMethod != VerificationSelfComputedWeak {
		t.Errorf("verification = %q, want %q", sheet.VerificationMethod, VerificationSelfComputedWeak)
	}
	if sheet.SHA256 != nil {
		t.Errorf("non-binary plan sheet SHA256 = %v, want nil", sheet.SHA256)
	}
	if sheet.Origin != up.URL {
		t.Errorf("origin = %q, want %q", sheet.Origin, up.URL)
	}
}

func testBumpCatalog(t *testing.T, upstream *Upstream) *Catalog {
	t.Helper()
	c := newCatalog([]Package{
		{
			Name:     "node",
			Kind:     KindBinary,
			Version:  "1.2.3",
			SHA256:   map[string]string{"amd64": bumpDigest("a"), "arm64": bumpDigest("b")},
			Upstream: upstream,
		},
		{Name: "tool", Kind: KindNpm, Version: "1.0.0", Requires: []string{"node"}},
		{Name: "plugin", Kind: KindPip, Version: "1.0.0", Requires: []string{"tool"}},
	}, []Bundle{{Name: "base", Packages: []string{"tool"}}}, nil)
	if err := c.Validate(); err != nil {
		t.Fatalf("test catalog invalid: %v", err)
	}
	return c
}

func signedBumpUpstream() *Upstream {
	return &Upstream{
		Kind:        "node-dist",
		URL:         "https://downloads.example.invalid/node/releases",
		ManifestURL: "https://downloads.example.invalid/node/v{version}/SHASUMS256.txt",
		Asset: map[string]string{
			"amd64": "https://downloads.example.invalid/node/v{version}/node-v{version}-linux-amd64.tar.gz",
			"arm64": "https://downloads.example.invalid/node/v{version}/node-v{version}-linux-arm64.tar.gz",
		},
	}
}

func manifestFixture(entries map[string]string) string {
	var b strings.Builder
	for filename, sha := range entries {
		fmt.Fprintf(&b, "%s  %s\n", sha, filename)
	}
	return b.String()
}

func bumpDigest(s string) string { return strings.Repeat(s, 64) }

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return fmt.Sprintf("%x", sum[:])
}

func requireErrContains(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error containing %q, got nil", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q does not contain %q", err.Error(), want)
	}
}
