package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/control/pb"
	"github.com/freakhill/safeslop/internal/engine/sandbox"
)

func trustPolicy(t *testing.T, path string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir()) // isolate the trust store from the real ~/.config/safeslop
	if err := enforceTrust(path, true); err != nil {
		t.Fatalf("trust %s: %v", path, err)
	}
}

const resolverCue = `package safeslop
safeslop: {
	version: 1
	profiles: {
		h: {agent: "claude", environment: "host", network: "deny"}
		s: {agent: "claude", environment: "sandbox", network: "deny"}
		c: {agent: "claude", environment: "container", network: "deny"}
		v: {agent: "claude", environment: "vm", network: "allow"}
	}
}
`

func writeResolverCue(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "safeslop.cue")
	if err := os.WriteFile(path, []byte(resolverCue), 0o644); err != nil {
		t.Fatal(err)
	}
	trustPolicy(t, path)
	return path
}

func TestResolveSessionHostAndSandbox(t *testing.T) {
	path := writeResolverCue(t)

	h, err := resolveSession("h", path)
	if err != nil {
		t.Fatalf("host resolve: %v", err)
	}
	if len(h.Argv) == 0 || h.Argv[0] != "claude" {
		t.Fatalf("host argv = %v, want it to start with claude", h.Argv)
	}
	if h.OnClose == nil {
		t.Fatal("host session must carry a cleanup (per-session stage-dir wipe)")
	}
	h.OnClose() // must not panic

	s, err := resolveSession("s", path)
	if err != nil {
		t.Fatalf("sandbox resolve: %v", err)
	}
	if len(s.Argv) == 0 || s.Argv[0] != sandbox.SandboxExecPath {
		t.Fatalf("sandbox argv = %v, want it to start with %s", s.Argv, sandbox.SandboxExecPath)
	}
	if s.OnClose == nil {
		t.Fatal("sandbox session must carry a cleanup (temp profile removal)")
	}
	s.OnClose() // must not panic
}

func TestResolveSessionContainerVMErrorWhenToolingAbsent(t *testing.T) {
	path := writeResolverCue(t)
	t.Chdir(t.TempDir()) // any cockpit-* stage dir lands under a throwaway cwd, not the repo
	t.Setenv("PATH", "") // docker + tart unavailable

	// The error must come from the real provisioning path (PrepareSession -> "docker"/"tart"
	// unavailable), not the pre-SP7c-2 "is SP7c-2" sentinel — that's what makes this fail first.
	if _, err := resolveSession("c", path); err == nil || !strings.Contains(err.Error(), "docker") {
		t.Fatalf("container resolve must reach PrepareSession and fail on docker availability, got %v", err)
	}
	if _, err := resolveSession("v", path); err == nil || !strings.Contains(err.Error(), "tart") {
		t.Fatalf("vm resolve must reach PrepareSession and fail on tart availability, got %v", err)
	}
}

const secretHostCue = `package safeslop
safeslop: {
	version: 1
	profiles: {
		h: {agent: "claude", environment: "host", network: "deny", secrets: {FOO: "env:TEST_SAFESLOP_SECRET"}}
	}
}
`

const sshHostCue = `package safeslop
safeslop: {
	version: 1
	profiles: {
		h: {agent: "claude", environment: "host", network: "deny", credentials: {ssh: {}}}
	}
}
`

func TestResolveSessionDeliversSecretToHostEnv(t *testing.T) {
	t.Setenv("TEST_SAFESLOP_SECRET", "s3cr3t")
	dir := t.TempDir()
	path := filepath.Join(dir, "safeslop.cue")
	if err := os.WriteFile(path, []byte(secretHostCue), 0o644); err != nil {
		t.Fatal(err)
	}
	trustPolicy(t, path)
	t.Chdir(dir) // any cockpit-* stage dir lands under a throwaway cwd

	spec, err := resolveSession("h", path)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !slices.Contains(spec.Env, "FOO=s3cr3t") {
		t.Fatalf("secret not delivered to host env: %v", spec.Env)
	}
	if spec.OnClose != nil {
		spec.OnClose() // stage-dir wipe must not panic
	}
}

func TestResolveSessionRejectsSshCreds(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "safeslop.cue")
	if err := os.WriteFile(path, []byte(sshHostCue), 0o644); err != nil {
		t.Fatal(err)
	}
	trustPolicy(t, path)
	t.Chdir(dir)
	if _, err := resolveSession("h", path); err == nil || !strings.Contains(err.Error(), "ssh credentials") {
		t.Fatalf("expected ssh-cred rejection, got %v", err)
	}
}

func TestResolveSessionRefusesUntrustedPolicy(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // empty trust store
	dir := t.TempDir()
	path := filepath.Join(dir, "safeslop.cue")
	if err := os.WriteFile(path, []byte(resolverCue), 0o644); err != nil {
		t.Fatal(err)
	}
	// NOT trusted -> OpenSession's resolver must fail closed (the in-sandbox escape this closes).
	if _, err := resolveSession("h", path); err == nil || !strings.Contains(err.Error(), "not trusted") {
		t.Fatalf("untrusted policy must be refused by resolveSession, got %v", err)
	}
}

// TestCockpitTrustUnblocksResolveSession is the engine side of the GUI trust flow: an untrusted
// policy fails OpenSession; the cockpit's Trust RPC (cockpitTrust) approves it; OpenSession passes.
func TestCockpitTrustUnblocksResolveSession(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // empty trust store
	dir := t.TempDir()
	path := filepath.Join(dir, "safeslop.cue")
	if err := os.WriteFile(path, []byte(resolverCue), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir) // cockpit stage dirs land under a throwaway cwd

	if _, err := resolveSession("h", path); err == nil || !strings.Contains(err.Error(), "not trusted") {
		t.Fatalf("untrusted must fail closed, got %v", err)
	}
	abs, err := cockpitTrust(path)
	if err != nil {
		t.Fatalf("cockpitTrust: %v", err)
	}
	if abs == "" {
		t.Fatal("cockpitTrust must return the approved absolute path")
	}
	if _, err := resolveSession("h", path); err != nil {
		t.Fatalf("after Trust, resolveSession must pass: %v", err)
	}
}

// TestCockpitListProfiles: the GUI launcher's ListProfiles source returns each profile tagged with
// its honest EnvTier tier + note (single source of truth for the cockpit's tier indicator).
func TestCockpitListProfiles(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // isolate the trust-store read
	dir := t.TempDir()
	path := filepath.Join(dir, "safeslop.cue")
	if err := os.WriteFile(path, []byte(resolverCue), 0o644); err != nil {
		t.Fatal(err)
	}
	profs, err := cockpitListProfiles(path) // listing is ungated — no trust needed
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]*pb.Profile{}
	for _, p := range profs {
		byName[p.Name] = p
	}
	want := map[string]string{"h": "none", "s": "mistake-guard", "c": "egress-allowlisted", "v": "adversary-grade"}
	for name, tier := range want {
		p := byName[name]
		if p == nil {
			t.Fatalf("profile %q missing from list", name)
		}
		if p.Tier != tier {
			t.Errorf("profile %q tier = %q, want %q", name, p.Tier, tier)
		}
		if p.TierNote == "" {
			t.Errorf("profile %q must carry a tier note", name)
		}
	}
}

// TestCockpitListProfilesTrustStatus: the launcher source reports per-policy trust state so the
// GUI can badge it before launch (anti-ambush). Fresh => untrusted; after cockpitTrust => trusted.
func TestCockpitListProfilesTrustStatus(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // isolated trust store
	dir := t.TempDir()
	path := filepath.Join(dir, "safeslop.cue")
	if err := os.WriteFile(path, []byte(resolverCue), 0o644); err != nil {
		t.Fatal(err)
	}
	profs, err := cockpitListProfiles(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range profs {
		if p.TrustStatus != "untrusted" {
			t.Fatalf("fresh policy must list as untrusted, got %q for %q", p.TrustStatus, p.Name)
		}
	}
	if _, err := cockpitTrust(path); err != nil {
		t.Fatal(err)
	}
	profs2, err := cockpitListProfiles(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range profs2 {
		if p.TrustStatus != "trusted" {
			t.Fatalf("after trust, must list as trusted, got %q for %q", p.TrustStatus, p.Name)
		}
	}
}
