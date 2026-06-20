package policy

import (
	"sort"
	"strings"
)

// Risk is the safety arbiter's view of a profile, derived from the compiled policy + EnvTier — the
// "show safety as concrete consequence, never a score or tier name" principle (specs/0029, the second
// load-bearing cross-model finding). Lines are break-glass sentences the user reads before trusting;
// Headline is a one-liner for the Launch row; Level is a coarse band used only for color.
type Risk struct {
	Headline string
	Lines    []string
	Level    string // "high" | "elevated" | "contained"
}

// RiskSummary computes the break-glass capability summary for a profile. It states what the agent
// CAN do if compromised (network reach, file reach, secrets, credentials) plus the honest tier
// caveat — concrete consequences, so the user confronts the actual blast radius.
func RiskSummary(p Profile) Risk {
	env := p.Environment
	if env == "" {
		env = "sandbox"
	}
	_, note := EnvTier(env)

	lines := []string{
		"Network: " + networkReach(env, p.Network),
		"Files: " + fileReach(env),
	}
	if len(p.Secrets) > 0 {
		names := make([]string, 0, len(p.Secrets))
		for k := range p.Secrets {
			names = append(names, k)
		}
		sort.Strings(names)
		lines = append(lines, "Secrets injected: "+strings.Join(names, ", "))
	}
	if creds := credLines(p.Credentials); len(creds) > 0 {
		lines = append(lines, "Credentials: "+strings.Join(creds, ", ")+" (ephemeral, wiped on exit)")
	}
	lines = append(lines, "Tier: "+note)

	return Risk{Headline: headline(env, p.Network), Lines: lines, Level: level(env, p.Network)}
}

func networkReach(env, network string) string {
	switch env {
	case "host":
		return "unrestricted — uses your full host network"
	case "container":
		if network == "allow" {
			return "OPEN egress — can reach the entire internet"
		}
		return "egress-allowlisted — only approved domains (github, npm, pypi, anthropic, …)"
	case "vm":
		if network == "allow" {
			return "full VM network"
		}
		return "egress via the configured proxy only"
	default: // sandbox
		if network == "allow" {
			return "OPEN — outbound connections to anywhere"
		}
		return "denied — offline"
	}
}

func fileReach(env string) string {
	switch env {
	case "host":
		return "your ENTIRE account — home, ~/.ssh, ~/.aws, every file you can touch"
	case "container":
		return "only the mounted workspace — no host files"
	case "vm":
		return "only what's copied into the disposable VM — no host files"
	default: // sandbox
		return "reads system + toolchain; reads/writes only the workspace + temp dirs"
	}
}

func credLines(c *Credentials) []string {
	if c == nil {
		return nil
	}
	var out []string
	if len(c.Pnpm) > 0 {
		out = append(out, "npm/pnpm registry token")
	}
	if c.Aws != nil {
		out = append(out, "AWS (short-lived)")
	}
	if c.Gcp != nil {
		out = append(out, "GCP access token")
	}
	if c.Kube != nil {
		out = append(out, "kubeconfig")
	}
	if c.Ssh != nil {
		w := "read-only"
		if c.Ssh.Write {
			w = "read-WRITE"
		}
		out = append(out, "SSH deploy key ("+w+")")
	}
	return out
}

func headline(env, network string) string {
	switch {
	case env == "host":
		return "Runs as you — no isolation, full account + network"
	case network == "allow" && env == "sandbox":
		return "Workspace-confined files, but OPEN network (can exfil)"
	case network == "allow":
		return "File-isolated, but OPEN egress (can exfil)"
	case env == "container":
		return "Workspace-only files, egress limited to the allowlist"
	case env == "vm":
		return "Disposable VM — strongest isolation"
	default: // sandbox deny
		return "Workspace-confined files, offline"
	}
}

func level(env, network string) string {
	switch {
	case env == "host":
		return "high"
	case network == "allow":
		return "elevated"
	default:
		return "contained"
	}
}
