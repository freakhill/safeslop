package creds

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/freakhill/safeslop/internal/engine/policy"
	"github.com/freakhill/safeslop/internal/engine/secrets"
)

const patTokenFile = ".git-pat-token"

// stageGitHubPAT stages one existing fine-grained GitHub token as an HTTPS credential for the
// declared repos. It deliberately avoids putting the token value in .gitconfig or an env var: the
// credential helper reads it from a 0600 file inside stageDir, which is wiped on exit. safeslop does
// not mint or revoke account PATs; rotate/revoke them at the forge.
func stageGitHubPAT(ctx context.Context, sc *policy.SshCreds, stageDir string) ([]string, error) {
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

// stageForgejoPAT is the Forgejo/Gitea PAT sibling of stageGitHubPAT. URL is required because PAT
// mode is repo-list driven (there is no single origin remote to infer the instance from).
func stageForgejoPAT(ctx context.Context, fc *policy.ForgejoCreds, stageDir string) ([]string, error) {
	if fc.URL == "" {
		return nil, fmt.Errorf("forgejo mode:pat requires `url` (the instance base, e.g. https://codeberg.org)")
	}
	if fc.Pat == "" {
		return nil, fmt.Errorf("forgejo mode:pat requires `pat` (an env: or op:// secret ref)")
	}
	if len(fc.Repos) == 0 {
		return nil, fmt.Errorf("forgejo mode:pat requires repos (one fine-grained token staged for explicit repos)")
	}
	host := hostFromURL(fc.URL)
	if host == "" {
		return nil, fmt.Errorf("could not parse host from forgejo url %q", fc.URL)
	}
	port := "22"
	if fc.SSHPort != 0 {
		port = strconv.Itoa(fc.SSHPort)
	}
	token, err := secrets.Resolve(ctx, fc.Pat)
	if err != nil {
		return nil, fmt.Errorf("forgejo pat: %w", err)
	}
	return stageHTTPSPAT(stageDir, strings.TrimRight(fc.URL, "/"), host, port, token, fc.Repos)
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

func renderPATGitConfig(baseURL, hostName, sshPort, tokenPath string, repos []policy.RepoCred) string {
	base := strings.TrimRight(baseURL, "/")
	var b strings.Builder
	b.WriteString("[include]\n\tpath = ~/.gitconfig\n")
	b.WriteString("[credential]\n\tuseHttpPath = true\n")
	for _, rc := range repos {
		owner, repo, err := splitOwnerRepo(rc.Repo)
		if err != nil {
			// Validation happens before this pure renderer in stageHTTPSPAT; skip impossible bad entries here.
			continue
		}
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
