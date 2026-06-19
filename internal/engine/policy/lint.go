package policy

import "sort"

// Warning is a non-fatal advisory about a dangerous profile combination,
// surfaced by `safeslop validate` and `safeslop run` (never blocks).
type Warning struct {
	Profile string `json:"profile"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Lint reports profiles whose configuration is legal but risky. Today it flags
// the one egress combination with no compensating control: credentials/secrets
// staged under environment:sandbox + network:allow. The Seatbelt boundary has no
// egress topology (it cannot do a per-IP/URL allowlist — that is the container's
// or SP8's job), so a prompt-injected agent can exfiltrate the staged creds.
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
		p := cfg.Profiles[n]
		hasCreds := len(p.Secrets) > 0 || p.Credentials != nil
		if p.Environment == "sandbox" && p.Network == "allow" && hasCreds {
			out = append(out, Warning{
				Profile: n,
				Code:    "sandbox-open-egress-with-creds",
				Message: "stages credentials/secrets under environment:sandbox with network:allow — " +
					"the Seatbelt boundary has no egress filtering, so a compromised agent can exfiltrate them; " +
					"use environment:container/vm, or set network:deny",
			})
		}
		if p.Credentials != nil && p.Credentials.Ssh != nil && p.Credentials.Ssh.Write && p.Network == "allow" {
			out = append(out, Warning{
				Profile: n,
				Code:    "ssh-write-open-egress",
				Message: "a write-capable ssh deploy key with network:allow can be exfiltrated and used off-host — " +
					"set network:deny with a forge-only egress allowlist, or use a read-only key (specs/0011)",
			})
		}
	}
	return out
}
