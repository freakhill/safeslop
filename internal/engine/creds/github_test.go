package creds

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/creds/githubapp"
	"github.com/freakhill/safeslop/internal/engine/policy"
	"github.com/freakhill/safeslop/internal/engine/userconfig"
)

// fakeForge implements githubapp.ForgeHTTP: it returns a distinct token per mint so tests can
// assert per-owner/per-partition separation, and records mint bodies + revoked bearer tokens.
type fakeForge struct {
	mu      sync.Mutex
	mints   []string // POST .../access_tokens request bodies, in order
	revoked []string // bearer tokens seen by DELETE /installation/token
	n       int
}

func (f *fakeForge) Do(_ context.Context, method, url string, headers map[string]string, body []byte) ([]byte, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case strings.HasSuffix(url, "/access_tokens"):
		f.n++
		f.mints = append(f.mints, string(body))
		return []byte(fmt.Sprintf(`{"token":"ghs_tok_%d","expires_at":"2026-07-03T13:00:00Z"}`, f.n)), 201, nil
	case method == "DELETE" && strings.HasSuffix(url, "/installation/token"):
		f.revoked = append(f.revoked, strings.TrimPrefix(headers["Authorization"], "Bearer "))
		return nil, 204, nil
	}
	return []byte(`{}`), 200, nil
}

// testAppKeyEnv generates a real RSA key (so the App JWT actually signs) and exposes it as an
// env: secret ref for the accounts link.
func testAppKeyEnv(t *testing.T) string {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)})
	t.Setenv("TEST_GH_APP_KEY", string(pemBytes))
	return "env:TEST_GH_APP_KEY"
}

func ghAccounts(ref string, owners ...string) *userconfig.Accounts {
	acc := &userconfig.Accounts{Accounts: map[string]userconfig.Account{}}
	for i, o := range owners {
		acc.Upsert(userconfig.Account{
			Forge: "github", Host: "github.com", Owner: o,
			Github: &userconfig.GithubAccount{AppID: 100 + i, InstallationID: 200 + i, PrivateKeyRef: ref},
		})
	}
	return acc
}

func TestStageGithubMultiOwner(t *testing.T) {
	ref := testAppKeyEnv(t)
	stage := t.TempDir()
	acc := ghAccounts(ref, "acme", "globex")
	f := &fakeForge{}
	repos := []policy.RepoCred{{Repo: "acme/web"}, {Repo: "globex/api"}}

	env, err := stageGithubApp(context.Background(), repos, stage, acc, githubapp.New(f, "http://x"))
	if err != nil {
		t.Fatalf("stageGithubApp: %v", err)
	}

	acmeTok, _ := os.ReadFile(filepath.Join(stage, "git", "token-acme"))
	globexTok, _ := os.ReadFile(filepath.Join(stage, "git", "token-globex"))
	if len(acmeTok) == 0 || len(globexTok) == 0 || string(acmeTok) == string(globexTok) {
		t.Fatalf("expected two distinct token files, got %q / %q", acmeTok, globexTok)
	}
	for _, p := range []string{"token-acme", "token-globex"} {
		fi, err := os.Stat(filepath.Join(stage, "git", p))
		if err != nil || fi.Mode().Perm() != 0o600 {
			t.Fatalf("%s must be staged 0600: %v", p, err)
		}
	}

	gc, _ := os.ReadFile(filepath.Join(stage, ".gitconfig"))
	cfg := string(gc)
	if !strings.Contains(cfg, "cat '"+filepath.Join(stage, "git", "token-acme")+"'") {
		t.Fatalf("acme/web helper must point at token-acme:\n%s", cfg)
	}
	if !strings.Contains(cfg, "cat '"+filepath.Join(stage, "git", "token-globex")+"'") {
		t.Fatalf("globex/api helper must point at token-globex:\n%s", cfg)
	}
	if !strings.Contains(cfg, `[url "https://github.com/acme/web.git"]`) || !strings.Contains(cfg, "insteadOf = git@github.com:acme/web.git") {
		t.Fatalf("missing ssh->HTTPS insteadOf rewrites:\n%s", cfg)
	}

	if len(f.mints) != 2 {
		t.Fatalf("expected one mint per owner, got %d: %v", len(f.mints), f.mints)
	}
	joined := strings.Join(f.mints, "\n")
	if !strings.Contains(joined, `"web"`) || !strings.Contains(joined, `"api"`) {
		t.Fatalf("mint bodies must name their repos: %v", f.mints)
	}
	if strings.Contains(joined, `"contents":"write"`) {
		t.Fatalf("read-only repos must not request write: %v", f.mints)
	}
	if !strings.Contains(strings.Join(env, " "), "GIT_CONFIG_GLOBAL="+filepath.Join(stage, ".gitconfig")) {
		t.Fatalf("env = %v", env)
	}
}

func TestStageGithubMixedWritePartition(t *testing.T) {
	ref := testAppKeyEnv(t)
	stage := t.TempDir()
	acc := ghAccounts(ref, "acme")
	f := &fakeForge{}
	repos := []policy.RepoCred{{Repo: "acme/web"}, {Repo: "acme/api", Write: true}}

	if _, err := stageGithubApp(context.Background(), repos, stage, acc, githubapp.New(f, "http://x")); err != nil {
		t.Fatalf("stageGithubApp: %v", err)
	}

	roTok := filepath.Join(stage, "git", "token-acme")
	rwTok := filepath.Join(stage, "git", "token-acme-rw")
	if _, err := os.Stat(roTok); err != nil {
		t.Fatalf("ro token missing: %v", err)
	}
	if _, err := os.Stat(rwTok); err != nil {
		t.Fatalf("rw token missing: %v", err)
	}

	gc, _ := os.ReadFile(filepath.Join(stage, ".gitconfig"))
	cfg := string(gc)
	if !strings.Contains(cfg, "cat '"+roTok+"'") || !strings.Contains(cfg, "cat '"+rwTok+"'") {
		t.Fatalf("both partition token paths must appear:\n%s", cfg)
	}

	if len(f.mints) != 2 {
		t.Fatalf("expected 2 mints (ro + rw partitions), got %d: %v", len(f.mints), f.mints)
	}
	// The write token must scope to api only; the read token must not carry write.
	for _, b := range f.mints {
		if strings.Contains(b, `"contents":"write"`) {
			if !strings.Contains(b, `"api"`) || strings.Contains(b, `"web"`) {
				t.Fatalf("write token must scope to api only: %s", b)
			}
		} else if !strings.Contains(b, `"web"`) || strings.Contains(b, `"api"`) {
			t.Fatalf("read token must scope to web only: %s", b)
		}
	}
}

func TestStageGithubDenyWithoutLink(t *testing.T) {
	ref := testAppKeyEnv(t)
	stage := t.TempDir()
	acc := ghAccounts(ref, "acme") // no link for globex
	f := &fakeForge{}

	_, err := stageGithubApp(context.Background(), []policy.RepoCred{{Repo: "globex/api"}}, stage, acc, githubapp.New(f, "http://x"))
	if err == nil || !strings.Contains(err.Error(), "safeslop creds link github") {
		t.Fatalf("missing link must hard-deny with a link hint, got %v", err)
	}
	if len(f.mints) != 0 {
		t.Fatalf("must not mint when a link is missing: %v", f.mints)
	}
}

func TestStageGithubNilAccountsDenies(t *testing.T) {
	ref := testAppKeyEnv(t)
	stage := t.TempDir()
	_, err := stageGithubApp(context.Background(), []policy.RepoCred{{Repo: "acme/web"}}, stage, nil, githubapp.New(&fakeForge{}, "http://x"))
	_ = ref
	if err == nil || !strings.Contains(err.Error(), "creds link github") {
		t.Fatalf("nil accounts must hard-deny, got %v", err)
	}
}

func TestStageGithubMetaNoTokenBytes(t *testing.T) {
	ref := testAppKeyEnv(t)
	stage := t.TempDir()
	acc := ghAccounts(ref, "acme")
	if _, err := stageGithubApp(context.Background(), []policy.RepoCred{{Repo: "acme/web"}}, stage, acc, githubapp.New(&fakeForge{}, "http://x")); err != nil {
		t.Fatal(err)
	}
	meta, _ := os.ReadFile(filepath.Join(stage, "git", "github-meta.json"))
	if strings.Contains(string(meta), "ghs_tok_") {
		t.Fatalf("meta must not contain token bytes:\n%s", meta)
	}
	if !strings.Contains(string(meta), `"minExpiresAt"`) || !strings.Contains(string(meta), "git/token-acme") {
		t.Fatalf("meta missing expected value-free fields:\n%s", meta)
	}
}

func TestStageGithubContainerVariant(t *testing.T) {
	ref := testAppKeyEnv(t)
	stage := t.TempDir()
	acc := ghAccounts(ref, "acme")
	if _, err := stageGithubApp(context.Background(), []policy.RepoCred{{Repo: "acme/web"}}, stage, acc, githubapp.New(&fakeForge{}, "http://x")); err != nil {
		t.Fatal(err)
	}
	cc, _ := os.ReadFile(filepath.Join(stage, ".gitconfig.container"))
	if !strings.Contains(string(cc), "cat '/safeslop/runtime/git/token-acme'") {
		t.Fatalf("container gitconfig must reference the runtime token path:\n%s", cc)
	}
}

func TestStageGithubApiEnabledIsP2(t *testing.T) {
	_, err := StageGithub(context.Background(),
		&policy.Credentials{Github: &policy.GithubCreds{Api: &policy.GithubApi{Enabled: true}}}, t.TempDir(), nil)
	if err == nil || !strings.Contains(err.Error(), "P2") {
		t.Fatalf("api.enabled staging must be a P2 error, got %v", err)
	}
}

func TestStageGithubNilIsNoop(t *testing.T) {
	env, err := StageGithub(context.Background(), &policy.Credentials{}, t.TempDir(), nil)
	if err != nil || env != nil {
		t.Fatalf("nil github creds must be a no-op: env=%v err=%v", env, err)
	}
}

func TestRevokeGithubRevokesEachToken(t *testing.T) {
	ref := testAppKeyEnv(t)
	stage := t.TempDir()
	acc := ghAccounts(ref, "acme")
	f := &fakeForge{}
	client := githubapp.New(f, "http://x")
	if _, err := stageGithubApp(context.Background(), []policy.RepoCred{{Repo: "acme/web"}, {Repo: "acme/api", Write: true}}, stage, acc, client); err != nil {
		t.Fatal(err)
	}

	revokeGithubWith(context.Background(), stage, client)
	if len(f.revoked) != 2 {
		t.Fatalf("expected 2 token revokes (ro + rw), got %d: %v", len(f.revoked), f.revoked)
	}
	for _, tok := range f.revoked {
		if !strings.HasPrefix(tok, "ghs_tok_") {
			t.Fatalf("revoke must send the staged token as bearer, got %q", tok)
		}
	}
}
