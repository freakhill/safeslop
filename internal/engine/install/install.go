// Package install inventories whether safeslop itself (binary, app, toolchains, runtimes) is
// installed and current — the read-only half of the installer (specs/0012 §5). No side effects.
package install

import (
	"context"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
)

// Tool is one external dependency's install state.
type Tool struct {
	Name    string `json:"name"`
	Present bool   `json:"present"`
	Path    string `json:"path,omitempty"`
	Version string `json:"version,omitempty"`
}

// Self is the running safeslop binary's own install state.
type Self struct {
	Version string `json:"version"`
	Path    string `json:"path,omitempty"` // os.Executable()
	OnPath  bool   `json:"on_path"`        // a `safeslop` resolves on PATH
}

// App is the SafeSlop.app presence (signing verification is a later slice).
type App struct {
	Present bool   `json:"present"`
	Path    string `json:"path,omitempty"`
}

// State is the full install inventory.
type State struct {
	Self       Self   `json:"self"`
	App        App    `json:"app"`
	Toolchains []Tool `json:"toolchains"`
	Runtimes   []Tool `json:"runtimes"`
}

// Status probes the environment. version is the running binary's version (from cli.Version).
func Status(ctx context.Context, version string) State {
	exe, _ := os.Executable()
	_, lookErr := osexec.LookPath("safeslop")
	st := State{
		Self: Self{Version: version, Path: exe, OnPath: lookErr == nil},
		App:  detectApp(),
		Toolchains: []Tool{
			probe(ctx, "mise", "--version"),
			probe(ctx, "nix", "--version"),
		},
		Runtimes: []Tool{
			probe(ctx, "docker", "--version"),
			probe(ctx, "tart", "--version"),
		},
	}
	return st
}

// probe reports a tool's presence + first-line version output (best-effort).
func probe(ctx context.Context, name string, versionArgs ...string) Tool {
	path, err := osexec.LookPath(name)
	if err != nil {
		return Tool{Name: name, Present: false}
	}
	t := Tool{Name: name, Present: true, Path: path}
	if out, verr := osexec.CommandContext(ctx, name, versionArgs...).Output(); verr == nil {
		t.Version = strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	}
	return t
}

// detectApp looks for SafeSlop.app in the standard install locations.
func detectApp() App {
	candidates := []string{"/Applications/SafeSlop.app"}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, "Applications", "SafeSlop.app"))
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return App{Present: true, Path: p}
		}
	}
	return App{Present: false}
}
