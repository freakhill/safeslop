package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/trust"
	"github.com/freakhill/safeslop/internal/jsoncontract"
)

const profileEgressCue = `package safeslop

safeslop: {
	version: 1
	profiles: reviewed: {
		agent: "pi"
		environment: "container"
		network: "deny"
		egress: [".legacy.example.com"]
		secrets: {TOKEN: "env:PROFILE_EGRESS_TEST_TOKEN"}
	}
}
`

func writeProfileEgressCue(t *testing.T) string {
	t.Helper()
	ws := t.TempDir()
	path := filepath.Join(ws, "safeslop.cue")
	if err := os.WriteFile(path, []byte(profileEgressCue), 0o644); err != nil {
		t.Fatal(err)
	}
	return ws
}

func TestProfileEgressPreviewGoldenMatchesEmittedEnvelope(t *testing.T) {
	got, err := jsoncontract.Marshal(jsoncontract.OK(profileEgressMutationData(
		"reviewed", "/workspace/safeslop.cue", "api.example.com", 443, "preview", "sha256:current", "sha256:candidate",
	)))
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join("..", "jsoncontract", "testdata", "ok-profile-egress-preview.golden.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("profile egress preview golden drifted\n--- got ---\n%s--- want ---\n%s", got, want)
	}
}

func TestProfileEgressPreviewAddRemoveAreHashCheckedAndValueFree(t *testing.T) {
	ws := writeProfileEgressCue(t)
	path := filepath.Join(ws, "safeslop.cue")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	originalHash := trust.Hash(original)
	trustFixtureForTest(t, ws)

	preview, err := runRootForTest(t, ws, "profile", "egress", "preview", "reviewed", "--host", "API.Example.com", "--port", "443", "--expected-policy-hash", originalHash, "--output", "json")
	if err != nil {
		t.Fatalf("preview: %v\nout=%s", err, preview)
	}
	previewEnv := parseEnvelopeForTest(t, preview)
	if !previewEnv.OK || previewEnv.Data["current_policy_hash"] != originalHash || previewEnv.Data["candidate_policy_hash"] == originalHash {
		t.Fatalf("preview envelope = %+v", previewEnv)
	}
	if strings.Contains(preview, "env:PROFILE_EGRESS_TEST_TOKEN") || strings.Contains(preview, "TOKEN") {
		t.Fatalf("preview leaked secret field: %s", preview)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != string(original) {
		t.Fatalf("preview mutated policy: bytes=%q err=%v", got, err)
	}

	added, err := runRootForTest(t, ws, "profile", "egress", "add", "reviewed", "--host", "API.Example.com", "--port", "443", "--expected-policy-hash", originalHash, "--output", "json")
	if err != nil {
		t.Fatalf("add: %v\nout=%s", err, added)
	}
	addedEnv := parseEnvelopeForTest(t, added)
	if !addedEnv.OK || addedEnv.Data["current_policy_hash"] != originalHash {
		t.Fatalf("add envelope = %+v", addedEnv)
	}
	_, cfg, err := loadConfigForProfileCredentialMutation(path)
	if err != nil {
		t.Fatal(err)
	}
	profile := cfg.Profiles["reviewed"]
	if _, _, status, err := checkTrust(path); err != nil || status != trust.Changed {
		t.Fatalf("persistent-rule write must require re-trust: status=%v err=%v", status, err)
	}
	if len(profile.PersistentEgress) != 1 || profile.PersistentEgress[0].FQDN != "api.example.com" || profile.PersistentEgress[0].Port != 443 || len(profile.Egress) != 1 || profile.Egress[0] != ".legacy.example.com" {
		t.Fatalf("mutated profile lost typed/legacy separation: %+v", profile)
	}

	beforeStale, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	stale, err := runRootForTest(t, ws, "profile", "egress", "remove", "reviewed", "--host", "api.example.com", "--port", "443", "--expected-policy-hash", originalHash, "--output", "json")
	if !errors.Is(err, errOutputEmitted) {
		t.Fatalf("stale remove error = %v, want contract error\nout=%s", err, stale)
	}
	staleEnv := parseEnvelopeForTest(t, stale)
	if staleEnv.OK || len(staleEnv.Errors) != 1 || !strings.Contains(staleEnv.Errors[0].Message, "stale") {
		t.Fatalf("stale envelope = %+v", staleEnv)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != string(beforeStale) {
		t.Fatalf("stale mutation changed policy: bytes=%q err=%v", got, err)
	}

	currentHash := trust.Hash(beforeStale)
	removed, err := runRootForTest(t, ws, "profile", "egress", "remove", "reviewed", "--host", "api.example.com", "--port", "443", "--expected-policy-hash", currentHash, "--output", "json")
	if err != nil {
		t.Fatalf("remove: %v\nout=%s", err, removed)
	}
	removedEnv := parseEnvelopeForTest(t, removed)
	if !removedEnv.OK || removedEnv.Data["candidate_policy_hash"] == currentHash {
		t.Fatalf("remove envelope = %+v", removedEnv)
	}
	_, cfg, err = loadConfigForProfileCredentialMutation(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Profiles["reviewed"]; len(got.PersistentEgress) != 0 || len(got.Egress) != 1 || got.Egress[0] != ".legacy.example.com" {
		t.Fatalf("remove changed legacy egress or retained typed entry: %+v", got)
	}
}

func TestProfileEgressRejectsInvalidOrNonEnforceableRulesWithoutMutation(t *testing.T) {
	ws := writeProfileEgressCue(t)
	path := filepath.Join(ws, "safeslop.cue")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	hash := trust.Hash(original)
	out, err := runRootForTest(t, ws, "profile", "egress", "add", "reviewed", "--host", "127.0.0.1", "--port", "443", "--expected-policy-hash", hash, "--output", "json")
	if !errors.Is(err, errOutputEmitted) {
		t.Fatalf("invalid rule error = %v, want contract error\nout=%s", err, out)
	}
	if env := parseEnvelopeForTest(t, out); env.OK || len(env.Errors) != 1 || !strings.Contains(env.Errors[0].Message, "IP literals") {
		t.Fatalf("invalid rule envelope = %+v", env)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != string(original) {
		t.Fatalf("invalid rule changed policy: bytes=%q err=%v", got, err)
	}
}
