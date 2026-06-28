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

// GcpAdc stages a short-lived GCP access token from ADC, refresh token stripped (specs/0009).
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

// SshCreds stages a per-run repo-scoped ephemeral SSH deploy key (read-only unless Write).
// The host mints it; only the private key crosses the boundary (specs/0001 §7.1, specs/0011).
// When Repos is non-empty, one key is minted per repo and staged with per-repo SSH aliases +
// git insteadOf rewrites (specs/0047 P2); otherwise the single repo is inferred from origin.
type SshCreds struct {
	Mode  string     `json:"mode,omitempty"` // "deploy-key" (default) or "pat" (stage an existing fine-grained token)
	Write bool       `json:"write,omitempty"`
	Ttl   string     `json:"ttl,omitempty"`
	Pat   string     `json:"pat,omitempty"` // secret ref for mode:"pat"; token value is staged 0600, never embedded in config
	Repos []RepoCred `json:"repos,omitempty"`
}

// ForgejoCreds is the Forgejo/Gitea sibling of SshCreds: a per-run repo-scoped ephemeral deploy
// key on a non-GitHub forge (Codeberg, self-hosted, etc.). Forgejo has no `gh`-style ambient
// auth, so Token is an explicit secret ref (op://... or env:NAME) for the API call. URL is the
// instance base (e.g. "https://codeberg.org"); when empty the host is inferred from the cwd
// origin remote (specs/0047).
type ForgejoCreds struct {
	Mode    string     `json:"mode,omitempty"` // "deploy-key" (default) or "pat" (stage an existing fine-grained token)
	Write   bool       `json:"write,omitempty"`
	Ttl     string     `json:"ttl,omitempty"`
	URL     string     `json:"url,omitempty"`
	Token   string     `json:"token,omitempty"`    // API token ref for deploy-key registration/revocation
	Pat     string     `json:"pat,omitempty"`      // secret ref for mode:"pat"; token value is staged 0600, never embedded in config
	Repos   []RepoCred `json:"repos,omitempty"`    // multi-repo: one deploy key/PAT scope per entry (specs/0047 P2/P2.3)
	SSHPort int        `json:"ssh-port,omitempty"` // instance git SSH port for multi-repo rewrites (default 22)
}

// Credentials groups the credential providers a profile uses (SP2; aws/gcp SP/0009; kube SP/0010;
// ssh SP/0011; forgejo specs/0047).
type Credentials struct {
	Pnpm    []PnpmRegistry `json:"pnpm,omitempty"`
	Aws     *AwsSso        `json:"aws,omitempty"`
	Gcp     *GcpAdc        `json:"gcp,omitempty"`
	Kube    *KubeCluster   `json:"kube,omitempty"`
	Ssh     *SshCreds      `json:"ssh,omitempty"`
	Forgejo *ForgejoCreds  `json:"forgejo,omitempty"`
}

// Toolchain layers a pinned tool environment onto any environment (SP5). When Run is set,
// safeslop launches that mise task / nix app instead of the agent; otherwise the agent is wrapped.
type Toolchain struct {
	Kind string `json:"kind"`
	Run  string `json:"run,omitempty"`
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
	// bundle is always included so the agent can launch.
	Bundles  []string `json:"bundles,omitempty"`
	Packages []string `json:"packages,omitempty"`
}

// Config is the decoded top-level `safeslop:` value from safeslop.cue.
type Config struct {
	Version  int                `json:"version"`
	Profiles map[string]Profile `json:"profiles"`
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
		return nil, fmt.Errorf("load:\n%s", errors.Details(insts[0].Err, nil))
	}
	val := ctx.BuildInstance(insts[0])
	if err := val.Err(); err != nil {
		return nil, fmt.Errorf("build:\n%s", errors.Details(err, nil))
	}
	if err := val.Validate(cue.Concrete(true)); err != nil {
		return nil, fmt.Errorf("invalid:\n%s", errors.Details(err, nil))
	}
	var cfg Config
	if err := val.LookupPath(cue.ParsePath("safeslop")).Decode(&cfg); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	for name, prof := range cfg.Profiles {
		prof.Agent = NormalizeAgent(prof.Agent)
		cfg.Profiles[name] = prof
	}
	return &cfg, nil
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
