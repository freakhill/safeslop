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
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/freakhill/safeslop/internal/engine/buildinfo"
	"github.com/freakhill/safeslop/internal/engine/hostenv"
	"github.com/freakhill/safeslop/internal/engine/install"
	"github.com/freakhill/safeslop/internal/engine/sandbox"
)

var (
	errAlreadyPresent = errors.New("tool already installed — safeslop won't touch an existing install")
	errNeedsBrew      = errors.New("this tool installs via Homebrew, which isn't on PATH (install brew first)")
	errNoRoute        = errors.New("no install route for this tool")
	errNoBrew         = errors.New("brew is not resolvable on the reconstructed PATH")
	// errUsePin is an internal sentinel: the tool has an embedded checksum-pinned binary release, so it
	// installs via the fail-closed verified Route A (install.Apply), not an argv. InstallByName catches
	// it and runs installPinned; it is never surfaced to a caller as a failure.
	errUsePin = errors.New("install via verified embedded pin")
	// errUseInstaller is the analogous sentinel for a checksum-pinned installer binary (rustup-init): it
	// is fetched + verified, then executed. InstallByName catches it and runs installVerifiedInstaller.
	errUseInstaller = errors.New("install via verified installer binary")
)

// pinFor returns the embedded verified-install pin for a catalog tool, when one exists. A tool with a
// pin installs through the checksum-verified Route A (sha256 → notarized-binary trust chain) instead of
// piping a remote script into a shell (specs/0036 item ①). The pin's Name must match the catalog Name.
func pinFor(name string) (install.Pin, bool) {
	for _, p := range install.DesiredState() {
		if p.Name == name {
			return p, true
		}
	}
	return install.Pin{}, false
}

func hasPin(name string) bool {
	_, ok := pinFor(name)
	return ok
}

// pinForTool matches a pin by the catalog Name OR any Detect binary name, so a pin named for the binary
// (e.g. "claude") routes a catalog entry whose display Name differs (e.g. "Claude Code").
func pinForTool(t Tool) (install.Pin, bool) {
	if p, ok := pinFor(t.Name); ok {
		return p, true
	}
	for _, d := range t.Detect {
		if p, ok := pinFor(d); ok {
			return p, true
		}
	}
	return install.Pin{}, false
}

func hasPinForTool(t Tool) bool {
	_, ok := pinForTool(t)
	return ok
}

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
	Name      string             // display name + stable id
	Category  string             // one of the Cat* constants
	Detect    []string           // candidate CLI names; first found on PATH wins
	AppPath   string             // optional /Applications/X.app (for cask apps without a CLI)
	Brew      string             // brew formula (may be tapped, e.g. "cirruslabs/cli/tart")
	Cask      string             // brew cask name
	Installer *VerifiedInstaller // checksum-pinned installer binary — the verified replacement for Script
	Script    string             // fallback install one-liner when brew is unavailable/unsuitable
	Note      string             // honest one-line description
	// PostInstall is an honest disclosure of what happens AFTER safeslop's verified bootstrap, for tools
	// whose verification covers only the first artifact: a self-updating tool (claude) or a multi-component
	// installer that fetches more code under its own trust root (rustup's toolchain, nix's components). The
	// cockpit appends it to the precautions so the "verified" badge isn't read as an ongoing guarantee.
	PostInstall string
}

// VerifiedInstaller is a checksum-pinned installer binary (e.g. rustup-init) that safeslop fetches,
// sha256-verifies, then EXECUTES with Args — the verified replacement for a `curl … | sh` Script. Most
// "script-only" tools (rustup, nix) just download a versioned installer and run it; pinning that binary
// (Route A trust) eliminates the unverified remote code. Unlike a placed-binary pin it runs and may
// modify the environment, so the cockpit classifies it as "verified-installer" (specs/0036 Task 6).
type VerifiedInstaller struct {
	URL     string   // the versioned, pinnable installer binary URL (never "latest")
	SHA256  string   // sha256 of that binary
	Version string   // pinned installer version
	Args    []string // args to run the installer with, e.g. ["-y"]
	// Confine runs the installer under sandbox-exec with the secret-read-deny scope (specs/0038): a
	// confinable installer (rustup) gets broad home write + network but cannot read the user's SSH keys,
	// cloud creds, or shell history. Installers needing root + system-wide changes (nix) set this false
	// and run unconfined.
	Confine bool
	// Provenance records how SHA256 was obtained (install.ProvenanceVendor when it matches a vendor-
	// published checksum, install.ProvenanceTLS/"" when no vendor checksum exists and the pin is
	// safeslop's own TOFU hash). Surfaced as the cockpit's trust badge; defaults to the cautious TLS
	// reading when unset, so an un-annotated installer never over-claims "vendor".
	Provenance string
}

// vendorChecksum reports whether the installer's pinned SHA matches a vendor-published checksum.
func (v *VerifiedInstaller) vendorChecksum() bool { return v.Provenance == install.ProvenanceVendor }

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
			Installer: &VerifiedInstaller{
				// The `… | sh -s -- install` one-liner just downloads this versioned Determinate
				// nix-installer and runs it; pinning + sha256-verifying the binary replaces the unverified
				// remote script. Determinate publishes no checksum sidecar, so this sha256 (computed from
				// the pinned tag over TLS, 2026-06-22) is the trust floor — fail-closed on every install,
				// weaker pin-time provenance (same posture as pnpm).
				URL:        "https://install.determinate.systems/nix/tag/v3.21.2/nix-installer-aarch64-darwin",
				SHA256:     "17c0845f0133c9544b293449d853f5873ef9692b61cea1fe2ddf3b3a2500b81b",
				Version:    "3.21.2",
				Args:       []string{"install", "--no-confirm"},
				Confine:    false,                 // creates /nix + a daemon via sudo — cannot run under a user sandbox
				Provenance: install.ProvenanceTLS, // Determinate publishes no checksum sidecar — TOFU hash
			},
			Script: "curl --proto '=https' --tlsv1.2 -sSf -L https://install.determinate.systems/nix | sh -s -- install",
			Note:   "Nix package manager (Determinate installer)",
			PostInstall: "The installer then fetches and configures Nix system-wide; those components are " +
				"verified by the installer itself, not by safeslop's pin."},

		// Languages & toolchains
		{Name: "Go", Category: CatLang, Detect: []string{"go"}, Brew: "go",
			Note: "Go toolchain — also builds the safeslop engine"},
		{Name: "Rust", Category: CatLang, Detect: []string{"cargo", "rustc"},
			Installer: &VerifiedInstaller{
				// `curl https://sh.rustup.rs | sh` just downloads this versioned rustup-init and runs it;
				// pinning + sha256-verifying it (matches rustup's published .sha256, 2026-06-22) replaces
				// the unverified remote script. rustup-init then fetches the toolchain, which rustup
				// verifies against its own signed manifests.
				URL:        "https://static.rust-lang.org/rustup/archive/1.29.0/aarch64-apple-darwin/rustup-init",
				SHA256:     "aeb4105778ca1bd3c6b0e75768f581c656633cd51368fa61289b6a71696ac7e1",
				Version:    "1.29.0",
				Args:       []string{"-y"},
				Confine:    true,                     // user-space install (~/.rustup, ~/.cargo) — sandbox it with secret-deny
				Provenance: install.ProvenanceVendor, // matches rustup's published .sha256
			},
			Script: "curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y",
			Note:   "Rust toolchain — cargo/rustc via rustup",
			PostInstall: "rustup-init then downloads the Rust toolchain, which rustup verifies against its " +
				"own signed manifests (not safeslop's pin)."},
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
			Script: "curl -fsSL https://claude.ai/install.sh | bash", Note: "the Claude Code agent",
			PostInstall: "After install, Claude Code keeps itself up to date; safeslop verifies this first " +
				"download only."},
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
// then — for a tool with an embedded checksum pin — the verified Route A (signalled by errUsePin, which
// InstallByName handles), and only as a last resort the tool's own remote script. It refuses for a
// present tool (no-clobber) or one with no route. The pin precedes the script so a pinned tool can
// never fall back to `curl … | sh` (specs/0036 item ①).
func InstallArgv(s Status) ([]string, error) {
	return installArgv(s, brewOnPath())
}

// installArgv is the pure route resolver; brewAvail is injected so the ordering is testable without a
// live brew on PATH.
func installArgv(s Status, brewAvail bool) ([]string, error) {
	if s.Present {
		return nil, errAlreadyPresent
	}
	t := s.Tool
	switch {
	case t.Brew != "" && brewAvail:
		return []string{"brew", "install", t.Brew}, nil
	case t.Cask != "" && brewAvail:
		return []string{"brew", "install", "--cask", t.Cask}, nil
	case hasPinForTool(t):
		return nil, errUsePin // verified embedded-pin install — not an argv (kills the curl|sh route)
	case t.Installer != nil:
		return nil, errUseInstaller // verified installer binary — fetched, verified, then executed
	case t.Script != "":
		return []string{"/bin/sh", "-c", t.Script}, nil
	case t.Cask != "" || t.Brew != "":
		return nil, errNeedsBrew
	default:
		return nil, errNoRoute
	}
}

// Verification classifies how trustworthy a missing tool's install route is — the axis the cockpit
// consent gate and hover tooltip surface (specs/0037).
type Verification string

const (
	VerifiedPin            Verification = "verified-pin"       // sha256-pinned binary, notarized-trust chain, no remote code
	VerifiedInstallerRoute Verification = "verified-installer" // sha256-pinned installer binary, fetched+verified, then run
	BrewManaged            Verification = "brew"               // delegated to Homebrew (its own verification)
	UnverifiedRun          Verification = "unverified-run"     // runs a remote script with user privileges — highest blast radius
)

const (
	brewPrecautions = "safeslop installs this through your existing Homebrew (brew install). safeslop runs " +
		"no remote code itself; Homebrew performs its own download and checksum verification."
	unverifiedPrecautions = "⚠︎ No checksum-pinned release exists for this tool, so installing it runs " +
		"a script downloaded from the internet with your user privileges. safeslop shows you the exact command and " +
		"requires explicit confirmation before running it — but nothing is verified beyond HTTPS transport."
	needsBrewPrecautions = "Requires Homebrew, which isn't on PATH — install Homebrew first, then safeslop can " +
		"install this via brew (no remote code run by safeslop)."
)

// trustAnchor describes where the embedded checksum's authority comes from. Only a notarized release
// seals the pin set under Apple's code signature (tampering breaks the signature); a dev/adhoc build
// merely compiles the checksum in (no on-disk tamper-resistance). The precaution must not claim the
// former when only the latter holds — the running build decides (specs/0036 honesty fix).
func trustAnchor() string {
	if buildinfo.Notarized() {
		return "a checksum sealed into the notarized safeslop binary (tampering with the pin breaks " +
			"safeslop's own Apple signature)"
	}
	return "a checksum compiled into safeslop itself (this build is not notarized, so the pin resists " +
		"network tampering but not modification of safeslop on disk)"
}

// provenanceClause discloses whether the pinned checksum is vendor-published or safeslop's own trust-on-
// first-use hash — the legibility distinction the cockpit must not flatten (specs/0036). Leads with a
// space so it appends cleanly after the verify sentence.
func provenanceClause(vendorChecksum bool) string {
	if vendorChecksum {
		return " The pinned checksum matches one the vendor publishes for this release."
	}
	return " ⚠︎ The vendor publishes no checksum for this release, so the pin is the hash safeslop " +
		"recorded from the download itself (trust-on-first-use) — weaker provenance than a vendor-published checksum."
}

// withPostInstall appends a tool's after-the-bootstrap disclosure, if any (fix for the "verified badge
// read as an ongoing guarantee" gap — self-updaters and multi-component installers).
func withPostInstall(s, postInstall string) string {
	if postInstall == "" {
		return s
	}
	return s + " " + postInstall
}

// verifiedPinPrecautions is the hover/consent text for a sha256-pinned placed-binary install.
func verifiedPinPrecautions(vendorChecksum bool, postInstall string) string {
	s := "safeslop downloads this from the pinned release over HTTPS and verifies its SHA-256 against " +
		trustAnchor() + " before installing — no remote script runs." +
		provenanceClause(vendorChecksum) +
		" The previous version is kept for one-command rollback."
	return withPostInstall(s, postInstall)
}

func installerVerifyPrefix() string {
	return "safeslop downloads this tool's official installer over HTTPS and verifies its SHA-256 against " +
		trustAnchor() + " before running it — replacing an unverified `curl | sh`."
}

// installerConfinedPrecautions is the text for a verified installer that runs under the secret-deny sandbox.
func installerConfinedPrecautions(vendorChecksum bool, postInstall string) string {
	s := installerVerifyPrefix() + provenanceClause(vendorChecksum) +
		" It then runs under a macOS sandbox that blocks it from reading your SSH keys, cloud credentials, " +
		"and shell history, and confines its writes to your home directory."
	return withPostInstall(s, postInstall)
}

// installerUnconfinedPrecautions is the text for a verified installer that needs root and runs unconfined.
func installerUnconfinedPrecautions(vendorChecksum bool, postInstall string) string {
	s := installerVerifyPrefix() + provenanceClause(vendorChecksum) +
		" ⚠︎ This installer needs administrator privileges and makes system-wide changes, so it runs " +
		"UNCONFINED — only proceed if you trust it."
	return withPostInstall(s, postInstall)
}

// provenanceLabel maps a vendor-checksum bool to the cockpit's provenance badge value.
func provenanceLabel(vendorChecksum bool) string {
	if vendorChecksum {
		return install.ProvenanceVendor
	}
	return install.ProvenanceTLS
}

func manualPrecautions(name string) string {
	return "No automatic install route — safeslop won't fetch or run anything. Install " + name + " yourself."
}

// Preview describes how a missing tool would be installed and the precautions safeslop takes. It is the
// single source of truth shared by the cockpit hover tooltip and the install consent gate (specs/0037),
// so the two surfaces can never disagree about what an install does. An empty Route means the tool is
// present (no-clobber) or has no actionable route.
type Preview struct {
	Name         string
	Route        string // brew | cask | verified-pin | script | needs-brew
	Command      string // the literal command that runs, or the verified-install description
	SourceURL    string // pin URL (verified) or "" (brew/script source lives in Command)
	SHA256       string // pinned sha256 (verified-pin only)
	Version      string // pinned version (verified-pin only)
	Verification Verification
	Provenance   string // verified-pin/installer only: install.ProvenanceVendor | install.ProvenanceTLS
	Precautions  string
	NeedsConsent bool // typed confirmation demanded — an unverified script OR an unconfined admin installer
}

// InstallPreview classifies a missing tool's install route against the live host (brew availability).
func InstallPreview(s Status) Preview { return installPreview(s, brewOnPath()) }

// installPreview is the pure classifier; brewAvail is injected so it is testable without a live brew. It
// mirrors installArgv's route order exactly, so the gate's preview matches what InstallByName will run.
func installPreview(s Status, brewAvail bool) Preview {
	p := Preview{Name: s.Tool.Name}
	if s.Present {
		return p // present tools are not installable; Precautions handles their text
	}
	t := s.Tool
	switch {
	case t.Brew != "" && brewAvail:
		p.Route, p.Verification, p.Command, p.Precautions = "brew", BrewManaged, "brew install "+t.Brew, brewPrecautions
	case t.Cask != "" && brewAvail:
		p.Route, p.Verification, p.Command, p.Precautions = "cask", BrewManaged, "brew install --cask "+t.Cask, brewPrecautions
	case hasPinForTool(t):
		pin, _ := pinForTool(t)
		p.Route, p.Verification = "verified-pin", VerifiedPin
		p.SourceURL, p.SHA256, p.Version = pin.URL, pin.SHA256, pin.Version
		p.Provenance = provenanceLabel(pin.VendorChecksum())
		p.Command = "verified install: " + pin.Name + " " + pin.Version + " (sha256-pinned binary)"
		p.Precautions = verifiedPinPrecautions(pin.VendorChecksum(), t.PostInstall)
	case t.Installer != nil:
		p.Route, p.Verification = "verified-installer", VerifiedInstallerRoute
		p.SourceURL, p.SHA256, p.Version = t.Installer.URL, t.Installer.SHA256, t.Installer.Version
		p.Provenance = provenanceLabel(t.Installer.vendorChecksum())
		p.Command = strings.Join(append([]string{filepath.Base(t.Installer.URL)}, t.Installer.Args...), " ")
		if t.Installer.Confine {
			p.Precautions = installerConfinedPrecautions(t.Installer.vendorChecksum(), t.PostInstall)
		} else {
			p.Precautions = installerUnconfinedPrecautions(t.Installer.vendorChecksum(), t.PostInstall)
			// Unconfined + admin/root has blast radius ≥ an unverified user script — gate it on a typed
			// confirm too, so friction tracks risk and not merely "is this a raw remote script" (specs/0037).
			p.NeedsConsent = true
		}
	case t.Script != "":
		p.Route, p.Verification, p.NeedsConsent = "script", UnverifiedRun, true
		p.Command, p.Precautions = t.Script, unverifiedPrecautions
	case t.Brew != "" || t.Cask != "":
		p.Route, p.Precautions = "needs-brew", needsBrewPrecautions
	default:
		p.Precautions = manualPrecautions(t.Name)
	}
	return p
}

// Precautions is the hover-tooltip text for ANY tool — present (no-clobber, plus a shadow note),
// installable (the route's precautions), or manual (no route). Shares InstallPreview for installables.
func Precautions(s Status) string {
	if s.Present {
		base := "Already installed"
		if s.Source != "" && s.Source != "missing" {
			base += " via " + s.Source
		}
		base += ". safeslop never modifies, upgrades, or clobbers an existing install."
		if n := len(s.ShadowedPaths); n > 0 {
			base += fmt.Sprintf(" It resolves to %s and shadows %d other executable(s) of the same name later on PATH.", s.Path, n)
		}
		return base
	}
	if s.Installable() {
		return InstallPreview(s).Precautions
	}
	return manualPrecautions(s.Tool.Name)
}

// InstallRouteHint returns a human-readable description of how a missing tool would be installed — the
// brew/cask/script argv joined, or the verified-pin route for a tool with an embedded checksum pin. ""
// when not installable. Used by the cockpit Installs-tab preview (the control server's InstallHint).
func InstallRouteHint(s Status) string {
	return InstallPreview(s).Command // single source of truth for "what runs" across every route
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
	if errors.Is(err, errUsePin) {
		return installPinned(s.Tool, emit) // verified Route A instead of curl|sh
	}
	if errors.Is(err, errUseInstaller) {
		return installVerifiedInstaller(s.Tool, emit) // fetch+verify+run the pinned installer
	}
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

// installPinned installs a tool that ships an embedded checksum-pinned binary release through the
// fail-closed verified installer (install.Apply: download → sha256 verify → install) instead of a raw
// remote `curl … | sh`. The catalog already established the tool is missing, so this is always an
// install; install.Plan validates the pin (fail-closed) and yields the single Action.
func installPinned(t Tool, emit func(line string)) error {
	pin, ok := pinForTool(t)
	if !ok {
		return errNoRoute
	}
	dirs, err := install.DefaultDirs()
	if err != nil {
		return err
	}
	res, err := install.Plan(install.State{}, []install.Pin{pin}) // empty state → ActionInstall, pin validated
	if err != nil {
		return err
	}
	emit("$ safeslop verified-install " + pin.Name + " " + pin.Version + " (sha256-pinned, no curl|sh)")
	return install.Apply(context.Background(), res, dirs, install.HTTPFetcher{}, func(e install.Event) {
		emit("  [" + e.Tool + "] " + string(e.Kind) + " " + e.Msg)
	})
}

// installVerifiedInstaller fetches the tool's pinned installer binary, sha256-verifies it (fail-closed
// via install.FetchVerified), then executes the VERIFIED local file with its args — running known,
// checksum-matched code instead of piping an unverified remote script into a shell. The installer runs
// on the host as the user with the reconstructed environment, like the curl|sh it replaces. A confinable
// installer is additionally sandbox-wrapped with the secret-read-deny scope (installerRunArgv, specs/0038).
// A host-tier squid egress allowlist was investigated and rejected as infeasible on macOS (Seatbelt can't
// host-allowlist) and low-value once installers are verified + self-verifying — see specs/0038.
func installVerifiedInstaller(t Tool, emit func(line string)) error {
	in := t.Installer
	if in == nil {
		return errNoRoute
	}
	dirs, err := install.DefaultDirs()
	if err != nil {
		return err
	}
	emit("$ safeslop verified-installer " + t.Name + " " + in.Version + " (sha256-pinned, no curl|sh)")
	emit("  downloading + verifying " + in.URL)
	path, cleanup, err := install.FetchVerified(context.Background(), in.URL, in.SHA256, dirs.TmpDir, install.HTTPFetcher{})
	if err != nil {
		return err
	}
	defer cleanup()

	argv, sbCleanup, confined := installerRunArgv(in, path)
	defer sbCleanup()
	if confined {
		emit("  verified — running installer under a sandbox (no access to your secrets)")
	} else {
		emit("  verified — running installer (unconfined: needs admin privileges)")
	}
	env := hostenv.Reconstruct()
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = env.Environ()
	lw := &lineWriter{emit: emit}
	cmd.Stdout = lw
	cmd.Stderr = lw
	runErr := cmd.Run()
	lw.flush()
	return runErr
}

// installerRunArgv builds the command to run a verified installer at binPath, wrapping it under
// sandbox-exec with the secret-read-deny scope when the installer is confinable AND sandbox-exec is
// available (specs/0038). Returns the argv, a cleanup for any sandbox temp profile, and whether confined.
// Confinement grants broad home write + network so the install works, but the sandbox's auto-denied
// credential set means the installer can't read or exfiltrate the user's secrets.
func installerRunArgv(in *VerifiedInstaller, binPath string) (argv []string, cleanup func(), confined bool) {
	base := append([]string{binPath}, in.Args...)
	if !in.Confine || !sandbox.Available() {
		return base, func() {}, false
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return base, func() {}, false
	}
	wrapped, cl, err := sandbox.WrapArgv(base, home, "allow", sandbox.Scope{Write: []string{"~"}})
	if err != nil {
		return base, func() {}, false // fail-open to unconfined: a verified installer that can't be
		// sandboxed should still install, not silently no-op
	}
	return wrapped, cl, true
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
