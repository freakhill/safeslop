package tools

import "testing"

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
