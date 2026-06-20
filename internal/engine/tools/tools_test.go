package tools

import "testing"

func TestCatalogIsPopulatedAndCategorized(t *testing.T) {
	cat := Catalog()
	if len(cat) < 15 {
		t.Fatalf("catalog unexpectedly small: %d", len(cat))
	}
	want := map[string]bool{CatRuntime: false, CatForge: false, CatContainer: false, CatSecrets: false, CatCore: false, CatAgents: false}
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
	for _, n := range []string{"uv", "bun", "pnpm", "mise", "nix", "git", "GitHub CLI", "tea",
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
