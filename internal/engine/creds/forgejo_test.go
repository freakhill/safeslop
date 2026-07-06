package creds

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/policy"
	"github.com/freakhill/safeslop/internal/engine/userconfig"
)

// forgejoAcc builds an in-memory accounts store with one forgejo link (specs/0069 T6: the deploy-key
// registration token comes from accounts.cue, not policy).
func forgejoAcc(host, owner, ref string, sshPort int) *userconfig.Accounts {
	fa := &userconfig.ForgejoAccount{TokenRef: ref}
	if sshPort != 0 {
		fa.SSHPort = sshPort
	}
	acc := &userconfig.Accounts{Accounts: map[string]userconfig.Account{}}
	acc.Upsert(userconfig.Account{Forge: "forgejo", Host: host, Owner: owner, Forgejo: fa})
	return acc
}

func TestStageForgejoDenyWithoutLink(t *testing.T) {
	binDir := t.TempDir()
	fakeStub(t, binDir, "ssh-keygen", `eval "p=\${$#}"; echo PRIV > "$p"; echo "ssh-ed25519 AAAAPUB" > "$p.pub"`)
	fakeStub(t, binDir, "ssh-keyscan", `eval "h=\${$#}"; echo "$h ssh-ed25519 AAAAHOSTKEY"`)
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))
	creds := &policy.Credentials{Forgejo: &policy.ForgejoCreds{
		URL:   "https://git.example.org",
		Repos: []policy.RepoCred{{Repo: "jojo/web"}},
	}}
	_, err := StageForgejo(context.Background(), creds, t.TempDir(), &userconfig.Accounts{Accounts: map[string]userconfig.Account{}})
	if err == nil || !strings.Contains(err.Error(), "safeslop creds link forgejo") {
		t.Fatalf("missing forgejo link must hard-deny, got %v", err)
	}
}

func TestParseForgejoRemote(t *testing.T) {
	type want struct{ host, port, owner, repo string }
	cases := map[string]want{
		"git@codeberg.org:acme/repo.git\n":                     {"codeberg.org", "22", "acme", "repo"},
		"https://codeberg.org/acme/repo.git\n":                 {"codeberg.org", "22", "acme", "repo"},
		"https://forgejo.example.com/acme/repo\n":              {"forgejo.example.com", "22", "acme", "repo"},
		"ssh://git@git.example.org/acme/repo.git\n":            {"git.example.org", "22", "acme", "repo"},
		"ssh://git@forgejojo.lucyjojo.me:2222/jojo/slop.git\n": {"forgejojo.lucyjojo.me", "2222", "jojo", "slop"},
	}
	for in, w := range cases {
		h, p, o, r, err := parseForgejoRemote([]byte(in))
		if err != nil || h != w.host || p != w.port || o != w.owner || r != w.repo {
			t.Fatalf("parseForgejoRemote(%q) = %q:%q %q/%q err=%v", in, h, p, o, r, err)
		}
	}
	if _, _, _, _, err := parseForgejoRemote([]byte("git@github.com:acme/repo.git\n")); err == nil {
		t.Fatal("expected error: github.com is the ssh provider's job, not forgejo")
	}
	if _, _, _, _, err := parseForgejoRemote([]byte("\n")); err == nil {
		t.Fatal("expected error on empty remote")
	}
	bad := []string{
		"git@codeberg.org:acme/repo\"\nProxyCommand=sh.git\n",
		"git@codeberg.org:acme\nHost */repo.git\n",
		"https://codeberg.org/acme/re po.git\n",
		"git@codeberg.org\nProxyCommand=sh:acme/repo.git\n",
	}
	for _, in := range bad {
		if _, _, _, _, err := parseForgejoRemote([]byte(in)); err == nil {
			t.Fatalf("parseForgejoRemote(%q) unexpectedly succeeded", in)
		}
	}
}

func TestForgejoURLs(t *testing.T) {
	if got := forgejoKeysURL("https://codeberg.org", "acme", "repo"); got != "https://codeberg.org/api/v1/repos/acme/repo/keys" {
		t.Fatalf("keys url = %q", got)
	}
	if got := forgejoKeyURL("https://codeberg.org", "acme", "repo", "42"); got != "https://codeberg.org/api/v1/repos/acme/repo/keys/42" {
		t.Fatalf("key url = %q", got)
	}
}

func TestForgejoKeyBody(t *testing.T) {
	var m map[string]any
	if err := json.Unmarshal(forgejoKeyBody("safeslop-run1", "ssh-ed25519 AAAA", false), &m); err != nil {
		t.Fatal(err)
	}
	if m["title"] != "safeslop-run1" || m["key"] != "ssh-ed25519 AAAA" || m["read_only"] != true {
		t.Fatalf("ro body = %v", m)
	}
	_ = json.Unmarshal(forgejoKeyBody("t", "k", true), &m)
	if m["read_only"] != false {
		t.Fatalf("rw body read_only = %v", m["read_only"])
	}
}

func TestStageForgejoMintsAndStages(t *testing.T) {
	var gotAuth, gotPath, gotBody, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotAuth, gotPath = r.Method, r.Header.Get("Authorization"), r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":7788,"read_only":true}`))
	}))
	defer srv.Close()

	binDir := t.TempDir()
	fakeStub(t, binDir, "git", `echo "git@codeberg.org:acme/repo.git"`)
	fakeStub(t, binDir, "ssh-keygen", `eval "p=\${$#}"; echo PRIV > "$p"; echo "ssh-ed25519 AAAAPUB safeslop" > "$p.pub"`)
	fakeStub(t, binDir, "ssh-keyscan", `echo "codeberg.org ssh-ed25519 AAAAHOSTKEY"`)
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))
	t.Setenv("FORGEJO_TOKEN", "secret-tok")

	stage := t.TempDir()
	creds := &policy.Credentials{Forgejo: &policy.ForgejoCreds{URL: srv.URL}}
	acc := forgejoAcc(hostFromURL(srv.URL), "acme", "env:FORGEJO_TOKEN", 0)
	env, err := StageForgejo(context.Background(), creds, stage, acc)
	if err != nil {
		t.Fatalf("StageForgejo: %v", err)
	}

	if gotMethod != "POST" || gotAuth != "token secret-tok" || gotPath != "/api/v1/repos/acme/repo/keys" {
		t.Fatalf("register req = %s %s auth=%q", gotMethod, gotPath, gotAuth)
	}
	if !strings.Contains(gotBody, `"read_only":true`) {
		t.Fatalf("body = %q", gotBody)
	}

	keyPath := filepath.Join(stage, ".ssh", "id_acme-repo")
	joined := strings.Join(env, " ")
	if !strings.Contains(joined, "GIT_SSH_COMMAND=ssh -F "+filepath.Join(stage, ".ssh", "config")) || !strings.Contains(joined, "GIT_CONFIG_GLOBAL="+filepath.Join(stage, ".gitconfig")) {
		t.Fatalf("env = %v", env)
	}
	if fi, _ := os.Stat(keyPath); fi == nil || fi.Mode().Perm() != 0o600 {
		t.Fatal("private key not staged 0600")
	}
	if _, err := os.Stat(keyPath + ".pub"); !os.IsNotExist(err) {
		t.Fatal(".pub must not remain staged")
	}
	khPath := filepath.Join(stage, ".ssh", "known_hosts")
	if b, _ := os.ReadFile(khPath); !strings.Contains(string(b), "codeberg.org ssh-ed25519") {
		t.Fatalf("known_hosts not pinned: %q", b)
	}
	ri, _ := os.ReadFile(filepath.Join(stage, ".ssh", "revoke-info"))
	if !strings.Contains(string(ri), "acme/repo 7788 env:FORGEJO_TOKEN") {
		t.Fatalf("revoke-info = %q", ri)
	}
	if strings.Contains(string(ri), "secret-tok") {
		t.Fatal("token VALUE must never be written to disk")
	}
}

func TestStageForgejoRemovesPubKeyBeforeRegistrationFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"boom"}`))
	}))
	defer srv.Close()

	binDir := t.TempDir()
	fakeStub(t, binDir, "ssh-keygen", `eval "p=\${$#}"; echo PRIV > "$p"; echo "ssh-ed25519 AAAAPUB safeslop" > "$p.pub"`)
	fakeStub(t, binDir, "ssh-keyscan", `eval "h=\${$#}"; echo "$h ssh-ed25519 AAAAHOSTKEY"`)
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))
	t.Setenv("FORGEJO_TOKEN", "secret-tok")

	stage := t.TempDir()
	host := hostFromURL(srv.URL)
	creds := &policy.Credentials{Forgejo: &policy.ForgejoCreds{
		URL:   srv.URL,
		Repos: []policy.RepoCred{{Repo: "acme/repo"}},
	}}
	acc := forgejoAcc(host, "acme", "env:FORGEJO_TOKEN", 0)
	_, err := StageForgejo(context.Background(), creds, stage, acc)
	if err == nil || !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("StageForgejo err = %v, want registration HTTP 500", err)
	}

	pubPath := filepath.Join(stage, ".ssh", "id_acme-repo.pub")
	if _, statErr := os.Stat(pubPath); !os.IsNotExist(statErr) {
		t.Fatalf("public key must be removed before registration can fail; stat err=%v", statErr)
	}
}

func TestStageForgejoNilIsNoop(t *testing.T) {
	env, err := StageForgejo(context.Background(), &policy.Credentials{}, t.TempDir(), nil)
	if err != nil || env != nil {
		t.Fatalf("nil forgejo creds must be a no-op: env=%v err=%v", env, err)
	}
}

func TestRevokeForgejoCallsDelete(t *testing.T) {
	var gotMethod, gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotAuth = r.Method, r.URL.Path, r.Header.Get("Authorization")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	t.Setenv("FORGEJO_TOKEN", "secret-tok")

	stage := t.TempDir()
	if err := os.MkdirAll(filepath.Join(stage, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, ".ssh", "revoke-info"),
		[]byte(srv.URL+" acme/repo 7788 env:FORGEJO_TOKEN\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	RevokeForgejo(context.Background(), stage)
	if gotMethod != "DELETE" || gotPath != "/api/v1/repos/acme/repo/keys/7788" || gotAuth != "token secret-tok" {
		t.Fatalf("delete req = %s %s auth=%q", gotMethod, gotPath, gotAuth)
	}
}

func TestRevokeForgejoNoInfoIsSilent(t *testing.T) {
	RevokeForgejo(context.Background(), t.TempDir())
}

func TestHostFromURL(t *testing.T) {
	cases := map[string]string{
		"https://codeberg.org":                "codeberg.org",
		"https://forgejojo.lucyjojo.me:3000/": "forgejojo.lucyjojo.me",
		"https://git.example.com/":            "git.example.com",
	}
	for in, want := range cases {
		if got := hostFromURL(in); got != want {
			t.Fatalf("hostFromURL(%q) = %q want %q", in, got, want)
		}
	}
}

func TestStageForgejoMultiRepo(t *testing.T) {
	type req struct{ path, body string }
	var reqs []req
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		reqs = append(reqs, req{r.URL.Path, string(b)})
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "api") {
			_, _ = w.Write([]byte(`{"id":555}`))
		}
	}))
	defer srv.Close()

	binDir := t.TempDir()
	stage := t.TempDir()
	fakeStub(t, binDir, "ssh-keygen", `eval "p=\${$#}"; echo PRIV > "$p"; echo "ssh-ed25519 AAAAPUB safeslop" > "$p.pub"`)
	fakeStub(t, binDir, "ssh-keyscan", `eval "h=\${$#}"; echo "$h ssh-ed25519 AAAAHOSTKEY"`)
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))
	t.Setenv("FORGEJO_TOKEN", "secret-tok")

	host := hostFromURL(srv.URL)
	creds := &policy.Credentials{Forgejo: &policy.ForgejoCreds{
		URL: srv.URL, SSHPort: 2222,
		Repos: []policy.RepoCred{{Repo: "jojo/web"}, {Repo: "jojo/api", Write: true}},
	}}
	acc := forgejoAcc(host, "jojo", "env:FORGEJO_TOKEN", 0)
	env, err := StageForgejo(context.Background(), creds, stage, acc)
	if err != nil {
		t.Fatalf("StageForgejo multi: %v", err)
	}

	for _, slug := range []string{"jojo-web", "jojo-api"} {
		kp := filepath.Join(stage, ".ssh", "id_"+slug)
		if fi, _ := os.Stat(kp); fi == nil || fi.Mode().Perm() != 0o600 {
			t.Fatalf("key %s not staged 0600", slug)
		}
		if _, err := os.Stat(kp + ".pub"); !os.IsNotExist(err) {
			t.Fatalf(".pub for %s must be removed", slug)
		}
	}

	cfg, _ := os.ReadFile(filepath.Join(stage, ".ssh", "config"))
	for _, want := range []string{
		"Host " + host + "-jojo-web",
		"Host " + host + "-jojo-api",
		"HostName " + host,
		"Port 2222",
	} {
		if !strings.Contains(string(cfg), want) {
			t.Fatalf("ssh config missing %q:\n%s", want, cfg)
		}
	}

	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "GIT_SSH_COMMAND=ssh -F "+filepath.Join(stage, ".ssh", "config")) ||
		!strings.Contains(joined, "GIT_CONFIG_GLOBAL="+filepath.Join(stage, ".gitconfig")) {
		t.Fatalf("env = %v", env)
	}

	// insteadOf covers both the scp-like and the ssh://host:port spellings.
	gc, _ := os.ReadFile(filepath.Join(stage, ".gitconfig"))
	for _, want := range []string{
		"insteadOf = git@" + host + ":jojo/web.git",
		"insteadOf = ssh://git@" + host + ":2222/jojo/web.git",
	} {
		if !strings.Contains(string(gc), want) {
			t.Fatalf("gitconfig missing %q:\n%s", want, gc)
		}
	}

	// revoke-info: one 4-field line per key, token ref (never value).
	ri, _ := os.ReadFile(filepath.Join(stage, ".ssh", "revoke-info"))
	if !strings.Contains(string(ri), "jojo/web 555 env:FORGEJO_TOKEN") || !strings.Contains(string(ri), "jojo/api 555 env:FORGEJO_TOKEN") {
		t.Fatalf("revoke-info = %q", ri)
	}
	if strings.Contains(string(ri), "secret-tok") {
		t.Fatal("token VALUE must never be on disk")
	}

	// API hit each repo; repo2 read-write.
	var web, api string
	for _, r := range reqs {
		if strings.Contains(r.path, "/jojo/web/keys") {
			web = r.body
		}
		if strings.Contains(r.path, "/jojo/api/keys") {
			api = r.body
		}
	}
	if !strings.Contains(web, `"read_only":true`) {
		t.Fatalf("web should be ro: %q", web)
	}
	if !strings.Contains(api, `"read_only":false`) {
		t.Fatalf("api should be rw: %q", api)
	}
}

func TestRevokeForgejoMultiRevokesAll(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			paths = append(paths, r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	t.Setenv("FORGEJO_TOKEN", "secret-tok")

	stage := t.TempDir()
	if err := os.MkdirAll(filepath.Join(stage, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	info := srv.URL + " jojo/web 555 env:FORGEJO_TOKEN\n" + srv.URL + " jojo/api 556 env:FORGEJO_TOKEN\n"
	if err := os.WriteFile(filepath.Join(stage, ".ssh", "revoke-info"), []byte(info), 0o600); err != nil {
		t.Fatal(err)
	}
	RevokeForgejo(context.Background(), stage)
	for _, want := range []string{"/api/v1/repos/jojo/web/keys/555", "/api/v1/repos/jojo/api/keys/556"} {
		found := false
		for _, p := range paths {
			if p == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("revoke missing %q (got %v)", want, paths)
		}
	}
}
