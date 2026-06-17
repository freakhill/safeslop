package vm

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestLaunchRejectsWhenUnavailable(t *testing.T) {
	t.Setenv("PATH", "")
	_, err := Launch(context.Background(), []string{"zsh"}, "allow", nil, t.TempDir(), "p", "")
	if err == nil {
		t.Fatal("expected error when tart unavailable")
	}
}

func TestLaunchDenyNeedsProxyURL(t *testing.T) {
	t.Setenv("PATH", "")
	t.Setenv("SLOP_VM_PROXY_URL", "")
	_, err := Launch(context.Background(), []string{"zsh"}, "deny", nil, t.TempDir(), "p", "")
	if err == nil {
		t.Fatal("expected error (tart unavailable and/or deny without proxy URL)")
	}
}

func TestWriteSecretsEnvEscapesAndIs0600(t *testing.T) {
	dir := t.TempDir()
	p, err := writeSecretsEnv(dir, []string{`ANTHROPIC_API_KEY=sk-a'b`})
	if err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(p)
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("secrets.env perm = %v want 0600", fi.Mode().Perm())
	}
	b, _ := os.ReadFile(p)
	if string(b) != "ANTHROPIC_API_KEY='sk-a'\\''b'\n" {
		t.Fatalf("escaping wrong: %q", string(b))
	}
	if got, _ := writeSecretsEnv(filepath.Join(dir, "x"), nil); got != "" {
		t.Fatal("no secrets should yield no file")
	}
}
