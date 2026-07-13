// Package policy loads and validates a user's safeslop.cue against the embedded
// engine schema, returning a typed Config.
//
// The schema is compiled into the binary via go:embed and unified with the
// user's file in-process using cuelang.org/go, so there is no dependency on an
// external `cue` binary (specs/0001 §6.1 — the central win of the Go rewrite).
package policy

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/load"
)

//go:embed schema/schema.cue
var schemaSrc string

// PnpmRegistry authenticates an npm/pnpm registry by staging a scoped .npmrc
// whose _authToken is sourced from Token (an op:// or env: secret ref).
type PnpmRegistry struct {
	Host  string `json:"host"`
	Token string `json:"token"`
	Scope string `json:"scope,omitempty"`
}

// AwsSso mints short-lived AWS creds from an SSO-configured profile (specs/0009). RoleArn +
// SessionPolicy (both, optional) downscope the creds via `sts assume-role` with an inline session
// policy — least-privilege, scope-first (specs/0027); the role must be assumable by the SSO identity.
type AwsSso struct {
	Profile       string `json:"profile"`
	Region        string `json:"region,omitempty"`
	RoleArn       string `json:"roleArn,omitempty"`
	SessionPolicy string `json:"sessionPolicy,omitempty"`
}

// GcpAdc delivers a short-lived GCP access token from ADC, refresh token stripped (specs/0009).
// Scopes, when set, downscope the minted token to least-privilege (scope-first, specs/0026),
// e.g. ["https://www.googleapis.com/auth/devstorage.read_only"]; empty = ADC's default scopes.
type GcpAdc struct {
	Scopes []string `json:"scopes,omitempty"`
}

// EksCluster pre-authenticates an EKS cluster: the host runs `aws eks get-token`
// (bearer) + `aws eks describe-cluster` (endpoint/CA) under Profile's SSO (specs/0010).
type EksCluster struct {
	Name    string `json:"name"`
	Region  string `json:"region,omitempty"`
	Profile string `json:"profile,omitempty"`
}

// GkeCluster pre-authenticates a GKE cluster: `gke-gcloud-auth-plugin` (bearer, via
// ADC) + `gcloud container clusters describe` (endpoint/CA). Location is a zone or
// region (specs/0010).
type GkeCluster struct {
	Name     string `json:"name"`
	Location string `json:"location"`
	Project  string `json:"project,omitempty"`
}

// KubeCluster stages a scoped one-cluster kubeconfig (token inside, 0600) so the
// agent's kubectl needs no cloud CLI/creds inside the boundary. Exactly one of
// Eks/Gke (specs/0010).
type KubeCluster struct {
	Eks *EksCluster `json:"eks,omitempty"`
	Gke *GkeCluster `json:"gke,omitempty"`
}

// RepoCred names one repository in a multi-repo credential, with its own access (specs/0047 P2).
type RepoCred struct {
	Repo  string `json:"repo"`            // "owner/name"
	Write bool   `json:"write,omitempty"` // rw deploy key for this repo (default ro)
}

// GithubCreds stages per-run repo-scoped ephemeral GitHub credentials over HTTPS. In "app" mode
// (default) the host mints a short-lived installation token from a linked GitHub App account
// (~/.config/safeslop/accounts.cue); in "pat" mode an existing fine-grained token is staged from
// Pat. Only the token crosses the boundary; git talks HTTPS via per-URL credential helpers
// (specs/0069, generalizing the specs/0047 PAT renderer). App-token permissions are token-wide, so
// Repos partition into ro/rw scopes by Write and one token is minted per partition (specs/0068 F1).
type GithubCreds struct {
	Mode  string     `json:"mode,omitempty"` // "app" (default) or "pat" (stage an existing fine-grained token)
	Write bool       `json:"write,omitempty"`
	Ttl   string     `json:"ttl,omitempty"`
	Pat   string     `json:"pat,omitempty"` // secret ref for mode:"pat"; token value is staged 0600, never embedded in config
	Repos []RepoCred `json:"repos,omitempty"`
	Api   *GithubApi `json:"api,omitempty"` // opt-in staged API token (P2; staging with Enabled errors in P1)
}

// GithubApi opts a profile into a staged GitHub API token whose permission set is token-wide
// (specs/0068 F5). P1 lands the struct for schema stability; staging with Enabled is a P2 error.
type GithubApi struct {
	Enabled     bool     `json:"enabled,omitempty"`
	Permissions []string `json:"permissions,omitempty"`
}

// ForgejoCreds is the Forgejo/Gitea sibling of GithubCreds: a per-run repo-scoped ephemeral deploy
// key on a non-GitHub forge (Codeberg, self-hosted, etc.). Forgejo has no `gh`-style ambient
// auth, so Token is an explicit secret ref (op://... or env:NAME) for the API call. URL is the
// instance base (e.g. "https://codeberg.org"); when empty the host is inferred from the cwd
// origin remote (specs/0047).
type ForgejoCreds struct {
	Write   bool        `json:"write,omitempty"`
	Ttl     string      `json:"ttl,omitempty"`
	URL     string      `json:"url,omitempty"`
	Repos   []RepoCred  `json:"repos,omitempty"`    // multi-repo: one deploy key per entry (specs/0047 P2)
	SSHPort int         `json:"ssh-port,omitempty"` // instance git SSH port for multi-repo rewrites (default 22)
	Api     *ForgejoApi `json:"api,omitempty"`      // opt-in staged API token (P2; enabling requires AckAccountWide, specs/0068 F5)
	// The account token that registers/revokes deploy keys now lives in ~/.config/safeslop/accounts.cue
	// (safeslop creds link forgejo), never in policy — Mode/Token/Pat were removed in specs/0069.
}

// ForgejoApi opts a profile into a staged Forgejo API token. Forgejo tokens are account-wide (not
// repo-scoped), so Enabled requires AckAccountWide (enforced at load). P1 lands the struct for
// schema stability; staging with Enabled is a P2 error.
type ForgejoApi struct {
	Enabled        bool `json:"enabled,omitempty"`
	AckAccountWide bool `json:"ackAccountWide,omitempty"`
}

// Credentials groups the credential providers a profile uses (SP2; aws/gcp SP/0009; kube SP/0010;
// github SP/0011, specs/0069; forgejo specs/0047).
type Credentials struct {
	Pnpm    []PnpmRegistry `json:"pnpm,omitempty"`
	Aws     *AwsSso        `json:"aws,omitempty"`
	Gcp     *GcpAdc        `json:"gcp,omitempty"`
	Kube    *KubeCluster   `json:"kube,omitempty"`
	Github  *GithubCreds   `json:"github,omitempty"`
	Forgejo *ForgejoCreds  `json:"forgejo,omitempty"`
}

// Toolchain layers a pinned tool environment onto any environment (SP5). When Run is set,
// safeslop launches that mise task / nix app instead of the agent; otherwise the agent is wrapped.
type Toolchain struct {
	Kind string `json:"kind"`
	Run  string `json:"run,omitempty"`
}

// ProjectionItem is one allowlisted host source copied read-only into the ephemeral home
// (specs/0096 T1 FLO verdict). Source is a host path, optionally ~/$HOME or $XDG_CONFIG_HOME
// relative. Kind is "file" (default), "dir", or "glob". Optional defaults to true (a nil pointer
// is treated as true by the resolver); a required item fails closed when absent/unreadable.
// Label carries provenance/legibility text (e.g. "pi-agent", "fish").
type ProjectionItem struct {
	Source   string `json:"source"`
	Target   string `json:"target,omitempty"`
	Kind     string `json:"kind,omitempty"`
	Optional *bool  `json:"optional,omitempty"`
	Label    string `json:"label,omitempty"`
}

// Projection is the engine-owned read-only host config projection model: a positive allowlist
// of host config sources staged read-only under opaque paths and copied into /home/agent tmpfs
// by the entrypoint (specs/0096). Engine-owned in MVP — user-authored projection in safeslop.cue
// is rejected at load; only embedded builtin profiles populate this.
type Projection struct {
	Enabled bool             `json:"enabled,omitempty"`
	Items   []ProjectionItem `json:"items,omitempty"`
}

// Profile is one launchable configuration from safeslop.cue.
type Profile struct {
	Agent       string `json:"agent"`
	Environment string `json:"environment"`
	Workspace   string `json:"workspace,omitempty"`
	Network     string `json:"network"`
	// Egress lists extra allowlist domains for environment:container with network:deny,
	// unioned with the base allowlist + the agent's built-in providers (specs/0046).
	Egress []string `json:"egress,omitempty"`
	// Secrets maps an env var name to a secret ref (op://... or env:NAME),
	// resolved at launch and injected into the agent's environment.
	Secrets map[string]string `json:"secrets,omitempty"`
	// Credentials are staged before launch and wiped on exit.
	Credentials *Credentials `json:"credentials,omitempty"`
	// Toolchain provisions a pinned tool environment, orthogonal to Environment (SP5).
	Toolchain *Toolchain `json:"toolchain,omitempty"`
	// Bundles and Packages select build-time packages from the curated catalog
	// (specs/0058): Bundles are named sets, Packages are à la carte. Resolved by
	// policy.Resolve against the catalog (unknown names error); the agent's default
	// bundle is included unless BareAgent explicitly opts out.
	Bundles  []string `json:"bundles,omitempty"`
	Packages []string `json:"packages,omitempty"`
	// BareAgent honors `profile create --no-default-bundle`: launch exactly the
	// declared bundles/packages, even for agents that normally imply a bundle.
	BareAgent bool `json:"bareAgent,omitempty"`
	// Projection is the engine-owned read-only host config projection (specs/0096).
	// MVP: populated only by embedded builtins; a user-authored projection in safeslop.cue
	// is rejected at load with a spec-cited error.
	Projection *Projection `json:"projection,omitempty"`
}

// Config is the decoded top-level `safeslop:` value from safeslop.cue.
type Config struct {
	Version  int                `json:"version"`
	Profiles map[string]Profile `json:"profiles"`
}

// projectionAuthored reports whether a decoded Projection carries any engine-meaningful
// content (enabled flag or items). A bare projection:{} decodes to a zero struct and is
// treated as absent — only an enabled/item-bearing projection trips the MVP user-authored
// reject in LoadBytes (specs/0096).
func projectionAuthored(p Projection) bool {
	return p.Enabled || len(p.Items) > 0
}

// NormalizeAgent returns the canonical engine agent name for accepted aliases.
func NormalizeAgent(agent string) string {
	if agent == "claude-code" {
		return "claude"
	}
	return agent
}

// IsLaunchableAgent reports whether name — already canonical (NormalizeAgent
// applied by the caller) — is an agent that `session create` will accept. It is
// the shared allowlist for session-create validation and mirrors the agentArgv /
// seedAgentDefaults switches. The generic "shell" agent is intentionally absent:
// it remains a profile-only value (launchable via `safeslop run`, mapped by
// agentArgv) but is not session-creatable — the explicit fish/zsh agents
// supersede it. "claude-code" normalizes to "claude" before reaching here.
func IsLaunchableAgent(name string) bool {
	switch name {
	case "claude", "pi", "fish", "zsh":
		return true
	}
	return false
}

// virtualDir is an in-memory CUE package built from the embedded schema plus the
// user's file via an overlay, so neither needs to live in a real CUE module.
const virtualDir = "/__safeslop__"

// Load reads, validates, and decodes the safeslop.cue at path. Validation errors
// are rendered with cue/errors.Details for cue-vet-quality messages.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	cfg, err := LoadBytes(data)
	if err != nil {
		return nil, fmt.Errorf("%s:\n%w", path, err)
	}
	return cfg, nil
}

// LoadBytes validates + decodes safeslop.cue content held in memory — the path-free core of Load,
// used by the GUI editor's live validation (so it can vet unsaved text without a temp file). Errors
// carry cue/errors.Details for cue-vet-quality messages.
func LoadBytes(data []byte) (*Config, error) {
	ctx := cuecontext.New()
	overlay := map[string]load.Source{
		filepath.Join(virtualDir, "cue.mod", "module.cue"): load.FromString(`module: "safeslop.local/cfg"` + "\n" + `language: version: "v0.9.0"`),
		filepath.Join(virtualDir, "schema.cue"):            load.FromString(schemaSrc),
		filepath.Join(virtualDir, "safeslop.cue"):          load.FromBytes(data),
	}
	insts := load.Instances([]string{"."}, &load.Config{Dir: virtualDir, Overlay: overlay})
	if len(insts) == 0 {
		return nil, fmt.Errorf("no CUE instance produced")
	}
	if insts[0].Err != nil {
		d := errors.Details(insts[0].Err, nil)
		return nil, fmt.Errorf("load:\n%s%s", d, migrationHint(data, d))
	}
	val := ctx.BuildInstance(insts[0])
	if err := val.Err(); err != nil {
		d := errors.Details(err, nil)
		return nil, fmt.Errorf("build:\n%s%s", d, migrationHint(data, d))
	}
	if err := val.Validate(cue.Concrete(true)); err != nil {
		d := errors.Details(err, nil)
		return nil, fmt.Errorf("invalid:\n%s%s", d, migrationHint(data, d))
	}
	var cfg Config
	if err := val.LookupPath(cue.ParsePath("safeslop")).Decode(&cfg); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	for name, prof := range cfg.Profiles {
		prof.Agent = NormalizeAgent(prof.Agent)
		cfg.Profiles[name] = prof
		// Projection is engine-owned in MVP (specs/0096 T1 FLO verdict): a safeslop.cue that
		// sets a non-empty projection is rejected here with a spec-cited error. Embedded builtins
		// populate Projection as a Go struct directly, so they never flow through LoadBytes and are
		// unaffected. The field is decoded (and schema-typed) so the error is precise and future
		// trust/UI work can flip this gate without a schema migration.
		if prof.Projection != nil && projectionAuthored(*prof.Projection) {
			return nil, fmt.Errorf("profile %q: projection is engine-owned and not yet settable in safeslop.cue (specs/0096); use a builtin profile (pi/claude/fish/zsh)", name)
		}
		// Forgejo API tokens are account-wide, not repo-scoped: enabling API staging must be an
		// explicit, acknowledged decision (specs/0068 F5). Enforced here (post-decode) rather than in
		// CUE because "required-only-when-enabled, no silent default" is awkward with CUE defaults.
		// Staging with api.enabled still errors as P2 (see StageForgejo).
		if c := prof.Credentials; c != nil && c.Forgejo != nil && c.Forgejo.Api != nil &&
			c.Forgejo.Api.Enabled && !c.Forgejo.Api.AckAccountWide {
			return nil, fmt.Errorf("profile %q: credentials.forgejo.api.enabled requires api.ackAccountWide: true — Forgejo API tokens are account-wide, not repo-scoped (specs/0068 F5)", name)
		}
	}
	return &cfg, nil
}

// migrationHint appends pointed specs/0069 guidance when a failing load looks like a pre-0069
// credentials shape (credentials.ssh, or github.mode:"deploy-key"), so an operator editing an old
// safeslop.cue gets the rename rather than a bare "field not allowed". Best-effort and additive: it
// inspects the source bytes + rendered error and returns "" when nothing matches.
func migrationHint(src []byte, errText string) string {
	var hints []string
	if strings.Contains(string(src), "ssh:") {
		hints = append(hints, `credentials.ssh was renamed to credentials.github (specs/0069); GitHub now stages an app or pat token over HTTPS, not an SSH deploy key`)
	}
	if strings.Contains(errText, "github.mode") {
		hints = append(hints, `credentials.github.mode must be "app" (default) or "pat"; the "deploy-key" mode was removed (specs/0069)`)
	}
	if strings.Contains(errText, "forgejo.token") {
		hints = append(hints, `credentials.forgejo.token moved to ~/.config/safeslop/accounts.cue — run: safeslop creds link forgejo (specs/0069)`)
	}
	if strings.Contains(errText, "forgejo.mode") || strings.Contains(errText, "forgejo.pat") {
		hints = append(hints, `credentials.forgejo.mode and .pat were removed; deploy keys are the only forgejo staging and the token comes from accounts.cue (specs/0069)`)
	}
	if len(hints) == 0 {
		return ""
	}
	return "\n\nspecs/0069 migration:\n  - " + strings.Join(hints, "\n  - ")
}

// EnvTier returns an honest one-line characterization of an environment's isolation strength so
// run/doctor (and the GUI) never overstate the boundary (ayo specs/0012 §10.5 H1). tier is a short
// label; note is the honest caveat. environment is required (specs/0053), so an empty/unknown env is
// not a valid run — it is reported as no boundary rather than silently treated as isolated.
func EnvTier(env string) (tier, note string) {
	switch env {
	case "host":
		return "none", "no isolation boundary — the agent runs as you, with your full account"
	case "container":
		return "egress-allowlisted", "container + default-deny per-domain egress allowlist (SNI-trust): stops curl|sh + accidental beaconing, not exfil via an allowed domain; shared-kernel file isolation"
	default: // "" / unknown — not a valid environment (specs/0053): treat as no boundary, never imply isolation
		return "none", "unrecognized environment — no isolation boundary"
	}
}

// Validate is Load without returning the config — used by `safeslop validate`.
func Validate(path string) error {
	_, err := Load(path)
	return err
}
