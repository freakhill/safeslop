package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/creds"
)

const credsCue = `package safeslop

safeslop: profiles: {
	app: {
		agent: "claude"
		environment: "container"
		network: "deny"
		secrets: {TOKEN: "env:APP_TOKEN"}
		credentials: {github: {}, aws: {profile: "acme", region: "eu-west-1"}}
	}
	other: {
		agent: "pi"
		environment: "host"
		network: "deny"
		secrets: {K: "env:OTHER_K"}
	}
}
`

func writeCredsCue(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "safeslop.cue"), []byte(credsCue), 0o600); err != nil {
		t.Fatal(err)
	}
}

// withFakeProber swaps the CLI's credential prober for a hermetic one (no `op`, controlled env),
// so `creds` tests never shell out and are deterministic regardless of the host's 1Password state.
func withFakeProber(t *testing.T, env map[string]string) *dependencies {
	t.Helper()
	d := defaultDependencies()
	d.credsProber = func() creds.Prober {
		return creds.Prober{
			OpAvailable: func() bool { return false },
			OpSignedIn:  func(context.Context) bool { return false },
			LookupEnv:   func(n string) (string, bool) { v, ok := env[n]; return v, ok },
			ResolveOp:   func(context.Context, string) error { return nil },
		}
	}
	return d
}

func credRows(t *testing.T, data map[string]any) []map[string]any {
	t.Helper()
	raw, ok := data["credentials"].([]any)
	if !ok {
		t.Fatalf("credentials not an array: %#v", data["credentials"])
	}
	out := make([]map[string]any, len(raw))
	for i, r := range raw {
		out[i] = r.(map[string]any)
	}
	return out
}

func findRow(rows []map[string]any, kind, name string) map[string]any {
	for _, r := range rows {
		if r["kind"] == kind && r["name"] == name {
			return r
		}
	}
	return nil
}

func TestCredsListEmitsContractEnvelope(t *testing.T) {
	ws := t.TempDir()
	writeCredsCue(t, ws)
	d := withFakeProber(t, map[string]string{"APP_TOKEN": "x", "OTHER_K": "y"})
	out, err := runRootForTestWithDeps(t, ws, d, "creds", "list", "--output", "json")
	if err != nil {
		t.Fatalf("creds list: %v\nout=%s", err, out)
	}
	env := parseEnvelopeForTest(t, out)
	if !env.OK {
		t.Fatalf("error envelope: %+v", env.Errors)
	}
	if env.Data["config"] == "" {
		t.Fatalf("missing config path: %#v", env.Data)
	}
	rows := credRows(t, env.Data)
	if r := findRow(rows, "secret", "TOKEN"); r == nil || r["status"] != "resolvable" {
		t.Fatalf("secret TOKEN not resolvable: %#v", r)
	}
	if r := findRow(rows, "github", "origin"); r == nil || r["status"] != "ephemeral" {
		t.Fatalf("ssh not ephemeral: %#v", r)
	}
	if r := findRow(rows, "aws", "acme"); r == nil || r["status"] != "ambient" {
		t.Fatalf("aws not ambient: %#v", r)
	}
}

func TestCredsShowScopesToProfile(t *testing.T) {
	ws := t.TempDir()
	writeCredsCue(t, ws)
	d := withFakeProber(t, map[string]string{"APP_TOKEN": "x", "OTHER_K": "y"})
	out, err := runRootForTestWithDeps(t, ws, d, "creds", "show", "other", "--output", "json")
	if err != nil {
		t.Fatalf("creds show: %v\nout=%s", err, out)
	}
	env := parseEnvelopeForTest(t, out)
	if !env.OK {
		t.Fatalf("error envelope: %+v", env.Errors)
	}
	rows := credRows(t, env.Data)
	if len(rows) == 0 {
		t.Fatalf("expected other's secret row")
	}
	for _, r := range rows {
		if r["profile"] != "other" {
			t.Fatalf("show leaked another profile's row: %#v", r)
		}
	}
}

func TestCredsShowUnknownProfileErrors(t *testing.T) {
	ws := t.TempDir()
	writeCredsCue(t, ws)
	d := withFakeProber(t, nil)
	out, _ := runRootForTestWithDeps(t, ws, d, "creds", "show", "nope", "--output", "json")
	env := parseEnvelopeForTest(t, out)
	if env.OK {
		t.Fatalf("expected error envelope for unknown profile, got %#v", env.Data)
	}
}

func TestCredsListRequiresJSON(t *testing.T) {
	ws := t.TempDir()
	writeCredsCue(t, ws)
	if _, err := runRootForTest(t, ws, "creds", "list"); err == nil {
		t.Fatalf("expected error without --output json")
	}
}

func TestCredsGCRequiresHostRepoAndYes(t *testing.T) {
	ws := t.TempDir()
	cases := []struct {
		name string
		args []string
		want string
	}{
		{name: "host", args: []string{"creds", "gc", "--repo", "acme/web", "--yes"}, want: "--host"},
		{name: "repo", args: []string{"creds", "gc", "--host", "forge.example"}, want: "--repo"},
		{name: "default dry run", args: []string{"creds", "gc", "--host", "forge.example", "--repo", "acme/web"}, want: "no Forgejo account link"},
		{name: "exclusive flags", args: []string{"creds", "gc", "--host", "forge.example", "--repo", "acme/web", "--yes", "--dry-run"}, want: "mutually exclusive"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := runRootForTest(t, ws, tc.args...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}
