package tools

import (
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/install"
	"github.com/freakhill/safeslop/internal/engine/sandbox"
)

// catalogTool fetches a catalog entry by display name, failing the test if absent.
func catalogTool(t *testing.T, name string) Tool {
	t.Helper()
	for _, c := range Catalog() {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("catalog must contain %q", name)
	return Tool{}
}

func TestCatalogIsPopulatedAndCategorized(t *testing.T) {
	cat := Catalog()
	if len(cat) < 15 {
		t.Fatalf("catalog unexpectedly small: %d", len(cat))
	}
	want := map[string]bool{CatRuntime: false, CatLang: false, CatForge: false, CatContainer: false, CatSecrets: false, CatCore: false, CatAgents: false}
	names := map[string]bool{}
	for _, tool := range cat {
		if tool.Name == "" || tool.Category == "" {
			t.Fatalf("tool with empty name/category: %+v", tool)
		}
		if names[tool.Name] {
			t.Fatalf("duplicate tool name %q", tool.Name)
		}
		names[tool.Name] = true
		want[tool.Category] = true
		// every tool must be detectable somehow (a CLI or an app)
		if len(tool.Detect) == 0 && tool.AppPath == "" {
			t.Fatalf("tool %q has no detection route", tool.Name)
		}
	}
	for cat, seen := range want {
		if !seen {
			t.Errorf("no tool in category %q", cat)
		}
	}
	// the user's named tools must all be present
	for _, n := range []string{"uv", "bun", "pnpm", "mise", "nix", "Go", "Rust", "Swift", "Clojure",
		"Babashka", "Lean 4", "Xcode", "git", "GitHub CLI", "tea",
		"Docker CLI", "OrbStack", "Tart", "1Password CLI", "Bitwarden CLI", "KeePassXC", "Proton Pass"} {
		if !names[n] {
			t.Errorf("catalog missing required tool %q", n)
		}
	}
}

func TestDetectClassifiesSource(t *testing.T) {
	uv := Tool{Name: "uv", Detect: []string{"uv"}, Brew: "uv"}
	tart := Tool{Name: "Tart", Detect: []string{"tart"}, Brew: "cirruslabs/cli/tart"}
	op := Tool{Name: "1Password", AppPath: "/Applications/1Password.app", Cask: "1password"}

	// present via brew formula (leaf match for a tapped formula)
	p := probe{
		lookPath:   func(b string) (string, bool) { return "/opt/homebrew/bin/" + b, b == "tart" },
		appExists:  func(string) bool { return false },
		formulae:   map[string]bool{"tart": true},
		casks:      map[string]bool{},
		brewPrefix: "/opt/homebrew",
	}
	if s := detect(p, tart); !s.Present || s.Source != "brew" {
		t.Errorf("tapped brew formula: got present=%v source=%q", s.Present, s.Source)
	}

	// present by brew-prefix path even when not in the formula set (e.g. a keg-only dep)
	p2 := probe{
		lookPath:   func(b string) (string, bool) { return "/opt/homebrew/bin/uv", b == "uv" },
		appExists:  func(string) bool { return false },
		formulae:   map[string]bool{},
		brewPrefix: "/opt/homebrew",
	}
	if s := detect(p2, uv); s.Source != "brew" {
		t.Errorf("brew-prefix path: got source %q, want brew", s.Source)
	}

	// present as a standalone install (not under brew prefix, not in formula set)
	p3 := probe{
		lookPath:   func(b string) (string, bool) { return "/Users/x/.local/bin/uv", b == "uv" },
		appExists:  func(string) bool { return false },
		formulae:   map[string]bool{},
		brewPrefix: "/opt/homebrew",
	}
	if s := detect(p3, uv); s.Source != "standalone" {
		t.Errorf("standalone: got source %q, want standalone", s.Source)
	}

	// present as a brew cask app
	p4 := probe{
		lookPath:  func(string) (string, bool) { return "", false },
		appExists: func(path string) bool { return path == "/Applications/1Password.app" },
		casks:     map[string]bool{"1password": true},
	}
	if s := detect(p4, op); !s.Present || s.Source != "cask" {
		t.Errorf("cask app: got present=%v source=%q", s.Present, s.Source)
	}

	// missing
	p5 := probe{lookPath: func(string) (string, bool) { return "", false }, appExists: func(string) bool { return false }}
	if s := detect(p5, uv); s.Present || s.Source != "missing" {
		t.Errorf("missing: got present=%v source=%q", s.Present, s.Source)
	}
}

func TestDetectFlagsShadowedBinary(t *testing.T) {
	// Two `docker` on PATH: the earlier one wins (Path); the later one is shadowed.
	p := probe{
		lookPath: func(b string) (string, bool) { return "/opt/homebrew/bin/docker", b == "docker" },
		lookAll:  func(b string) []string { return []string{"/opt/homebrew/bin/docker", "/usr/local/bin/docker"} },
	}
	s := detect(p, Tool{Name: "Docker", Detect: []string{"docker"}})
	if !s.Present || s.Path != "/opt/homebrew/bin/docker" {
		t.Fatalf("detect = %+v", s)
	}
	if len(s.ShadowedPaths) != 1 || s.ShadowedPaths[0] != "/usr/local/bin/docker" {
		t.Errorf("ShadowedPaths = %v, want the shadowed /usr/local path", s.ShadowedPaths)
	}

	// A single match is not shadowed.
	p2 := probe{
		lookPath: func(b string) (string, bool) { return "/opt/homebrew/bin/uv", b == "uv" },
		lookAll:  func(b string) []string { return []string{"/opt/homebrew/bin/uv"} },
	}
	if s := detect(p2, Tool{Name: "uv", Detect: []string{"uv"}}); len(s.ShadowedPaths) != 0 {
		t.Errorf("single match should not be shadowed: %v", s.ShadowedPaths)
	}

	// A nil lookAll (a probe built without the seam) degrades to no shadow info, not a crash.
	p3 := probe{lookPath: func(b string) (string, bool) { return "/usr/bin/git", b == "git" }}
	if s := detect(p3, Tool{Name: "git", Detect: []string{"git"}}); len(s.ShadowedPaths) != 0 {
		t.Errorf("nil lookAll should yield no shadows: %v", s.ShadowedPaths)
	}
}

func TestProbeFromEnvUsesReconstructedPathAndDegradesWithoutBrew(t *testing.T) {
	// Reconstructed lookPath resolves git off a brew dir but cannot find brew itself (the bundled-app
	// failure mode: brew not on the process PATH). Detection must still find git, and the brew-derived
	// sets must degrade to empty rather than crash.
	lp := func(b string) (string, bool) {
		if b == "git" {
			return "/opt/homebrew/bin/git", true
		}
		return "", false
	}
	p := probeFromEnv(lp, nil, nil)
	if len(p.formulae) != 0 || len(p.casks) != 0 {
		t.Errorf("no brew → empty formula/cask sets, got %v / %v", p.formulae, p.casks)
	}
	if p.brewPrefix != "" {
		t.Errorf("no brew → empty prefix, got %q", p.brewPrefix)
	}
	git := Tool{Name: "git", Detect: []string{"git"}}
	if s := detect(p, git); !s.Present || s.Path != "/opt/homebrew/bin/git" {
		t.Errorf("git must be found via the reconstructed PATH: present=%v path=%q", s.Present, s.Path)
	}
}

func TestBrewRunnerRefusesWhenBrewUnresolvable(t *testing.T) {
	br := brewRunner{lookPath: func(string) (string, bool) { return "", false }}
	if _, err := br.output("--prefix"); err != errNoBrew {
		t.Errorf("output without brew should return errNoBrew, got %v", err)
	}
	if got := br.list("--formula"); len(got) != 0 {
		t.Errorf("list without brew should be empty, got %v", got)
	}
}

func TestInstallableOnlyWhenMissing(t *testing.T) {
	// no-clobber: a present tool is never installable, regardless of route
	present := Status{Tool: Tool{Name: "uv", Brew: "uv", Script: "x"}, Present: true, Source: "brew"}
	if present.Installable() {
		t.Fatal("present tool must NOT be installable (no-clobber guarantee broken)")
	}
	missing := Status{Tool: Tool{Name: "uv", Brew: "uv"}, Present: false, Source: "missing"}
	if !missing.Installable() {
		t.Fatal("missing tool with a brew route should be installable")
	}
	noRoute := Status{Tool: Tool{Name: "x"}, Present: false, Source: "missing"}
	if noRoute.Installable() {
		t.Fatal("missing tool with no route is not installable")
	}
}

// TestUvUsesPinnedBinaryNotCurlSh locks in specs/0036 item ①: uv ships a checksum-pinned binary, so
// a missing uv must route to the verified Route A (or brew), never to its raw `curl … | sh` script.
func TestUvUsesPinnedBinaryNotCurlSh(t *testing.T) {
	var uv Tool
	for _, c := range Catalog() {
		if c.Name == "uv" {
			uv = c
		}
	}
	if uv.Name != "uv" {
		t.Fatal("uv must be in the catalog")
	}
	pin, ok := pinFor("uv")
	if !ok || pin.Version == "" || pin.SHA256 == "" {
		t.Fatalf("uv must have a fully-specified embedded pin, got %+v ok=%v", pin, ok)
	}
	missing := Status{Tool: uv, Present: false, Source: "missing"}

	// With brew unavailable, the OLD behavior fell to the raw curl|sh script; now it routes to the pin.
	if _, err := installArgv(missing, false); !errors.Is(err, errUsePin) {
		t.Fatalf("uv without brew must route to the verified pin, got err=%v", err)
	}
	// uv must NEVER resolve to a /bin/sh -c curl argv, brew present or not.
	for _, brewAvail := range []bool{true, false} {
		argv, err := installArgv(missing, brewAvail)
		if err == nil && len(argv) >= 3 && argv[0] == "/bin/sh" && strings.Contains(argv[2], "curl") {
			t.Fatalf("uv must never resolve to a curl|sh argv (brewAvail=%v): %v", brewAvail, argv)
		}
	}
	// With brew present, brew stays the preferred route.
	if argv, err := installArgv(missing, true); err != nil || argv[0] != "brew" {
		t.Fatalf("uv with brew should prefer brew, got argv=%v err=%v", argv, err)
	}
	// The route hint surfaced to the cockpit reflects the verified pin when brew is the resolver's choice
	// or the pin — in all cases it must not advertise curl|sh.
	if h := InstallRouteHint(missing); strings.Contains(h, "curl") {
		t.Fatalf("the cockpit install hint for uv must not mention curl|sh, got %q", h)
	}
}

// TestBunPnpmRouteToPinNotCurlSh extends the uv guarantee to the other pinned curl|sh tools (Task 5):
// a missing bun/pnpm must route to the verified pin when brew is absent, never to their install script.
func TestBunPnpmRouteToPinNotCurlSh(t *testing.T) {
	for _, name := range []string{"bun", "pnpm"} {
		var tool Tool
		for _, c := range Catalog() {
			if c.Name == name {
				tool = c
			}
		}
		if tool.Name != name {
			t.Fatalf("%s must be in the catalog", name)
		}
		if pin, ok := pinFor(name); !ok || pin.SHA256 == "" || pin.Version == "" {
			t.Fatalf("%s must have a fully-specified embedded pin, got %+v ok=%v", name, pin, ok)
		}
		missing := Status{Tool: tool, Present: false, Source: "missing"}
		if _, err := installArgv(missing, false); !errors.Is(err, errUsePin) {
			t.Fatalf("%s without brew must route to the verified pin, got err=%v", name, err)
		}
		for _, brewAvail := range []bool{true, false} {
			argv, err := installArgv(missing, brewAvail)
			if err == nil && len(argv) >= 3 && argv[0] == "/bin/sh" && strings.Contains(argv[2], "curl") {
				t.Fatalf("%s must never resolve to a curl|sh argv (brewAvail=%v): %v", name, brewAvail, argv)
			}
		}
	}
}

// TestInstallPreview locks the shared backend that drives the cockpit hover tooltip + consent gate
// (specs/0037): verified-pin is one-click with url/sha/version; an unverified remote script demands
// consent; a present tool promises no-clobber.
func TestInstallPreview(t *testing.T) {
	get := func(name string) Tool {
		for _, c := range Catalog() {
			if c.Name == name {
				return c
			}
		}
		t.Fatalf("%s not in catalog", name)
		return Tool{}
	}
	// Verified pin (uv, brew unavailable → the pin route): one-click, carries url/sha/version.
	uv := installPreview(Status{Tool: get("uv"), Present: false, Source: "missing"}, false)
	if uv.Verification != VerifiedPin || uv.NeedsConsent {
		t.Fatalf("uv should be a one-click verified pin, got %+v", uv)
	}
	if uv.SHA256 == "" || uv.Version == "" || uv.SourceURL == "" {
		t.Fatalf("verified pin preview must carry url/sha/version: %+v", uv)
	}
	if !strings.Contains(uv.Precautions, "checksum") {
		t.Fatalf("verified precautions should mention the checksum: %q", uv.Precautions)
	}
	// Brew route (uv with brew available) is preferred and brew-managed.
	if b := installPreview(Status{Tool: get("uv"), Present: false, Source: "missing"}, true); b.Verification != BrewManaged {
		t.Fatalf("uv with brew should be brew-managed, got %+v", b)
	}
	// Unverified remote script (Codex — npm, intentionally not pinned): needs consent, shows the command.
	script := installPreview(Status{Tool: get("Codex"), Present: false, Source: "missing"}, true)
	if script.Verification != UnverifiedRun || !script.NeedsConsent {
		t.Fatalf("a script-only tool is an unverified remote script needing consent, got %+v", script)
	}
	if !strings.Contains(script.Command, "codex") {
		t.Fatalf("unverified preview must show the literal command, got %q", script.Command)
	}
	// Present tool → no-clobber promise; a shadowed one notes the shadowing.
	if p := Precautions(Status{Tool: get("git"), Present: true, Source: "brew", Path: "/opt/homebrew/bin/git"}); !strings.Contains(p, "clobber") {
		t.Fatalf("present precaution should promise no-clobber, got %q", p)
	}
	if p := Precautions(Status{Tool: get("Docker CLI"), Present: true, Source: "standalone", Path: "/a/docker", ShadowedPaths: []string{"/b/docker"}}); !strings.Contains(p, "shadows") {
		t.Fatalf("shadowed present tool should note shadowing, got %q", p)
	}
}

// TestRustupUsesVerifiedInstallerNotCurlSh locks specs/0036 Task 6: rustup installs via a fetched +
// sha256-verified rustup-init binary, never its `curl … | sh` script.
func TestRustupUsesVerifiedInstallerNotCurlSh(t *testing.T) {
	var rust Tool
	for _, c := range Catalog() {
		if c.Name == "Rust" {
			rust = c
		}
	}
	if rust.Installer == nil {
		t.Fatal("Rust must carry a VerifiedInstaller")
	}
	missing := Status{Tool: rust, Present: false, Source: "missing"}
	// Route resolves to the verified installer regardless of brew (Rust has no brew formula).
	for _, brewAvail := range []bool{true, false} {
		if _, err := installArgv(missing, brewAvail); !errors.Is(err, errUseInstaller) {
			t.Fatalf("rustup must route to the verified installer (brewAvail=%v), got %v", brewAvail, err)
		}
	}
	pv := installPreview(missing, true)
	if pv.Verification != VerifiedInstallerRoute || pv.NeedsConsent {
		t.Fatalf("rustup should be a one-click verified installer, got %+v", pv)
	}
	if pv.SHA256 == "" || pv.Version == "" || !strings.Contains(pv.SourceURL, "rustup-init") {
		t.Fatalf("verified installer preview must carry url/sha/version: %+v", pv)
	}
	if strings.Contains(pv.Command, "curl") {
		t.Fatalf("rustup command must be the verified installer, not curl|sh: %q", pv.Command)
	}
}

// TestCatalogInstallersAreFullyPinned is the fail-closed gate for VerifiedInstaller entries: every one
// must declare a 64-hex sha256, a non-empty version, and a versioned (never "latest") URL.
func TestCatalogInstallersAreFullyPinned(t *testing.T) {
	sha256Re := regexp.MustCompile(`^[0-9a-f]{64}$`)
	for _, c := range Catalog() {
		if c.Installer == nil {
			continue
		}
		in := c.Installer
		if !sha256Re.MatchString(in.SHA256) {
			t.Fatalf("%s installer must declare a 64-hex sha256, got %q", c.Name, in.SHA256)
		}
		if in.Version == "" || in.Version == "latest" {
			t.Fatalf("%s installer must pin an exact version, got %q", c.Name, in.Version)
		}
		if in.URL == "" || strings.Contains(in.URL, "latest") {
			t.Fatalf("%s installer URL must be versioned, never latest: %q", c.Name, in.URL)
		}
	}
}

// TestClaudeUsesPinnedBinaryNotCurlSh covers the Detect-name pin match: "Claude Code" (Detect ["claude"])
// resolves the "claude" pin and routes to verified Route A, never its `curl … | bash` script.
func TestClaudeUsesPinnedBinaryNotCurlSh(t *testing.T) {
	var c Tool
	for _, x := range Catalog() {
		if x.Name == "Claude Code" {
			c = x
		}
	}
	if c.Name != "Claude Code" {
		t.Fatal("Claude Code must be in the catalog")
	}
	if !hasPinForTool(c) {
		t.Fatal("Claude Code must resolve a pin via its Detect name")
	}
	missing := Status{Tool: c, Present: false, Source: "missing"}
	for _, brewAvail := range []bool{true, false} {
		if _, err := installArgv(missing, brewAvail); !errors.Is(err, errUsePin) {
			t.Fatalf("Claude Code must route to the verified pin (brewAvail=%v), got %v", brewAvail, err)
		}
	}
	pv := installPreview(missing, true)
	if pv.Verification != VerifiedPin || strings.Contains(pv.Command, "curl") {
		t.Fatalf("Claude Code should be a verified pin, not curl|sh: %+v", pv)
	}
}

// TestInstallerConfinement verifies specs/0038: a confinable installer (rustup) runs sandbox-wrapped
// with the secret-deny scope, while an unconfined one (nix) runs bare.
func TestInstallerConfinement(t *testing.T) {
	nix := &VerifiedInstaller{URL: "https://x/nix-installer", Args: []string{"install"}, Confine: false}
	argv, cleanup, confined := installerRunArgv(nix, "/tmp/nix-installer")
	cleanup()
	if confined || len(argv) == 0 || argv[0] != "/tmp/nix-installer" {
		t.Fatalf("nix must run unconfined, got confined=%v argv=%v", confined, argv)
	}

	if !sandbox.Available() {
		t.Skip("sandbox-exec unavailable — confinement path not exercised on this host")
	}
	rustup := &VerifiedInstaller{URL: "https://x/rustup-init", Args: []string{"-y"}, Confine: true}
	argv, cleanup, confined = installerRunArgv(rustup, "/tmp/rustup-init")
	defer cleanup()
	if !confined || argv[0] != sandbox.SandboxExecPath {
		t.Fatalf("rustup must be sandbox-wrapped, got confined=%v argv0=%q", confined, argv[0])
	}
	var sbFile string
	for i, a := range argv {
		if a == "-f" && i+1 < len(argv) {
			sbFile = argv[i+1]
		}
	}
	prof, err := os.ReadFile(sbFile)
	if err != nil {
		t.Fatalf("read sandbox profile: %v", err)
	}
	if !strings.Contains(string(prof), "id_ed25519") || !strings.Contains(string(prof), "deny file-read") {
		t.Fatalf("a confined installer's profile must deny reading ssh private keys:\n%s", prof)
	}
}

// TestPinnedToolsNeverFallToScript generalizes the claude-specific guard (specs/0036 coherency fix):
// ANY catalog tool that resolves an embedded pin must route to the verified pin and never silently fall
// back to its `curl … | sh` Script — the dead-fallback fragility (a pin Name that stops matching Detect
// would drop the tool to the script route unnoticed). Run with brew unavailable so the pin route wins.
func TestPinnedToolsNeverFallToScript(t *testing.T) {
	any := false
	for _, c := range Catalog() {
		if !hasPinForTool(c) {
			continue
		}
		any = true
		missing := Status{Tool: c, Present: false, Source: "missing"}
		if _, err := installArgv(missing, false); !errors.Is(err, errUsePin) {
			t.Errorf("%s has a pin but did not route to it (brewAvail=false): %v", c.Name, err)
		}
		pv := installPreview(missing, false)
		if pv.Verification != VerifiedPin {
			t.Errorf("%s pin must preview as verified-pin, got %q", c.Name, pv.Verification)
		}
		if strings.Contains(pv.Command, "curl") || strings.Contains(pv.Command, "| sh") {
			t.Errorf("%s pin must not resolve to its curl|sh script: %q", c.Name, pv.Command)
		}
	}
	if !any {
		t.Fatal("expected at least one pinned catalog tool to exercise this guard")
	}
}

// TestVerifiedChecksumProvenanceSurfaced locks the legibility fix (specs/0036): a vendor-published
// checksum and a trust-on-first-use hash must NOT share the same "verified" surface. uv (vendor) and
// pnpm (TOFU) are both verified-pin one-click, but carry distinct provenance + disclosure.
func TestVerifiedChecksumProvenanceSurfaced(t *testing.T) {
	uv := installPreview(Status{Tool: catalogTool(t, "uv"), Present: false, Source: "missing"}, false)
	if uv.Provenance != install.ProvenanceVendor {
		t.Fatalf("uv pin must be vendor-checksum provenance, got %q", uv.Provenance)
	}
	if !strings.Contains(uv.Precautions, "vendor publishes") {
		t.Fatalf("vendor-checksum precautions must say so: %q", uv.Precautions)
	}
	pnpm := installPreview(Status{Tool: catalogTool(t, "pnpm"), Present: false, Source: "missing"}, false)
	if pnpm.Provenance != install.ProvenanceTLS {
		t.Fatalf("pnpm pin must be TOFU provenance, got %q", pnpm.Provenance)
	}
	if !strings.Contains(pnpm.Precautions, "trust-on-first-use") {
		t.Fatalf("TOFU precautions must disclose it: %q", pnpm.Precautions)
	}
	if uv.Verification != VerifiedPin || pnpm.Verification != VerifiedPin {
		t.Fatal("both uv and pnpm must still be verified-pin (the distinction is provenance, not route)")
	}
}

// TestUnconfinedInstallerDemandsConsent locks the friction-tracks-risk fix (specs/0037): an unconfined
// admin installer (nix, runs as root) is gated on a typed confirm just like an unverified script, while
// a confined installer (rustup) stays one-click.
func TestUnconfinedInstallerDemandsConsent(t *testing.T) {
	nix := installPreview(Status{Tool: catalogTool(t, "nix"), Present: false, Source: "missing"}, true)
	if nix.Verification != VerifiedInstallerRoute {
		t.Fatalf("nix must be a verified installer, got %q", nix.Verification)
	}
	if !nix.NeedsConsent {
		t.Fatal("an unconfined admin installer (nix) must demand typed confirmation")
	}
	if !strings.Contains(nix.Precautions, "UNCONFINED") {
		t.Fatalf("nix precautions must flag UNCONFINED: %q", nix.Precautions)
	}
	rust := installPreview(Status{Tool: catalogTool(t, "Rust"), Present: false, Source: "missing"}, true)
	if rust.NeedsConsent {
		t.Fatal("a confined installer (rustup) must stay one-click, not gated")
	}
}

// TestPostInstallDisclosed locks the bootstrap-vs-steady-state fix (specs/0036): tools whose verification
// covers only the first artifact disclose what happens afterward, so the badge isn't read as a guarantee.
func TestPostInstallDisclosed(t *testing.T) {
	cases := map[string]string{
		"Claude Code": "keeps itself up to date",
		"Rust":        "signed manifests",
		"nix":         "verified by the installer",
	}
	for name, want := range cases {
		p := InstallPreview(Status{Tool: catalogTool(t, name), Present: false, Source: "missing"})
		if !strings.Contains(p.Precautions, want) {
			t.Errorf("%s precautions must disclose post-install behavior (%q): %q", name, want, p.Precautions)
		}
	}
}

func TestInstallArgvRefusesPresentAndPicksRoute(t *testing.T) {
	if _, err := InstallArgv(Status{Tool: Tool{Brew: "uv"}, Present: true}); err == nil {
		t.Fatal("InstallArgv must refuse a present tool")
	}
	// script route works without brew on PATH
	argv, err := InstallArgv(Status{Tool: Tool{Script: "echo hi"}, Present: false})
	if err != nil || len(argv) != 3 || argv[0] != "/bin/sh" {
		t.Fatalf("script route: argv=%v err=%v", argv, err)
	}
}

// TestCatalogIncludesPi locks specs/0045: the Pi coding agent is a registered, detectable agent
// (an npm script-only tool, like Codex — installed with --ignore-scripts for supply-chain hygiene).
func TestCatalogIncludesPi(t *testing.T) {
	pi := catalogTool(t, "Pi")
	if pi.Category != CatAgents {
		t.Errorf("Pi must be in the agents category, got %q", pi.Category)
	}
	if len(pi.Detect) != 1 || pi.Detect[0] != "pi" {
		t.Errorf("Pi must detect the `pi` binary, got %v", pi.Detect)
	}
	if !strings.Contains(pi.Script, "@earendil-works/pi-coding-agent") || !strings.Contains(pi.Script, "ignore-scripts") {
		t.Errorf("Pi install script must be the pinned-package npm install with --ignore-scripts, got %q", pi.Script)
	}
}
