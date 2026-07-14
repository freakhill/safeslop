package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/freakhill/safeslop/internal/engine/policy"
)

func TestProfileEvaluationUnsavedCollectsOneLocalSnapshot(t *testing.T) {
	fixed := time.Date(2026, 7, 14, 12, 34, 56, 0, time.FixedZone("test", 2*60*60))
	calls := 0
	oldNow := profileEvaluationNow
	profileEvaluationNow = func() time.Time {
		calls++
		return fixed
	}
	t.Cleanup(func() { profileEvaluationNow = oldNow })

	dir := t.TempDir()
	evaluation := evaluateProfile(profileEvaluationInput{
		Source:     profileEvaluationSourceUnsaved,
		Name:       "preview",
		PolicyPath: filepath.Join(dir, "safeslop.cue"),
		Profile: policy.Profile{
			Agent: "shell", Environment: "host", Network: "deny", Workspace: ".",
		},
	})

	if calls != 1 {
		t.Fatalf("clock calls = %d, want exactly one per evaluation snapshot", calls)
	}
	if evaluation.SchemaVersion != policy.EvaluationSchemaVersion {
		t.Fatalf("schema_version = %d", evaluation.SchemaVersion)
	}
	if evaluation.Trust.State != policy.TrustStateNotApplicable || evaluation.Trust.Basis != policy.TrustBasisUnsaved || evaluation.Trust.CheckedAt != nil {
		t.Fatalf("unsaved trust = %+v, want not_applicable/unsaved with null timestamp", evaluation.Trust)
	}
	if finding := profileFinding(evaluation.Trust.Findings, "trust.unsaved"); finding == nil || finding.Outcome != policy.FindingOutcomeNotApplicable {
		t.Fatalf("unsaved trust finding = %+v", finding)
	}
	if evaluation.Readiness.State != policy.ReadinessStateReady {
		t.Fatalf("readiness state = %q, want ready; findings=%+v", evaluation.Readiness.State, evaluation.Readiness.Findings)
	}
	wantTime := fixed.UTC()
	if evaluation.Readiness.CheckedAt == nil || !evaluation.Readiness.CheckedAt.Equal(wantTime) {
		t.Fatalf("readiness checked_at = %v, want %s", evaluation.Readiness.CheckedAt, wantTime)
	}
	if workspace := profileFinding(evaluation.Readiness.Findings, "readiness.workspace"); workspace == nil || workspace.Outcome != policy.FindingOutcomePass {
		t.Fatalf("workspace readiness = %+v, want pass", workspace)
	}
	if err := policy.ValidateAuthorityEvaluation(evaluation.Authority); err != nil {
		t.Fatalf("authority validation: %v", err)
	}
}

func TestProfileEvaluationProjectUsesExactBytesForTrust(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	fixed := time.Date(2026, 7, 14, 15, 0, 0, 0, time.UTC)
	oldNow := profileEvaluationNow
	profileEvaluationNow = func() time.Time { return fixed }
	t.Cleanup(func() { profileEvaluationNow = oldNow })

	path := canonicalPolicyPath(filepath.Join(t.TempDir(), "safeslop.cue"))
	approved := []byte("exact approved bytes")
	if err := approvePolicyBytes(path, approved); err != nil {
		t.Fatalf("approve exact bytes: %v", err)
	}
	prof := policy.Profile{Agent: "shell", Environment: "host", Network: "deny", Workspace: t.TempDir()}

	trusted := evaluateProfile(profileEvaluationInput{
		Source: profileEvaluationSourceProject, Name: "review", PolicyPath: path,
		PolicyHash: "not-a-trust-input", PolicyBytes: approved, Profile: prof,
	})
	if trusted.Trust.State != policy.TrustStateTrusted || trusted.Trust.Basis != policy.TrustBasisProjectExactBytes {
		t.Fatalf("trusted exact bytes = %+v", trusted.Trust)
	}
	if trusted.Trust.CheckedAt == nil || !trusted.Trust.CheckedAt.Equal(fixed) {
		t.Fatalf("trusted checked_at = %v, want %s", trusted.Trust.CheckedAt, fixed)
	}

	changed := evaluateProfile(profileEvaluationInput{
		Source: profileEvaluationSourceProject, Name: "review", PolicyPath: path,
		PolicyBytes: []byte("changed bytes"), Profile: prof,
	})
	if changed.Trust.State != policy.TrustStateChanged || changed.Trust.Basis != policy.TrustBasisProjectExactBytes {
		t.Fatalf("changed exact bytes = %+v", changed.Trust)
	}
	finding := profileFinding(changed.Trust.Findings, "trust.project.changed")
	if finding == nil || finding.Outcome != policy.FindingOutcomeFail || finding.Remediation == nil || finding.Remediation.Kind != policy.RemediationKindReviewAndTrust {
		t.Fatalf("changed trust finding = %+v, want fail + review workflow", finding)
	}
}

func TestProfileEvaluationBuiltinUsesEmbeddedProvenance(t *testing.T) {
	oldRuntime := profileEvaluationRuntime
	profileEvaluationRuntime = func(string) profilePrerequisiteCheck {
		return profilePrerequisiteCheck{State: profilePrerequisitePass}
	}
	t.Cleanup(func() { profileEvaluationRuntime = oldRuntime })

	builtin, ok := policy.BuiltinProfileByName("pi")
	if !ok {
		t.Fatal("pi builtin missing")
	}
	evaluation := evaluateProfile(profileEvaluationInput{
		Source: profileEvaluationSourceBuiltin, Name: builtin.Name,
		PolicyPath: "builtin:" + builtin.Name, PolicyHash: builtin.Hash, Profile: builtin.Profile,
	})
	if evaluation.Trust.State != policy.TrustStateTrusted || evaluation.Trust.Basis != policy.TrustBasisEmbeddedBuiltin {
		t.Fatalf("builtin trust = %+v, want trusted/embedded_builtin", evaluation.Trust)
	}
	finding := profileFinding(evaluation.Trust.Findings, "trust.builtin.trusted")
	if finding == nil || finding.Outcome != policy.FindingOutcomePass {
		t.Fatalf("builtin trust finding = %+v, want pass", finding)
	}
}

func TestProfileEvaluationBlockedReadinessDoesNotChangeAuthority(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	prof := policy.Profile{
		Agent: "shell", Environment: "host", Network: "deny",
		Workspace: filepath.Join(t.TempDir(), "DO_NOT_EMIT_WORKSPACE"),
		Secrets:   map[string]string{"DO_NOT_EMIT_SECRET_NAME": "op://DO_NOT_EMIT_REF/item/field"},
		Credentials: &policy.Credentials{Github: &policy.GithubCreds{Repos: []policy.RepoCred{
			{Repo: "acme/review"},
		}}},
	}
	before := policy.EvaluateAuthority(prof)
	evaluation := evaluateProfile(profileEvaluationInput{Source: profileEvaluationSourceUnsaved, Profile: prof})
	afterJSON, err := json.Marshal(evaluation.Authority)
	if err != nil {
		t.Fatal(err)
	}
	beforeJSON, err := json.Marshal(before)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(beforeJSON, afterJSON) {
		t.Fatalf("readiness changed static authority:\nbefore=%s\n after=%s", beforeJSON, afterJSON)
	}
	if evaluation.Readiness.State != policy.ReadinessStateBlocked {
		t.Fatalf("readiness = %q, want blocked; findings=%+v", evaluation.Readiness.State, evaluation.Readiness.Findings)
	}

	wire, err := json.Marshal(evaluation)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"DO_NOT_EMIT_WORKSPACE", "DO_NOT_EMIT_SECRET_NAME", "DO_NOT_EMIT_REF", "op://"} {
		if strings.Contains(string(wire), forbidden) {
			t.Errorf("evaluation leaked forbidden workspace/secret material %q: %s", forbidden, wire)
		}
	}
}

func withProfileEvaluationLocalPass(t *testing.T) time.Time {
	t.Helper()
	fixed := time.Date(2026, 7, 14, 18, 30, 0, 0, time.UTC)
	oldNow, oldWorkspace := profileEvaluationNow, profileEvaluationWorkspace
	oldHelper, oldRuntime, oldAccounts := profileEvaluationHelper, profileEvaluationRuntime, profileEvaluationAccountLinks
	profileEvaluationNow = func() time.Time { return fixed }
	profileEvaluationWorkspace = func(string) profilePrerequisiteCheck {
		return profilePrerequisiteCheck{State: profilePrerequisitePass}
	}
	profileEvaluationHelper = func(string) profilePrerequisiteCheck {
		return profilePrerequisiteCheck{State: profilePrerequisitePass}
	}
	profileEvaluationRuntime = func(string) profilePrerequisiteCheck {
		return profilePrerequisiteCheck{State: profilePrerequisitePass}
	}
	profileEvaluationAccountLinks = func(policy.Profile) profileAccountLinkChecks { return profileAccountLinkChecks{} }
	t.Cleanup(func() {
		profileEvaluationNow, profileEvaluationWorkspace = oldNow, oldWorkspace
		profileEvaluationHelper, profileEvaluationRuntime, profileEvaluationAccountLinks = oldHelper, oldRuntime, oldAccounts
	})
	return fixed
}

func profileFinding(findings []policy.Finding, ruleID string) *policy.Finding {
	for i := range findings {
		if findings[i].RuleID == ruleID {
			return &findings[i]
		}
	}
	return nil
}

func TestProfileEvaluationShadowedHelperStaysBlockedAndPathFree(t *testing.T) {
	oldHelper := profileEvaluationHelper
	profileEvaluationHelper = func(name string) profilePrerequisiteCheck {
		if name == "op" {
			return profilePrerequisiteCheck{State: profilePrerequisiteFail, Problem: profileProblemResolution}
		}
		return profilePrerequisiteCheck{State: profilePrerequisitePass}
	}
	t.Cleanup(func() { profileEvaluationHelper = oldHelper })

	evaluation := evaluateProfile(profileEvaluationInput{
		Source: profileEvaluationSourceUnsaved,
		Profile: policy.Profile{
			Agent: "fish", Environment: "host", Network: "deny", Workspace: t.TempDir(),
			Secrets: map[string]string{"TOKEN": "op://private/account/ref"},
		},
	})
	if evaluation.Readiness.State != policy.ReadinessStateBlocked {
		t.Fatalf("shadowed helper readiness = %q, want blocked", evaluation.Readiness.State)
	}
	finding := profileFinding(evaluation.Readiness.Findings, "readiness.secret-provider")
	if finding == nil || finding.Outcome != policy.FindingOutcomeFail || finding.Remediation == nil || finding.Remediation.Kind != policy.RemediationKindRepairHelperResolution {
		t.Fatalf("shadowed helper finding = %+v", finding)
	}
	wire, err := json.Marshal(evaluation)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"op://", "/safe/bin", "/private/account/ref"} {
		if strings.Contains(string(wire), forbidden) {
			t.Errorf("helper evaluation leaked path/ref %q: %s", forbidden, wire)
		}
	}
}

func TestProfileEvaluationRuntimeAndAccountFailuresAreIndependent(t *testing.T) {
	oldRuntime, oldHelper, oldAccounts := profileEvaluationRuntime, profileEvaluationHelper, profileEvaluationAccountLinks
	profileEvaluationRuntime = func(string) profilePrerequisiteCheck {
		return profilePrerequisiteCheck{State: profilePrerequisiteFail, Problem: profileProblemResolution}
	}
	profileEvaluationHelper = func(string) profilePrerequisiteCheck {
		return profilePrerequisiteCheck{State: profilePrerequisitePass}
	}
	profileEvaluationAccountLinks = func(policy.Profile) profileAccountLinkChecks {
		return profileAccountLinkChecks{Github: profileProviderLinkCheck{Required: true, State: profilePrerequisiteFail, Count: 1}}
	}
	t.Cleanup(func() {
		profileEvaluationRuntime, profileEvaluationHelper, profileEvaluationAccountLinks = oldRuntime, oldHelper, oldAccounts
	})

	evaluation := evaluateProfile(profileEvaluationInput{
		Source: profileEvaluationSourceUnsaved,
		Profile: policy.Profile{
			Agent: "pi", Environment: "container", Network: "deny", Workspace: t.TempDir(),
			Credentials: &policy.Credentials{Github: &policy.GithubCreds{Repos: []policy.RepoCred{{Repo: "acme/review"}}}},
		},
	})
	if evaluation.Readiness.State != policy.ReadinessStateBlocked {
		t.Fatalf("readiness = %q, want blocked", evaluation.Readiness.State)
	}
	runtimeFinding := profileFinding(evaluation.Readiness.Findings, "readiness.container-runtime")
	if runtimeFinding == nil || runtimeFinding.Remediation == nil || runtimeFinding.Remediation.Kind != policy.RemediationKindRepairHelperResolution {
		t.Fatalf("runtime finding = %+v", runtimeFinding)
	}
	accountFinding := profileFinding(evaluation.Readiness.Findings, "readiness.github-account")
	if accountFinding == nil || accountFinding.Remediation == nil || accountFinding.Remediation.ActionID != "link-github-account" || len(accountFinding.ScopeIDs) != 1 {
		t.Fatalf("account finding = %+v", accountFinding)
	}
}

func TestProfileEvaluationCompatibilityJSONStillDecodesOldFields(t *testing.T) {
	oldRuntime := profileEvaluationRuntime
	profileEvaluationRuntime = func(string) profilePrerequisiteCheck {
		return profilePrerequisiteCheck{State: profilePrerequisitePass}
	}
	t.Cleanup(func() { profileEvaluationRuntime = oldRuntime })

	data, err := profileResolvedData("safeslop.cue", "review", policy.Profile{
		Agent: "fish", Environment: "container", Network: "deny",
	})
	if err != nil {
		t.Fatal(err)
	}
	wire, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	var oldClient struct {
		Path     string          `json:"path"`
		Name     string          `json:"name"`
		Profile  json.RawMessage `json:"profile"`
		Risk     json.RawMessage `json:"risk"`
		RiskAxes json.RawMessage `json:"risk_axes"`
		Resolved json.RawMessage `json:"resolved"`
		RecipeID string          `json:"recipeID"`
		Image    string          `json:"image"`
		Base     string          `json:"base"`
		BaseImg  string          `json:"baseImage"`
		Recipe   json.RawMessage `json:"recipe"`
	}
	if err := json.Unmarshal(wire, &oldClient); err != nil {
		t.Fatalf("old client decode: %v", err)
	}
	if oldClient.Path == "" || oldClient.Name != "review" || len(oldClient.Profile) == 0 || len(oldClient.Risk) == 0 || len(oldClient.RiskAxes) == 0 || len(oldClient.Resolved) == 0 || oldClient.RecipeID == "" || oldClient.Image == "" || oldClient.Base == "" || oldClient.BaseImg == "" || len(oldClient.Recipe) == 0 {
		t.Fatalf("additive evaluation removed an existing key: %+v", oldClient)
	}
}

func TestProfileEvaluationProjectUntrustedWhenNoApproval(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "safeslop.cue")
	if err := os.WriteFile(path, []byte("current bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	evaluation := evaluateProfile(profileEvaluationInput{
		Source: profileEvaluationSourceProject, PolicyPath: canonicalPolicyPath(path),
		PolicyBytes: []byte("current bytes"),
		Profile:     policy.Profile{Agent: "shell", Environment: "host", Network: "deny", Workspace: filepath.Dir(path)},
	})
	if evaluation.Trust.State != policy.TrustStateUntrusted {
		t.Fatalf("trust = %+v, want untrusted exact current bytes", evaluation.Trust)
	}
}
