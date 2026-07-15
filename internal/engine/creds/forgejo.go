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
	"time"

	"github.com/freakhill/safeslop/internal/engine/policy"
	"github.com/freakhill/safeslop/internal/engine/secrets"
	"github.com/freakhill/safeslop/internal/engine/userconfig"
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
	if err := validateGitHost(host); err != nil {
		return "", "", "", "", fmt.Errorf("origin remote %q: %w", u, err)
	}
	if strings.EqualFold(host, "github.com") {
		return "", "", "", "", fmt.Errorf("origin is github.com (%q); use ssh creds (the GitHub provider) for that", u)
	}
	rest = strings.TrimSuffix(rest, ".git")
	parts := strings.Split(rest, "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", "", "", fmt.Errorf("could not parse owner/repo from %q", u)
	}
	if err := validateOwnerRepo(parts[0], parts[1]); err != nil {
		return "", "", "", "", fmt.Errorf("origin remote %q: %w", u, err)
	}
	return host, port, parts[0], parts[1], nil
}

// ---- transport ----

const forgejoAPITokenFile = ".forgejo-api-token"

// ForgejoHTTP is the host-only Forgejo REST transport seam shared by staging, cleanup, and the
// explicit GC command. Implementations receive the token only long enough to place it in the
// Authorization header; they must never log or persist it.
type ForgejoHTTP interface {
	Do(ctx context.Context, method, url, token string, body []byte) ([]byte, int, error)
}

type forgejoHTTP struct{ client *http.Client }

// NewForgejoHTTP returns the production Forgejo transport with a bounded request timeout.
func NewForgejoHTTP() ForgejoHTTP {
	return &forgejoHTTP{client: &http.Client{Timeout: 30 * time.Second}}
}

// forgejoDo retains the package-local convenience for probes. Staging and cleanup take an
// explicit ForgejoHTTP, keeping their tests hermetic and their authority narrow.
func forgejoDo(ctx context.Context, method, url, token string, body []byte) ([]byte, int, error) {
	return forgejoDoWith(ctx, NewForgejoHTTP(), method, url, token, body)
}

// forgejoDoWith delegates to the injected host-only transport.
func forgejoDoWith(ctx context.Context, client ForgejoHTTP, method, url, token string, body []byte) ([]byte, int, error) {
	return client.Do(ctx, method, url, token, body)
}

func (h *forgejoHTTP) Do(ctx context.Context, method, url, token string, body []byte) ([]byte, int, error) {
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
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return b, resp.StatusCode, nil
}

// ProbeForgejo verifies a Forgejo/Gitea account token by listing one repo
// (GET /api/v1/user/repos?limit=1) against the instance base. It returns only an error class; the
// token value is never logged (specs/0069 T5, value-free probes). base is the instance base URL.
func ProbeForgejo(ctx context.Context, base, token string) error {
	u := strings.TrimRight(base, "/") + "/api/v1/user/repos?limit=1"
	_, code, err := forgejoDo(ctx, http.MethodGet, u, token, nil)
	if err != nil {
		return fmt.Errorf("forgejo probe transport error: %w", err)
	}
	if code == http.StatusUnauthorized || code == http.StatusForbidden {
		return fmt.Errorf("forgejo token rejected (HTTP %d) \u2014 check the token and its scopes", code)
	}
	if code/100 != 2 {
		return fmt.Errorf("forgejo probe failed (HTTP %d)", code)
	}
	return nil
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
// https://<host>. Like StageGithub, no revoke is relied upon — the stageDir wipe destroys the key.
func StageForgejo(ctx context.Context, creds *policy.Credentials, stageDir string, accounts *userconfig.Accounts) ([]string, error) {
	if creds == nil || creds.Forgejo == nil {
		return nil, nil
	}
	return stageForgejoWithHTTP(ctx, creds.Forgejo, stageDir, accounts, NewForgejoHTTP())
}

func stageForgejoWithHTTP(ctx context.Context, fc *policy.ForgejoCreds, stageDir string, accounts *userconfig.Accounts, client ForgejoHTTP) ([]string, error) {
	if fc == nil {
		return nil, nil
	}
	resolved := fc
	if len(fc.Repos) == 0 {
		rOut, err := runSSHCmd(ctx, []string{"git", "remote", "get-url", "origin"}, "run safeslop from a repo with a Forgejo origin")
		if err != nil {
			return nil, err
		}
		host, port, owner, repo, err := parseForgejoRemote(rOut)
		if err != nil {
			return nil, err
		}
		resolved = &policy.ForgejoCreds{Write: fc.Write, Ttl: fc.Ttl, URL: forgejoAPIBase(fc, host), Repos: []policy.RepoCred{{Repo: owner + "/" + repo, Write: fc.Write}}, SSHPort: atoiOrZero(port), Api: fc.Api}
	}

	var apiToken string
	if resolved.Api != nil && resolved.Api.Enabled {
		if !resolved.Api.AckAccountWide {
			return nil, fmt.Errorf("forgejo API staging requires api.ackAccountWide: true")
		}
		var err error
		apiToken, err = resolveForgejoAPIToken(ctx, resolved, accounts)
		if err != nil {
			return nil, err
		}
	}
	env, err := stageForgejoMultiWithHTTP(ctx, resolved, stageDir, accounts, client)
	if err != nil {
		return nil, err
	}
	if apiToken == "" {
		return env, nil
	}
	path, err := stageForgejoAPIToken(stageDir, apiToken)
	if err != nil {
		return nil, err
	}
	return append(env, "SAFESLOP_FORGEJO_TOKEN_FILE="+path), nil
}

func resolveForgejoAPIToken(ctx context.Context, fc *policy.ForgejoCreds, accounts *userconfig.Accounts) (string, error) {
	host := hostFromURL(fc.URL)
	if host == "" {
		return "", fmt.Errorf("could not parse host from forgejo url")
	}
	owners := map[string]struct{}{}
	for _, rc := range fc.Repos {
		owner, _, err := splitOwnerRepo(rc.Repo)
		if err != nil {
			return "", err
		}
		owners[owner] = struct{}{}
	}
	if len(owners) != 1 {
		return "", fmt.Errorf("forgejo API staging requires repos for exactly one linked owner")
	}
	var owner string
	for owner = range owners {
	}
	link := accounts.Lookup(host, owner)
	if link == nil || link.Forgejo == nil {
		return "", fmt.Errorf("no forgejo account link for %s/%s — run: safeslop creds link forgejo", host, owner)
	}
	token, err := secrets.Resolve(ctx, link.Forgejo.TokenRef)
	if err != nil {
		return "", fmt.Errorf("forgejo API token: %w", err)
	}
	return token, nil
}

func stageForgejoAPIToken(stageDir, token string) (string, error) {
	path := filepath.Join(stageDir, forgejoAPITokenFile)
	if err := os.WriteFile(path+".new", []byte(token), 0o600); err != nil {
		return "", err
	}
	if err := os.Chmod(path+".new", 0o600); err != nil {
		return "", err
	}
	if err := os.Rename(path+".new", path); err != nil {
		return "", err
	}
	return path, nil
}

// RevokeForgejo best-effort revokes the staged Forgejo deploy keys. Never relied upon for
// security; errors are swallowed because stage wipe is the cleanup guarantee.
func RevokeForgejo(ctx context.Context, stageDir string) {
	revokeForgejoWithHTTP(ctx, stageDir, NewForgejoHTTP())
}

func revokeForgejoWithHTTP(ctx context.Context, stageDir string, client ForgejoHTTP) {
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
		_, _, _ = forgejoDoWith(ctx, client, http.MethodDelete, forgejoKeyURL(base, or[0], or[1], id), token, nil)
	}
}

// ExpireForgejo applies the bounded Forgejo horizon: it removes the canonical API token first,
// then attempts deploy-key cleanup. An unbounded lease keeps both until normal teardown.
func ExpireForgejo(ctx context.Context, stageDir string) {
	expireForgejoWithHTTP(ctx, stageDir, NewForgejoHTTP())
}

func expireForgejoWithHTTP(ctx context.Context, stageDir string, client ForgejoHTTP) {
	_ = os.Remove(filepath.Join(stageDir, forgejoAPITokenFile))
	revokeForgejoWithHTTP(ctx, stageDir, client)
}
