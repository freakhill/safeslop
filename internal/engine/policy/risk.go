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

// RiskAxis is one capability dimension with its restriction status, so frontends can show what is
// UNRESTRICTED as loudly as what is restricted (ayo S2 — hiding an absence is a dark pattern). Computed
// alongside RiskSummary so callers never re-derive "is this open" (single source of truth).
type RiskAxis struct {
	Name       string // "network" | "files"
	Value      string // short status: "unrestricted" | "open egress" | "whole account" | "workspace-only" | ...
	Restricted bool   // true = bounded; false = unrestricted/open (the loud, amber/red case)
	Severity   string // "high" | "elevated" | "contained" — color only; Value carries the meaning
}

// RiskAxes returns the per-dimension restriction status for a profile — network + files, the two
// dimensions whose "unrestricted" state is the high-impact danger the meta line's positives hide.
// Secrets/credentials stay in RiskSummary.Lines (the break-glass enumeration); these two are the ones
// that need loud surfacing on the compact Launch row.
func RiskAxes(p Profile) []RiskAxis {
	env := p.Environment
	if env == "" {
		env = "sandbox"
	}
	return []RiskAxis{networkAxis(env, p.Network), filesAxis(env)}
}

func networkAxis(env, network string) RiskAxis {
	switch env {
	case "host":
		return RiskAxis{"network", "unrestricted", false, "high"}
	case "container":
		if network == "allow" {
			return RiskAxis{"network", "open egress", false, "elevated"}
		}
		return RiskAxis{"network", "egress-allowlisted", true, "contained"}
	case "vm":
		if network == "allow" {
			return RiskAxis{"network", "full VM network", false, "elevated"}
		}
		return RiskAxis{"network", "proxy-only", true, "contained"}
	default: // sandbox
		if network == "allow" {
			return RiskAxis{"network", "open egress", false, "elevated"}
		}
		return RiskAxis{"network", "offline", true, "contained"}
	}
}

func filesAxis(env string) RiskAxis {
	switch env {
	case "host":
		return RiskAxis{"files", "whole account", false, "high"}
	case "container":
		return RiskAxis{"files", "workspace-only", true, "contained"}
	case "vm":
		return RiskAxis{"files", "VM-only", true, "contained"}
	default: // sandbox
		return RiskAxis{"files", "workspace + temp", true, "contained"}
	}
}

// TechStack lists the underlying technologies a profile uses — the stack behind the tier label —
// for the Launch tab's hover tooltip: which agent, which isolation mechanism (Seatbelt / Docker+squid
// / Tart), the network mechanism, plus any toolchain + credential providers.
func TechStack(p Profile) []string {
	s := []string{
		"Agent: " + agentLabel(p.Agent),
		"Isolation: " + isolationTech(p.Environment),
		"Network: " + networkTech(p.Environment, p.Network),
	}
	if p.Toolchain != nil && p.Toolchain.Kind != "" {
		s = append(s, "Toolchain: "+p.Toolchain.Kind)
	}
	if cl := credLines(p.Credentials); len(cl) > 0 {
		s = append(s, "Credentials: "+strings.Join(cl, ", "))
	}
	if len(p.Secrets) > 0 {
		s = append(s, "Secrets channel: "+secretProviders(p.Secrets))
	}
	return s
}

func agentLabel(a string) string {
	switch a {
	case "claude":
		return "Claude Code"
	case "codex":
		return "Codex"
	case "pi":
		return "Pi"
	case "", "shell":
		return "shell"
	default:
		return a
	}
}

func isolationTech(env string) string {
	switch env {
	case "host":
		return "none — runs directly on macOS"
	case "container":
		return "Docker container + squid egress proxy"
	case "vm":
		return "Tart virtual machine"
	default:
		return "macOS Seatbelt (sandbox-exec)"
	}
}

func networkTech(env, network string) string {
	switch env {
	case "host":
		return "host network (unrestricted)"
	case "container":
		if network == "allow" {
			return "open egress (bridge)"
		}
		return "squid egress allowlist"
	case "vm":
		if network == "allow" {
			return "full VM network"
		}
		return "proxy egress"
	default:
		if network == "allow" {
			return "Seatbelt: network allowed"
		}
		return "Seatbelt: network denied"
	}
}

// secretProviders summarizes the secret backends in use (1Password op:// vs shell env:) without
// revealing any value.
func secretProviders(secrets map[string]string) string {
	op, env := false, false
	for _, ref := range secrets {
		switch {
		case strings.HasPrefix(ref, "op://"):
			op = true
		case strings.HasPrefix(ref, "env:"):
			env = true
		}
	}
	switch {
	case op && env:
		return "1Password + shell env"
	case op:
		return "1Password (op://)"
	case env:
		return "shell env"
	default:
		return "configured"
	}
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
