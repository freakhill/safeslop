package cli

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/freakhill/safeslop/internal/engine/policy"
	engsession "github.com/freakhill/safeslop/internal/engine/session"
	"github.com/freakhill/safeslop/internal/jsoncontract"
)

var profileCredentialRepoComponentRE = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func cmdProfileCredentials() *cobra.Command {
	c := &cobra.Command{Use: "credentials", Short: "Set or clear forge credentials on a profile"}
	c.AddCommand(cmdProfileCredentialsSet(), cmdProfileCredentialsClear())
	return c
}

func cmdProfileCredentialsSet() *cobra.Command {
	var provider, url, output string
	var useOrigin bool
	var repos, writeRepos []string
	var sshPort int
	c := &cobra.Command{
		Use:   "set <profile> [safeslop.cue] --provider github|forgejo [--use-origin | --repo owner/name ...] --output json",
		Short: "Set GitHub or Forgejo repo credentials for a profile",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(_ *cobra.Command, args []string) error {
			if output != "json" {
				return fmt.Errorf("profile credentials set requires --output json")
			}
			forge, err := buildProfileForgeCredentials(provider, useOrigin, repos, writeRepos, url, sshPort)
			if err != nil {
				return emitContractError(jsoncontract.CodeInvalidArgument, err.Error(), nil)
			}
			path, cfg, err := loadConfigForProfileCredentialMutation(argAt(args, 1))
			if err != nil {
				return emitContractError(jsoncontract.CodeNotFound, "load safeslop.cue", map[string]any{"error": err.Error()})
			}
			name := args[0]
			prof, ok := cfg.Profiles[name]
			if !ok {
				return emitContractError(jsoncontract.CodeNotFound, fmt.Sprintf("no profile %q in safeslop.cue", name), map[string]any{"profile": name, "path": path})
			}
			prof = applyProfileForgeCredentials(prof, forge)
			if err := saveProfileCredentialMutation(path, cfg, name, prof); err != nil {
				return err
			}
			data, err := profileCredentialMutationData(path, name, prof)
			if err != nil {
				return emitContractError(jsoncontract.CodeInvalidArgument, "resolve updated profile", map[string]any{"profile": name, "error": err.Error()})
			}
			emitContract(jsoncontract.OK(data))
			return nil
		},
	}
	c.Flags().StringVar(&provider, "provider", "", "forge provider: github or forgejo")
	c.Flags().BoolVar(&useOrigin, "use-origin", false, "infer the repository from the profile workspace's origin remote at stage time")
	c.Flags().StringArrayVar(&repos, "repo", nil, "read-only owner/name repository (repeatable)")
	c.Flags().StringArrayVar(&writeRepos, "write-repo", nil, "read/write owner/name repository (repeatable)")
	c.Flags().StringVar(&url, "url", "", "Forgejo/Gitea instance URL (required for explicit Forgejo repos)")
	c.Flags().IntVar(&sshPort, "ssh-port", 0, "Forgejo/Gitea SSH port")
	c.Flags().StringVar(&output, "output", "", "output format: json")
	return c
}

func cmdProfileCredentialsClear() *cobra.Command {
	var output string
	c := &cobra.Command{
		Use:   "clear <profile> [safeslop.cue] --output json",
		Short: "Remove GitHub/Forgejo credentials from a profile",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(_ *cobra.Command, args []string) error {
			if output != "json" {
				return fmt.Errorf("profile credentials clear requires --output json")
			}
			path, cfg, err := loadConfigForProfileCredentialMutation(argAt(args, 1))
			if err != nil {
				return emitContractError(jsoncontract.CodeNotFound, "load safeslop.cue", map[string]any{"error": err.Error()})
			}
			name := args[0]
			prof, ok := cfg.Profiles[name]
			if !ok {
				return emitContractError(jsoncontract.CodeNotFound, fmt.Sprintf("no profile %q in safeslop.cue", name), map[string]any{"profile": name, "path": path})
			}
			prof = clearProfileForgeCredentials(prof)
			if err := saveProfileCredentialMutation(path, cfg, name, prof); err != nil {
				return err
			}
			data, err := profileCredentialMutationData(path, name, prof)
			if err != nil {
				return emitContractError(jsoncontract.CodeInvalidArgument, "resolve updated profile", map[string]any{"profile": name, "error": err.Error()})
			}
			emitContract(jsoncontract.OK(data))
			return nil
		},
	}
	c.Flags().StringVar(&output, "output", "", "output format: json")
	return c
}

type profileForgeCredentials struct {
	provider string
	github   *policy.GithubCreds
	forgejo  *policy.ForgejoCreds
}

func buildProfileForgeCredentials(provider string, useOrigin bool, repos, writeRepos []string, url string, sshPort int) (profileForgeCredentials, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider != "github" && provider != "forgejo" {
		return profileForgeCredentials{}, fmt.Errorf("--provider must be github or forgejo")
	}
	if sshPort < 0 {
		return profileForgeCredentials{}, fmt.Errorf("--ssh-port must be positive")
	}
	if provider == "github" && (url != "" || sshPort != 0) {
		return profileForgeCredentials{}, fmt.Errorf("--url and --ssh-port apply only to --provider forgejo")
	}
	if useOrigin && (len(repos) > 0 || len(writeRepos) > 0) {
		return profileForgeCredentials{}, fmt.Errorf("--use-origin cannot be combined with --repo or --write-repo")
	}
	if !useOrigin && len(repos) == 0 && len(writeRepos) == 0 {
		return profileForgeCredentials{}, fmt.Errorf("one of --use-origin, --repo, or --write-repo is required")
	}
	parsed, err := parseProfileCredentialRepos(repos, writeRepos)
	if err != nil {
		return profileForgeCredentials{}, err
	}
	if provider == "forgejo" && len(parsed) > 0 && url == "" {
		return profileForgeCredentials{}, fmt.Errorf("--url is required when setting explicit Forgejo repos")
	}
	switch provider {
	case "github":
		return profileForgeCredentials{provider: provider, github: &policy.GithubCreds{Mode: "app", Repos: parsed}}, nil
	case "forgejo":
		return profileForgeCredentials{provider: provider, forgejo: &policy.ForgejoCreds{URL: url, SSHPort: sshPort, Repos: parsed}}, nil
	default:
		panic("unreachable")
	}
}

func parseProfileCredentialRepos(readRepos, writeRepos []string) ([]policy.RepoCred, error) {
	seen := map[string]bool{}
	out := make([]policy.RepoCred, 0, len(readRepos)+len(writeRepos))
	add := func(repo string, write bool) error {
		if err := validateProfileCredentialRepo(repo); err != nil {
			return err
		}
		if prev, ok := seen[repo]; ok {
			if prev != write {
				return fmt.Errorf("conflicting read/write declarations for repo %s", repo)
			}
			return nil
		}
		seen[repo] = write
		out = append(out, policy.RepoCred{Repo: repo, Write: write})
		return nil
	}
	for _, repo := range readRepos {
		if err := add(repo, false); err != nil {
			return nil, err
		}
	}
	for _, repo := range writeRepos {
		if err := add(repo, true); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func validateProfileCredentialRepo(repo string) error {
	parts := strings.Split(repo, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return fmt.Errorf("repo %q must be owner/repo", repo)
	}
	if !profileCredentialRepoComponentRE.MatchString(parts[0]) || !profileCredentialRepoComponentRE.MatchString(parts[1]) {
		return fmt.Errorf("repo %q must be owner/repo with components matching [A-Za-z0-9._-]+", repo)
	}
	return nil
}

func loadConfigForProfileCredentialMutation(pathArg string) (string, *policy.Config, error) {
	path, err := findConfig(pathArg)
	if err != nil {
		return "", nil, err
	}
	cfg, err := policy.Load(path)
	if err != nil {
		return "", nil, err
	}
	if cfg.Profiles == nil {
		cfg.Profiles = map[string]policy.Profile{}
	}
	return path, cfg, nil
}

func applyProfileForgeCredentials(prof policy.Profile, forge profileForgeCredentials) policy.Profile {
	creds := prof.Credentials
	if creds == nil {
		creds = &policy.Credentials{}
	}
	switch forge.provider {
	case "github":
		creds.Github = forge.github
		creds.Forgejo = nil
	case "forgejo":
		creds.Forgejo = forge.forgejo
		creds.Github = nil
	}
	prof.Credentials = creds
	return prof
}

func clearProfileForgeCredentials(prof policy.Profile) policy.Profile {
	if prof.Credentials == nil {
		return prof
	}
	prof.Credentials.Github = nil
	prof.Credentials.Forgejo = nil
	if profileCredentialsEmpty(prof.Credentials) {
		prof.Credentials = nil
	}
	return prof
}

func profileCredentialsEmpty(c *policy.Credentials) bool {
	return c == nil || (len(c.Pnpm) == 0 && c.Aws == nil && c.Gcp == nil && c.Kube == nil && c.Github == nil && c.Forgejo == nil && c.Pi == nil)
}

func saveProfileCredentialMutation(path string, cfg *policy.Config, name string, prof policy.Profile) error {
	cfg.Profiles[name] = prof
	rendered, err := renderConfigCUE(cfg)
	if err != nil {
		return emitContractError(jsoncontract.CodeInternal, "render safeslop.cue", map[string]any{"error": err.Error()})
	}
	if _, err := policy.LoadBytes(rendered); err != nil {
		return emitContractError(jsoncontract.CodeSchemaViolation, "rendered safeslop.cue did not validate; not writing", map[string]any{"error": err.Error()})
	}
	if err := os.WriteFile(path, rendered, 0o644); err != nil {
		return emitContractError(jsoncontract.CodeIOError, "write safeslop.cue", map[string]any{"path": path, "error": err.Error()})
	}
	return nil
}

func profileCredentialMutationData(path, name string, prof policy.Profile) (map[string]any, error) {
	return map[string]any{
		"path":              path,
		"name":              name,
		"profile":           prof,
		"credential_scopes": credentialScopesOrEmpty(credentialScopesFromProfile(prof)),
	}, nil
}

func credentialScopesOrEmpty(scopes []engsession.CredentialScope) []engsession.CredentialScope {
	if scopes == nil {
		return []engsession.CredentialScope{}
	}
	return scopes
}
