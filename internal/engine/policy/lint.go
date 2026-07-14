package policy

import "sort"

// Warning is a non-fatal advisory about a dangerous profile combination,
// surfaced by `safeslop validate` and `safeslop run` (never blocks).
type Warning struct {
	Profile string `json:"profile"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Lint reports profiles whose configuration is legal but risky: a write-capable
// forge credential with open egress, and egress domains set where they are ignored.
func Lint(cfg *Config) []Warning {
	if cfg == nil {
		return nil
	}
	names := make([]string, 0, len(cfg.Profiles))
	for n := range cfg.Profiles {
		names = append(names, n)
	}
	sort.Strings(names)

	var out []Warning
	for _, n := range names {
		facts := normalizeAuthorityFacts(cfg.Profiles[n])
		if facts.compatibilityRuleApplies("github-write-open-egress") {
			message := "a write-capable github credential with network:allow can be exfiltrated and used off-host — " +
				"set network:deny with a forge-only egress allowlist, or use a read-only credential (specs/0011, specs/0069)"
			if facts.Network == authorityNetworkHostUnrestricted {
				message = "a write-capable github credential on the unrestricted host network can be exfiltrated and used off-host — " +
					"use environment:container with network:deny and a forge-only egress allowlist, or use a read-only credential (specs/0011, specs/0069)"
			}
			out = append(out, Warning{Profile: n, Code: "github-write-open-egress", Message: message})
		}
		if facts.compatibilityRuleApplies("forgejo-write-open-egress") {
			message := "a write-capable forgejo credential with network:allow can be exfiltrated and used off-host — " +
				"set network:deny with a forge-only egress allowlist, or use a read-only credential (specs/0047, specs/0069)"
			if facts.Network == authorityNetworkHostUnrestricted {
				message = "a write-capable forgejo credential on the unrestricted host network can be exfiltrated and used off-host — " +
					"use environment:container with network:deny and a forge-only egress allowlist, or use a read-only credential (specs/0047, specs/0069)"
			}
			out = append(out, Warning{Profile: n, Code: "forgejo-write-open-egress", Message: message})
		}
		if facts.compatibilityRuleApplies("egress-ignored") {
			out = append(out, Warning{
				Profile: n,
				Code:    "egress-ignored",
				Message: "sets egress domains but they are ignored — the egress allowlist is honored only on " +
					"environment:container with network:deny (network:allow bypasses it; host is unrestricted)",
			})
		}
	}
	return out
}
