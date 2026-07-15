package creds

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/freakhill/safeslop/internal/engine/creds/githubapp"
	"github.com/freakhill/safeslop/internal/engine/policy"
	"github.com/freakhill/safeslop/internal/engine/secrets"
	"github.com/freakhill/safeslop/internal/engine/userconfig"
)

const (
	githubHost         = "github.com"
	githubDir          = "git" // stageDir subdir holding git token files + meta
	githubMetaFile     = "github-meta.json"
	githubAPIDir       = "github-api"
	githubAPITokenFile = "token"
	githubAPIMetaFile  = "manifest.json"
	githubRetiredDir   = ".github-retired"
)

// githubTokenMeta is value-free metadata for a minted token. TokenPath is relative to the
// ephemeral stage directory; it is never emitted outside host-side staging/teardown.
type githubTokenMeta struct {
	Owner     string   `json:"owner"`
	Write     bool     `json:"write"`
	TokenPath string   `json:"tokenPath"`
	Repos     []string `json:"repos"`
	ExpiresAt string   `json:"expiresAt"`
}

type githubMeta struct {
	Host         string            `json:"host"`
	Tokens       []githubTokenMeta `json:"tokens"`
	APITokens    []githubTokenMeta `json:"apiTokens,omitempty"`
	Retired      []githubTokenMeta `json:"retired,omitempty"`
	MinExpiresAt string            `json:"minExpiresAt"`
}

// githubAPIMeta is deliberately token-free. With multiple partitions, callers select the named
// file using this manifest instead of receiving an ambiguous token environment variable.
type githubAPIMeta struct {
	Host   string            `json:"host"`
	Tokens []githubTokenMeta `json:"tokens"`
}

type githubPartition struct {
	owner string
	write bool
	repos []string
	link  *userconfig.Account
	key   []byte

	gitPath string
	apiPath string
	gitTok  *githubapp.Token
	apiTok  *githubapp.Token
}

// StageGithub stages GitHub credentials as git-over-HTTPS. App tokens are staged as canonical
// files, so Git's helper can reread them after a host-only renewal. API tokens, when opted in,
// are independently minted and delivered only as non-secret file paths.
func StageGithub(ctx context.Context, creds *policy.Credentials, stageDir string, accounts *userconfig.Accounts) ([]string, error) {
	if creds == nil || creds.Github == nil {
		return nil, nil
	}
	gc := creds.Github
	if gc.Mode == "pat" {
		return stageGitHubPAT(ctx, gc, stageDir)
	}
	repos := gc.Repos
	if len(repos) == 0 {
		rOut, err := runSSHCmd(ctx, []string{"git", "remote", "get-url", "origin"}, "run safeslop from a repo with a github.com origin")
		if err != nil {
			return nil, err
		}
		owner, repo, err := parseOwnerRepo(rOut)
		if err != nil {
			return nil, err
		}
		repos = []policy.RepoCred{{Repo: owner + "/" + repo, Write: gc.Write}}
	}
	return stageGithubAppWithAPI(ctx, repos, stageDir, accounts, githubapp.New(githubapp.NewHTTP(), ""), gc.Api)
}

// stageGithubApp retains the P1 test seam. P2 callers that request API access use the explicit
// sibling so the API capability cannot be accidentally inherited by git-only staging.
func stageGithubApp(ctx context.Context, repos []policy.RepoCred, stageDir string, accounts *userconfig.Accounts, client *githubapp.Client) ([]string, error) {
	return stageGithubAppWithAPI(ctx, repos, stageDir, accounts, client, nil)
}

// stageGithubAppWithAPI mints every git/API partition before replacing any canonical file. The
// replacement files are 0600 siblings in the same directory, then renamed into place. Previous
// tokens are copied to a stage-private retirement directory for teardown-only best-effort revoke;
// renewal itself never calls the revoke endpoint.
func stageGithubAppWithAPI(ctx context.Context, repos []policy.RepoCred, stageDir string, accounts *userconfig.Accounts, client *githubapp.Client, api *policy.GithubApi) ([]string, error) {
	partitions, err := githubPartitions(ctx, repos, accounts)
	if err != nil {
		return nil, err
	}
	for i := range partitions {
		p := &partitions[i]
		perms := map[string]string{"contents": "read", "metadata": "read"}
		if p.write {
			perms["contents"] = "write"
		}
		p.gitTok, err = client.MintToken(ctx, p.link.Github.AppID, p.link.Github.InstallationID, p.key, githubapp.MintRequest{Repositories: p.repos, Permissions: perms})
		if err != nil {
			return nil, err
		}
		if err := validGithubNativeLifetime(p.gitTok.ExpiresAt); err != nil {
			return nil, err
		}
		if api != nil && api.Enabled {
			p.apiTok, err = client.MintToken(ctx, p.link.Github.AppID, p.link.Github.InstallationID, p.key, githubapp.MintRequest{Repositories: p.repos, Permissions: githubAPIPermissions(api.Permissions)})
			if err != nil {
				return nil, err
			}
			if err := validGithubNativeLifetime(p.apiTok.ExpiresAt); err != nil {
				return nil, err
			}
		}
	}

	gitDir := filepath.Join(stageDir, githubDir)
	if err := os.MkdirAll(gitDir, 0o700); err != nil {
		return nil, err
	}
	if api != nil && api.Enabled {
		if err := os.MkdirAll(filepath.Join(stageDir, githubAPIDir), 0o700); err != nil {
			return nil, err
		}
	}

	oldMeta := readGithubMeta(stageDir)
	meta, apiMeta, gitConfig, containerConfig := githubBatchMetadata(stageDir, repos, partitions, oldMeta)
	if err := writeGithubBatchTemps(stageDir, partitions, meta, apiMeta, gitConfig, containerConfig); err != nil {
		removeGithubBatchTemps(stageDir, partitions, api != nil && api.Enabled)
		return nil, err
	}
	if err := retainGithubTokens(stageDir, oldMeta); err != nil {
		removeGithubBatchTemps(stageDir, partitions, api != nil && api.Enabled)
		return nil, err
	}
	if err := commitGithubBatch(stageDir, partitions, api != nil && api.Enabled); err != nil {
		return nil, err
	}
	removeExpiredGithubRetired(stageDir, meta.Retired)

	env := []string{"GIT_CONFIG_GLOBAL=" + filepath.Join(stageDir, ".gitconfig"), "GIT_TERMINAL_PROMPT=0"}
	if api == nil || !api.Enabled {
		return env, nil
	}
	apiDir := filepath.Join(stageDir, githubAPIDir)
	if len(partitions) == 1 {
		return append(env, "SAFESLOP_GITHUB_TOKEN_FILE="+filepath.Join(stageDir, partitions[0].apiPath)), nil
	}
	return append(env,
		"SAFESLOP_GITHUB_TOKEN_DIR="+apiDir,
		"SAFESLOP_GITHUB_TOKEN_MANIFEST="+filepath.Join(apiDir, githubAPIMetaFile),
	), nil
}

func githubPartitions(ctx context.Context, repos []policy.RepoCred, accounts *userconfig.Accounts) ([]githubPartition, error) {
	type ownerGroup struct{ ro, rw []string }
	owners := map[string]*ownerGroup{}
	var ownerOrder []string
	for _, rc := range repos {
		owner, repo, err := splitOwnerRepo(rc.Repo)
		if err != nil {
			return nil, err
		}
		g, ok := owners[owner]
		if !ok {
			g = &ownerGroup{}
			owners[owner] = g
			ownerOrder = append(ownerOrder, owner)
		}
		if rc.Write {
			g.rw = append(g.rw, repo)
		} else {
			g.ro = append(g.ro, repo)
		}
	}
	sort.Strings(ownerOrder)
	var out []githubPartition
	for _, owner := range ownerOrder {
		link := accounts.Lookup(githubHost, owner)
		if link == nil || link.Github == nil {
			return nil, fmt.Errorf("no GitHub account link for %s — run: safeslop creds link github", owner)
		}
		key, err := secrets.Resolve(ctx, link.Github.PrivateKeyRef)
		if err != nil {
			return nil, fmt.Errorf("github: resolve app key for %s: %w", owner, err)
		}
		for _, group := range []struct {
			write bool
			repos []string
		}{{repos: owners[owner].ro}, {write: true, repos: owners[owner].rw}} {
			if len(group.repos) == 0 {
				continue
			}
			name := "token-" + owner
			if group.write {
				name += "-rw"
			}
			out = append(out, githubPartition{owner: owner, write: group.write, repos: group.repos, link: link, key: []byte(key), gitPath: filepath.ToSlash(filepath.Join(githubDir, name))})
		}
	}
	return out, nil
}

func githubAPIPermissions(raw []string) map[string]string {
	out := make(map[string]string, len(raw))
	for _, p := range raw {
		permission, access, _ := strings.Cut(p, ":")
		out[permission] = access
	}
	return out
}

func validGithubNativeLifetime(expiry time.Time) error {
	if expiry.Sub(time.Now()) < leaseMinimumUsableLifetime {
		return fmt.Errorf("github: minted token lifetime is under %s", leaseMinimumUsableLifetime)
	}
	return nil
}

func githubBatchMetadata(stageDir string, repos []policy.RepoCred, partitions []githubPartition, old githubMeta) (githubMeta, githubAPIMeta, string, string) {
	hostTokenPath := make(map[string]string, len(repos))
	ctrTokenPath := make(map[string]string, len(repos))
	meta := githubMeta{Host: githubHost, Retired: retainedGithubMetadata(old)}
	apiMeta := githubAPIMeta{Host: githubHost}
	var minExp time.Time
	for i := range partitions {
		p := &partitions[i]
		if p.apiTok != nil {
			if len(partitions) == 1 {
				p.apiPath = filepath.ToSlash(filepath.Join(githubAPIDir, githubAPITokenFile))
			} else {
				p.apiPath = filepath.ToSlash(filepath.Join(githubAPIDir, filepath.Base(p.gitPath)))
			}
		}
		gitMeta := githubTokenMeta{Owner: p.owner, Write: p.write, TokenPath: p.gitPath, Repos: append([]string(nil), p.repos...), ExpiresAt: p.gitTok.ExpiresAt.UTC().Format(time.RFC3339)}
		meta.Tokens = append(meta.Tokens, gitMeta)
		if minExp.IsZero() || p.gitTok.ExpiresAt.Before(minExp) {
			minExp = p.gitTok.ExpiresAt
		}
		for _, repo := range p.repos {
			ownerRepo := p.owner + "/" + repo
			hostTokenPath[ownerRepo] = filepath.Join(stageDir, filepath.FromSlash(p.gitPath))
			ctrTokenPath[ownerRepo] = "/safeslop/runtime/" + p.gitPath
		}
		if p.apiTok != nil {
			apiToken := githubTokenMeta{Owner: p.owner, Write: p.write, TokenPath: p.apiPath, Repos: append([]string(nil), p.repos...), ExpiresAt: p.apiTok.ExpiresAt.UTC().Format(time.RFC3339)}
			meta.APITokens = append(meta.APITokens, apiToken)
			apiMeta.Tokens = append(apiMeta.Tokens, apiToken)
			if p.apiTok.ExpiresAt.Before(minExp) {
				minExp = p.apiTok.ExpiresAt
			}
		}
	}
	if !minExp.IsZero() {
		meta.MinExpiresAt = minExp.UTC().Format(time.RFC3339)
	}
	return meta, apiMeta,
		renderGitCredsConfig("https://github.com", githubHost, "22", repos, func(or string) string { return hostTokenPath[or] }),
		renderGitCredsConfig("https://github.com", githubHost, "22", repos, func(or string) string { return ctrTokenPath[or] })
}

func writeGithubBatchTemps(stageDir string, partitions []githubPartition, meta githubMeta, apiMeta githubAPIMeta, gitConfig, containerConfig string) error {
	for _, p := range partitions {
		if err := writeGitHubTemp(filepath.Join(stageDir, filepath.FromSlash(p.gitPath)), []byte(p.gitTok.Token)); err != nil {
			return err
		}
		if p.apiTok != nil {
			if err := writeGitHubTemp(filepath.Join(stageDir, filepath.FromSlash(p.apiPath)), []byte(p.apiTok.Token)); err != nil {
				return err
			}
		}
	}
	mb, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	if err := writeGitHubTemp(filepath.Join(stageDir, githubDir, githubMetaFile), mb); err != nil {
		return err
	}
	if len(apiMeta.Tokens) > 0 {
		ab, err := json.MarshalIndent(apiMeta, "", "  ")
		if err != nil {
			return err
		}
		if err := writeGitHubTemp(filepath.Join(stageDir, githubAPIDir, githubAPIMetaFile), ab); err != nil {
			return err
		}
	}
	if err := writeGitHubTemp(filepath.Join(stageDir, ".gitconfig"), []byte(gitConfig)); err != nil {
		return err
	}
	return writeGitHubTemp(filepath.Join(stageDir, ".gitconfig.container"), []byte(containerConfig))
}

func writeGitHubTemp(path string, content []byte) error {
	temp := path + ".new"
	if err := os.WriteFile(temp, content, 0o600); err != nil {
		return err
	}
	return os.Chmod(temp, 0o600)
}

func commitGithubBatch(stageDir string, partitions []githubPartition, hasAPI bool) error {
	for _, p := range partitions {
		if err := renameGitHubTemp(filepath.Join(stageDir, filepath.FromSlash(p.gitPath))); err != nil {
			return err
		}
		if p.apiTok != nil {
			if err := renameGitHubTemp(filepath.Join(stageDir, filepath.FromSlash(p.apiPath))); err != nil {
				return err
			}
		}
	}
	if err := renameGitHubTemp(filepath.Join(stageDir, ".gitconfig")); err != nil {
		return err
	}
	if err := renameGitHubTemp(filepath.Join(stageDir, ".gitconfig.container")); err != nil {
		return err
	}
	if hasAPI {
		if err := renameGitHubTemp(filepath.Join(stageDir, githubAPIDir, githubAPIMetaFile)); err != nil {
			return err
		}
	}
	return renameGitHubTemp(filepath.Join(stageDir, githubDir, githubMetaFile))
}

func renameGitHubTemp(path string) error { return os.Rename(path+".new", path) }

func removeGithubBatchTemps(stageDir string, partitions []githubPartition, hasAPI bool) {
	for _, p := range partitions {
		_ = os.Remove(filepath.Join(stageDir, filepath.FromSlash(p.gitPath)) + ".new")
		if p.apiTok != nil {
			_ = os.Remove(filepath.Join(stageDir, filepath.FromSlash(p.apiPath)) + ".new")
		}
	}
	for _, path := range []string{filepath.Join(stageDir, ".gitconfig") + ".new", filepath.Join(stageDir, ".gitconfig.container") + ".new", filepath.Join(stageDir, githubDir, githubMetaFile) + ".new"} {
		_ = os.Remove(path)
	}
	if hasAPI {
		_ = os.Remove(filepath.Join(stageDir, githubAPIDir, githubAPIMetaFile) + ".new")
	}
}

func readGithubMeta(stageDir string) githubMeta {
	b, err := os.ReadFile(filepath.Join(stageDir, githubDir, githubMetaFile))
	if err != nil {
		return githubMeta{}
	}
	var meta githubMeta
	if json.Unmarshal(b, &meta) != nil {
		return githubMeta{}
	}
	return meta
}

func retainedGithubMetadata(old githubMeta) []githubTokenMeta {
	out := make([]githubTokenMeta, 0, len(old.Retired)+len(old.Tokens)+len(old.APITokens))
	now := time.Now()
	for _, token := range old.Retired {
		exp, err := time.Parse(time.RFC3339, token.ExpiresAt)
		if err == nil && !exp.After(now) {
			continue
		}
		out = append(out, token)
	}
	for _, token := range append(append([]githubTokenMeta(nil), old.Tokens...), old.APITokens...) {
		if token.ExpiresAt == "" {
			continue
		}
		retired := token
		retired.TokenPath = filepath.ToSlash(filepath.Join(githubRetiredDir, githubRetiredName(token)))
		out = append(out, retired)
	}
	return out
}

func githubRetiredName(token githubTokenMeta) string {
	return filepath.Base(token.TokenPath) + "-" + strings.ReplaceAll(token.ExpiresAt, ":", "_")
}

func retainGithubTokens(stageDir string, old githubMeta) error {
	if len(old.Tokens) == 0 && len(old.APITokens) == 0 {
		return nil
	}
	retiredDir := filepath.Join(stageDir, githubRetiredDir)
	if err := os.MkdirAll(retiredDir, 0o700); err != nil {
		return err
	}
	for _, token := range append(append([]githubTokenMeta(nil), old.Tokens...), old.APITokens...) {
		if token.ExpiresAt == "" {
			continue
		}
		contents, err := os.ReadFile(filepath.Join(stageDir, filepath.FromSlash(token.TokenPath)))
		if err != nil {
			continue // a missing old file is already unusable and cannot be retained.
		}
		path := filepath.Join(retiredDir, githubRetiredName(token))
		if err := writeGitHubTemp(path, contents); err != nil {
			return err
		}
		if err := renameGitHubTemp(path); err != nil {
			return err
		}
	}
	return nil
}

func removeExpiredGithubRetired(stageDir string, retired []githubTokenMeta) {
	now := time.Now()
	for _, token := range retired {
		exp, err := time.Parse(time.RFC3339, token.ExpiresAt)
		if err == nil && !exp.After(now) {
			_ = os.Remove(filepath.Join(stageDir, filepath.FromSlash(token.TokenPath)))
		}
	}
}

// RevokeGithub best-effort revokes current and retained App tokens. Renewal never calls it;
// only teardown does, before the stage directory is wiped.
func RevokeGithub(ctx context.Context, stageDir string) {
	revokeGithubWith(ctx, stageDir, githubapp.New(githubapp.NewHTTP(), ""))
}

func revokeGithubWith(ctx context.Context, stageDir string, client *githubapp.Client) {
	meta := readGithubMeta(stageDir)
	seen := map[string]bool{}
	for _, tm := range append(append(append([]githubTokenMeta(nil), meta.Tokens...), meta.APITokens...), meta.Retired...) {
		path := filepath.Join(stageDir, filepath.FromSlash(tm.TokenPath))
		if seen[path] {
			continue
		}
		seen[path] = true
		tb, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if err := client.Revoke(ctx, strings.TrimSpace(string(tb))); err != nil {
			fmt.Fprintf(os.Stderr, "safeslop: github token revoke (%s): %v\n", tm.Owner, err)
		}
	}
	// Retirement files have no token bytes in a manifest and are host-private. Scan them only
	// during teardown so every still-readable prior token receives the same best-effort revoke.
	entries, err := os.ReadDir(filepath.Join(stageDir, githubRetiredDir))
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(stageDir, githubRetiredDir, entry.Name())
		if seen[path] {
			continue
		}
		tb, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if err := client.Revoke(ctx, strings.TrimSpace(string(tb))); err != nil {
			fmt.Fprintf(os.Stderr, "safeslop: github retained token revoke: %v\n", err)
		}
	}
}
