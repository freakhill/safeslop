package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/policy"
	engsession "github.com/freakhill/safeslop/internal/engine/session"
	"github.com/freakhill/safeslop/internal/engine/trust"
)

// The exact fields the 0073 bug dropped: a credentials block and a secret.
const pinnedPolicyCue = `package safeslop

safeslop: {
	version: 1
	profiles: {
		smoke: {
			agent:       "fish"
			environment: "container"
			network:     "deny"
			secrets: {HOOK: "env:HOOK_SRC"}
			credentials: github: repos: [{repo: "acme/web", write: true}]
		}
	}
}
`

func writePinnedPolicy(t *testing.T) (path, hash string) {
	t.Helper()
	path = filepath.Join(t.TempDir(), "safeslop.cue")
	if err := os.WriteFile(path, []byte(pinnedPolicyCue), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, trust.Hash([]byte(pinnedPolicyCue))
}

func pinnedSession(path, hash string) engsession.Session {
	return engsession.Session{
		ID: "s1", Profile: "smoke", Agent: "fish", Environment: "container",
		Network: "deny", Workspace: "/ws", PolicyPath: path, PolicyHash: hash,
	}
}

func TestSessionProfileRebuildsPinnedPolicy(t *testing.T) {
	path, hash := writePinnedPolicy(t)
	prof, err := sessionProfile(pinnedSession(path, hash))
	if err != nil {
		t.Fatalf("sessionProfile: %v", err)
	}
	if prof.Credentials == nil || prof.Credentials.Github == nil {
		t.Fatalf("credentials.github dropped from session profile: %+v", prof.Credentials)
	}
	if got := prof.Secrets["HOOK"]; got != "env:HOOK_SRC" {
		t.Fatalf("secrets dropped from session profile, got %q", got)
	}
	if prof.Workspace != "/ws" {
		t.Fatalf("workspace must stay record-canonical, got %q", prof.Workspace)
	}
}

func TestSessionProfileResolvedOverrideApplies(t *testing.T) {
	path, hash := writePinnedPolicy(t)
	sess := pinnedSession(path, hash)
	sess.Resolved = &engsession.ResolvedMetadata{IdentitySet: []string{"git"}}
	prof, err := sessionProfile(sess)
	if err != nil {
		t.Fatalf("sessionProfile: %v", err)
	}
	if !prof.BareAgent || len(prof.Packages) != 1 || prof.Packages[0] != "git" {
		t.Fatalf("pinned identity-set override lost: bare=%v packages=%v", prof.BareAgent, prof.Packages)
	}
	if prof.Credentials == nil || prof.Credentials.Github == nil {
		t.Fatalf("credentials must survive the resolved override")
	}
}

func TestSessionProfileHashMismatchFailsClosed(t *testing.T) {
	path, _ := writePinnedPolicy(t)
	_, err := sessionProfile(pinnedSession(path, "not-the-approved-hash"))
	if err == nil || !strings.Contains(err.Error(), "changed since") {
		t.Fatalf("want fail-closed hash-mismatch error, got %v", err)
	}
}

func TestSessionProfileVanishedProfileFailsClosed(t *testing.T) {
	path, hash := writePinnedPolicy(t)
	sess := pinnedSession(path, hash)
	sess.Profile = "gone"
	_, err := sessionProfile(sess)
	if err == nil || !strings.Contains(err.Error(), `"gone"`) {
		t.Fatalf("want fail-closed vanished-profile error, got %v", err)
	}
}

func TestSessionProfileUnreadablePolicyFailsClosed(t *testing.T) {
	sess := pinnedSession(filepath.Join(t.TempDir(), "absent.cue"), "irrelevant")
	if _, err := sessionProfile(sess); err == nil {
		t.Fatal("want fail-closed error for unreadable pinned policy")
	}
}

func TestSessionProfileRebuildsBuiltinAndFailsClosedOnDrift(t *testing.T) {
	builtin, ok := policy.BuiltinProfileByName("pi")
	if !ok {
		t.Fatal("pi builtin missing")
	}
	sess := engsession.Session{ID: "builtin", Profile: "pi", ProfileSource: "builtin", Agent: "pi", Environment: "container", Network: "deny", Workspace: "/ws", PolicyPath: "builtin:pi", PolicyHash: builtin.Hash}
	prof, err := sessionProfile(sess)
	if err != nil {
		t.Fatalf("rebuild builtin session: %v", err)
	}
	if prof.Agent != "pi" || prof.Workspace != "/ws" {
		t.Fatalf("builtin profile = %#v", prof)
	}
	sess.PolicyHash = "sha256:drift"
	if _, err := sessionProfile(sess); err == nil || !strings.Contains(err.Error(), "changed or is unavailable") {
		t.Fatalf("builtin hash drift must fail closed, got %v", err)
	}
}

func TestSessionProfileAdHocStaysSynthetic(t *testing.T) {
	sess := engsession.Session{ID: "s2", Agent: "zsh", Environment: "container", Network: "allow", Workspace: "/w2"}
	prof, err := sessionProfile(sess)
	if err != nil {
		t.Fatalf("sessionProfile: %v", err)
	}
	if prof.Agent != "zsh" || prof.Network != "allow" || prof.Workspace != "/w2" {
		t.Fatalf("synthetic ad-hoc profile mangled: %+v", prof)
	}
	if prof.Credentials != nil || prof.Secrets != nil {
		t.Fatalf("ad-hoc session must not grow credentials/secrets: %+v", prof)
	}
}
