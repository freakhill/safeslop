package safeslopcfg

// User-level safeslop preferences (~/.config/safeslop/config.cue) — distinct from per-repo safeslop.cue.
terminal: "Terminal.app" | "iTerm2" | "Ghostty" | "WezTerm" | "kitty" | "generic" | *"Terminal.app"
shell?:   string
tag: {
	oscTitle:     bool | *true
	promptMarker: bool | *false
}
