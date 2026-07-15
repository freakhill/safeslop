package policy

import (
	"encoding/json"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func TestEvaluationJSONContractUsesArraysAndExplicitNulls(t *testing.T) {
	e := Evaluation{
		SchemaVersion: EvaluationSchemaVersion,
		Authority: EvaluateAuthority(Profile{
			Environment: "container",
			Network:     "deny",
		}),
	}
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	var wire map[string]any
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if got := wire["schema_version"]; got != float64(1) {
		t.Fatalf("schema_version = %#v, want 1", got)
	}
	for _, section := range []string{"authority", "trust", "readiness"} {
		if _, ok := wire[section].(map[string]any); !ok {
			t.Fatalf("%s = %#v, want an emitted object", section, wire[section])
		}
	}
	authority := wire["authority"].(map[string]any)
	if _, ok := authority["findings"].([]any); !ok {
		t.Fatalf("authority.findings = %#v, want an array", authority["findings"])
	}
	if got, ok := authority["credential_scopes"].([]any); !ok || got == nil {
		t.Fatalf("authority.credential_scopes = %#v, want [] rather than null", authority["credential_scopes"])
	}
	for _, section := range []string{"trust", "readiness"} {
		got := wire[section].(map[string]any)
		if findings, ok := got["findings"].([]any); !ok || findings == nil {
			t.Fatalf("%s.findings = %#v, want [] rather than null", section, got["findings"])
		}
		if got["checked_at"] != nil {
			t.Fatalf("%s.checked_at = %#v, want explicit null", section, got["checked_at"])
		}
	}
	for _, raw := range authority["findings"].([]any) {
		finding := raw.(map[string]any)
		if scopeIDs, ok := finding["scope_ids"].([]any); !ok || scopeIDs == nil {
			t.Fatalf("finding.scope_ids = %#v, want [] rather than null", finding["scope_ids"])
		}
		if _, ok := finding["remediation"]; !ok {
			t.Fatalf("finding omits remediation: %#v", finding)
		}
	}
}

func TestEvaluateAuthorityCoreAxesAndDeterministicOrdering(t *testing.T) {
	p := Profile{
		Environment: "container",
		Network:     "deny",
		Egress:      []string{"z.example.com", ".a.example.com"},
	}
	first := EvaluateAuthority(p)
	firstJSON, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 25; i++ {
		got, err := json.Marshal(EvaluateAuthority(p))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(firstJSON) {
			t.Fatalf("EvaluateAuthority is not deterministic:\nfirst %s\n  got %s", firstJSON, got)
		}
	}

	wantAxes := []string{
		FindingAxisNetwork,
		FindingAxisFiles,
		FindingAxisProjection,
		FindingAxisSecrets,
		FindingAxisCredentials,
	}
	var gotAxes []string
	seen := map[string]bool{}
	for _, finding := range first.Findings {
		if !seen[finding.Axis] {
			seen[finding.Axis] = true
			gotAxes = append(gotAxes, finding.Axis)
		}
	}
	if !reflect.DeepEqual(gotAxes, wantAxes) {
		t.Fatalf("axis order = %v, want %v; findings=%+v", gotAxes, wantAxes, first.Findings)
	}
	for _, axis := range wantAxes {
		if !seen[axis] {
			t.Errorf("core authority axis %q is absent", axis)
		}
	}

	network := findAuthority(first, "authority.network.container-allowlist")
	if network == nil {
		t.Fatal("missing bounded container allowlist finding")
	}
	if network.Outcome != FindingOutcomeBounded || network.Severity != FindingSeverityInfo {
		t.Errorf("allowlist disposition = %s/%s, want bounded/info", network.Outcome, network.Severity)
	}
	if !strings.Contains(network.Consequence, ".a.example.com, z.example.com") || !strings.Contains(network.Consequence, "exfiltration") {
		t.Errorf("allowlist consequence must name sorted possible exfiltration destinations: %q", network.Consequence)
	}
	for _, id := range []string{
		"authority.files.workspace",
		"authority.projection.absent",
		"authority.secrets.absent",
		"authority.credentials.absent",
	} {
		f := findAuthority(first, id)
		if f == nil || (f.Outcome != FindingOutcomeBounded && f.Outcome != FindingOutcomeNotApplicable) {
			t.Errorf("bounded/absent row %q missing or greenwashed: %+v", id, f)
		}
	}
	if err := ValidateAuthorityEvaluation(first); err != nil {
		t.Fatalf("engine produced invalid authority evaluation: %v", err)
	}
}

func TestEvaluateAuthorityHostAndUnknownAreLoud(t *testing.T) {
	host := EvaluateAuthority(Profile{Environment: "host", Network: "deny"})
	for _, id := range []string{"authority.network.host-unrestricted", "authority.files.host-account"} {
		f := findAuthority(host, id)
		if f == nil {
			t.Fatalf("missing host finding %q", id)
		}
		if f.Outcome != FindingOutcomeConcern || f.Severity != FindingSeverityCritical || f.Remediation == nil {
			t.Errorf("host finding %q = %+v, want concern/critical with remediation", id, f)
		}
	}

	unknown := EvaluateAuthority(Profile{Environment: "future-boundary", Network: "future-network"})
	for _, axis := range []string{FindingAxisNetwork, FindingAxisFiles, FindingAxisProjection} {
		f := firstFindingOnAxis(unknown, axis)
		if f == nil {
			t.Fatalf("unknown profile omitted %q axis", axis)
		}
		if f.Outcome != FindingOutcomeUnknown || f.Remediation == nil {
			t.Errorf("unknown %q finding = %+v, want loud unknown with remediation", axis, f)
		}
		if f.Outcome == FindingOutcomePass || f.Outcome == FindingOutcomeBounded {
			t.Errorf("unknown %q authority was rendered green: %+v", axis, f)
		}
	}

	unknownNetwork := EvaluateAuthority(Profile{Environment: "container", Network: "future-network"})
	if f := firstFindingOnAxis(unknownNetwork, FindingAxisNetwork); f == nil || f.Outcome != FindingOutcomeUnknown {
		t.Fatalf("unknown container network = %+v, want unknown", f)
	}
}

func TestEvaluateAuthorityProjectionSecretsAndIgnoredEgress(t *testing.T) {
	p := Profile{
		Environment: "container",
		Network:     "allow",
		Egress:      []string{"ignored.example.com"},
		Secrets: map[string]string{
			"DO_NOT_EMIT_NAME": "op://private-vault/private-item/private-field",
		},
		Projection: &Projection{Enabled: true, Items: []ProjectionItem{
			{Source: "/Users/private/.config/pi", Target: "/home/agent/.pi", Label: "DO_NOT_EMIT_LABEL"},
		}},
	}
	a := EvaluateAuthority(p)

	projection := findAuthority(a, "authority.projection.live-host-config")
	if projection == nil || projection.Outcome != FindingOutcomeConcern {
		t.Fatalf("live projection finding = %+v, want concern", projection)
	}
	for _, text := range []string{"live host", "readable", "instruction"} {
		if !strings.Contains(strings.ToLower(projection.Consequence), text) {
			t.Errorf("projection consequence %q must contain %q", projection.Consequence, text)
		}
	}
	secrets := findAuthority(a, "authority.secrets.injected")
	if secrets == nil || secrets.Outcome != FindingOutcomeConcern || !strings.Contains(secrets.Consequence, "1 secret") {
		t.Fatalf("secret finding = %+v, want a count-only concern", secrets)
	}
	ignored := findAuthority(a, "egress-ignored")
	if ignored == nil || ignored.Outcome != FindingOutcomeConcern || !strings.Contains(ignored.Consequence, "does not constrain") {
		t.Fatalf("ignored egress finding = %+v", ignored)
	}

	wire, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"DO_NOT_EMIT_NAME", "DO_NOT_EMIT_LABEL", "op://", "/Users/private", "/home/agent"} {
		if strings.Contains(string(wire), forbidden) {
			t.Errorf("authority JSON leaked forbidden material %q:\n%s", forbidden, wire)
		}
	}
}

func TestCredentialScopeAllProvidersAndEffectiveWrite(t *testing.T) {
	p := Profile{
		Environment: "container",
		Network:     "allow",
		Credentials: &Credentials{
			Pnpm: []PnpmRegistry{{Host: "registry.npmjs.org", Token: "op://vault/npm/token"}},
			Aws: &AwsSso{
				Profile:       "dev-admin",
				RoleArn:       "arn:aws:iam::123456789012:role/build",
				SessionPolicy: `{"Statement":[{"Resource":"arn:private:DO_NOT_EMIT_POLICY"}]}`,
			},
			Gcp:  &GcpAdc{Scopes: []string{"scope.z", "scope.a"}},
			Kube: &KubeCluster{Gke: &GkeCluster{Name: "production", Location: "europe-west1", Project: "acme"}},
			Github: &GithubCreds{Repos: []RepoCred{
				{Repo: "acme/read"},
				{Repo: "acme/write", Write: true},
			}},
			Forgejo: &ForgejoCreds{Write: true, URL: "https://forge.example.com", Repos: []RepoCred{
				{Repo: "acme/read-by-declaration"},
				{Repo: "acme/write", Write: true},
			}},
		},
	}
	a := EvaluateAuthority(p)
	if err := ValidateAuthorityEvaluation(a); err != nil {
		t.Fatalf("authority validation: %v", err)
	}

	providers := map[string]bool{}
	byProviderTarget := map[string]CredentialScope{}
	scopeIDPattern := regexp.MustCompile(`^credential\.(pnpm|aws|gcp|kube|github|forgejo)\.[0-9]{3}$`)
	for _, scope := range a.CredentialScopes {
		providers[scope.Provider] = true
		byProviderTarget[scope.Provider+"/"+scope.Target] = scope
		if !scopeIDPattern.MatchString(scope.ScopeID) {
			t.Errorf("scope ID is not an engine-only symbol: %q", scope.ScopeID)
		}
		if strings.Contains(scope.ScopeID, scope.Target) {
			t.Errorf("scope ID %q embeds user target %q", scope.ScopeID, scope.Target)
		}
	}
	for _, provider := range []string{
		CredentialProviderPnpm,
		CredentialProviderAWS,
		CredentialProviderGCP,
		CredentialProviderKube,
		CredentialProviderGitHub,
		CredentialProviderForgejo,
	} {
		if !providers[provider] {
			t.Errorf("credential provider %q has no value-free scope", provider)
		}
		if findAuthority(a, "authority.credentials."+provider) == nil {
			t.Errorf("credential provider %q has no authority finding", provider)
		}
	}

	assertScopeAccess(t, byProviderTarget, "github/acme/read", CredentialAccessReadOnly)
	assertScopeAccess(t, byProviderTarget, "github/acme/write", CredentialAccessReadWrite)
	// Provider-level write applies to every Forgejo repository, even when RepoCred.Write is false.
	assertScopeAccess(t, byProviderTarget, "forgejo/acme/read-by-declaration", CredentialAccessReadWrite)
	assertScopeAccess(t, byProviderTarget, "forgejo/acme/write", CredentialAccessReadWrite)
	if got := byProviderTarget["github/acme/read"].Lifetime; got != CredentialLifetimeShortLived {
		t.Errorf("GitHub app lifetime = %q, want short_lived", got)
	}
	if got := byProviderTarget["aws/role arn:aws:iam::123456789012:role/build"].Access; got != CredentialAccessExternalPolicy {
		t.Errorf("AWS access = %q, want external_policy", got)
	}
	if _, ok := byProviderTarget["gcp/scope.a"]; !ok {
		t.Errorf("missing declared GCP scope; scopes=%+v", a.CredentialScopes)
	}

	for _, comboID := range []string{"github-write-open-egress", "forgejo-write-open-egress"} {
		combo := findAuthority(a, comboID)
		if combo == nil || combo.Outcome != FindingOutcomeConcern || combo.Severity != FindingSeverityCritical {
			t.Errorf("write/open combination %q = %+v, want concern/critical", comboID, combo)
		}
		if combo != nil && len(combo.ScopeIDs) == 0 {
			t.Errorf("write/open combination %q does not link its credential scopes", comboID)
		}
	}

	wire, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"op://vault/npm/token", "DO_NOT_EMIT_POLICY"} {
		if strings.Contains(string(wire), forbidden) {
			t.Errorf("credential evaluation leaked %q:\n%s", forbidden, wire)
		}
	}
}

func TestCredentialScopeAPIDisclosureIsScopeAccurate(t *testing.T) {
	a := EvaluateAuthority(Profile{
		Environment: "container",
		Network:     "deny",
		Credentials: &Credentials{
			Github:  &GithubCreds{Api: &GithubApi{Enabled: true, Permissions: []string{"contents:read"}}},
			Forgejo: &ForgejoCreds{Api: &ForgejoApi{Enabled: true, AckAccountWide: true}},
		},
	})
	wire, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	text := string(wire)
	for _, want := range []string{"contents:read", "repository and permission downscoped", "operator-provisioned scope unverified", "may be account-wide"} {
		if !strings.Contains(text, want) {
			t.Errorf("API authority disclosure missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "op://") || strings.Contains(text, "token-ref") {
		t.Fatalf("API authority disclosure leaked secret material: %s", text)
	}
}

func TestCredentialScopeProviderWriteAndPersistentPAT(t *testing.T) {
	a := EvaluateAuthority(Profile{
		Environment: "host",
		Network:     "deny",
		Credentials: &Credentials{Github: &GithubCreds{
			Mode:  "pat",
			Pat:   "env:DO_NOT_EMIT_PAT",
			Write: true,
			Repos: []RepoCred{{Repo: "acme/one"}, {Repo: "acme/two"}},
		}},
	})
	for _, scope := range a.CredentialScopes {
		if scope.Provider != CredentialProviderGitHub {
			continue
		}
		if scope.Access != CredentialAccessReadWrite {
			t.Errorf("provider write did not apply to %q: %+v", scope.Target, scope)
		}
		if scope.Lifetime != CredentialLifetimePersistent {
			t.Errorf("PAT scope %q lifetime = %q, want persistent", scope.Target, scope.Lifetime)
		}
	}
	if findAuthority(a, "github-write-open-egress") == nil {
		t.Error("host network is unrestricted, so write credentials need the combination finding")
	}
	wire, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wire), "DO_NOT_EMIT_PAT") || strings.Contains(string(wire), "env:") {
		t.Errorf("PAT ref leaked: %s", wire)
	}
}

func TestCredentialScopeMalformedMetadataIsUnknownAndValueFree(t *testing.T) {
	a := EvaluateAuthority(Profile{
		Environment: "container",
		Network:     "deny",
		Credentials: &Credentials{
			Pnpm:   []PnpmRegistry{{Host: "/Users/private/.npmrc", Token: "env:TOKEN"}},
			Gcp:    &GcpAdc{Scopes: []string{"../../private-scope", ".config/private-scope"}},
			Github: &GithubCreds{Repos: []RepoCred{{Repo: "op://vault/item/field", Write: true}}},
		},
	})
	for _, provider := range []string{CredentialProviderPnpm, CredentialProviderGCP, CredentialProviderGitHub} {
		finding := findAuthority(a, "authority.credentials."+provider)
		if finding == nil || finding.Outcome != FindingOutcomeUnknown || finding.Remediation == nil {
			t.Errorf("malformed %s metadata = %+v, want remediable unknown", provider, finding)
		}
	}
	wire, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"/Users/private", "../../private-scope", ".config/private-scope", "op://", "env:TOKEN"} {
		if strings.Contains(string(wire), forbidden) {
			t.Errorf("malformed metadata leaked %q: %s", forbidden, wire)
		}
	}

	future := EvaluateAuthority(Profile{
		Environment: "container",
		Network:     "deny",
		Credentials: &Credentials{Github: &GithubCreds{
			Mode: "future-mode", Repos: []RepoCred{{Repo: "acme/read"}},
		}},
	})
	if got := future.CredentialScopes[0].Access; got != CredentialAccessUnknown {
		t.Errorf("future credential mode access = %q, want explicit unknown rather than read_only", got)
	}
}

func TestFindingRegistryEnforcesLaws(t *testing.T) {
	if err := ValidateFindingRegistry(); err != nil {
		t.Fatalf("built-in finding registry is invalid: %v", err)
	}

	duplicate := append([]authorityRule(nil), authorityRuleRegistry...)
	duplicate = append(duplicate, authorityRuleRegistry[0])
	if err := validateAuthorityRuleRegistry(duplicate); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate registry ID was accepted: %v", err)
	}

	base := EvaluateAuthority(Profile{Environment: "host", Network: "deny"})
	cases := []struct {
		name   string
		mutate func(*AuthorityEvaluation)
	}{
		{
			name: "unregistered ID",
			mutate: func(a *AuthorityEvaluation) {
				a.Findings[0].RuleID = "authority.network.not-registered"
			},
		},
		{
			name: "invalid enum combination",
			mutate: func(a *AuthorityEvaluation) {
				a.Findings[0].Severity = FindingSeverityInfo
			},
		},
		{
			name: "required remediation",
			mutate: func(a *AuthorityEvaluation) {
				a.Findings[0].Remediation = nil
			},
		},
		{
			name: "forbidden finding material",
			mutate: func(a *AuthorityEvaluation) {
				a.Findings[0].Consequence = "read op://private/vault/item from /Users/private"
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			candidate := cloneAuthority(t, base)
			tc.mutate(&candidate)
			if err := ValidateAuthorityEvaluation(candidate); err == nil {
				t.Fatalf("invalid authority evaluation was accepted: %+v", candidate)
			}
		})
	}

	withScope := EvaluateAuthority(Profile{
		Environment: "container",
		Network:     "deny",
		Credentials: &Credentials{Github: &GithubCreds{Repos: []RepoCred{{Repo: "acme/repo"}}}},
	})
	withScope.CredentialScopes[0].Target = "~/.ssh/id_ed25519"
	if err := ValidateAuthorityEvaluation(withScope); err == nil {
		t.Fatal("forbidden private path in a credential target was accepted")
	}
}

func findAuthority(a AuthorityEvaluation, id string) *Finding {
	for i := range a.Findings {
		if a.Findings[i].RuleID == id {
			return &a.Findings[i]
		}
	}
	return nil
}

func firstFindingOnAxis(a AuthorityEvaluation, axis string) *Finding {
	for i := range a.Findings {
		if a.Findings[i].Axis == axis {
			return &a.Findings[i]
		}
	}
	return nil
}

func assertScopeAccess(t *testing.T, scopes map[string]CredentialScope, key, want string) {
	t.Helper()
	scope, ok := scopes[key]
	if !ok {
		t.Errorf("missing credential scope %q; got %+v", key, scopes)
		return
	}
	if scope.Access != want {
		t.Errorf("credential scope %q access = %q, want %q", key, scope.Access, want)
	}
}

func cloneAuthority(t *testing.T, in AuthorityEvaluation) AuthorityEvaluation {
	t.Helper()
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out AuthorityEvaluation
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}
