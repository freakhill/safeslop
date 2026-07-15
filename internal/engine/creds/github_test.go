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
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/freakhill/safeslop/internal/engine/creds/githubapp"
	"github.com/freakhill/safeslop/internal/engine/policy"
	"github.com/freakhill/safeslop/internal/engine/userconfig"
)

// fakeForge implements githubapp.ForgeHTTP: it returns a distinct token per mint so tests can
// assert per-owner/per-partition separation, and records mint bodies + revoked bearer tokens.
type fakeForge struct {
	mu       sync.Mutex
	mints    []string // POST .../access_tokens request bodies, in order
	revoked  []string // bearer tokens seen by DELETE /installation/token
	n        int
	failAt   int
	lifetime time.Duration
}

func (f *fakeForge) Do(_ context.Context, method, url string, headers map[string]string, body []byte) ([]byte, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case strings.HasSuffix(url, "/access_tokens"):
		f.n++
		f.mints = append(f.mints, string(body))
		if f.failAt == f.n {
			return []byte(`{"message":"mint failed"}`), 500, nil
		}
		lifetime := f.lifetime
		if lifetime == 0 {
			lifetime = time.Hour
		}
		expiresAt := time.Now().Add(lifetime).UTC().Format(time.RFC3339)
		return []byte(fmt.Sprintf(`{"token":"ghs_tok_%d","expires_at":"%s"}`, f.n, expiresAt)), 201, nil
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

func TestStageGithubNilIsNoop(t *testing.T) {
	env, err := StageGithub(context.Background(), &policy.Credentials{}, t.TempDir(), nil)
	if err != nil || env != nil {
		t.Fatalf("nil github creds must be a no-op: env=%v err=%v", env, err)
	}
}

func TestStageGithubRenewalIsAtomicAndRetainsPriorTokens(t *testing.T) {
	ref := testAppKeyEnv(t)
	stage := t.TempDir()
	acc := ghAccounts(ref, "acme")
	f := &fakeForge{}
	client := githubapp.New(f, "http://x")
	repos := []policy.RepoCred{{Repo: "acme/web"}, {Repo: "acme/api", Write: true}}
	if _, err := stageGithubApp(context.Background(), repos, stage, acc, client); err != nil {
		t.Fatal(err)
	}
	beforeRO, err := os.ReadFile(filepath.Join(stage, githubDir, "token-acme"))
	if err != nil {
		t.Fatal(err)
	}
	beforeRW, err := os.ReadFile(filepath.Join(stage, githubDir, "token-acme-rw"))
	if err != nil {
		t.Fatal(err)
	}

	// The second replacement mint fails. Neither canonical token may change.
	f.failAt = f.n + 2
	if _, err := stageGithubApp(context.Background(), repos, stage, acc, client); err == nil {
		t.Fatal("partial replacement batch must fail")
	}
	for path, want := range map[string][]byte{
		"token-acme":    beforeRO,
		"token-acme-rw": beforeRW,
	} {
		got, err := os.ReadFile(filepath.Join(stage, githubDir, path))
		if err != nil || string(got) != string(want) {
			t.Fatalf("failed batch changed canonical %s: got %q err %v", path, got, err)
		}
	}

	f.failAt = 0
	if _, err := stageGithubApp(context.Background(), repos, stage, acc, client); err != nil {
		t.Fatalf("successful replacement: %v", err)
	}
	for _, path := range []string{"token-acme", "token-acme-rw"} {
		fi, err := os.Stat(filepath.Join(stage, githubDir, path))
		if err != nil || fi.Mode().Perm() != 0o600 {
			t.Fatalf("replacement %s must be canonical 0600: %v", path, err)
		}
	}
	if len(f.revoked) != 0 {
		t.Fatalf("ordinary renewal must not revoke active prior tokens: %v", f.revoked)
	}

	revokeGithubWith(context.Background(), stage, client)
	if len(f.revoked) != 4 { // two current plus two retained prior tokens
		t.Fatalf("teardown must revoke current and retained tokens, got %v", f.revoked)
	}
}

func TestStageGithubRejectsShortNativeLifetime(t *testing.T) {
	ref := testAppKeyEnv(t)
	stage := t.TempDir()
	f := &fakeForge{lifetime: 9 * time.Minute}
	_, err := stageGithubApp(context.Background(), []policy.RepoCred{{Repo: "acme/web"}}, stage, ghAccounts(ref, "acme"), githubapp.New(f, "http://x"))
	if err == nil || !strings.Contains(err.Error(), "lifetime") {
		t.Fatalf("short native lifetime must be rejected, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(stage, githubDir, "token-acme")); !os.IsNotExist(err) {
		t.Fatalf("short lifetime must not change canonical stage, stat err=%v", err)
	}
}

func TestStageGithubAPIFileDeliveryAndDownscoping(t *testing.T) {
	ref := testAppKeyEnv(t)
	stage := t.TempDir()
	acc := ghAccounts(ref, "acme")
	f := &fakeForge{}
	api := &policy.GithubApi{Enabled: true, Permissions: []string{"issues:write", "metadata:read"}}
	env, err := stageGithubAppWithAPI(context.Background(), []policy.RepoCred{{Repo: "acme/web"}}, stage, acc, githubapp.New(f, "http://x"), api)
	if err != nil {
		t.Fatal(err)
	}
	apiFile := filepath.Join(stage, githubAPIDir, githubAPITokenFile)
	fi, err := os.Stat(apiFile)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("canonical API token must be 0600: %v", err)
	}
	if !slices.Contains(env, "SAFESLOP_GITHUB_TOKEN_FILE="+apiFile) {
		t.Fatalf("single API partition needs canonical file env, got %v", env)
	}
	if slices.ContainsFunc(env, func(v string) bool { return strings.HasPrefix(v, "GITHUB_TOKEN=") }) {
		t.Fatalf("API token must not be exported as a conventional value: %v", env)
	}
	if len(f.mints) != 2 {
		t.Fatalf("git and API tokens must mint separately, got %d", len(f.mints))
	}
	apiMint := f.mints[1]
	if !strings.Contains(apiMint, `"issues":"write"`) || !strings.Contains(apiMint, `"metadata":"read"`) || !strings.Contains(apiMint, `"web"`) {
		t.Fatalf("API mint must be repository/permission downscoped: %s", apiMint)
	}
	manifestPath := filepath.Join(stage, githubAPIDir, githubAPIMetaFile)
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(manifestPath); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("API manifest must be 0600: %v", err)
	}
	if _, err := os.Stat(apiFile + ".new"); !os.IsNotExist(err) {
		t.Fatalf("successful atomic replacement must not leave token temp: %v", err)
	}
	if strings.Contains(string(manifest), "ghs_tok_") {
		t.Fatalf("API manifest must not contain token bytes: %s", manifest)
	}
}

func TestStageGithubAPIMultiplePartitionsUsesDirectoryManifest(t *testing.T) {
	ref := testAppKeyEnv(t)
	stage := t.TempDir()
	acc := ghAccounts(ref, "acme")
	api := &policy.GithubApi{Enabled: true, Permissions: []string{"issues:read"}}
	env, err := stageGithubAppWithAPI(context.Background(), []policy.RepoCred{{Repo: "acme/web"}, {Repo: "acme/api", Write: true}}, stage, acc, githubapp.New(&fakeForge{}, "http://x"), api)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(env, "SAFESLOP_GITHUB_TOKEN_DIR="+filepath.Join(stage, githubAPIDir)) || !slices.Contains(env, "SAFESLOP_GITHUB_TOKEN_MANIFEST="+filepath.Join(stage, githubAPIDir, githubAPIMetaFile)) {
		t.Fatalf("multiple API partitions need directory + manifest, got %v", env)
	}
	for _, forbidden := range []string{"SAFESLOP_GITHUB_TOKEN_FILE=", "GITHUB_TOKEN="} {
		if slices.ContainsFunc(env, func(v string) bool { return strings.HasPrefix(v, forbidden) }) {
			t.Fatalf("multiple partitions must not choose an ambiguous default: %v", env)
		}
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
