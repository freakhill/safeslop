package creds

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/freakhill/safeslop/internal/engine/policy"
	"github.com/freakhill/safeslop/internal/engine/secrets"
)

// ---- url + body builders (pure) ----

func forgejoKeysURL(base, owner, repo string) string {
	return base + "/api/v1/repos/" + owner + "/" + repo + "/keys"
}

func forgejoKeyURL(base, owner, repo, id string) string {
	return forgejoKeysURL(base, owner, repo) + "/" + id
}

func forgejoKeyBody(title, key string, write bool) []byte {
	b, _ := json.Marshal(map[string]any{"title": title, "key": key, "read_only": !write})
	return b
}

// forgejoAPIBase is the instance base URL: the explicit creds.URL when set, else https://<host>.
func forgejoAPIBase(fc *policy.ForgejoCreds, host string) string {
	if fc.URL != "" {
		return strings.TrimRight(fc.URL, "/")
	}
	return "https://" + host
}

// hostFromURL returns the hostname (no port) of an instance base URL.
func hostFromURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// ---- parsers ----

// parseForgejoRemote extracts (host, port, owner, repo) from a non-GitHub git remote URL. It
// handles scp-like (git@host:owner/repo.git), ssh:// (with optional :port), and http(s):// forms.
// GitHub remotes are rejected — that is the ssh.go (GitHub) provider's job.
func parseForgejoRemote(out []byte) (host, port, owner, repo string, err error) {
	u := strings.TrimSpace(string(out))
	if u == "" {
		return "", "", "", "", fmt.Errorf("empty origin remote")
	}
	port = "22"
	s := u
	hasScheme := false
	if i := strings.Index(s, "://"); i >= 0 {
		hasScheme = true
		s = s[i+3:]
	}
	if i := strings.Index(s, "@"); i >= 0 {
		s = s[i+1:]
	}
	var rest string
	if hasScheme {
		// host[:port]/owner/repo — the colon is a port, the path starts at the first slash.
		slash := strings.Index(s, "/")
		if slash < 0 {
			return "", "", "", "", fmt.Errorf("could not parse path from %q", u)
		}
		host = s[:slash]
		rest = strings.TrimLeft(s[slash:], "/")
		if c := strings.Index(host, ":"); c >= 0 {
			port = host[c+1:]
			host = host[:c]
		}
	} else {
		// scp-like host:owner/repo — the colon separates host from path (no port in scp syntax).
		colon := strings.Index(s, ":")
		if colon < 0 {
			return "", "", "", "", fmt.Errorf("could not parse host from %q", u)
		}
		host = s[:colon]
		rest = s[colon+1:]
	}
	if host == "" {
		return "", "", "", "", fmt.Errorf("could not parse host from %q", u)
	}
	if strings.EqualFold(host, "github.com") {
		return "", "", "", "", fmt.Errorf("origin is github.com (%q); use ssh creds (the GitHub provider) for that", u)
	}
	rest = strings.TrimSuffix(rest, ".git")
	parts := strings.Split(rest, "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", "", "", fmt.Errorf("could not parse owner/repo from %q", u)
	}
	return host, port, parts[0], parts[1], nil
}

// ---- transport ----

// forgejoDo issues an authenticated Forgejo API request and returns body + status. The token is a
// resolved secret value; it is sent in the Authorization header and never logged.
func forgejoDo(ctx context.Context, method, url, token string, body []byte) ([]byte, int, error) {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, r)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return b, resp.StatusCode, nil
}

// forgejoKeyscan pins the instance's ed25519 host key for this run (StrictHostKeyChecking=yes, no
// TOFU inside the boundary). For non-22 ports ssh-keyscan emits a "[host]:port" entry that the
// staged GIT_SSH_COMMAND's ssh matches automatically.
func forgejoKeyscan(ctx context.Context, host, port string) ([]byte, error) {
	argv := []string{"ssh-keyscan", "-t", "ed25519"}
	if port != "" && port != "22" {
		argv = append(argv, "-p", port)
	}
	argv = append(argv, host)
	out, err := runSSHCmd(ctx, argv, "could not reach the Forgejo instance to pin its host key")
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(out))) == 0 {
		return nil, fmt.Errorf("ssh-keyscan returned no ed25519 host key for %s", host)
	}
	return out, nil
}

// StageForgejo mints a fresh ed25519 keypair into stageDir/.ssh, registers the public key as a
// repo-scoped Forgejo/Gitea deploy key (read-only unless creds.Forgejo.Write), pins the instance
// host key via ssh-keyscan, stages ONLY the 0600 private key + known_hosts + a revoke-info file
// (which records the token *ref*, never its value), and returns GIT_SSH_COMMAND as a non-secret
// path env. owner/repo/host come from the cwd's `origin` remote; the API base is creds.URL or
// https://<host>. Like StageSSH, no revoke is relied upon — the stageDir wipe destroys the key.
func StageForgejo(ctx context.Context, creds *policy.Credentials, stageDir string) ([]string, error) {
	if creds == nil || creds.Forgejo == nil {
		return nil, nil
	}
	fc := creds.Forgejo

	// Multi-repo: one deploy key per named repo, staged with SSH aliases + insteadOf (specs/0047 P2).
	if len(fc.Repos) > 0 {
		return stageForgejoMulti(ctx, fc, stageDir)
	}

	rOut, err := runSSHCmd(ctx, []string{"git", "remote", "get-url", "origin"}, "run safeslop from a repo with a Forgejo origin")
	if err != nil {
		return nil, err
	}
	host, port, owner, repo, err := parseForgejoRemote(rOut)
	if err != nil {
		return nil, err
	}
	token, err := secrets.Resolve(ctx, fc.Token)
	if err != nil {
		return nil, fmt.Errorf("forgejo token: %w", err)
	}
	base := forgejoAPIBase(fc, host)

	sshDir := filepath.Join(stageDir, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		return nil, err
	}
	keyPath := filepath.Join(sshDir, "id")
	khPath := filepath.Join(sshDir, "known_hosts")

	title := "safeslop-" + owner + "-" + repo
	if _, err := runSSHCmd(ctx, keygenArgv(keyPath, title), "is ssh-keygen on PATH?"); err != nil {
		return nil, err
	}
	pub, err := os.ReadFile(keyPath + ".pub")
	if err != nil {
		return nil, fmt.Errorf("read generated public key: %w", err)
	}

	body := forgejoKeyBody(title, strings.TrimSpace(string(pub)), fc.Write)
	respBody, code, err := forgejoDo(ctx, http.MethodPost, forgejoKeysURL(base, owner, repo), token, body)
	if err != nil {
		return nil, fmt.Errorf("forgejo deploy-key register: %w", err)
	}
	if code < 200 || code >= 300 {
		return nil, fmt.Errorf("forgejo deploy-key register failed: HTTP %d (is the token valid with repo admin?)", code)
	}
	keyID, err := parseKeyID(respBody)
	if err != nil {
		return nil, err
	}

	_ = os.Remove(keyPath + ".pub") // only the private key crosses the boundary
	if err := os.Chmod(keyPath, 0o600); err != nil {
		return nil, err
	}

	kh, err := forgejoKeyscan(ctx, host, port)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(khPath, kh, 0o600); err != nil {
		return nil, err
	}

	// revoke-info: "<base> <owner>/<repo> <id> <token-ref>". The token REF (op://… or env:NAME),
	// never its value, so re-resolving at revoke time keeps no secret on disk.
	info := base + " " + owner + "/" + repo + " " + keyID + " " + fc.Token + "\n"
	if err := os.WriteFile(filepath.Join(sshDir, "revoke-info"), []byte(info), 0o600); err != nil {
		return nil, err
	}

	return []string{"GIT_SSH_COMMAND=" + renderGitSSHCommand(keyPath, khPath)}, nil
}

// RevokeForgejo best-effort revokes the staged Forgejo deploy key (reads stageDir/.ssh/revoke-info,
// re-resolves the token ref). Never relied upon for security; errors are swallowed (the stageDir
// wipe is the real cleanup).
func RevokeForgejo(ctx context.Context, stageDir string) {
	b, err := os.ReadFile(filepath.Join(stageDir, ".ssh", "revoke-info"))
	if err != nil {
		return
	}
	// One "<base> <owner>/<repo> <id> <token-ref>" line per staged key (1 single-repo, N multi).
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		f := strings.Fields(line)
		if len(f) != 4 {
			continue
		}
		base, ownerRepo, id, tokenRef := f[0], f[1], f[2], f[3]
		or := strings.SplitN(ownerRepo, "/", 2)
		if len(or) != 2 {
			continue
		}
		token, err := secrets.Resolve(ctx, tokenRef)
		if err != nil {
			continue
		}
		_, _, _ = forgejoDo(ctx, http.MethodDelete, forgejoKeyURL(base, or[0], or[1], id), token, nil)
	}
}
