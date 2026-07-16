package policy

import (
	"crypto/sha256"
	"embed"
	"fmt"
	"sort"
	"strings"
)

//go:embed builtins/*.cue
var builtinsFS embed.FS

// BuiltinProfile is a complete, binary-embedded launchable profile.
type BuiltinProfile struct {
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Profile     Profile `json:"profile"`
	CUE         string  `json:"cue,omitempty"`
	Hash        string  `json:"policy_hash"`
}

// BuiltinProfiles returns launchable profiles embedded in the signed binary. Its
// CUE bytes are the policy identity: every entry is parsed with the production
// schema before it is returned, and Hash is the SHA-256 of those exact bytes.
func BuiltinProfiles() []BuiltinProfile {
	entries, err := builtinsFS.ReadDir("builtins")
	if err != nil {
		panic(fmt.Sprintf("read embedded builtin profiles: %v", err))
	}
	out := make([]BuiltinProfile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".cue") {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".cue")
		cue, err := builtinsFS.ReadFile("builtins/" + entry.Name())
		if err != nil {
			panic(fmt.Sprintf("read embedded builtin profile %q: %v", name, err))
		}
		cfg, err := LoadBytes(cue)
		if err != nil {
			panic(fmt.Sprintf("invalid embedded builtin profile %q: %v", name, err))
		}
		profile, ok := cfg.Profiles[name]
		if !ok || len(cfg.Profiles) != 1 {
			panic(fmt.Sprintf("embedded builtin profile %q must contain exactly profile %q", name, name))
		}
		profile.Projection = builtinProjection(name)
		sum := sha256.Sum256(cue)
		out = append(out, BuiltinProfile{
			Name:        name,
			Description: firstCommentLine(string(cue)),
			Profile:     profile,
			CUE:         string(cue),
			Hash:        fmt.Sprintf("sha256:%x", sum),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func builtinProjection(name string) *Projection {
	optional := func(v bool) *bool { return &v }
	p := &Projection{Enabled: true}
	switch name {
	case "pi", "claude":
		p.Items = []ProjectionItem{{Source: "~/.pi/agent/AGENTS.md", Label: "pi-agent", Optional: optional(false)}, {Source: "~/.pi/agent/skills", Kind: "dir", Label: "pi-skills", Optional: optional(true)}}
	case "fish":
		// Eager host config is not portable into the contained tool/OS environment. Project only
		// Fish's demand-loaded assets; normal container-owned startup remains authoritative.
		p.Items = []ProjectionItem{{Source: "~/.config/fish/functions/*.fish", Kind: "glob", Label: "fish-functions", Optional: optional(true)}, {Source: "~/.config/fish/completions/*.fish", Kind: "glob", Label: "fish-completions", Optional: optional(true)}}
	case "zsh":
		p.Items = []ProjectionItem{{Source: "~/.zshrc", Label: "zshrc", Optional: optional(true)}, {Source: "~/.zprofile", Label: "zprofile", Optional: optional(true)}, {Source: "~/.zshenv", Label: "zshenv", Optional: optional(true)}, {Source: "~/.config/starship.toml", Label: "starship", Optional: optional(true)}}
	}
	return p
}

// BuiltinProfileByName looks up one binary-embedded launchable profile.
func BuiltinProfileByName(name string) (BuiltinProfile, bool) {
	for _, builtin := range BuiltinProfiles() {
		if builtin.Name == name {
			return builtin, true
		}
	}
	return BuiltinProfile{}, false
}
