package policy

import (
	"embed"
	"sort"
	"strings"
)

//go:embed presets/*.cue
var presetsFS embed.FS

// Preset is a premade safeslop.cue the GUI offers as a starting point — the "stdlib" of reference
// policies. Name comes from the filename; Description is the leading `//` comment line.
type Preset struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	CUE         string `json:"cue"`
}

// Presets returns the embedded premade policies, sorted by name.
func Presets() []Preset {
	entries, _ := presetsFS.ReadDir("presets")
	out := make([]Preset, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".cue") {
			continue
		}
		b, err := presetsFS.ReadFile("presets/" + e.Name())
		if err != nil {
			continue
		}
		cue := string(b)
		out = append(out, Preset{
			Name:        strings.TrimSuffix(e.Name(), ".cue"),
			Description: firstCommentLine(cue),
			CUE:         cue,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// firstCommentLine returns the first `//` comment's text (the preset's one-line description).
func firstCommentLine(cue string) string {
	for _, line := range strings.Split(cue, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "//") {
			return strings.TrimSpace(strings.TrimPrefix(t, "//"))
		}
		if t != "" && !strings.HasPrefix(t, "//") {
			break // a non-comment, non-blank line ends the header
		}
	}
	return ""
}
