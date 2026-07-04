package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/freakhill/safeslop/internal/engine/creds"
	"github.com/freakhill/safeslop/internal/engine/creds/githubapp"
	"github.com/freakhill/safeslop/internal/engine/secrets"
	"github.com/freakhill/safeslop/internal/engine/userconfig"
)

// The account store (~/.config/safeslop/accounts.cue) holds forge account links: secret refs plus
// non-secret ids ONLY, never a secret value (specs/0069 L1). These verbs manage it. Forge probes go
// through seams — a githubapp.Client and a forgejo base-URL func — so the run* cores test hermetically
// against httptest without touching a real forge or `op` (AGENTS.md).

func accountsPathOrErr() (string, error) { return userconfig.DefaultAccountsPath() }

func cmdCredsLink() *cobra.Command {
	c := &cobra.Command{Use: "link", Short: "Link a forge account (stores secret refs + ids only, never a value)"}
	c.AddCommand(cmdCredsLinkGithub(), cmdCredsLinkForgejo())
	return c
}

func cmdCredsLinkGithub() *cobra.Command {
	var appID, instID int
	var keyRef, host string
	c := &cobra.Command{
		Use:   "github --app-id N --installation-id N --key-ref REF [--host github.com]",
		Short: "Link a GitHub App installation (owner derived from the installation, no token minted)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := accountsPathOrErr()
			if err != nil {
				return err
			}
			out, err := runLinkGithub(cmd.Context(), path, appID, instID, keyRef, host, githubapp.New(githubapp.NewHTTP(), ""))
			if err != nil {
				return err
			}
			fmt.Fprint(cmd.OutOrStdout(), out)
			return nil
		},
	}
	c.Flags().IntVar(&appID, "app-id", 0, "GitHub App id")
	c.Flags().IntVar(&instID, "installation-id", 0, "App installation id")
	c.Flags().StringVar(&keyRef, "key-ref", "", "secret ref for the App private key (e.g. op://vault/item/field)")
	c.Flags().StringVar(&host, "host", "github.com", "forge host")
	return c
}

func cmdCredsLinkForgejo() *cobra.Command {
	var host, owner, tokenRef string
	var sshPort int
	c := &cobra.Command{
		Use:   "forgejo --host H --owner LOGIN --token-ref REF [--ssh-port N]",
		Short: "Link a Forgejo/Gitea account token (owner is explicit; the token is account-wide)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := accountsPathOrErr()
			if err != nil {
				return err
			}
			out, err := runLinkForgejo(cmd.Context(), path, host, owner, tokenRef, sshPort, "https://"+host)
			if err != nil {
				return err
			}
			fmt.Fprint(cmd.OutOrStdout(), out)
			return nil
		},
	}
	c.Flags().StringVar(&host, "host", "", "instance host (e.g. codeberg.org)")
	c.Flags().StringVar(&owner, "owner", "", "account login the token belongs to")
	c.Flags().StringVar(&tokenRef, "token-ref", "", "secret ref for the account token")
	c.Flags().IntVar(&sshPort, "ssh-port", 0, "instance git SSH port (default 22)")
	return c
}

func cmdCredsUnlink() *cobra.Command {
	c := &cobra.Command{
		Use:   "unlink <host>/<owner>",
		Short: "Remove a forge account link",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := accountsPathOrErr()
			if err != nil {
				return err
			}
			out, err := runUnlink(path, args[0])
			if err != nil {
				return err
			}
			fmt.Fprint(cmd.OutOrStdout(), out)
			return nil
		},
	}
	return c
}

func cmdCredsStatus() *cobra.Command {
	var jsonOut bool
	c := &cobra.Command{
		Use:   "status [--json]",
		Short: "Show linked forge accounts with a value-free probe result + TTL model",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := accountsPathOrErr()
			if err != nil {
				return err
			}
			out, err := runCredsStatus(cmd.Context(), path, jsonOut, githubapp.New(githubapp.NewHTTP(), ""), func(host string) string { return "https://" + host })
			if err != nil {
				return err
			}
			fmt.Fprint(cmd.OutOrStdout(), out)
			return nil
		},
	}
	c.Flags().BoolVar(&jsonOut, "json", false, "emit JSON")
	return c
}

// ---- testable cores (no cobra, no default paths) ----

// runLinkGithub probes the installation to derive the owner (no token minted), then upserts the
// link. Only the key *ref* is stored — the resolved key is used for the probe and discarded.
func runLinkGithub(ctx context.Context, accountsPath string, appID, instID int, keyRef, host string, client *githubapp.Client) (string, error) {
	if host == "" {
		host = "github.com"
	}
	if appID <= 0 || instID <= 0 {
		return "", fmt.Errorf("creds link github requires --app-id and --installation-id (both > 0)")
	}
	if keyRef == "" {
		return "", fmt.Errorf("creds link github requires --key-ref (a secret ref, e.g. op://vault/item/field)")
	}
	keyPEM, err := secrets.Resolve(ctx, keyRef)
	if err != nil {
		return "", fmt.Errorf("resolve --key-ref: %w", err)
	}
	inst, err := client.InstallationInfo(ctx, appID, instID, []byte(keyPEM))
	if err != nil {
		return "", err
	}
	owner := inst.AccountLogin()
	if owner == "" {
		return "", fmt.Errorf("github: installation %d reported no account login", instID)
	}
	acc, err := userconfig.LoadAccounts(accountsPath)
	if err != nil {
		return "", err
	}
	acc.Upsert(userconfig.Account{
		Forge: "github", Host: host, Owner: owner,
		Github: &userconfig.GithubAccount{AppID: appID, InstallationID: instID, PrivateKeyRef: keyRef},
	})
	if err := userconfig.SaveAccounts(accountsPath, acc); err != nil {
		return "", err
	}
	return fmt.Sprintf("linked github %s/%s (app %d, installation %d) — key ref stored; the value is never read into config\n", host, owner, appID, instID), nil
}

// runLinkForgejo probes the token (value-free) then upserts the link, storing only the token ref.
func runLinkForgejo(ctx context.Context, accountsPath, host, owner, tokenRef string, sshPort int, probeBase string) (string, error) {
	if host == "" || owner == "" {
		return "", fmt.Errorf("creds link forgejo requires --host and --owner")
	}
	if tokenRef == "" {
		return "", fmt.Errorf("creds link forgejo requires --token-ref (a secret ref)")
	}
	token, err := secrets.Resolve(ctx, tokenRef)
	if err != nil {
		return "", fmt.Errorf("resolve --token-ref: %w", err)
	}
	if err := creds.ProbeForgejo(ctx, probeBase, token); err != nil {
		return "", err
	}
	fj := &userconfig.ForgejoAccount{TokenRef: tokenRef}
	if sshPort != 0 {
		fj.SSHPort = sshPort
	}
	acc, err := userconfig.LoadAccounts(accountsPath)
	if err != nil {
		return "", err
	}
	acc.Upsert(userconfig.Account{Forge: "forgejo", Host: host, Owner: owner, Forgejo: fj})
	if err := userconfig.SaveAccounts(accountsPath, acc); err != nil {
		return "", err
	}
	return fmt.Sprintf("linked forgejo %s/%s — token ref stored (account-wide token; the value is never read into config)\n", host, owner), nil
}

func runUnlink(accountsPath, key string) (string, error) {
	acc, err := userconfig.LoadAccounts(accountsPath)
	if err != nil {
		return "", err
	}
	if !acc.Remove(key) {
		return fmt.Sprintf("no link %q to remove\n", key), nil
	}
	if err := userconfig.SaveAccounts(accountsPath, acc); err != nil {
		return "", err
	}
	return fmt.Sprintf("unlinked %s\n", key), nil
}

// statusRow is one link's value-free status.
type statusRow struct {
	Forge          string `json:"forge"`
	Host           string `json:"host"`
	Owner          string `json:"owner"`
	AppID          int    `json:"appID,omitempty"`
	InstallationID int    `json:"installationID,omitempty"`
	SSHPort        int    `json:"sshPort,omitempty"`
	Probe          string `json:"probe"` // "ok" | error class (never secret bytes)
	TTL            string `json:"ttl"`
}

// runCredsStatus lists every link with a value-free probe result + TTL model. Probe failures are
// captured per-row (never abort the listing).
func runCredsStatus(ctx context.Context, accountsPath string, jsonOut bool, ghClient *githubapp.Client, forgejoBase func(string) string) (string, error) {
	acc, err := userconfig.LoadAccounts(accountsPath)
	if err != nil {
		return "", err
	}
	keys := make([]string, 0, len(acc.Accounts))
	for k := range acc.Accounts {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	rows := make([]statusRow, 0, len(keys))
	for _, k := range keys {
		a := acc.Accounts[k]
		row := statusRow{Forge: a.Forge, Host: a.Host, Owner: a.Owner, Probe: "ok"}
		switch a.Forge {
		case "github":
			if a.Github != nil {
				row.AppID = a.Github.AppID
				row.InstallationID = a.Github.InstallationID
				row.TTL = "1h-renewable"
				if keyPEM, rerr := secrets.Resolve(ctx, a.Github.PrivateKeyRef); rerr != nil {
					row.Probe = "secret-unresolved"
				} else if _, perr := ghClient.InstallationInfo(ctx, a.Github.AppID, a.Github.InstallationID, []byte(keyPEM)); perr != nil {
					row.Probe = probeClass(perr)
				}
			}
		case "forgejo":
			if a.Forgejo != nil {
				row.SSHPort = a.Forgejo.SSHPort
				row.TTL = "account-wide token"
				if token, rerr := secrets.Resolve(ctx, a.Forgejo.TokenRef); rerr != nil {
					row.Probe = "secret-unresolved"
				} else if perr := creds.ProbeForgejo(ctx, forgejoBase(a.Host), token); perr != nil {
					row.Probe = probeClass(perr)
				}
			}
		}
		rows = append(rows, row)
	}

	if jsonOut {
		bs, _ := json.MarshalIndent(struct {
			Links []statusRow `json:"links"`
		}{rows}, "", "  ")
		return string(bs) + "\n", nil
	}
	if len(rows) == 0 {
		return "no forge account links (run: safeslop creds link github|forgejo)\n", nil
	}
	var b strings.Builder
	for _, r := range rows {
		switch r.Forge {
		case "github":
			fmt.Fprintf(&b, "github   %s/%s  app=%d inst=%d  probe=%s  ttl=%s\n", r.Host, r.Owner, r.AppID, r.InstallationID, r.Probe, r.TTL)
		case "forgejo":
			port := ""
			if r.SSHPort != 0 {
				port = fmt.Sprintf(" ssh-port=%d", r.SSHPort)
			}
			fmt.Fprintf(&b, "forgejo  %s/%s%s  probe=%s  ttl=%s\n", r.Host, r.Owner, port, r.Probe, r.TTL)
		}
	}
	return b.String(), nil
}

// probeClass reduces a probe error to a short, value-free class (never secret bytes).
func probeClass(err error) string {
	s := err.Error()
	switch {
	case strings.Contains(s, "transport"):
		return "unreachable"
	case strings.Contains(s, "rejected"), strings.Contains(s, "not found"),
		strings.Contains(s, "401"), strings.Contains(s, "403"), strings.Contains(s, "404"):
		return "denied"
	default:
		return "error"
	}
}
