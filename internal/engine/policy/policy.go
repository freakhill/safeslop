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

// AwsSso mints short-lived AWS creds from an SSO-configured profile (specs/0009).
type AwsSso struct {
	Profile string `json:"profile"`
	Region  string `json:"region,omitempty"`
}

// GcpAdc stages a short-lived GCP access token from ADC, refresh token stripped (specs/0009).
type GcpAdc struct{}

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

// SshCreds stages a per-run repo-scoped ephemeral SSH deploy key (read-only unless Write).
// The host mints it; only the private key crosses the boundary (specs/0001 §7.1, specs/0011).
type SshCreds struct {
	Write bool   `json:"write,omitempty"`
	Ttl   string `json:"ttl,omitempty"`
}

// Credentials groups the credential providers a profile uses (SP2; aws/gcp SP/0009; kube SP/0010; ssh SP/0011).
type Credentials struct {
	Pnpm []PnpmRegistry `json:"pnpm,omitempty"`
	Aws  *AwsSso        `json:"aws,omitempty"`
	Gcp  *GcpAdc        `json:"gcp,omitempty"`
	Kube *KubeCluster   `json:"kube,omitempty"`
	Ssh  *SshCreds      `json:"ssh,omitempty"`
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
	// Secrets maps an env var name to a secret ref (op://... or env:NAME),
	// resolved at launch and injected into the agent's environment.
	Secrets map[string]string `json:"secrets,omitempty"`
	// Credentials are staged before launch and wiped on exit.
	Credentials *Credentials `json:"credentials,omitempty"`
	// Toolchain provisions a pinned tool environment, orthogonal to Environment (SP5).
	Toolchain *Toolchain `json:"toolchain,omitempty"`
}

// Config is the decoded top-level `safeslop:` value from safeslop.cue.
type Config struct {
	Version  int                `json:"version"`
	Profiles map[string]Profile `json:"profiles"`
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
	ctx := cuecontext.New()
	overlay := map[string]load.Source{
		filepath.Join(virtualDir, "cue.mod", "module.cue"): load.FromString(`module: "safeslop.local/cfg"` + "\n" + `language: version: "v0.9.0"`),
		filepath.Join(virtualDir, "schema.cue"):            load.FromString(schemaSrc),
		filepath.Join(virtualDir, "safeslop.cue"):          load.FromBytes(data),
	}
	insts := load.Instances([]string{"."}, &load.Config{Dir: virtualDir, Overlay: overlay})
	if len(insts) == 0 {
		return nil, fmt.Errorf("no CUE instance produced for %s", path)
	}
	if insts[0].Err != nil {
		return nil, fmt.Errorf("load %s:\n%s", path, errors.Details(insts[0].Err, nil))
	}
	val := ctx.BuildInstance(insts[0])
	if err := val.Err(); err != nil {
		return nil, fmt.Errorf("build %s:\n%s", path, errors.Details(err, nil))
	}
	if err := val.Validate(cue.Concrete(true)); err != nil {
		return nil, fmt.Errorf("invalid %s:\n%s", path, errors.Details(err, nil))
	}
	var cfg Config
	if err := val.LookupPath(cue.ParsePath("safeslop")).Decode(&cfg); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	return &cfg, nil
}

// EnvTier returns an honest one-line characterization of an environment's isolation strength so
// run/doctor (and the GUI) never imply the default sandbox contains a determined adversary
// (ayo specs/0012 §10.5 H1). tier is a short label; note is the honest caveat. An empty env is the
// default (sandbox).
func EnvTier(env string) (tier, note string) {
	switch env {
	case "host":
		return "none", "no isolation boundary — the agent runs as you, with your full account"
	case "container":
		return "network-enforced", "container + egress allowlist: real per-URL network control, shared-kernel file isolation"
	case "vm":
		return "adversary-grade", "disposable hardware-virtualized VM: the strongest boundary, heaviest to run"
	default: // "sandbox" and "" (the default)
		return "mistake-guard", "Seatbelt confines files + exec: guards agent mistakes + accidental exfil, not a malicious-code escape"
	}
}

// Validate is Load without returning the config — used by `safeslop validate`.
func Validate(path string) error {
	_, err := Load(path)
	return err
}
