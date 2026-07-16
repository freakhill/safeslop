package creds

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/policy"
)

// fakeProber builds a hermetic Prober: env is the visible process env, opAvail/opSignedIn are the
// op state, and opResolvable maps an op:// ref to whether ResolveOp reports it resolvable. calls
// counts ResolveOp invocations so tests can assert the op-down short-circuit never probes.
func fakeProber(env map[string]string, opAvail, opSignedIn bool, opResolvable map[string]bool, calls *int) Prober {
	return Prober{
		OpAvailable: func() bool { return opAvail },
		OpSignedIn:  func(context.Context) bool { return opSignedIn },
		LookupEnv:   func(name string) (string, bool) { v, ok := env[name]; return v, ok },
		ResolveOp: func(_ context.Context, ref string) error {
			if calls != nil {
				*calls++
			}
			if opResolvable[ref] {
				return nil
			}
			return errFakeUnresolvable
		},
	}
}

var errFakeUnresolvable = &fakeErr{}

type fakeErr struct{}

func (*fakeErr) Error() string { return "unresolvable (fake)" }

func rowFor(rows []CredRow, kind, name string) (CredRow, bool) {
	for _, r := range rows {
		if r.Kind == kind && r.Name == name {
			return r, true
		}
	}
	return CredRow{}, false
}

func cfgWith(prof policy.Profile) *policy.Config {
	return &policy.Config{Version: 1, Profiles: map[string]policy.Profile{"p": prof}}
}

func TestInspectEnvSecretResolvable(t *testing.T) {
	cfg := cfgWith(policy.Profile{Secrets: map[string]string{"FOO": "env:BAR"}})
	rep := Inspect(context.Background(), cfg, fakeProber(map[string]string{"BAR": "s3cr3t"}, false, false, nil, nil))
	r, ok := rowFor(rep.Rows, "secret", "FOO")
	if !ok {
		t.Fatalf("no secret row FOO: %+v", rep.Rows)
	}
	if r.Status != StatusResolvable {
		t.Fatalf("want resolvable, got %q", r.Status)
	}
	if r.Ref != "env:BAR" {
		t.Fatalf("ref must be the source ref, got %q", r.Ref)
	}
}

func TestInspectEnvSecretMissing(t *testing.T) {
	cfg := cfgWith(policy.Profile{Secrets: map[string]string{"FOO": "env:BAR"}})
	rep := Inspect(context.Background(), cfg, fakeProber(map[string]string{}, false, false, nil, nil))
	r, _ := rowFor(rep.Rows, "secret", "FOO")
	if r.Status != StatusMissing {
		t.Fatalf("want missing, got %q", r.Status)
	}
}

func TestInspectOpUnavailableShortCircuits(t *testing.T) {
	calls := 0
	cfg := cfgWith(policy.Profile{Secrets: map[string]string{"FOO": "op://v/i/f"}})
	rep := Inspect(context.Background(), cfg, fakeProber(nil, false, false, nil, &calls))
	r, _ := rowFor(rep.Rows, "secret", "FOO")
	if r.Status != StatusOpUnavailable {
		t.Fatalf("want op-unavailable, got %q", r.Status)
	}
	if calls != 0 {
		t.Fatalf("op unavailable must not attempt resolution; got %d calls", calls)
	}
}

func TestInspectOpSignedOutShortCircuits(t *testing.T) {
	calls := 0
	cfg := cfgWith(policy.Profile{Secrets: map[string]string{"FOO": "op://v/i/f"}})
	rep := Inspect(context.Background(), cfg, fakeProber(nil, true, false, nil, &calls))
	r, _ := rowFor(rep.Rows, "secret", "FOO")
	if r.Status != StatusOpSignedOut {
		t.Fatalf("want op-signed-out, got %q", r.Status)
	}
	if calls != 0 {
		t.Fatalf("signed-out must not attempt resolution; got %d calls", calls)
	}
}

func TestInspectOpResolvable(t *testing.T) {
	cfg := cfgWith(policy.Profile{Secrets: map[string]string{"FOO": "op://v/i/f"}})
	rep := Inspect(context.Background(), cfg, fakeProber(nil, true, true, map[string]bool{"op://v/i/f": true}, nil))
	r, _ := rowFor(rep.Rows, "secret", "FOO")
	if r.Status != StatusResolvable {
		t.Fatalf("want resolvable, got %q", r.Status)
	}
}

func TestInspectSshDeployKeyEphemeral(t *testing.T) {
	cfg := cfgWith(policy.Profile{Credentials: &policy.Credentials{Github: &policy.GithubCreds{}}})
	rep := Inspect(context.Background(), cfg, fakeProber(nil, false, false, nil, nil))
	r, ok := rowFor(rep.Rows, "github", "origin")
	if !ok {
		t.Fatalf("no ssh origin row: %+v", rep.Rows)
	}
	if r.Status != StatusEphemeral {
		t.Fatalf("deploy-key ssh must be ephemeral, got %q", r.Status)
	}
	if r.Ref != "" {
		t.Fatalf("ephemeral key has no source ref, got %q", r.Ref)
	}
}

func TestInspectSshPatProbed(t *testing.T) {
	cfg := cfgWith(policy.Profile{Credentials: &policy.Credentials{Github: &policy.GithubCreds{Mode: "pat", Pat: "env:TOK"}}})
	rep := Inspect(context.Background(), cfg, fakeProber(map[string]string{"TOK": "x"}, false, false, nil, nil))
	r, ok := rowFor(rep.Rows, "github", "origin")
	if !ok {
		t.Fatalf("no ssh row: %+v", rep.Rows)
	}
	if r.Status != StatusResolvable || r.Ref != "env:TOK" {
		t.Fatalf("pat ssh must probe its ref: status=%q ref=%q", r.Status, r.Ref)
	}
}

func TestInspectForgejoEphemeral(t *testing.T) {
	cfg := cfgWith(policy.Profile{Credentials: &policy.Credentials{Forgejo: &policy.ForgejoCreds{}}})
	rep := Inspect(context.Background(), cfg, fakeProber(map[string]string{}, false, false, nil, nil))
	r, ok := rowFor(rep.Rows, "forgejo", "origin")
	if !ok {
		t.Fatalf("no forgejo row: %+v", rep.Rows)
	}
	// The registration token now lives in accounts.cue, resolved per session (specs/0069 T6), so
	// inspect reports forgejo value-free as ephemeral — no policy-level token ref to probe.
	if r.Status != StatusEphemeral || r.Ref != "" {
		t.Fatalf("forgejo readiness must be ephemeral + value-free: status=%q ref=%q", r.Status, r.Ref)
	}
}

func TestInspectMixedRepoAccess(t *testing.T) {
	for _, tc := range []struct {
		name  string
		creds *policy.Credentials
		kind  string
		mode  string
	}{
		{
			name: "github",
			creds: &policy.Credentials{Github: &policy.GithubCreds{Repos: []policy.RepoCred{
				{Repo: "acme/web"}, {Repo: "acme/api", Write: true},
			}}},
			kind: "github", mode: "app",
		},
		{
			name: "forgejo",
			creds: &policy.Credentials{Forgejo: &policy.ForgejoCreds{Repos: []policy.RepoCred{
				{Repo: "acme/web"}, {Repo: "acme/api", Write: true},
			}}},
			kind: "forgejo", mode: "deploy-key",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rep := Inspect(context.Background(), cfgWith(policy.Profile{Credentials: tc.creds}), fakeProber(nil, false, false, nil, nil))
			read, ok := rowFor(rep.Rows, tc.kind, "acme/web")
			if !ok {
				t.Fatalf("missing read-only row: %+v", rep.Rows)
			}
			write, ok := rowFor(rep.Rows, tc.kind, "acme/api")
			if !ok {
				t.Fatalf("missing write row: %+v", rep.Rows)
			}
			if read.Scope != tc.mode+" ro" || write.Scope != tc.mode+" rw" {
				t.Fatalf("mixed access scopes = %q / %q, want %q / %q", read.Scope, write.Scope, tc.mode+" ro", tc.mode+" rw")
			}
		})
	}
}

func TestInspectCloudAmbient(t *testing.T) {
	cfg := cfgWith(policy.Profile{Credentials: &policy.Credentials{
		Aws:  &policy.AwsSso{Profile: "acme"},
		Gcp:  &policy.GcpAdc{},
		Kube: &policy.KubeCluster{Eks: &policy.EksCluster{Name: "c1", Region: "eu-west-1"}},
	}})
	rep := Inspect(context.Background(), cfg, fakeProber(nil, false, false, nil, nil))
	for _, k := range []struct{ kind, name string }{{"aws", "acme"}, {"gcp", "adc"}, {"kube", "c1"}} {
		r, ok := rowFor(rep.Rows, k.kind, k.name)
		if !ok {
			t.Fatalf("no %s row %q: %+v", k.kind, k.name, rep.Rows)
		}
		if r.Status != StatusAmbient {
			t.Fatalf("%s must be ambient, got %q", k.kind, r.Status)
		}
	}
}

// TestInspectNeverLeaksValue is the load-bearing redaction guard: even a resolvable secret's
// value must never appear in any row (rows carry the ref, not the value).
func TestInspectNeverLeaksValue(t *testing.T) {
	const secret = "SUPERSECRETVALUE-abc123"
	cfg := cfgWith(policy.Profile{Secrets: map[string]string{"FOO": "env:BAR"}})
	rep := Inspect(context.Background(), cfg, fakeProber(map[string]string{"BAR": secret}, false, false, nil, nil))
	b, _ := json.Marshal(rep)
	if strings.Contains(string(b), secret) {
		t.Fatalf("secret value leaked into the report: %s", b)
	}
}

func TestInspectOpStateReported(t *testing.T) {
	cfg := cfgWith(policy.Profile{})
	rep := Inspect(context.Background(), cfg, fakeProber(nil, true, true, nil, nil))
	if !rep.Op.Available || !rep.Op.SignedIn {
		t.Fatalf("op state not reported: %+v", rep.Op)
	}
}
