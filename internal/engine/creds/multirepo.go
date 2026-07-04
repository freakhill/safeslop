package creds

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/freakhill/safeslop/internal/engine/policy"
	"github.com/freakhill/safeslop/internal/engine/secrets"
	"github.com/freakhill/safeslop/internal/engine/userconfig"
)

// repoSlug turns "owner/name" into a filesystem- and SSH-alias-safe "owner-name".
func repoSlug(ownerRepo string) string {
	return strings.ReplaceAll(strings.TrimSpace(ownerRepo), "/", "-")
}

func atoiOrZero(s string) int {
	i, _ := strconv.Atoi(s)
	return i
}

// splitOwnerRepo parses an "owner/name" spec from a #RepoCred.
func splitOwnerRepo(s string) (owner, repo string, err error) {
	p := strings.SplitN(strings.TrimSpace(s), "/", 2)
	if len(p) != 2 || p[0] == "" || p[1] == "" {
		return "", "", fmt.Errorf("repo %q must be \"owner/name\"", s)
	}
	return p[0], p[1], nil
}

// aliasEntry is one staged per-repo deploy key, ready to be wired into the SSH config + insteadOf.
type aliasEntry struct {
	slug, owner, repo, keyPath string
}

// stageRepoSSH writes the shared staging artifacts for a set of per-repo deploy keys and returns
// the env that points the agent's git at them:
//   - .ssh/known_hosts : the pinned host key(s) for hostName (shared by every alias)
//   - .ssh/config      : one "Host <hostName>-<slug>" block per repo, each with its own key
//   - .gitconfig       : git insteadOf rewrites mapping the real remote onto the alias, plus an
//     include of the boundary's ~/.gitconfig so user identity is preserved
//
// This is the "multiple deploy keys, one host" solution: deploy keys are 1:1 with a repo, so N
// repos need N keys; distinct host aliases + insteadOf let git select the right key per repo.
func stageRepoSSH(stageDir, hostName, port string, knownHosts []byte, entries []aliasEntry) ([]string, error) {
	sshDir := filepath.Join(stageDir, ".ssh")
	khPath := filepath.Join(sshDir, "known_hosts")
	if err := os.WriteFile(khPath, knownHosts, 0o600); err != nil {
		return nil, err
	}

	cfgPath := filepath.Join(sshDir, "config")
	if err := os.WriteFile(cfgPath, []byte(renderAliasSSHConfig(hostName, port, khPath, entries, func(p string) string { return p })), 0o600); err != nil {
		return nil, err
	}
	// Container runs see the same staged tree at a different path; keep a separate SSH config so
	// IdentityFile/UserKnownHostsFile point at the path that exists inside the container.
	containerKH := "/safeslop/runtime/.ssh/known_hosts"
	if err := os.WriteFile(filepath.Join(sshDir, "config.container"), []byte(renderAliasSSHConfig(hostName, port, containerKH, entries, func(p string) string { return "/safeslop/runtime/.ssh/" + filepath.Base(p) })), 0o600); err != nil {
		return nil, err
	}

	var gc strings.Builder
	// Preserve the agent's identity (user.name/email) when present; a missing include is ignored.
	gc.WriteString("[include]\n\tpath = ~/.gitconfig\n")
	for _, e := range entries {
		alias := hostName + "-" + e.slug
		fmt.Fprintf(&gc, "[url \"git@%s:%s/%s.git\"]\n", alias, e.owner, e.repo)
		fmt.Fprintf(&gc, "\tinsteadOf = git@%s:%s/%s.git\n", hostName, e.owner, e.repo)
		if port != "" && port != "22" {
			// Non-22 forges spell the remote as ssh://git@host:port/owner/repo.git; rewrite that too.
			fmt.Fprintf(&gc, "\tinsteadOf = ssh://git@%s:%s/%s/%s.git\n", hostName, port, e.owner, e.repo)
		}
	}
	gcPath := filepath.Join(stageDir, ".gitconfig")
	if err := os.WriteFile(gcPath, []byte(gc.String()), 0o600); err != nil {
		return nil, err
	}

	return []string{
		"GIT_SSH_COMMAND=ssh -F " + cfgPath,
		"GIT_CONFIG_GLOBAL=" + gcPath,
	}, nil
}

// renderAliasSSHConfig renders one "Host <hostName>-<slug>" SSH block per entry, each pinned to its
// own IdentityFile plus the shared known_hosts. keyPath maps a staged key path into the host or
// container view. Used by the Forgejo per-repo deploy-key staging (specs/0047 P2).
func renderAliasSSHConfig(hostName, port, knownHostsPath string, entries []aliasEntry, keyPath func(string) string) string {
	var cfg strings.Builder
	for _, e := range entries {
		alias := hostName + "-" + e.slug
		fmt.Fprintf(&cfg, "Host %s\n", alias)
		fmt.Fprintf(&cfg, "  HostName %s\n", hostName)
		fmt.Fprintf(&cfg, "  User git\n")
		if port != "" && port != "22" {
			fmt.Fprintf(&cfg, "  Port %s\n", port)
		}
		fmt.Fprintf(&cfg, "  IdentityFile %s\n", keyPath(e.keyPath))
		fmt.Fprintf(&cfg, "  IdentitiesOnly yes\n")
		fmt.Fprintf(&cfg, "  StrictHostKeyChecking yes\n")
		fmt.Fprintf(&cfg, "  UserKnownHostsFile %s\n\n", knownHostsPath)
	}
	return cfg.String()
}

// stageForgejoMulti mints one ephemeral Forgejo/Gitea deploy key per repo in fc.Repos (all on the
// same instance) and stages them with per-repo SSH aliases + insteadOf (specs/0047 P2). The
// instance host comes from fc.URL (required here — no single origin to infer it from) and the git
// SSH port from fc.SSHPort (default 22); the host key is pinned once via ssh-keyscan. revoke-info
// gets one "<base> <owner>/<repo> <id> <token-ref>" line per key (the token REF, never its value).
func stageForgejoMulti(ctx context.Context, fc *policy.ForgejoCreds, stageDir string, accounts *userconfig.Accounts) ([]string, error) {
	if fc.URL == "" {
		return nil, fmt.Errorf("forgejo multi-repo (repos) requires `url` (the instance base, e.g. https://codeberg.org)")
	}
	host := hostFromURL(fc.URL)
	if host == "" {
		return nil, fmt.Errorf("could not parse host from forgejo url %q", fc.URL)
	}
	port := "22"
	if fc.SSHPort != 0 {
		port = strconv.Itoa(fc.SSHPort)
	}
	base := strings.TrimRight(fc.URL, "/")
	// The account token that registers each deploy key comes from the accounts link for that repo's
	// owner (specs/0069 T6): resolved into host memory, cached per owner, never written. revoke-info
	// stores the token REF only.
	tokenByOwner := map[string]string{}
	refByOwner := map[string]string{}

	sshDir := filepath.Join(stageDir, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		return nil, err
	}
	kh, err := forgejoKeyscan(ctx, host, port)
	if err != nil {
		return nil, err
	}
	entries := make([]aliasEntry, 0, len(fc.Repos))
	var revoke strings.Builder
	for _, rc := range fc.Repos {
		owner, repo, err := splitOwnerRepo(rc.Repo)
		if err != nil {
			return nil, err
		}
		slug := repoSlug(rc.Repo)
		tok, ok := tokenByOwner[owner]
		if !ok {
			link := accounts.Lookup(host, owner)
			if link == nil || link.Forgejo == nil {
				return nil, fmt.Errorf("no forgejo account link for %s/%s — run: safeslop creds link forgejo", host, owner)
			}
			resolved, rerr := secrets.Resolve(ctx, link.Forgejo.TokenRef)
			if rerr != nil {
				return nil, fmt.Errorf("forgejo token for %s: %w", owner, rerr)
			}
			tokenByOwner[owner], refByOwner[owner], tok = resolved, link.Forgejo.TokenRef, resolved
		}
		keyPath := filepath.Join(sshDir, "id_"+slug)
		title := "safeslop-" + owner + "-" + repo
		if _, err := runSSHCmd(ctx, keygenArgv(keyPath, title), "is ssh-keygen on PATH?"); err != nil {
			return nil, err
		}
		pub, err := os.ReadFile(keyPath + ".pub")
		if err != nil {
			return nil, fmt.Errorf("read generated public key for %s: %w", rc.Repo, err)
		}
		body := forgejoKeyBody(title, strings.TrimSpace(string(pub)), rc.Write)
		respBody, code, err := forgejoDo(ctx, http.MethodPost, forgejoKeysURL(base, owner, repo), tok, body)
		if err != nil {
			return nil, fmt.Errorf("forgejo deploy-key register (%s): %w", rc.Repo, err)
		}
		if code < 200 || code >= 300 {
			return nil, fmt.Errorf("forgejo deploy-key register (%s) failed: HTTP %d (is the token valid with repo admin?)", rc.Repo, code)
		}
		keyID, err := parseKeyID(respBody)
		if err != nil {
			return nil, err
		}
		_ = os.Remove(keyPath + ".pub")
		if err := os.Chmod(keyPath, 0o600); err != nil {
			return nil, err
		}
		entries = append(entries, aliasEntry{slug: slug, owner: owner, repo: repo, keyPath: keyPath})
		fmt.Fprintf(&revoke, "%s %s/%s %s %s\n", base, owner, repo, keyID, refByOwner[owner])
	}
	if err := os.WriteFile(filepath.Join(sshDir, "revoke-info"), []byte(revoke.String()), 0o600); err != nil {
		return nil, err
	}
	return stageRepoSSH(stageDir, host, port, kh, entries)
}
