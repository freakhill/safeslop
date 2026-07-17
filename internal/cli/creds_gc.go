package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/freakhill/safeslop/internal/engine/creds"
	"github.com/freakhill/safeslop/internal/engine/secrets"
	"github.com/freakhill/safeslop/internal/engine/userconfig"
	"github.com/freakhill/safeslop/internal/jsoncontract"
)

var forgejoGCComponentRE = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// One item per page is intentionally conservative: instances may set a lower maximum response
// size, while Forgejo documents page/limit pagination for REST list responses.
const forgejoGCPageSize = 1
const forgejoGCMaxPages = 10000

type forgejoGCResult struct {
	Host       string `json:"host"`
	Repository string `json:"repository"`
	Title      string `json:"title"`
	Action     string `json:"action"`
	Count      int    `json:"count"`
	ErrorClass string `json:"error_class,omitempty"`
}

type forgejoGCReport struct {
	Results []forgejoGCResult `json:"results"`
}

type forgejoGCKey struct {
	ID    json.Number `json:"id"`
	Title string      `json:"title"`
}

type forgejoGCTarget struct {
	owner, repo, token string
}

// cmdCredsGC is deliberately a cleanup escape hatch, not a general deploy-key deleter. It only
// considers keys whose title is exactly the title safeslop itself creates, and --yes is required
// before it sends a DELETE request.
func cmdCredsGC() *cobra.Command {
	return cmdCredsGCWithDeps(defaultDependencies())
}

func cmdCredsGCWithDeps(d *dependencies) *cobra.Command {
	var host string
	var repos []string
	var yes bool
	var output string
	c := &cobra.Command{
		Use:   "gc --host HOST --repo OWNER/REPO [--repo OWNER/REPO ...] [--dry-run|--yes] [--output json]",
		Short: "Garbage-collect exact-title Forgejo deploy keys for explicitly named repositories",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if host == "" {
				return fmt.Errorf("creds gc requires --host")
			}
			if len(repos) == 0 {
				return fmt.Errorf("creds gc requires at least one --repo")
			}
			if yes && cmd.Flags().Changed("dry-run") {
				return fmt.Errorf("creds gc --yes and --dry-run are mutually exclusive")
			}
			if output != "" && output != "json" {
				return fmt.Errorf("creds gc --output must be json")
			}
			accountsPath, err := accountsPathOrErr()
			if err != nil {
				return err
			}
			report, runErr := runCredsGCWithClient(cmd.Context(), accountsPath, host, repos, yes, !yes, d.forgejoGCBaseForHost, d.newForgejoGCClient())
			out, formatErr := formatCredsGCOutput(report, output)
			if formatErr != nil {
				return formatErr
			}
			fmt.Fprint(cmd.OutOrStdout(), out)
			return runErr
		},
	}
	c.Flags().StringVar(&host, "host", "", "Forgejo instance host")
	c.Flags().StringSliceVar(&repos, "repo", nil, "exact owner/repository to inspect (repeatable)")
	c.Flags().Bool("dry-run", false, "inspect matching keys without deleting (default unless --yes)")
	c.Flags().BoolVar(&yes, "yes", false, "delete matching keys after rechecking each one")
	c.Flags().StringVar(&output, "output", "", "output format: json")
	return c
}

// runCredsGC discovers every requested repository before any deletion. Each candidate is then
// listed again and must retain its exact expected title before deletion, so a concurrent key change
// cannot expand the requested scope. The report contains only value-free operator metadata.
func runCredsGC(ctx context.Context, accountsPath, host string, repos []string, confirmed, dryRun bool, baseForHost func(string) string) (forgejoGCReport, error) {
	return runCredsGCWithClient(ctx, accountsPath, host, repos, confirmed, dryRun, baseForHost, creds.NewForgejoHTTP())
}

func runCredsGCWithClient(ctx context.Context, accountsPath, host string, repos []string, confirmed, dryRun bool, baseForHost func(string) string, client creds.ForgejoHTTP) (forgejoGCReport, error) {
	report := forgejoGCReport{Results: []forgejoGCResult{}}
	if confirmed == dryRun {
		return report, fmt.Errorf("creds gc requires exactly one of dry-run or confirmed deletion")
	}
	if !forgejoGCComponentRE.MatchString(host) {
		return report, fmt.Errorf("invalid Forgejo host")
	}
	targets, err := forgejoGCTargets(ctx, accountsPath, host, repos)
	if err != nil {
		return report, err
	}
	base := strings.TrimRight(baseForHost(host), "/")
	if base == "" {
		return report, fmt.Errorf("invalid Forgejo API base")
	}
	type candidate struct {
		target forgejoGCTarget
		key    forgejoGCKey
		result int
	}
	var candidates []candidate
	discoveryFailed := false
	for _, target := range targets {
		title := forgejoGCDeployKeyTitle(target.owner, target.repo)
		keys, status, class := listForgejoGCKeys(ctx, client, base, target)
		if status == http.StatusNotFound {
			report.Results = append(report.Results, forgejoGCResult{Host: host, Repository: target.owner + "/" + target.repo, Title: title, Action: "absent", Count: 0})
			continue
		}
		if class != "" {
			report.Results = append(report.Results, forgejoGCResult{Host: host, Repository: target.owner + "/" + target.repo, Title: title, Action: "error", Count: 0, ErrorClass: class})
			discoveryFailed = true
			continue
		}
		matched := false
		for _, key := range keys {
			if key.Title != title {
				continue
			}
			matched = true
			idx := len(report.Results)
			report.Results = append(report.Results, forgejoGCResult{Host: host, Repository: target.owner + "/" + target.repo, Title: title, Action: "dry-run", Count: 1})
			candidates = append(candidates, candidate{target: target, key: key, result: idx})
		}
		if !matched {
			report.Results = append(report.Results, forgejoGCResult{Host: host, Repository: target.owner + "/" + target.repo, Title: title, Action: "dry-run", Count: 0})
		}
	}
	if discoveryFailed {
		return report, fmt.Errorf("Forgejo deploy-key discovery failed")
	}
	if dryRun {
		return report, nil
	}

	deleteFailed := false
	for _, item := range candidates {
		keys, status, class := listForgejoGCKeys(ctx, client, base, item.target)
		if status == http.StatusNotFound {
			report.Results[item.result].Action, report.Results[item.result].Count = "absent", 0
			continue
		}
		if class != "" {
			report.Results[item.result].Action, report.Results[item.result].Count, report.Results[item.result].ErrorClass = "error", 0, class
			deleteFailed = true
			continue
		}
		if !hasExactForgejoGCKey(keys, item.key.ID, item.key.Title) {
			report.Results[item.result].Action, report.Results[item.result].Count = "absent", 0
			continue
		}
		_, status, err := client.Do(ctx, http.MethodDelete, forgejoGCKeyURL(base, item.target.owner, item.target.repo, item.key.ID.String()), item.target.token, nil)
		switch {
		case err != nil:
			report.Results[item.result].Action, report.Results[item.result].Count, report.Results[item.result].ErrorClass = "error", 0, "transport"
			deleteFailed = true
		case status == http.StatusNotFound:
			report.Results[item.result].Action, report.Results[item.result].Count = "absent", 0
		case status < 200 || status >= 300:
			report.Results[item.result].Action, report.Results[item.result].Count, report.Results[item.result].ErrorClass = "error", 0, "http"
			deleteFailed = true
		default:
			report.Results[item.result].Action, report.Results[item.result].Count = "deleted", 1
		}
	}
	if deleteFailed {
		return report, fmt.Errorf("Forgejo deploy-key garbage collection had failures")
	}
	return report, nil
}

func forgejoGCTargets(ctx context.Context, accountsPath, host string, repos []string) ([]forgejoGCTarget, error) {
	if len(repos) == 0 {
		return nil, fmt.Errorf("creds gc requires at least one --repo")
	}
	accounts, err := userconfig.LoadAccounts(accountsPath)
	if err != nil {
		return nil, fmt.Errorf("load Forgejo account links")
	}
	seen := map[string]bool{}
	tokens := map[string]string{}
	targets := make([]forgejoGCTarget, 0, len(repos))
	for _, raw := range repos {
		parts := strings.Split(raw, "/")
		if len(parts) != 2 || !forgejoGCComponentRE.MatchString(parts[0]) || !forgejoGCComponentRE.MatchString(parts[1]) {
			return nil, fmt.Errorf("invalid repository")
		}
		key := parts[0] + "/" + parts[1]
		if seen[key] {
			continue
		}
		seen[key] = true
		token, ok := tokens[parts[0]]
		if !ok {
			link := accounts.Lookup(host, parts[0])
			if link == nil || link.Forge != "forgejo" || link.Forgejo == nil {
				return nil, fmt.Errorf("no Forgejo account link for %s/%s", host, parts[0])
			}
			token, err = secrets.Resolve(ctx, link.Forgejo.TokenRef)
			if err != nil {
				return nil, fmt.Errorf("resolve Forgejo account token for %s/%s", host, parts[0])
			}
			tokens[parts[0]] = token
		}
		targets = append(targets, forgejoGCTarget{owner: parts[0], repo: parts[1], token: token})
	}
	return targets, nil
}

func forgejoGCDeployKeyTitle(owner, repo string) string { return "safeslop-" + owner + "-" + repo }

func forgejoGCKeysURL(base, owner, repo string, page int) string {
	query := url.Values{"limit": []string{fmt.Sprint(forgejoGCPageSize)}, "page": []string{fmt.Sprint(page)}}
	return base + "/api/v1/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/keys?" + query.Encode()
}

func forgejoGCKeyURL(base, owner, repo, id string) string {
	return base + "/api/v1/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/keys/" + url.PathEscape(id)
}

func listForgejoGCKeys(ctx context.Context, client creds.ForgejoHTTP, base string, target forgejoGCTarget) ([]forgejoGCKey, int, string) {
	keys := make([]forgejoGCKey, 0)
	seen := map[json.Number]bool{}
	for page := 1; page <= forgejoGCMaxPages; page++ {
		body, status, err := client.Do(ctx, http.MethodGet, forgejoGCKeysURL(base, target.owner, target.repo, page), target.token, nil)
		if err != nil {
			return nil, status, "transport"
		}
		if status == http.StatusNotFound {
			return nil, status, ""
		}
		if status < 200 || status >= 300 {
			return nil, status, "http"
		}
		var pageKeys []forgejoGCKey
		if err := json.Unmarshal(body, &pageKeys); err != nil {
			return nil, status, "invalid-response"
		}
		if len(pageKeys) == 0 {
			return keys, status, ""
		}
		for _, key := range pageKeys {
			if key.Title != "" && (key.ID.String() == "" || key.ID.String() == "0") {
				return nil, status, "invalid-response"
			}
			if !seen[key.ID] {
				seen[key.ID] = true
				keys = append(keys, key)
			}
		}
	}
	// Never delete from a list whose pagination did not terminate: the conservative failure
	// avoids treating a forge that ignores page parameters as a complete discovery.
	return nil, 0, "pagination"
}

func hasExactForgejoGCKey(keys []forgejoGCKey, id json.Number, title string) bool {
	for _, key := range keys {
		if key.ID == id && key.Title == title {
			return true
		}
	}
	return false
}

func formatCredsGCOutput(report forgejoGCReport, output string) (string, error) {
	if output == "json" {
		body, err := jsoncontract.Marshal(jsoncontract.OK(map[string]any{"results": report.Results}))
		return string(body), err
	}
	var b strings.Builder
	for _, result := range report.Results {
		fmt.Fprintf(&b, "%s %s %s action=%s count=%d", result.Host, result.Repository, result.Title, result.Action, result.Count)
		if result.ErrorClass != "" {
			fmt.Fprintf(&b, " error_class=%s", result.ErrorClass)
		}
		b.WriteByte('\n')
	}
	return b.String(), nil
}
