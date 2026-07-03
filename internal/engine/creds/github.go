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
	githubHost     = "github.com"
	githubDir      = "git" // stageDir subdir holding token files + meta (maps to /safeslop/runtime/git)
	githubMetaFile = "github-meta.json"
)

// githubTokenMeta records one minted installation token, value-free: the owner, its write flag, the
// repos it covers, the staged token *path* (never the token bytes), and the hard expiry. Read by
// RevokeGithub (stop path) and the session TTL status (T8).
type githubTokenMeta struct {
	Owner     string   `json:"owner"`
	Write     bool     `json:"write"`
	TokenPath string   `json:"tokenPath"` // forward-slash, relative to stageDir
	Repos     []string `json:"repos"`     // repo names, owner stripped
	ExpiresAt string   `json:"expiresAt"` // RFC3339
}

// githubMeta is the value-free manifest of a staged App-token set.
type githubMeta struct {
	Host         string            `json:"host"`
	Tokens       []githubTokenMeta `json:"tokens"`
	MinExpiresAt string            `json:"minExpiresAt"` // earliest expiry across tokens (drives the TTL cap)
}

// StageGithub stages GitHub credentials as git-over-HTTPS for the profile's repos. In mode:"pat" it
// stages an existing fine-grained token (unchanged). Otherwise (mode:"app", the default) it mints
// ephemeral, repo-scoped App installation tokens: each owner MUST have an accounts link (specs/0069
// C8) — there is no silent PAT fallback — and repos are partitioned by write so a read-only repo can
// never receive a write token (C4). Returns the non-secret path env; the stageDir wipe destroys the
// token files and RevokeGithub best-effort revokes the live tokens first.
func StageGithub(ctx context.Context, creds *policy.Credentials, stageDir string, accounts *userconfig.Accounts) ([]string, error) {
	if creds == nil || creds.Github == nil {
		return nil, nil
	}
	gc := creds.Github
	if gc.Api != nil && gc.Api.Enabled {
		return nil, fmt.Errorf("forge API staging lands in P2 (specs/0068 F5)")
	}
	if gc.Mode == "pat" {
		return stageGitHubPAT(ctx, gc, stageDir)
	}
	repos := gc.Repos
	if len(repos) == 0 {
		// Preserve the single-repo UX: infer owner/repo from the cwd's github.com origin.
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
	return stageGithubApp(ctx, repos, stageDir, accounts, githubapp.New(githubapp.NewHTTP(), ""))
}

// stageGithubApp is the testable core: mint per (owner, write-partition), stage 0600 token files
// under <stage>/git, render host + container gitconfigs pointing each repo at its partition's token,
// and drop a value-free meta manifest. The client seam keeps it hermetic.
func stageGithubApp(ctx context.Context, repos []policy.RepoCred, stageDir string, accounts *userconfig.Accounts, client *githubapp.Client) ([]string, error) {
	// Group repos by owner, splitting each owner's repos into read-only and read-write partitions.
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

	gitDir := filepath.Join(stageDir, githubDir)
	if err := os.MkdirAll(gitDir, 0o700); err != nil {
		return nil, err
	}

	hostTokenPath := map[string]string{} // "owner/name" -> host token file path
	ctrTokenPath := map[string]string{}  // "owner/name" -> container token file path
	var metaTokens []githubTokenMeta
	var minExp time.Time

	for _, owner := range ownerOrder {
		link := accounts.Lookup(githubHost, owner)
		if link == nil || link.Github == nil {
			return nil, fmt.Errorf("no GitHub account link for %s — run: safeslop creds link github", owner)
		}
		keyPEM, err := secrets.Resolve(ctx, link.Github.PrivateKeyRef)
		if err != nil {
			return nil, fmt.Errorf("github: resolve app key for %s: %w", owner, err)
		}
		g := owners[owner]
		partitions := []struct {
			write bool
			repos []string
		}{}
		if len(g.ro) > 0 {
			partitions = append(partitions, struct {
				write bool
				repos []string
			}{false, g.ro})
		}
		if len(g.rw) > 0 {
			partitions = append(partitions, struct {
				write bool
				repos []string
			}{true, g.rw})
		}
		for _, p := range partitions {
			perms := map[string]string{"contents": "read", "metadata": "read"}
			if p.write {
				perms["contents"] = "write"
			}
			tok, err := client.MintToken(ctx, link.Github.AppID, link.Github.InstallationID, []byte(keyPEM), githubapp.MintRequest{
				Repositories: p.repos,
				Permissions:  perms,
			})
			if err != nil {
				return nil, err
			}
			fileName := "token-" + owner
			if p.write {
				fileName += "-rw"
			}
			if err := os.WriteFile(filepath.Join(gitDir, fileName), []byte(tok.Token), 0o600); err != nil {
				return nil, err
			}
			relPath := githubDir + "/" + fileName
			for _, repo := range p.repos {
				hostTokenPath[owner+"/"+repo] = filepath.Join(gitDir, fileName)
				ctrTokenPath[owner+"/"+repo] = "/safeslop/runtime/" + relPath
			}
			metaTokens = append(metaTokens, githubTokenMeta{
				Owner: owner, Write: p.write, TokenPath: relPath, Repos: p.repos,
				ExpiresAt: tok.ExpiresAt.UTC().Format(time.RFC3339),
			})
			if minExp.IsZero() || tok.ExpiresAt.Before(minExp) {
				minExp = tok.ExpiresAt
			}
		}
	}

	gcPath := filepath.Join(stageDir, ".gitconfig")
	hostCfg := renderGitCredsConfig("https://github.com", githubHost, "22", repos, func(or string) string { return hostTokenPath[or] })
	if err := os.WriteFile(gcPath, []byte(hostCfg), 0o600); err != nil {
		return nil, err
	}
	ctrCfg := renderGitCredsConfig("https://github.com", githubHost, "22", repos, func(or string) string { return ctrTokenPath[or] })
	if err := os.WriteFile(filepath.Join(stageDir, ".gitconfig.container"), []byte(ctrCfg), 0o600); err != nil {
		return nil, err
	}

	meta := githubMeta{Host: githubHost, Tokens: metaTokens}
	if !minExp.IsZero() {
		meta.MinExpiresAt = minExp.UTC().Format(time.RFC3339)
	}
	mb, _ := json.MarshalIndent(meta, "", "  ")
	if err := os.WriteFile(filepath.Join(gitDir, githubMetaFile), mb, 0o600); err != nil {
		return nil, err
	}

	return []string{"GIT_CONFIG_GLOBAL=" + gcPath, "GIT_TERMINAL_PROMPT=0"}, nil
}

// RevokeGithub best-effort revokes every staged App token (reads <stage>/git/github-meta.json for
// the token file paths). Failures are logged, never fatal — the stageDir wipe is the real cleanup
// (L2). PAT-mode stages carry no meta file, so this is a silent no-op for them.
func RevokeGithub(ctx context.Context, stageDir string) {
	revokeGithubWith(ctx, stageDir, githubapp.New(githubapp.NewHTTP(), ""))
}

func revokeGithubWith(ctx context.Context, stageDir string, client *githubapp.Client) {
	b, err := os.ReadFile(filepath.Join(stageDir, githubDir, githubMetaFile))
	if err != nil {
		return
	}
	var meta githubMeta
	if err := json.Unmarshal(b, &meta); err != nil {
		return
	}
	seen := map[string]bool{}
	for _, tm := range meta.Tokens {
		if seen[tm.TokenPath] {
			continue
		}
		seen[tm.TokenPath] = true
		tb, err := os.ReadFile(filepath.Join(stageDir, filepath.FromSlash(tm.TokenPath)))
		if err != nil {
			continue
		}
		if err := client.Revoke(ctx, strings.TrimSpace(string(tb))); err != nil {
			fmt.Fprintf(os.Stderr, "safeslop: github token revoke (%s): %v\n", tm.Owner, err)
		}
	}
}
