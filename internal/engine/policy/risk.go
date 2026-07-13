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
	Headline string   `json:"headline"`
	Lines    []string `json:"lines"`
	Level    string   `json:"level"` // "high" | "elevated" | "contained"
}

// RiskSummary computes the break-glass capability summary for a profile. It states what the agent
// CAN do if compromised (network reach, file reach, secrets, credentials) plus the honest tier
// caveat — concrete consequences, so the user confronts the actual blast radius.
func RiskSummary(p Profile) Risk {
	env := p.Environment
	_, note := EnvTier(env)
	proj := ProjectionActive(p)

	lines := []string{
		"Network: " + networkReach(env, p.Network),
		"Files: " + fileReach(env, proj),
	}
	if pl := projectionLines(p); len(pl) > 0 {
		lines = append(lines, pl...)
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
	Name       string `json:"name"`       // "network" | "files"
	Value      string `json:"value"`      // short status: "unrestricted" | "open egress" | "whole account" | "workspace-only" | ...
	Restricted bool   `json:"restricted"` // true = bounded; false = unrestricted/open (the loud, amber/red case)
	Severity   string `json:"severity"`   // "high" | "elevated" | "contained" — color only; Value carries the meaning
}

// RiskAxes returns the per-dimension restriction status for a profile — network + files, the two
// dimensions whose "unrestricted" state is the high-impact danger the meta line's positives hide.
// Secrets/credentials stay in RiskSummary.Lines (the break-glass enumeration); these two are the ones
// that need loud surfacing on the compact Launch row.
func RiskAxes(p Profile) []RiskAxis {
	env := p.Environment
	return []RiskAxis{networkAxis(env, p.Network), filesAxis(env, ProjectionActive(p))}
}

// ProjectionActive reports whether a profile carries an engine-owned read-only host config
// projection that widens file reach beyond the workspace (specs/0096). Host profiles never
// project (host already sees the whole account); only container profiles with an enabled,
// item-bearing projection do.
func ProjectionActive(p Profile) bool {
	return p.Environment == "container" && p.Projection != nil && p.Projection.Enabled && len(p.Projection.Items) > 0
}

// projectionLines returns value-free risk lines describing an active projection: what is
// projected (labels/sources), that it is live host filesystem state (not content-pinned by
// the profile hash), and that shell/pi-skill config is readable instruction/code authority the
// agent or shell may execute or use inside the container (specs/0096 ayo lesson #2).
func projectionLines(p Profile) []string {
	if !ProjectionActive(p) {
		return nil
	}
	names := make([]string, 0, len(p.Projection.Items))
	for _, it := range p.Projection.Items {
		if it.Label != "" {
			names = append(names, it.Label)
		} else {
			names = append(names, it.Source)
		}
	}
	sort.Strings(names)
	return []string{
		"Host config projected (read-only, copied into ephemeral home): " + strings.Join(names, ", "),
		"Projection is live host filesystem state, not pinned by the profile hash; shell/pi-skill config is readable instruction/code authority the agent may execute or use",
	}
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
	default: // unknown/invalid env — never imply a boundary (specs/0053)
		return RiskAxis{"network", "unrestricted", false, "high"}
	}
}

func filesAxis(env string, proj bool) RiskAxis {
	switch env {
	case "host":
		return RiskAxis{"files", "whole account", false, "high"}
	case "container":
		if proj {
			// Still bounded/restricted: workspace (rw) + a read-only allowlist of host config copied
			// into the ephemeral home. No broad $HOME, no credential dirs (specs/0096).
			return RiskAxis{"files", "workspace + projected host config (read-only)", true, "contained"}
		}
		return RiskAxis{"files", "workspace-only", true, "contained"}
	default: // unknown/invalid env — never imply a boundary (specs/0053)
		return RiskAxis{"files", "whole account", false, "high"}
	}
}

// TechStack lists the underlying technologies a profile uses — the stack behind the tier label —
// for the Launch tab's hover tooltip: which agent, which isolation mechanism (Docker+squid),
// the network mechanism, plus any toolchain + credential providers.
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
	if ProjectionActive(p) {
		// Surface the projection set by label so an operator sees the readable host config
		// authority the agent gains (specs/0096 ayo lesson #2/10).
		s = append(s, "Projection: read-only host config copied into ephemeral home")
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
	default:
		return "none — unknown environment"
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
	default:
		return "unrestricted"
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
	default: // unknown/invalid env (specs/0053)
		return "unrestricted — assume the agent can reach anywhere"
	}
}

func fileReach(env string, proj bool) string {
	switch env {
	case "host":
		return "your ENTIRE account — home, ~/.ssh, ~/.aws, every file you can touch"
	case "container":
		if proj {
			return "the mounted workspace (read-write) plus a read-only allowlist of host config copied into the ephemeral home — no broad $HOME, no credential dirs"
		}
		return "only the mounted workspace — no host files"
	default: // unknown/invalid env (specs/0053)
		return "unknown environment — assume your ENTIRE account is reachable"
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
	if c.Github != nil {
		w := "read-only"
		if c.Github.Write {
			w = "read-WRITE"
		}
		out = append(out, "GitHub token ("+w+")")
	}
	return out
}

func headline(env, network string) string {
	switch {
	case env == "host":
		return "Runs as you — no isolation, full account + network"
	case network == "allow":
		return "File-isolated, but OPEN egress (can exfil)"
	case env == "container":
		return "Workspace-only files, egress limited to the allowlist"
	default: // unknown/invalid env (specs/0053)
		return "Unknown environment — assume no isolation"
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
