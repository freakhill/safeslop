// Package userconfig loads the user-level ~/.config/slop/config.cue (terminal-launch
// preferences), validated against an embedded CUE schema. Distinct from policy.slop.cue.
package userconfig

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

//go:embed schema/config.cue
var schemaSrc string

// Config is the resolved user preferences.
type Config struct {
	Terminal string `json:"terminal"`
	Shell    string `json:"shell,omitempty"`
	Tag      Tag    `json:"tag"`
}

// Tag controls session recognizability.
type Tag struct {
	OSCTitle     bool `json:"oscTitle"`
	PromptMarker bool `json:"promptMarker"`
}

const virtualDir = "/__slopcfg__"

// Load reads + validates path against the embedded schema, then decodes it. A missing file
// yields the schema defaults (Terminal.app, oscTitle on).
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		data = []byte("package slopcfg\n") // missing file => schema defaults
	}
	ctx := cuecontext.New()
	overlay := map[string]load.Source{
		filepath.Join(virtualDir, "cue.mod", "module.cue"): load.FromString(`module: "slop.local/cfg"` + "\n" + `language: version: "v0.9.0"`),
		filepath.Join(virtualDir, "schema.cue"):            load.FromString(schemaSrc),
		filepath.Join(virtualDir, "config.cue"):            load.FromBytes(data),
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
	if err := val.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	return &cfg, nil
}

// DefaultPath is ~/.config/slop/config.cue.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "slop", "config.cue"), nil
}
