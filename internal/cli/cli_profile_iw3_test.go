package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/freakhill/safeslop/internal/engine/policy"
	"github.com/freakhill/safeslop/internal/jsoncontract"
)

func TestProfileShowEnvelopeIncludesResolvedRecipe(t *testing.T) {
	dir := t.TempDir()
	cue := `package safeslop

safeslop: {
	version: 1
	profiles: {
		review: {agent: "claude", environment: "container", network: "deny", packages: ["pnpm"]}
	}
}
`
	if err := os.WriteFile(filepath.Join(dir, "safeslop.cue"), []byte(cue), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runRootForTest(t, dir, "profile", "show", "review", "--output", "json")
	if err != nil {
		t.Fatalf("profile show --output json: %v", err)
	}
	env := parseEnvelopeForTest(t, out)
	if !env.OK {
		t.Fatalf("profile show returned error envelope: %+v", env.Errors)
	}
	if env.Data["profile_source"] != "project" || env.Data["profile_name"] != "review" || env.Data["policy_hash"] == "" {
		t.Fatalf("project profile should override builtin with provenance: %#v", env.Data)
	}
	profile, ok := env.Data["profile"].(map[string]any)
	if !ok || profile["agent"] != "claude" || profile["environment"] != "container" {
		t.Fatalf("profile data wrong: %#v", env.Data["profile"])
	}
	resolved, ok := env.Data["resolved"].(map[string]any)
	if !ok {
		t.Fatalf("resolved data missing: %#v", env.Data)
	}
	ids, ok := resolved["identitySet"].([]any)
	if !ok {
		t.Fatalf("resolved.identitySet malformed: %#v", resolved)
	}
	for _, want := range []string{"claude-code", "node", "pnpm"} {
		if !stringSliceAnyContains(ids, want) {
			t.Fatalf("resolved identity missing %q: %#v", want, ids)
		}
	}
	if recipeID, _ := env.Data["recipeID"].(string); len(recipeID) != 12 {
		t.Fatalf("recipeID = %q, want 12 hex chars", recipeID)
	}
	if image, _ := env.Data["image"].(string); !strings.HasPrefix(image, "local/safeslop-tools:") {
		t.Fatalf("image = %q, want local/safeslop-tools tag", image)
	}
	if base, _ := env.Data["base"].(string); !strings.HasPrefix(base, "debian:bookworm-slim@sha256:") {
		t.Fatalf("base = %q, want pinned debian source", base)
	}
}

func TestProfileShowUnbuildablePackageReturnsEnvelope(t *testing.T) {
	dir := t.TempDir()
	cue := `package safeslop

safeslop: {
	version: 1
	profiles: {
		test: {agent: "pi", environment: "container", network: "allow", bundles: ["pi"], packages: ["bun"]}
	}
}
`
	if err := os.WriteFile(filepath.Join(dir, "safeslop.cue"), []byte(cue), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runRootForTest(t, dir, "profile", "show", "test", "--output", "json")
	if !errors.Is(err, errOutputEmitted) {
		t.Fatalf("profile show unbuildable package: err = %v, want errOutputEmitted; out=%s", err, out)
	}
	env := parseEnvelopeForTest(t, out)
	if env.OK || len(env.Errors) != 1 || env.Errors[0].Code != jsoncontract.CodeInvalidArgument {
		t.Fatalf("expected INVALID_ARGUMENT error envelope, got: %+v", env)
	}
	if !strings.Contains(env.Errors[0].Message, "resolve profile image recipe") {
		t.Fatalf("error message = %q, want recipe context", env.Errors[0].Message)
	}
	if got, _ := env.Errors[0].Details["profile"].(string); got != "test" {
		t.Fatalf("details.profile = %q, want test", got)
	}
}

func TestProfileCreateDryRunIncludesRiskAndDoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	out, err := runRootForTest(t, dir,
		"profile", "create",
		"--dry-run",
		"--name", "review",
		"--agent", "claude-code",
		"--environment", "container",
		"--bundle", "pi",
		"--package", "pnpm",
		"--workspace", ".",
		"--network", "deny",
		"--output", "json",
	)
	if err != nil {
		t.Fatalf("profile create --dry-run --output json: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "safeslop.cue")); !os.IsNotExist(err) {
		t.Fatalf("dry-run profile create should not write safeslop.cue; stat err=%v", err)
	}
	env := parseEnvelopeForTest(t, out)
	if !env.OK {
		t.Fatalf("profile create dry-run returned error envelope: %+v", env.Errors)
	}
	profile, ok := env.Data["profile"].(map[string]any)
	if !ok {
		t.Fatalf("data.profile missing: %#v", env.Data["profile"])
	}
	canonicalDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if profile["agent"] != "claude" || profile["environment"] != "container" || profile["workspace"] != canonicalDir || profile["network"] != "deny" {
		t.Fatalf("profile args not echoed under data.profile: %#v", profile)
	}
	if !stringSliceAnyContains(profile["bundles"].([]any), "pi") || !stringSliceAnyContains(profile["packages"].([]any), "pnpm") {
		t.Fatalf("profile package selectors not echoed: %#v", profile)
	}
	risk, ok := env.Data["risk"].(map[string]any)
	if !ok || risk["headline"] == "" || risk["level"] == "" || risk["lines"] == nil || risk["Headline"] != nil {
		t.Fatalf("data.risk lower-camel contract missing: %#v", env.Data["risk"])
	}
	axes, ok := env.Data["risk_axes"].([]any)
	if !ok || len(axes) == 0 {
		t.Fatalf("data.risk_axes missing: %#v", env.Data["risk_axes"])
	}
	axis, ok := axes[0].(map[string]any)
	if !ok || axis["name"] == "" || axis["value"] == "" || axis["restricted"] == nil || axis["severity"] == "" || axis["Name"] != nil {
		t.Fatalf("data.risk_axes lower-camel contract missing: %#v", axes[0])
	}
}

func TestProfileCreateDryRunAddsEvaluation(t *testing.T) {
	fixed := withProfileEvaluationLocalPass(t)
	dir := t.TempDir()
	out, err := runRootForTest(t, dir,
		"profile", "create", "--dry-run",
		"--name", "preview",
		"--agent", "fish",
		"--environment", "host",
		"--workspace", ".",
		"--network", "deny",
		"--output", "json",
	)
	if err != nil {
		t.Fatalf("profile create dry-run: %v\nout=%s", err, out)
	}
	env := parseEnvelopeForTest(t, out)
	evaluation, ok := env.Data["evaluation"].(map[string]any)
	if !ok {
		t.Fatalf("data.evaluation missing from additive dry-run contract: %#v", env.Data)
	}
	if evaluation["schema_version"] != float64(policy.EvaluationSchemaVersion) {
		t.Fatalf("evaluation schema = %#v", evaluation["schema_version"])
	}
	trustSection := evaluation["trust"].(map[string]any)
	if trustSection["state"] != policy.TrustStateNotApplicable || trustSection["basis"] != policy.TrustBasisUnsaved || trustSection["checked_at"] != nil {
		t.Fatalf("dry-run trust = %#v", trustSection)
	}
	readiness := evaluation["readiness"].(map[string]any)
	if readiness["state"] != policy.ReadinessStateReady || readiness["checked_at"] != fixed.Format(time.RFC3339) {
		t.Fatalf("dry-run readiness = %#v, want one fixed local snapshot", readiness)
	}
}

func TestProfileCreateWritesNewCue(t *testing.T) {
	dir := t.TempDir()
	out, err := runRootForTest(t, dir,
		"profile", "create",
		"--name", "review",
		"--agent", "fish",
		"--environment", "container",
		"--bundle", "pi",
		"--package", "pnpm",
		"--workspace", ".",
		"--network", "deny",
		"--output", "json",
	)
	if err != nil {
		t.Fatalf("profile create --output json: %v", err)
	}
	env := parseEnvelopeForTest(t, out)
	if !env.OK {
		t.Fatalf("profile create returned error envelope: %+v", env.Errors)
	}
	if env.Data["name"] != "review" {
		t.Fatalf("name = %#v", env.Data["name"])
	}
	cfg, err := policy.Load(filepath.Join(dir, "safeslop.cue"))
	if err != nil {
		t.Fatalf("created safeslop.cue should validate: %v", err)
	}
	p := cfg.Profiles["review"]
	if p.Agent != "fish" || p.Environment != "container" || p.Network != "deny" || p.Workspace != "." {
		t.Fatalf("created profile fields wrong: %+v", p)
	}
	if len(p.Bundles) != 1 || p.Bundles[0] != "pi" || len(p.Packages) != 1 || p.Packages[0] != "pnpm" {
		t.Fatalf("created package selectors wrong: bundles=%v packages=%v", p.Bundles, p.Packages)
	}
	resolved, ok := env.Data["resolved"].(map[string]any)
	if !ok || !stringSliceAnyContains(resolved["identitySet"].([]any), "pi") || !stringSliceAnyContains(resolved["identitySet"].([]any), "pnpm") {
		t.Fatalf("resolved output wrong: %#v", env.Data["resolved"])
	}
}

func TestProfileCreateUnbuildablePackageReturnsEnvelopeAndDoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	out, err := runRootForTest(t, dir,
		"profile", "create",
		"--name", "test",
		"--agent", "pi",
		"--environment", "container",
		"--bundle", "pi",
		"--package", "bun",
		"--network", "allow",
		"--output", "json",
	)
	if !errors.Is(err, errOutputEmitted) {
		t.Fatalf("profile create unbuildable package: err = %v, want errOutputEmitted; out=%s", err, out)
	}
	env := parseEnvelopeForTest(t, out)
	if env.OK || len(env.Errors) != 1 || env.Errors[0].Code != jsoncontract.CodeInvalidArgument {
		t.Fatalf("expected INVALID_ARGUMENT error envelope, got: %+v", env)
	}
	if !strings.Contains(env.Errors[0].Message, "resolve profile image recipe") {
		t.Fatalf("error message = %q, want recipe context", env.Errors[0].Message)
	}
	if got, _ := env.Errors[0].Details["profile"].(string); got != "test" {
		t.Fatalf("details.profile = %q, want test", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "safeslop.cue")); !os.IsNotExist(err) {
		t.Fatalf("unbuildable profile should not write safeslop.cue; stat err=%v", err)
	}
}

func TestProfileCreateWithRipgrepPackageWritesNewCue(t *testing.T) {
	dir := t.TempDir()
	out, err := runRootForTest(t, dir,
		"profile", "create",
		"--name", "test",
		"--agent", "pi",
		"--environment", "container",
		"--bundle", "pi",
		"--package", "ripgrep",
		"--network", "allow",
		"--output", "json",
	)
	if err != nil {
		t.Fatalf("profile create pi+ripgrep: %v\nout=%s", err, out)
	}
	env := parseEnvelopeForTest(t, out)
	if !env.OK {
		t.Fatalf("profile create returned error envelope: %+v", env.Errors)
	}
	resolved, ok := env.Data["resolved"].(map[string]any)
	if !ok {
		t.Fatalf("resolved output missing: %#v", env.Data["resolved"])
	}
	ids, _ := resolved["identitySet"].([]any)
	for _, want := range []string{"node", "pi", "ripgrep"} {
		if !stringSliceAnyContains(ids, want) {
			t.Fatalf("resolved identity missing %q: %#v", want, ids)
		}
	}
	cfg, err := policy.Load(filepath.Join(dir, "safeslop.cue"))
	if err != nil {
		t.Fatalf("created safeslop.cue should validate: %v", err)
	}
	p := cfg.Profiles["test"]
	if p.Network != "allow" || len(p.Packages) != 1 || p.Packages[0] != "ripgrep" {
		t.Fatalf("created profile fields wrong: %+v", p)
	}
}

func TestProfileCreateNoDefaultBundleWritesBareAgent(t *testing.T) {
	dir := t.TempDir()
	out, err := runRootForTest(t, dir,
		"profile", "create",
		"--name", "bare",
		"--agent", "claude",
		"--environment", "container",
		"--no-default-bundle",
		"--output", "json",
	)
	if err != nil {
		t.Fatalf("profile create --no-default-bundle: %v", err)
	}
	env := parseEnvelopeForTest(t, out)
	if !env.OK {
		t.Fatalf("profile create returned error envelope: %+v", env.Errors)
	}
	cfg, err := policy.Load(filepath.Join(dir, "safeslop.cue"))
	if err != nil {
		t.Fatalf("created safeslop.cue should validate: %v", err)
	}
	if !cfg.Profiles["bare"].BareAgent {
		t.Fatalf("BareAgent was not persisted: %+v", cfg.Profiles["bare"])
	}
	resolved := env.Data["resolved"].(map[string]any)
	ids, _ := resolved["identitySet"].([]any)
	if len(ids) != 0 {
		t.Fatalf("bare claude profile resolved default packages despite opt-out: %#v", ids)
	}
}

func TestProfileCreateRequiresOutputJSON(t *testing.T) {
	if _, err := runRootForTest(t, t.TempDir(), "profile", "create", "--name", "x", "--agent", "fish", "--environment", "host"); err == nil {
		t.Fatal("profile create without --output json should error")
	}
}

func stringSliceAnyContains(values []any, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
