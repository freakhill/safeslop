package userconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func loadStr(t *testing.T, src string) (*Config, error) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.cue")
	if err := os.WriteFile(p, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	return Load(p)
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := loadStr(t, "package slopcfg\n")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Terminal != "Terminal.app" || cfg.Shell != "" || cfg.Tag.OSCTitle != true {
		t.Fatalf("defaults wrong: %+v", cfg)
	}
}

func TestLoadExplicit(t *testing.T) {
	cfg, err := loadStr(t, `package slopcfg
terminal: "Ghostty"
shell: "/bin/zsh"
tag: {oscTitle: false, promptMarker: true}`)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Terminal != "Ghostty" || cfg.Shell != "/bin/zsh" || cfg.Tag.OSCTitle || !cfg.Tag.PromptMarker {
		t.Fatalf("parsed wrong: %+v", cfg)
	}
}

func TestLoadMissingFileIsDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "nope.cue"))
	if err != nil || cfg.Terminal != "Terminal.app" {
		t.Fatalf("missing file must yield defaults: cfg=%+v err=%v", cfg, err)
	}
}

func TestLoadRejectsUnknownTerminal(t *testing.T) {
	if _, err := loadStr(t, `package slopcfg
terminal: "Hyper"`); err == nil {
		t.Fatal("unknown terminal must be rejected by the schema")
	}
}
