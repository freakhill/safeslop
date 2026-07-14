package policy

import (
	"encoding/json"
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
		Credentials: &Credentials{Github: &GithubCreds{Write: true}},
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

func TestRiskSummaryUsesEffectiveForgeAccessAndValueFreeLines(t *testing.T) {
	cases := []struct {
		name        string
		credentials *Credentials
		want        string
	}{
		{
			name: "github repository write",
			credentials: &Credentials{Github: &GithubCreds{Repos: []RepoCred{
				{Repo: "private-owner/read"},
				{Repo: "private-owner/write", Write: true},
			}}},
			want: "GitHub token (read-WRITE)",
		},
		{
			name:        "forgejo read only",
			credentials: &Credentials{Forgejo: &ForgejoCreds{URL: "https://private-forge.example.com", Repos: []RepoCred{{Repo: "private-owner/read"}}}},
			want:        "Forgejo deploy key (read-only)",
		},
		{
			name:        "forgejo repository write",
			credentials: &Credentials{Forgejo: &ForgejoCreds{URL: "https://private-forge.example.com", Repos: []RepoCred{{Repo: "private-owner/write", Write: true}}}},
			want:        "Forgejo deploy key (read-WRITE)",
		},
		{
			name:        "forgejo provider write",
			credentials: &Credentials{Forgejo: &ForgejoCreds{Write: true, URL: "https://private-forge.example.com", Repos: []RepoCred{{Repo: "private-owner/read"}}}},
			want:        "Forgejo deploy key (read-WRITE)",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			joined := strings.Join(RiskSummary(Profile{
				Environment: "container",
				Network:     "deny",
				Credentials: tc.credentials,
			}).Lines, "\n")
			if !strings.Contains(joined, tc.want) {
				t.Fatalf("risk summary missing effective credential access %q:\n%s", tc.want, joined)
			}
			for _, private := range []string{"private-owner", "private-forge.example.com"} {
				if strings.Contains(joined, private) {
					t.Errorf("legacy credential line leaked private target %q:\n%s", private, joined)
				}
			}
		})
	}
}

func TestRiskLegacyValidHeadlineLevelMappings(t *testing.T) {
	cases := []struct {
		name     string
		profile  Profile
		headline string
		level    string
	}{
		{"host", Profile{Environment: "host", Network: "deny"}, "Runs as you — no isolation, full account + network", "high"},
		{"container open", Profile{Environment: "container", Network: "allow"}, "File-isolated, but OPEN egress (can exfil)", "elevated"},
		{"container deny", Profile{Environment: "container", Network: "deny"}, "Workspace-only files, egress limited to the allowlist", "contained"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := RiskSummary(tc.profile)
			if got.Headline != tc.headline || got.Level != tc.level {
				t.Errorf("risk headline/level = %q/%q, want %q/%q", got.Headline, got.Level, tc.headline, tc.level)
			}
		})
	}
}

func TestRiskSummaryPreservesLegacyLineOrder(t *testing.T) {
	lines := RiskSummary(Profile{
		Environment: "container",
		Network:     "deny",
		Projection: &Projection{Enabled: true, Items: []ProjectionItem{
			{Label: "pi-agent"},
		}},
		Secrets:     map[string]string{"TOKEN": "env:TOKEN"},
		Credentials: &Credentials{Github: &GithubCreds{}},
	}).Lines
	wantPrefixes := []string{
		"Network: ",
		"Files: ",
		"Host config projected ",
		"Projection is live ",
		"Secrets injected: ",
		"Credentials: ",
		"Tier: ",
	}
	if len(lines) != len(wantPrefixes) {
		t.Fatalf("risk lines = %v, want %d legacy rows", lines, len(wantPrefixes))
	}
	for i, prefix := range wantPrefixes {
		if !strings.HasPrefix(lines[i], prefix) {
			t.Errorf("risk line %d = %q, want prefix %q", i, lines[i], prefix)
		}
	}
}

func TestRiskLegacyJSONShapeAndAxisCardinality(t *testing.T) {
	profile := Profile{Environment: "container", Network: "deny"}
	assertKeys := func(label string, value any, want map[string]bool) {
		t.Helper()
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal %s: %v", label, err)
		}
		var got map[string]json.RawMessage
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatalf("unmarshal %s: %v", label, err)
		}
		if len(got) != len(want) {
			t.Fatalf("%s keys = %v, want exactly %v", label, got, want)
		}
		for key := range want {
			if _, ok := got[key]; !ok {
				t.Errorf("%s omitted compatibility key %q: %s", label, key, data)
			}
		}
	}

	assertKeys("risk", RiskSummary(profile), map[string]bool{
		"headline": true, "lines": true, "level": true,
	})
	axes := RiskAxes(profile)
	if len(axes) != 2 || axes[0].Name != "network" || axes[1].Name != "files" {
		t.Fatalf("risk axes = %+v, want exactly network then files", axes)
	}
	for i, axis := range axes {
		assertKeys("risk axis", axis, map[string]bool{
			"name": true, "value": true, "restricted": true, "severity": true,
		})
		if axis.Name == "" {
			t.Errorf("risk axis %d has no name", i)
		}
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

func TestRiskAxesContainerDenyIsAllRestricted(t *testing.T) {
	for _, a := range RiskAxes(Profile{Environment: "container", Network: "deny"}) {
		if !a.Restricted {
			t.Errorf("container+deny axis %q=%q should be restricted", a.Name, a.Value)
		}
	}
}

func TestRiskAxesOpenEgressIsLoudButFilesBounded(t *testing.T) {
	by := axesByName(RiskAxes(Profile{Environment: "container", Network: "allow"}))
	if by["network"].Restricted || by["network"].Severity != "elevated" {
		t.Errorf("container+allow network = %+v, want unrestricted elevated", by["network"])
	}
	if !by["files"].Restricted {
		t.Errorf("container files should be bounded: %+v", by["files"])
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
}

// TestAgentLabelAndTechStackPi locks specs/0045: the pi agent has a human label and surfaces
// in the tech-stack summary.
func TestAgentLabelAndTechStackPi(t *testing.T) {
	if got := agentLabel("pi"); got != "Pi" {
		t.Errorf("agentLabel(pi) = %q, want Pi", got)
	}
	s := strings.Join(TechStack(Profile{Agent: "pi", Environment: "host"}), "\n")
	if !strings.Contains(s, "Agent: Pi") {
		t.Errorf("tech stack must label the pi agent:\n%s", s)
	}
}

func TestRiskSummaryProjectionSurfacesHostConfig(t *testing.T) {
	// specs/0096: an active projection must surface as readable host config authority in the
	// break-glass lines, and call out that it is live host state (not hash-pinned) plus that
	// shell/pi-skill config is instruction/code the agent may execute.
	r := RiskSummary(Profile{
		Agent: "pi", Environment: "container", Network: "deny",
		Projection: &Projection{Enabled: true, Items: []ProjectionItem{
			{Source: "~/.pi/agent/AGENTS.md", Label: "pi-agent"},
			{Source: "~/.config/fish/config.fish", Label: "fish"},
		}},
	})
	joined := strings.Join(r.Lines, "\n")
	for _, want := range []string{"projected", "pi-agent", "fish", "live host filesystem state", "instruction/code authority"} {
		if !strings.Contains(joined, want) {
			t.Errorf("risk summary missing %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "ENTIRE account") {
		t.Errorf("projected container must not claim whole-account file reach:\n%s", joined)
	}
}

func TestRiskAxesProjectionKeepsFilesRestricted(t *testing.T) {
	// Projection widens file reach but stays bounded (allowlisted, read-only): the files axis
	// must remain restricted=contained, with a value that names the projected config.
	by := axesByName(RiskAxes(Profile{
		Environment: "container", Network: "deny",
		Projection: &Projection{Enabled: true, Items: []ProjectionItem{{Source: "~/.zshrc"}}},
	}))
	f := by["files"]
	if !f.Restricted || f.Severity != "contained" {
		t.Errorf("projected container files axis = %+v, want restricted/contained", f)
	}
	if !strings.Contains(f.Value, "projected") {
		t.Errorf("files axis value must mention projected config: %q", f.Value)
	}
}

func TestProjectionActiveRequiresContainerAndItems(t *testing.T) {
	// Host profiles never project (host already sees the whole account); a projection with no
	// items or disabled is inert. Only container+enabled+items is active (specs/0096).
	cases := []struct {
		env  string
		proj *Projection
		want bool
	}{
		{"host", &Projection{Enabled: true, Items: []ProjectionItem{{Source: "x"}}}, false},
		{"container", &Projection{Enabled: true}, false},
		{"container", &Projection{Items: []ProjectionItem{{Source: "x"}}}, false},
		{"container", &Projection{Enabled: true, Items: []ProjectionItem{{Source: "x"}}}, true},
		{"container", nil, false},
	}
	for _, c := range cases {
		got := ProjectionActive(Profile{Environment: c.env, Projection: c.proj})
		if got != c.want {
			t.Errorf("ProjectionActive(env=%q, proj=%+v) = %v, want %v", c.env, c.proj, got, c.want)
		}
	}
}

func TestRiskAxesContainerDenyWithoutProjectionUnchanged(t *testing.T) {
	// Regression guard: a plain container+deny profile (no projection) keeps the original
	// "workspace-only" files axis — projection is strictly additive (specs/0096).
	by := axesByName(RiskAxes(Profile{Environment: "container", Network: "deny"}))
	f := by["files"]
	if f.Value != "workspace-only" || !f.Restricted {
		t.Errorf("plain container files axis drifted: %+v", f)
	}
}
