package policy

import "strings"

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
	facts := normalizeAuthorityFacts(p)
	_, note := EnvTier(p.Environment)

	lines := []string{
		"Network: " + networkReach(facts.Network),
		"Files: " + fileReach(facts.Files, facts.Projection == authorityProjectionLiveHostConfig),
	}
	if pl := projectionLinesFromFacts(facts); len(pl) > 0 {
		lines = append(lines, pl...)
	}
	if len(facts.SecretNames) > 0 {
		lines = append(lines, "Secrets injected: "+strings.Join(facts.SecretNames, ", "))
	}
	if creds := credentialLines(facts.CredentialProviders); len(creds) > 0 {
		lines = append(lines, "Credentials: "+strings.Join(creds, ", ")+" (ephemeral, wiped on exit)")
	}
	lines = append(lines, "Tier: "+note)

	return Risk{Headline: headline(facts.Network), Lines: lines, Level: level(facts.Network)}
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
	facts := normalizeAuthorityFacts(p)
	return []RiskAxis{
		networkAxis(facts.Network),
		filesAxis(facts.Files, facts.Projection == authorityProjectionLiveHostConfig),
	}
}

// ProjectionActive reports whether a profile carries an engine-owned read-only host config
// projection that widens file reach beyond the workspace (specs/0096). Host profiles never
// project (host already sees the whole account); only container profiles with an enabled,
// item-bearing projection do.
func ProjectionActive(p Profile) bool {
	return normalizeAuthorityFacts(p).Projection == authorityProjectionLiveHostConfig
}

// projectionLines returns value-free risk lines describing an active projection: what is
// projected (labels/sources), that it is live host filesystem state (not content-pinned by
// the profile hash), and that shell/pi-skill config is readable instruction/code authority the
// agent or shell may execute or use inside the container (specs/0096 ayo lesson #2).
func projectionLines(p Profile) []string {
	return projectionLinesFromFacts(normalizeAuthorityFacts(p))
}

func projectionLinesFromFacts(facts normalizedAuthorityFacts) []string {
	if facts.Projection != authorityProjectionLiveHostConfig {
		return nil
	}
	return []string{
		"Host config projected (read-only, copied into ephemeral home): " + strings.Join(facts.ProjectionLabels, ", "),
		"Projection is live host filesystem state, not pinned by the profile hash; shell/pi-skill config is readable instruction/code authority the agent may execute or use",
	}
}

func networkAxis(reach authorityNetworkReach) RiskAxis {
	switch reach {
	case authorityNetworkHostUnrestricted:
		return RiskAxis{"network", "unrestricted", false, "high"}
	case authorityNetworkContainerOpen:
		return RiskAxis{"network", "open egress", false, "elevated"}
	case authorityNetworkContainerAllowlisted:
		return RiskAxis{"network", "egress-allowlisted", true, "contained"}
	default: // unknown/invalid authority — never imply a boundary (specs/0053)
		return RiskAxis{"network", "unrestricted", false, "high"}
	}
}

func filesAxis(reach authorityFileReach, proj bool) RiskAxis {
	switch reach {
	case authorityFilesHostAccount:
		return RiskAxis{"files", "whole account", false, "high"}
	case authorityFilesWorkspace:
		if proj {
			// Still bounded/restricted: workspace (rw) + a read-only allowlist of host config copied
			// into the ephemeral home. No broad $HOME, no credential dirs (specs/0096).
			return RiskAxis{"files", "workspace + projected host config (read-only)", true, "contained"}
		}
		return RiskAxis{"files", "workspace-only", true, "contained"}
	default: // unknown/invalid authority — never imply a boundary (specs/0053)
		return RiskAxis{"files", "whole account", false, "high"}
	}
}

// TechStack lists the underlying technologies a profile uses — the stack behind the tier label —
// for the Launch tab's hover tooltip: which agent, which isolation mechanism (Docker+squid),
// the network mechanism, plus any toolchain + credential providers.
func TechStack(p Profile) []string {
	facts := normalizeAuthorityFacts(p)
	s := []string{
		"Agent: " + agentLabel(p.Agent),
		"Isolation: " + isolationTech(p.Environment),
		"Network: " + networkTech(p.Environment, p.Network),
	}
	if p.Toolchain != nil && p.Toolchain.Kind != "" {
		s = append(s, "Toolchain: "+p.Toolchain.Kind)
	}
	if cl := credentialLines(facts.CredentialProviders); len(cl) > 0 {
		s = append(s, "Credentials: "+strings.Join(cl, ", "))
	}
	if len(p.Secrets) > 0 {
		s = append(s, "Secrets channel: "+secretProviders(p.Secrets))
	}
	if facts.Projection == authorityProjectionLiveHostConfig {
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

func networkReach(reach authorityNetworkReach) string {
	switch reach {
	case authorityNetworkHostUnrestricted:
		return "unrestricted — uses your full host network"
	case authorityNetworkContainerOpen:
		return "OPEN egress — can reach the entire internet"
	case authorityNetworkContainerAllowlisted:
		return "egress-allowlisted — only approved domains (github, npm, pypi, anthropic, …)"
	default: // unknown/invalid authority (specs/0053)
		return "unrestricted — assume the agent can reach anywhere"
	}
}

func fileReach(reach authorityFileReach, proj bool) string {
	switch reach {
	case authorityFilesHostAccount:
		return "your ENTIRE account — home, ~/.ssh, ~/.aws, every file you can touch"
	case authorityFilesWorkspace:
		if proj {
			return "the mounted workspace (read-write) plus a read-only allowlist of host config copied into the ephemeral home — no broad $HOME, no credential dirs"
		}
		return "only the mounted workspace — no host files"
	default: // unknown/invalid authority (specs/0053)
		return "unknown environment — assume your ENTIRE account is reachable"
	}
}

func credLines(c *Credentials) []string {
	_, providers := normalizeCredentialAuthority(c)
	return credentialLines(providers)
}

func credentialLines(providers []credentialProviderAuthority) []string {
	var out []string
	for _, provider := range providers {
		switch provider.Provider {
		case CredentialProviderPnpm:
			out = append(out, "npm/pnpm registry token")
		case CredentialProviderAWS:
			out = append(out, "AWS (short-lived)")
		case CredentialProviderGCP:
			out = append(out, "GCP access token")
		case CredentialProviderKube:
			out = append(out, "kubeconfig")
		case CredentialProviderGitHub:
			out = append(out, "GitHub token ("+legacyForgeAccess(provider)+")")
		case CredentialProviderForgejo:
			out = append(out, "Forgejo deploy key ("+legacyForgeAccess(provider)+")")
		}
	}
	return out
}

func legacyForgeAccess(provider credentialProviderAuthority) string {
	if len(provider.WriteScopeIDs) > 0 {
		return "read-WRITE"
	}
	return "read-only"
}

func headline(reach authorityNetworkReach) string {
	switch reach {
	case authorityNetworkHostUnrestricted:
		return "Runs as you — no isolation, full account + network"
	case authorityNetworkContainerOpen:
		return "File-isolated, but OPEN egress (can exfil)"
	case authorityNetworkContainerAllowlisted:
		return "Workspace-only files, egress limited to the allowlist"
	default: // unknown/invalid authority (specs/0053)
		return "Unknown environment — assume no isolation"
	}
}

func level(reach authorityNetworkReach) string {
	switch reach {
	case authorityNetworkHostUnrestricted:
		return "high"
	case authorityNetworkContainerOpen:
		return "elevated"
	case authorityNetworkContainerAllowlisted:
		return "contained"
	default:
		return "high"
	}
}
