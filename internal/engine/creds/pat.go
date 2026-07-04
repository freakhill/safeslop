package creds

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/freakhill/safeslop/internal/engine/policy"
	"github.com/freakhill/safeslop/internal/engine/secrets"
)

const patTokenFile = ".git-pat-token"

// stageGitHubPAT stages one existing fine-grained GitHub token as an HTTPS credential for the
// declared repos. It deliberately avoids putting the token value in .gitconfig or an env var: the
// credential helper reads it from a 0600 file inside stageDir, which is wiped on exit. safeslop does
// not mint or revoke account PATs; rotate/revoke them at the forge.
func stageGitHubPAT(ctx context.Context, sc *policy.GithubCreds, stageDir string) ([]string, error) {
	if sc.Pat == "" {
		return nil, fmt.Errorf("github ssh mode:pat requires `pat` (an env: or op:// secret ref)")
	}
	if len(sc.Repos) == 0 {
		return nil, fmt.Errorf("github ssh mode:pat requires repos (one fine-grained token staged for explicit repos)")
	}
	token, err := secrets.Resolve(ctx, sc.Pat)
	if err != nil {
		return nil, fmt.Errorf("github pat: %w", err)
	}
	return stageHTTPSPAT(stageDir, "https://github.com", "github.com", "22", token, sc.Repos)
}

func stageHTTPSPAT(stageDir, baseURL, hostName, sshPort, token string, repos []policy.RepoCred) ([]string, error) {
	for _, rc := range repos {
		if _, _, err := splitOwnerRepo(rc.Repo); err != nil {
			return nil, err
		}
	}
	if err := os.MkdirAll(stageDir, 0o700); err != nil {
		return nil, err
	}
	tokenPath := filepath.Join(stageDir, patTokenFile)
	if err := os.WriteFile(tokenPath, []byte(token), 0o600); err != nil {
		return nil, err
	}
	gcPath := filepath.Join(stageDir, ".gitconfig")
	if err := os.WriteFile(gcPath, []byte(renderPATGitConfig(baseURL, hostName, sshPort, tokenPath, repos)), 0o600); err != nil {
		return nil, err
	}
	containerTokenPath := "/safeslop/runtime/" + patTokenFile
	if err := os.WriteFile(filepath.Join(stageDir, ".gitconfig.container"), []byte(renderPATGitConfig(baseURL, hostName, sshPort, containerTokenPath, repos)), 0o600); err != nil {
		return nil, err
	}
	return []string{"GIT_CONFIG_GLOBAL=" + gcPath, "GIT_TERMINAL_PROMPT=0"}, nil
}

// renderPATGitConfig is the single-token specialization of renderGitCredsConfig: every repo reads
// the same staged token file (PAT mode). Behavior is unchanged from before the T4a generalization.
func renderPATGitConfig(baseURL, hostName, sshPort, tokenPath string, repos []policy.RepoCred) string {
	return renderGitCredsConfig(baseURL, hostName, sshPort, repos, func(string) string { return tokenPath })
}

// renderGitCredsConfig renders a git-over-HTTPS credential config for repos, pointing each repo at
// its own token file via tokenPathFor("owner/name"). Per-URL credential helpers `cat` the token at
// credential time (renewal-transparent by construction); useHttpPath keeps helpers repo-scoped;
// ssh->HTTPS insteadOf rewrites let agents keep git@ remotes. Generalized in specs/0069 T4a so App
// mode can point different repos at different owner/partition tokens; PAT mode passes one path.
func renderGitCredsConfig(baseURL, hostName, sshPort string, repos []policy.RepoCred, tokenPathFor func(string) string) string {
	base := strings.TrimRight(baseURL, "/")
	var b strings.Builder
	b.WriteString("[include]\n\tpath = ~/.gitconfig\n")
	b.WriteString("[credential]\n\tuseHttpPath = true\n")
	for _, rc := range repos {
		owner, repo, err := splitOwnerRepo(rc.Repo)
		if err != nil {
			// Validation happens before this pure renderer; skip impossible bad entries here.
			continue
		}
		tokenPath := tokenPathFor(rc.Repo)
		httpsURL := base + "/" + owner + "/" + repo + ".git"
		writeCredentialHelper(&b, httpsURL, tokenPath)
		writeCredentialHelper(&b, base+"/"+owner+"/"+repo, tokenPath)
		fmt.Fprintf(&b, "[url %q]\n", httpsURL)
		fmt.Fprintf(&b, "\tinsteadOf = git@%s:%s/%s.git\n", hostName, owner, repo)
		fmt.Fprintf(&b, "\tinsteadOf = ssh://git@%s/%s/%s.git\n", hostName, owner, repo)
		if sshPort != "" && sshPort != "22" {
			fmt.Fprintf(&b, "\tinsteadOf = ssh://git@%s:%s/%s/%s.git\n", hostName, sshPort, owner, repo)
		}
	}
	return b.String()
}

func writeCredentialHelper(b *strings.Builder, httpsURL, tokenPath string) {
	if u, err := url.Parse(httpsURL); err != nil || u.Scheme == "" || u.Host == "" {
		return
	}
	fmt.Fprintf(b, "[credential %q]\n", httpsURL)
	// Git credential helpers print protocol/host/username/password lines. The token is read at
	// credential time from tokenPath, keeping it out of command args, env, logs, and gitconfig.
	fmt.Fprintf(b, "\thelper = \"!f() { echo username=x-access-token; printf 'password='; cat '%s'; echo; }; f\"\n", escapeGitConfigSingleQuoted(tokenPath))
}

func escapeGitConfigSingleQuoted(s string) string {
	// Paths from stageDir should not contain single quotes in normal use, but shell-quote defensively.
	return strings.ReplaceAll(s, "'", "'\\''")
}
