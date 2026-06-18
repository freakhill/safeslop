package slopcfg

// User-level slop preferences (~/.config/slop/config.cue) — distinct from per-repo slop.cue.
terminal: "Terminal.app" | "iTerm2" | "Ghostty" | "WezTerm" | "kitty" | "generic" | *"Terminal.app"
shell?:   string
tag: {
	oscTitle:     bool | *true
	promptMarker: bool | *false
}
