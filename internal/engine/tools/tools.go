// Package tools is the cockpit's Installs-tab backend: a data-driven catalog of the dev tools,
// runtimes, container/VM hosts, secret managers, and agents safeslop works with, plus read-only
// detection of what is already present and how it was installed (brew formula / brew cask /
// standalone / not-installed).
//
// The load-bearing safety property is structural: detection never mutates anything, and an install
// is only ever OFFERED for a tool that is MISSING. A tool already present — however it was installed
// — is reported with its source and given no install action, so safeslop can never clobber or "fix"
// an existing install (the user's explicit requirement). People pick tools one at a time; there is no
// install-everything button.
package tools

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/freakhill/safeslop/internal/engine/hostenv"
)

var (
	errAlreadyPresent = errors.New("tool already installed — safeslop won't touch an existing install")
	errNeedsBrew      = errors.New("this tool installs via Homebrew, which isn't on PATH (install brew first)")
	errNoRoute        = errors.New("no install route for this tool")
	errNoBrew         = errors.New("brew is not resolvable on the reconstructed PATH")
)

// Category groups tools in the UI.
const (
	CatRuntime   = "Runtimes & package managers"
	CatLang      = "Languages & toolchains"
	CatForge     = "Source control & forges"
	CatContainer = "Containers & VMs"
	CatSecrets   = "Secret managers"
	CatCore      = "safeslop core"
	CatAgents    = "Agents"
)

// Tool is one catalog entry. Detect lists candidate binaries (any on PATH ⇒ present); AppPath is an
// optional .app for GUI-only tools (cask apps). Brew/Cask/Script are install routes, tried in that
// preference order and ONLY when the tool is missing.
type Tool struct {
	Name     string   // display name + stable id
	Category string   // one of the Cat* constants
	Detect   []string // candidate CLI names; first found on PATH wins
	AppPath  string   // optional /Applications/X.app (for cask apps without a CLI)
	Brew     string   // brew formula (may be tapped, e.g. "cirruslabs/cli/tart")
	Cask     string   // brew cask name
	Script   string   // fallback install one-liner when brew is unavailable/unsuitable
	Note     string   // honest one-line description
}

// brewLeaf is the formula name brew reports in `brew list` for a (possibly tapped) Brew field:
// "cirruslabs/cli/tart" → "tart", "uv" → "uv".
func (t Tool) brewLeaf() string {
	if t.Brew == "" {
		return ""
	}
	parts := strings.Split(t.Brew, "/")
	return parts[len(parts)-1]
}

// Status is a tool plus what detection found. Source is "brew" | "cask" | "standalone" | "missing".
type Status struct {
	Tool          Tool
	Present       bool
	Source        string
	Path          string   // resolved binary or app path ("" when missing)
	ShadowedPaths []string // other executables of the same name later on PATH (shadowed by Path); nil if none
}

// Installable reports whether safeslop should offer to install this tool: only when detection has run
// (Source != "unknown"), the tool is missing, AND an install route exists. Present tools are never
// installable (no-clobber guarantee); undetected tools show no Install button until classified.
func (s Status) Installable() bool {
	return s.Source != "unknown" && !s.Present && (s.Tool.Brew != "" || s.Tool.Cask != "" || s.Tool.Script != "")
}

// CatalogStatuses returns the whole catalog with detection DEFERRED (Source "unknown") — an instant
// first paint for the Installs tab, so every tool is listed immediately with a "?" while the
// brew-dependent detection pass runs.
func CatalogStatuses() []Status {
	cat := Catalog()
	out := make([]Status, 0, len(cat))
	for _, t := range cat {
		out = append(out, Status{Tool: t, Present: false, Source: "unknown"})
	}
	return out
}

// Catalog is the data-driven tool list. Add a row to extend; nothing else needs to change.
func Catalog() []Tool {
	return []Tool{
		// Runtimes & package managers
		{Name: "uv", Category: CatRuntime, Detect: []string{"uv"}, Brew: "uv",
			Script: "curl -LsSf https://astral.sh/uv/install.sh | sh", Note: "Python package/runtime manager"},
		{Name: "bun", Category: CatRuntime, Detect: []string{"bun", "bunx"}, Brew: "oven-sh/bun/bun",
			Script: "curl -fsSL https://bun.sh/install | bash", Note: "JS runtime + package manager (bunx)"},
		{Name: "pnpm", Category: CatRuntime, Detect: []string{"pnpm"}, Brew: "pnpm",
			Script: "curl -fsSL https://get.pnpm.io/install.sh | sh -", Note: "fast Node package manager"},
		{Name: "mise", Category: CatRuntime, Detect: []string{"mise"}, Brew: "mise",
			Script: "curl https://mise.run | sh", Note: "polyglot tool-version manager"},
		{Name: "nix", Category: CatRuntime, Detect: []string{"nix"},
			Script: "curl --proto '=https' --tlsv1.2 -sSf -L https://install.determinate.systems/nix | sh -s -- install",
			Note:   "Nix package manager (Determinate installer)"},

		// Languages & toolchains
		{Name: "Go", Category: CatLang, Detect: []string{"go"}, Brew: "go",
			Note: "Go toolchain — also builds the safeslop engine"},
		{Name: "Rust", Category: CatLang, Detect: []string{"cargo", "rustc"},
			Script: "curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y",
			Note:   "Rust toolchain — cargo/rustc via rustup"},
		{Name: "Swift", Category: CatLang, Detect: []string{"swift", "swiftc"}, Brew: "swiftly",
			Note: "Swift toolchain — brew installs swiftly, then `swiftly install latest` (or comes with Xcode)"},
		{Name: "Clojure", Category: CatLang, Detect: []string{"clojure", "clj"}, Brew: "clojure/tools/clojure",
			Note: "Clojure CLI (clj/clojure)"},
		{Name: "Babashka", Category: CatLang, Detect: []string{"bb"}, Brew: "borkdude/brew/babashka",
			Note: "fast-starting Clojure scripting (bb)"},
		{Name: "Lean 4", Category: CatLang, Detect: []string{"lean", "elan"}, Brew: "elan",
			Note: "Lean 4 theorem prover (via the elan toolchain manager)"},
		{Name: "Xcode", Category: CatLang, Detect: []string{"xcodebuild"}, AppPath: "/Applications/Xcode.app",
			Note: "Apple IDE + SDKs — install from the App Store"},

		// Source control & forges (repo pull + ephemeral-key flows)
		{Name: "git", Category: CatForge, Detect: []string{"git"}, Brew: "git",
			Note: "version control — required for repo operations"},
		{Name: "GitHub CLI", Category: CatForge, Detect: []string{"gh"}, Brew: "gh",
			Note: "GitHub auth, PRs, ephemeral deploy keys/tokens"},
		{Name: "tea", Category: CatForge, Detect: []string{"tea"}, Brew: "tea",
			Note: "Gitea/Forgejo CLI — Forgejo auth + ephemeral keys"},

		// Containers & VMs
		{Name: "Docker CLI", Category: CatContainer, Detect: []string{"docker"}, Brew: "docker",
			Note: "docker CLI — needs a daemon (OrbStack / Docker Desktop)"},
		{Name: "OrbStack", Category: CatContainer, Detect: []string{"orb", "orbctl"},
			AppPath: "/Applications/OrbStack.app", Cask: "orbstack", Note: "fast Docker + Linux VM host"},
		{Name: "Tart", Category: CatContainer, Detect: []string{"tart"}, Brew: "cirruslabs/cli/tart",
			Note: "Apple-silicon macOS/Linux VMs (the vm tier)"},

		// Secret managers
		{Name: "1Password CLI", Category: CatSecrets, Detect: []string{"op"}, Cask: "1password-cli",
			Note: "resolves op:// secret references"},
		{Name: "1Password", Category: CatSecrets, AppPath: "/Applications/1Password.app", Cask: "1password",
			Note: "1Password app (Touch ID unlock for op)"},
		{Name: "Bitwarden CLI", Category: CatSecrets, Detect: []string{"bw"}, Brew: "bitwarden-cli",
			Note: "Bitwarden vault CLI"},
		{Name: "KeePassXC", Category: CatSecrets, Detect: []string{"keepassxc-cli"},
			AppPath: "/Applications/KeePassXC.app", Cask: "keepassxc", Note: "offline KeePass vault"},
		{Name: "Proton Pass", Category: CatSecrets, AppPath: "/Applications/Proton Pass.app", Cask: "proton-pass",
			Note: "Proton Pass app"},

		// safeslop core
		{Name: "fish", Category: CatCore, Detect: []string{"fish"}, Brew: "fish", Note: "shell for the scripts stack"},

		// Agents
		{Name: "Claude Code", Category: CatAgents, Detect: []string{"claude"},
			Script: "curl -fsSL https://claude.ai/install.sh | bash", Note: "the Claude Code agent"},
		{Name: "Codex", Category: CatAgents, Detect: []string{"codex"},
			Script: "npm install -g @openai/codex", Note: "the OpenAI Codex agent (needs npm)"},
		{Name: "opencode", Category: CatAgents, Detect: []string{"opencode"}, Brew: "sst/tap/opencode",
			Note: "the opencode agent"},
	}
}

// probe is the injectable host environment detection reads (so DetectAll is testable without brew).
type probe struct {
	lookPath   func(string) (string, bool) // resolve a binary on PATH
	lookAll    func(string) []string       // all PATH matches for a binary (which -a); nil disables shadow detection
	appExists  func(string) bool           // does /Applications/X.app exist
	formulae   map[string]bool             // installed brew formula leaf names
	casks      map[string]bool             // installed brew cask names
	brewPrefix string                      // e.g. /opt/homebrew — for source classification
}

// detect classifies one tool against a probe. No mutation.
func detect(p probe, t Tool) Status {
	for _, bin := range t.Detect {
		if path, ok := p.lookPath(bin); ok {
			src := "standalone"
			if leaf := t.brewLeaf(); leaf != "" && p.formulae[leaf] {
				src = "brew"
			} else if p.brewPrefix != "" && strings.HasPrefix(path, p.brewPrefix) {
				src = "brew"
			}
			st := Status{Tool: t, Present: true, Source: src, Path: path}
			if p.lookAll != nil {
				if all := p.lookAll(bin); len(all) > 1 {
					st.ShadowedPaths = all[1:] // all[0] is the winner (== path); the rest are shadowed
				}
			}
			return st
		}
	}
	if t.AppPath != "" && p.appExists(t.AppPath) {
		src := "cask"
		if t.Cask != "" && !p.casks[t.Cask] {
			src = "app" // present on disk but not a brew-managed cask
		}
		return Status{Tool: t, Present: true, Source: src, Path: t.AppPath}
	}
	return Status{Tool: t, Present: false, Source: "missing"}
}

// realProbe builds the live host probe against the RECONSTRUCTED host environment, so detection works
// from a Finder-launched .app (stripped process PATH) exactly as it does from a terminal. PATH lookups
// and brew both resolve via hostenv; see internal/engine/hostenv for the two-environment firewall (the
// rich env is for host-side discovery only and never crosses into a sandbox).
func realProbe() probe {
	env := hostenv.Reconstruct()
	return probeFromEnv(env.LookPath, env.LookAll, env.Environ())
}

// probeFromEnv builds a host probe from a reconstructed lookPath + environment. brew is resolved via
// the reconstructed PATH and run with that environment; if brew can't be found the formula/cask sets
// are empty (present binaries then read as "standalone", cask-only tools simply aren't installable) —
// detection degrades, it does not crash.
func probeFromEnv(lookPath func(string) (string, bool), lookAll func(string) []string, environ []string) probe {
	br := brewRunner{lookPath: lookPath, environ: environ}
	prefix := ""
	if out, err := br.output("--prefix"); err == nil {
		prefix = strings.TrimSpace(out)
	}
	return probe{
		lookPath: lookPath,
		lookAll:  lookAll,
		appExists: func(p string) bool {
			_, err := os.Stat(p)
			return err == nil
		},
		formulae:   br.list("--formula"),
		casks:      br.list("--cask"),
		brewPrefix: prefix,
	}
}

// brewRunner resolves brew on the reconstructed PATH and runs it with the reconstructed environment,
// so brew works under a Finder launch (where bare `brew` is off the process PATH).
type brewRunner struct {
	lookPath func(string) (string, bool)
	environ  []string
}

// output runs `brew <args...>` and returns stdout, or errNoBrew when brew can't be resolved.
func (b brewRunner) output(args ...string) (string, error) {
	brew, ok := b.lookPath("brew")
	if !ok {
		return "", errNoBrew
	}
	cmd := exec.Command(brew, args...)
	cmd.Env = b.environ
	out, err := cmd.Output()
	return string(out), err
}

// list returns the set of installed names for `brew list <kind> -1`; empty when brew is missing.
func (b brewRunner) list(kind string) map[string]bool {
	set := map[string]bool{}
	out, err := b.output("list", kind, "-1")
	if err != nil {
		return set
	}
	for _, line := range strings.Split(out, "\n") {
		if name := strings.TrimSpace(line); name != "" {
			// casks/formulae may print as full paths under some configs; keep the leaf.
			set[filepath.Base(name)] = true
		}
	}
	return set
}

// DetectAll classifies the whole catalog against the live host (read-only).
func DetectAll() []Status {
	p := realProbe()
	cat := Catalog()
	out := make([]Status, 0, len(cat))
	for _, t := range cat {
		out = append(out, detect(p, t))
	}
	return out
}

// InstallArgv returns the command that installs a MISSING tool, preferring brew formula, then cask,
// then the tool's own script. It refuses for a present tool (no-clobber) or one with no route.
func InstallArgv(s Status) ([]string, error) {
	if s.Present {
		return nil, errAlreadyPresent
	}
	t := s.Tool
	switch {
	case t.Brew != "" && brewOnPath():
		return []string{"brew", "install", t.Brew}, nil
	case t.Cask != "" && brewOnPath():
		return []string{"brew", "install", "--cask", t.Cask}, nil
	case t.Script != "":
		return []string{"/bin/sh", "-c", t.Script}, nil
	case t.Cask != "" || t.Brew != "":
		return nil, errNeedsBrew
	default:
		return nil, errNoRoute
	}
}

func brewOnPath() bool {
	_, ok := hostenv.Reconstruct().LookPath("brew")
	return ok
}

// Detect classifies a single named tool against the live host, or false if the name isn't in the
// catalog. Used by InstallByName so the RPC layer works from a name alone.
func Detect(name string) (Status, bool) {
	p := realProbe()
	for _, t := range Catalog() {
		if t.Name == name {
			return detect(p, t), true
		}
	}
	return Status{}, false
}

// InstallByName installs the missing catalog tool `name`, streaming combined output lines to emit.
// Refuses unknown names and present tools (no-clobber). The command runs on the host as the user.
func InstallByName(name string, emit func(line string)) error {
	s, ok := Detect(name)
	if !ok {
		return errNoRoute
	}
	argv, err := InstallArgv(s)
	if err != nil {
		return err
	}
	emit("$ " + strings.Join(argv, " "))
	// Resolve the binary and run with the reconstructed environment so the install works under a
	// Finder launch: `brew` is resolved to its absolute path, and a curl|sh script inherits a PATH
	// that can find curl/sh. This runs on the host as the user — the rich env is correct here (the
	// sandbox firewall in cli.childEnv governs only what crosses into an isolated agent).
	env := hostenv.Reconstruct()
	bin := argv[0]
	if resolved, ok := env.LookPath(bin); ok {
		bin = resolved
	}
	cmd := exec.Command(bin, argv[1:]...)
	cmd.Env = env.Environ()
	lw := &lineWriter{emit: emit}
	cmd.Stdout = lw
	cmd.Stderr = lw // both streams share the writer (it is mutex-guarded)
	runErr := cmd.Run()
	lw.flush()
	return runErr
}

// lineWriter turns arbitrary Write chunks into whole emitted lines. Stdout and Stderr both target it,
// so it must be safe for concurrent writes.
type lineWriter struct {
	mu    sync.Mutex
	emit  func(string)
	carry string
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.carry += string(p)
	for {
		i := strings.IndexByte(w.carry, '\n')
		if i < 0 {
			break
		}
		w.emit(strings.TrimRight(w.carry[:i], "\r"))
		w.carry = w.carry[i+1:]
	}
	return len(p), nil
}

func (w *lineWriter) flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.carry != "" {
		w.emit(w.carry)
		w.carry = ""
	}
}
