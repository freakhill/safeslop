// Package policy loads and validates a user's slop.cue against the embedded
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

// Credentials groups the credential providers a profile uses (SP2).
type Credentials struct {
	Pnpm []PnpmRegistry `json:"pnpm,omitempty"`
}

// Profile is one launchable configuration from slop.cue.
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
}

// Config is the decoded top-level `slop:` value from slop.cue.
type Config struct {
	Version  int                `json:"version"`
	Profiles map[string]Profile `json:"profiles"`
}

// virtualDir is an in-memory CUE package built from the embedded schema plus the
// user's file via an overlay, so neither needs to live in a real CUE module.
const virtualDir = "/__slop__"

// Load reads, validates, and decodes the slop.cue at path. Validation errors
// are rendered with cue/errors.Details for cue-vet-quality messages.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	ctx := cuecontext.New()
	overlay := map[string]load.Source{
		filepath.Join(virtualDir, "cue.mod", "module.cue"): load.FromString(`module: "slop.local/cfg"` + "\n" + `language: version: "v0.9.0"`),
		filepath.Join(virtualDir, "schema.cue"):            load.FromString(schemaSrc),
		filepath.Join(virtualDir, "slop.cue"):              load.FromBytes(data),
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
	if err := val.LookupPath(cue.ParsePath("slop")).Decode(&cfg); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	return &cfg, nil
}

// Validate is Load without returning the config — used by `slop validate`.
func Validate(path string) error {
	_, err := Load(path)
	return err
}
