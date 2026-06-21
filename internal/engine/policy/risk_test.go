package policy

import (
	"strings"
	"testing"
)

func TestRiskSummaryHostIsHighAndHonest(t *testing.T) {
	r := RiskSummary(Profile{Agent: "claude", Environment: "host"})
	if r.Level != "high" {
		t.Errorf("host level = %q, want high", r.Level)
	}
	joined := strings.Join(r.Lines, "\n")
	if !strings.Contains(joined, "ENTIRE account") {
		t.Errorf("host risk must name full-account file access:\n%s", joined)
	}
	if !strings.Contains(joined, "no isolation") && !strings.Contains(r.Headline, "no isolation") {
		t.Errorf("host headline must say no isolation: %q", r.Headline)
	}
}

func TestRiskSummaryOpenEgressIsElevated(t *testing.T) {
	r := RiskSummary(Profile{Environment: "container", Network: "allow"})
	if r.Level != "elevated" {
		t.Errorf("container+allow level = %q, want elevated", r.Level)
	}
	if !strings.Contains(strings.Join(r.Lines, " "), "OPEN egress") {
		t.Errorf("open egress must be called out:\n%v", r.Lines)
	}
}

func TestRiskSummaryAllowlistIsContained(t *testing.T) {
	r := RiskSummary(Profile{Environment: "container", Network: "deny"})
	if r.Level != "contained" {
		t.Errorf("container+deny level = %q, want contained", r.Level)
	}
	if !strings.Contains(strings.Join(r.Lines, " "), "allowlist") {
		t.Errorf("allowlist mode must be stated:\n%v", r.Lines)
	}
}

func TestRiskSummaryListsSecretsAndCreds(t *testing.T) {
	r := RiskSummary(Profile{
		Environment: "container", Network: "deny",
		Secrets:     map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "op://x/y/z", "FOO": "env:FOO"},
		Credentials: &Credentials{Ssh: &SshCreds{Write: true}},
	})
	joined := strings.Join(r.Lines, "\n")
	if !strings.Contains(joined, "CLAUDE_CODE_OAUTH_TOKEN") || !strings.Contains(joined, "FOO") {
		t.Errorf("secret env names must be listed (sorted):\n%s", joined)
	}
	// names only — never a value
	if strings.Contains(joined, "op://") || strings.Contains(joined, "env:FOO") {
		t.Errorf("a secret REF/value leaked into the risk summary:\n%s", joined)
	}
	if !strings.Contains(joined, "read-WRITE") {
		t.Errorf("write SSH key must be flagged:\n%s", joined)
	}
}

func TestRiskSummarySandboxDenyIsContainedOffline(t *testing.T) {
	r := RiskSummary(Profile{Environment: "sandbox", Network: "deny"})
	if r.Level != "contained" {
		t.Errorf("sandbox+deny level = %q, want contained", r.Level)
	}
	if !strings.Contains(strings.Join(r.Lines, " "), "offline") {
		t.Errorf("sandbox deny should read offline:\n%v", r.Lines)
	}
}

func axesByName(axes []RiskAxis) map[string]RiskAxis {
	m := map[string]RiskAxis{}
	for _, a := range axes {
		m[a.Name] = a
	}
	return m
}

func TestRiskAxesHostIsAllUnrestricted(t *testing.T) {
	by := axesByName(RiskAxes(Profile{Environment: "host"}))
	if n := by["network"]; n.Restricted || n.Severity != "high" {
		t.Errorf("host network axis = %+v, want unrestricted high", n)
	}
	if f := by["files"]; f.Restricted || f.Severity != "high" {
		t.Errorf("host files axis = %+v, want unrestricted high", f)
	}
}

func TestRiskAxesSandboxDenyIsAllRestricted(t *testing.T) {
	for _, a := range RiskAxes(Profile{Environment: "sandbox", Network: "deny"}) {
		if !a.Restricted {
			t.Errorf("sandbox+deny axis %q=%q should be restricted", a.Name, a.Value)
		}
	}
}

func TestRiskAxesOpenEgressIsLoudButFilesBounded(t *testing.T) {
	by := axesByName(RiskAxes(Profile{Environment: "sandbox", Network: "allow"}))
	if by["network"].Restricted || by["network"].Severity != "elevated" {
		t.Errorf("sandbox+allow network = %+v, want unrestricted elevated", by["network"])
	}
	if !by["files"].Restricted {
		t.Errorf("sandbox files should be bounded: %+v", by["files"])
	}
}

func TestTechStackListsUnderlyingTech(t *testing.T) {
	s := TechStack(Profile{Agent: "claude", Environment: "container", Network: "deny",
		Secrets: map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "op://x/y/z"}})
	j := strings.Join(s, "\n")
	for _, want := range []string{"Claude Code", "Docker container + squid", "squid egress allowlist", "1Password"} {
		if !strings.Contains(j, want) {
			t.Errorf("tech stack missing %q:\n%s", want, j)
		}
	}
	// the provider label "1Password (op://)" is fine; the actual ref/value must NOT appear.
	if strings.Contains(j, "x/y/z") || strings.Contains(j, "CLAUDE_CODE_OAUTH_TOKEN") {
		t.Errorf("secret ref/name leaked into tech stack:\n%s", j)
	}
	// sandbox uses Seatbelt
	if !strings.Contains(strings.Join(TechStack(Profile{Agent: "shell", Environment: "sandbox"}), " "), "Seatbelt") {
		t.Error("sandbox tech stack must mention Seatbelt")
	}
}
