package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/freakhill/safeslop/internal/engine/hostexec"
	"github.com/freakhill/safeslop/internal/engine/policy"
	"github.com/freakhill/safeslop/internal/engine/userconfig"
)

func TestStageProfileResolvesEnvSecret(t *testing.T) {
	t.Setenv("TEST_SAFESLOP_SECRET", "s3cr3t")
	prof := policy.Profile{Secrets: map[string]string{"FOO": "env:TEST_SAFESLOP_SECRET"}}
	secretEnv, pathEnv, err := stageProfile(context.Background(), prof, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(secretEnv, "FOO=s3cr3t") {
		t.Fatalf("secretEnv missing the resolved secret: %v", secretEnv)
	}
	if len(pathEnv) != 0 {
		t.Fatalf("no credentials → pathEnv must be empty: %v", pathEnv)
	}
}

func TestStagePiOAuthProfileAcceptsLinked0755HomeSource(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	piDir := filepath.Join(home, "dotfiles", "pi")
	agentDir := filepath.Join(piDir, "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{filepath.Join(home, "dotfiles"), piDir, agentDir} {
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("dotfiles/pi", filepath.Join(home, ".pi")); err != nil {
		t.Fatal(err)
	}
	body := `{"openai-codex":{"type":"oauth","access":"ACCESS_CANARY","refresh":"REFRESH_SENTINEL","expires":` +
		fmt.Sprint(time.Now().Add(time.Hour).UnixMilli()) + `}}`
	if err := os.WriteFile(filepath.Join(agentDir, "auth.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	stage := t.TempDir()
	prof := policy.Profile{Agent: "pi", Environment: "container", Network: "deny", Credentials: &policy.Credentials{
		Pi: &policy.PiCreds{Provider: "openai-codex", Model: "gpt-5.6-luna"},
	}}
	secretEnv, pathEnv, err := stageProfile(context.Background(), prof, stage)
	if err != nil {
		t.Fatalf("stageProfile Pi OAuth: %v", err)
	}
	if len(secretEnv) != 0 || len(pathEnv) != 0 {
		t.Fatalf("Pi OAuth must stage as a file only: secret=%v path=%v", secretEnv, pathEnv)
	}
	staged, err := os.ReadFile(filepath.Join(stage, "pi", "openai-codex", "auth.json"))
	if err != nil {
		t.Fatalf("read staged Pi auth: %v", err)
	}
	if !strings.Contains(string(staged), "ACCESS_CANARY") || strings.Contains(string(staged), "REFRESH_SENTINEL") {
		t.Fatalf("staged Pi auth is not access-only: %s", staged)
	}
}

func TestStageProfilePreflightsOpBeforeWritingPnpmrc(t *testing.T) {
	stage := t.TempDir()
	withStageHostExecResolver(t, hostexec.New(cliFakeHostEnv{path: "/safe/bin"}))
	prof := policy.Profile{Credentials: &policy.Credentials{Pnpm: []policy.PnpmRegistry{{Token: "op://vault/npm/token"}}}}

	_, _, err := stageProfile(context.Background(), prof, stage)
	if !errors.Is(err, hostexec.ErrNotFound) {
		t.Fatalf("stageProfile err=%v, want ErrNotFound", err)
	}
	if _, statErr := os.Stat(filepath.Join(stage, ".npmrc")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf(".npmrc should not be written before helper preflight: stat err=%v", statErr)
	}
}

func TestRequiredProfileHostHelpersDeclaredForgejoAvoidsGit(t *testing.T) {
	prof := policy.Profile{Credentials: &policy.Credentials{Forgejo: &policy.ForgejoCreds{
		URL:   "https://codeberg.org",
		Repos: []policy.RepoCred{{Repo: "acme/repo"}},
	}}}
	accounts := &userconfig.Accounts{Accounts: map[string]userconfig.Account{
		"codeberg.org/acme": {Forge: "forgejo", Host: "codeberg.org", Owner: "acme", Forgejo: &userconfig.ForgejoAccount{TokenRef: "env:FORGEJO_TOKEN"}},
	}}

	names := helperNames(requiredProfileHostHelpers(prof, accounts))
	if names["git"] {
		t.Fatalf("declared forgejo repos should not require git origin inference: %v", names)
	}
	if !names["ssh-keygen"] || !names["ssh-keyscan"] {
		t.Fatalf("forgejo deploy keys should require ssh helpers: %v", names)
	}
	if names["op"] {
		t.Fatalf("env: account token should not require op: %v", names)
	}
}

func helperNames(specs []hostexec.Spec) map[string]bool {
	out := map[string]bool{}
	for _, spec := range specs {
		out[spec.Name] = true
	}
	return out
}

type cliFakeHostEnv struct {
	path     string
	all      map[string][]string
	sameFile func(string, string) (bool, error)
}

func (f cliFakeHostEnv) PATH() string              { return f.path }
func (f cliFakeHostEnv) Get(string) (string, bool) { return "", false }
func (f cliFakeHostEnv) LookPath(name string) (string, bool) {
	all := f.LookAll(name)
	if len(all) == 0 {
		return "", false
	}
	return all[0], true
}
func (f cliFakeHostEnv) LookAll(name string) []string { return append([]string(nil), f.all[name]...) }
func (f cliFakeHostEnv) SameFile(a, b string) (bool, error) {
	if f.sameFile != nil {
		return f.sameFile(a, b)
	}
	return a == b, nil
}

func withStageHostExecResolver(t *testing.T, r *hostexec.Resolver) {
	t.Helper()
	old := stageHostExecResolver
	stageHostExecResolver = func() *hostexec.Resolver { return r }
	t.Cleanup(func() { stageHostExecResolver = old })
}
