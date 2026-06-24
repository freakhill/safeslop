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
)

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
	creds := &policy.Credentials{Forgejo: &policy.ForgejoCreds{URL: srv.URL, Token: "env:FORGEJO_TOKEN"}}
	env, err := StageForgejo(context.Background(), creds, stage)
	if err != nil {
		t.Fatalf("StageForgejo: %v", err)
	}

	if gotMethod != "POST" || gotAuth != "token secret-tok" || gotPath != "/api/v1/repos/acme/repo/keys" {
		t.Fatalf("register req = %s %s auth=%q", gotMethod, gotPath, gotAuth)
	}
	if !strings.Contains(gotBody, `"read_only":true`) {
		t.Fatalf("body = %q", gotBody)
	}

	keyPath := filepath.Join(stage, ".ssh", "id")
	khPath := filepath.Join(stage, ".ssh", "known_hosts")
	joined := strings.Join(env, " ")
	if !strings.Contains(joined, "GIT_SSH_COMMAND=ssh -i "+keyPath) || !strings.Contains(joined, "UserKnownHostsFile="+khPath) {
		t.Fatalf("env = %v", env)
	}
	if fi, _ := os.Stat(keyPath); fi == nil || fi.Mode().Perm() != 0o600 {
		t.Fatal("private key not staged 0600")
	}
	if _, err := os.Stat(keyPath + ".pub"); !os.IsNotExist(err) {
		t.Fatal(".pub must not remain staged")
	}
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

func TestStageForgejoNilIsNoop(t *testing.T) {
	env, err := StageForgejo(context.Background(), &policy.Credentials{}, t.TempDir())
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
