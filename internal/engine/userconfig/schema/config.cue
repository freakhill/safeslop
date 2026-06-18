package slopcfg

// User-level slop preferences (~/.config/slop/config.cue) — distinct from per-repo slop.cue.
terminal: "Terminal.app" | "Ghostty" | "generic" | *"Terminal.app"
shell?:   string
tag: {
	oscTitle:     bool | *true
	promptMarker: bool | *false
}
