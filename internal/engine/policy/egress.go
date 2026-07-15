package policy

import (
	"net/url"
)

// AgentEgress returns the built-in extra egress allowlist domains a given agent needs
// beyond the shared base allowlist. The base already carries the common providers
// (.anthropic.com + .openrouter.ai) plus the clone/dep-install infra, so only agents
// whose provider reach goes beyond the base return entries here; everything else returns
// nil. These are unioned with the base + any per-profile `egress:` when the container
// allowlist is materialized at launch (specs/0046). A leading dot is a squid subdomain
// suffix match.
//
// pi is BYOK / multi-provider, so it carries the ZDR-clean provider set safeslop allows.
// OpenAI/xAI are a privacy hard line and deliberately absent. Hosts verified against each
// provider's API base URL (2026-06-24): .z.ai is the GLM endpoint (ai-router GLM_BASE_URL);
// .moonshot.ai is the international Kimi endpoint (the .cn endpoint 401s for intl keys).
func AgentEgress(agent string) []string {
	switch agent {
	case "pi":
		return []string{
			".pi.dev",
			".generativelanguage.googleapis.com", // Gemini
			".moonshot.ai",                       // Kimi (international)
			".z.ai",                              // GLM / z.ai
			".deepseek.com",
			".mistral.ai",
			".sakana.ai",
			".exa.ai",
		}
	default:
		return nil
	}
}

// CredsEgress returns the exact HTTPS destinations a profile's staged forge credentials need.
// Host-side mint/renew/revoke traffic never reaches this sandbox allowlist. GitHub git staging needs
// github.com plus clone/LFS CDN hosts; api.github.com is added only when the policy enables App API
// staging. Forgejo deploy-key SSH egress is handled separately; its API hostname is added only after
// policy validation accepted an enabled API declaration with HTTPS/default port 443.
func CredsEgress(prof *Profile) []string {
	if prof == nil || prof.Credentials == nil {
		return nil
	}
	var hosts []string
	if github := prof.Credentials.Github; github != nil {
		hosts = append(hosts, "github.com", "codeload.github.com", "objects.githubusercontent.com")
		if github.Api != nil && github.Api.Enabled {
			hosts = append(hosts, "api.github.com")
		}
	}
	if forgejo := prof.Credentials.Forgejo; forgejo != nil && forgejo.Api != nil && forgejo.Api.Enabled {
		if u, err := url.Parse(forgejo.URL); err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && (u.Port() == "" || u.Port() == "443") {
			hosts = append(hosts, u.Hostname())
		}
	}
	return hosts
}
