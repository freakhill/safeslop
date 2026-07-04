package cli

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/creds/githubapp"
	"github.com/freakhill/safeslop/internal/engine/userconfig"
)

// fakeGHHTTP implements githubapp.ForgeHTTP: it returns a canned installation object so the link/
// status probes run without a real forge.
type fakeGHHTTP struct {
	login  string
	status int
}

func (f fakeGHHTTP) Do(_ context.Context, _, _ string, _ map[string]string, _ []byte) ([]byte, int, error) {
	st := f.status
	if st == 0 {
		st = 200
	}
	return []byte(fmt.Sprintf(`{"id":99,"app_id":42,"account":{"login":%q,"type":"Organization"}}`, f.login)), st, nil
}

// ghKeyRefEnv puts a real RSA key behind an env: ref (so the App JWT actually signs during probes).
func ghKeyRefEnv(t *testing.T) string {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)})
	t.Setenv("TEST_LINK_KEY", string(p))
	return "env:TEST_LINK_KEY"
}

func TestRunLinkGithubDerivesOwnerAndStoresRefOnly(t *testing.T) {
	keyRef := ghKeyRefEnv(t)
	accPath := filepath.Join(t.TempDir(), "accounts.cue")
	client := githubapp.New(fakeGHHTTP{login: "acme"}, "http://x")

	out, err := runLinkGithub(context.Background(), accPath, 42, 99, keyRef, "", client)
	if err != nil {
		t.Fatalf("runLinkGithub: %v", err)
	}
	if !strings.Contains(out, "github.com/acme") {
		t.Fatalf("confirmation must name the derived owner: %q", out)
	}

	acc, err := userconfig.LoadAccounts(accPath)
	if err != nil {
		t.Fatal(err)
	}
	a := acc.Lookup("github.com", "acme")
	if a == nil || a.Github == nil || a.Github.AppID != 42 || a.Github.InstallationID != 99 {
		t.Fatalf("link not stored: %+v", a)
	}
	if a.Github.PrivateKeyRef != keyRef {
		t.Fatalf("must store the key ref, not the value: %q", a.Github.PrivateKeyRef)
	}
	raw, _ := os.ReadFile(accPath)
	if strings.Contains(string(raw), "PRIVATE KEY") || strings.Contains(string(raw), "BEGIN") {
		t.Fatalf("accounts file must not contain key bytes:\n%s", raw)
	}
}

func TestRunLinkForgejoProbesAndStoresRefOnly(t *testing.T) {
	t.Setenv("TEST_FJ_TOKEN", "fj_secret_value")
	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()
	accPath := filepath.Join(t.TempDir(), "accounts.cue")

	out, err := runLinkForgejo(context.Background(), accPath, "git.example.org", "bob", "env:TEST_FJ_TOKEN", 2222, srv.URL)
	if err != nil {
		t.Fatalf("runLinkForgejo: %v", err)
	}
	if gotAuth != "token fj_secret_value" {
		t.Fatalf("probe must send the resolved token: %q", gotAuth)
	}
	if gotPath != "/api/v1/user/repos" {
		t.Fatalf("probe hit wrong path: %q", gotPath)
	}
	if strings.Contains(out, "fj_secret_value") {
		t.Fatalf("confirmation leaked the token: %q", out)
	}
	acc, _ := userconfig.LoadAccounts(accPath)
	a := acc.Lookup("git.example.org", "bob")
	if a == nil || a.Forgejo == nil || a.Forgejo.TokenRef != "env:TEST_FJ_TOKEN" || a.Forgejo.SSHPort != 2222 {
		t.Fatalf("forgejo link not stored right: %+v", a)
	}
	raw, _ := os.ReadFile(accPath)
	if strings.Contains(string(raw), "fj_secret_value") {
		t.Fatalf("accounts file leaked the token value:\n%s", raw)
	}
}

func TestRunLinkForgejoRejectsBadToken(t *testing.T) {
	t.Setenv("TEST_FJ_TOKEN", "bad")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(401) }))
	defer srv.Close()
	accPath := filepath.Join(t.TempDir(), "accounts.cue")

	if _, err := runLinkForgejo(context.Background(), accPath, "h", "o", "env:TEST_FJ_TOKEN", 0, srv.URL); err == nil {
		t.Fatal("a 401 probe must fail the link")
	}
	if _, err := os.Stat(accPath); err == nil {
		t.Fatal("a failed link must not write the accounts file")
	}
}

func TestRunUnlink(t *testing.T) {
	accPath := filepath.Join(t.TempDir(), "accounts.cue")
	acc := &userconfig.Accounts{Accounts: map[string]userconfig.Account{}}
	acc.Upsert(userconfig.Account{Forge: "github", Host: "github.com", Owner: "acme",
		Github: &userconfig.GithubAccount{AppID: 1, InstallationID: 2, PrivateKeyRef: "env:X"}})
	if err := userconfig.SaveAccounts(accPath, acc); err != nil {
		t.Fatal(err)
	}
	if out, err := runUnlink(accPath, "github.com/acme"); err != nil || !strings.Contains(out, "unlinked github.com/acme") {
		t.Fatalf("unlink: out=%q err=%v", out, err)
	}
	if out, _ := runUnlink(accPath, "github.com/acme"); !strings.Contains(out, "no link") {
		t.Fatalf("second unlink must report absent: %q", out)
	}
}

func TestRunCredsStatusValueFree(t *testing.T) {
	keyRef := ghKeyRefEnv(t)
	t.Setenv("TEST_FJ_TOKEN", "fj_secret_value")
	accPath := filepath.Join(t.TempDir(), "accounts.cue")
	acc := &userconfig.Accounts{Accounts: map[string]userconfig.Account{}}
	acc.Upsert(userconfig.Account{Forge: "github", Host: "github.com", Owner: "acme",
		Github: &userconfig.GithubAccount{AppID: 42, InstallationID: 99, PrivateKeyRef: keyRef}})
	acc.Upsert(userconfig.Account{Forge: "forgejo", Host: "git.example.org", Owner: "bob",
		Forgejo: &userconfig.ForgejoAccount{TokenRef: "env:TEST_FJ_TOKEN", SSHPort: 2222}})
	if err := userconfig.SaveAccounts(accPath, acc); err != nil {
		t.Fatal(err)
	}

	ghClient := githubapp.New(fakeGHHTTP{login: "acme"}, "http://x")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()
	base := func(string) string { return srv.URL }

	out, err := runCredsStatus(context.Background(), accPath, false, ghClient, base)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"github", "github.com/acme", "app=42", "inst=99", "probe=ok", "ttl=1h-renewable",
		"forgejo", "git.example.org/bob", "ssh-port=2222", "ttl=account-wide token",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("status missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "fj_secret_value") {
		t.Fatalf("status leaked the token:\n%s", out)
	}

	js, err := runCredsStatus(context.Background(), accPath, true, ghClient, base)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(js, `"links"`) || !strings.Contains(js, `"probe": "ok"`) {
		t.Fatalf("json status shape wrong:\n%s", js)
	}
	if strings.Contains(js, "fj_secret_value") {
		t.Fatalf("json status leaked the token:\n%s", js)
	}
}
