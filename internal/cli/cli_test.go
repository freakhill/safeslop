package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/container/runtime"
	"github.com/freakhill/safeslop/internal/engine/creds"
	"github.com/freakhill/safeslop/internal/engine/hostexec"
	"github.com/freakhill/safeslop/internal/engine/policy"
)

// TestRunProfileStagesSecretsAndNpmrcThenWipes exercises the SP2 lifecycle end
// to end on the host environment: secrets are resolved + injected into the
// child env, a pnpm .npmrc is staged and pointed at via NPM_CONFIG_USERCONFIG,
// and the stage dir is wiped on exit. Hermetic — env: refs only, no live op.
func TestRunProfileStagesSecretsAndNpmrcThenWipes(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("SAFESLOP_IT_SECRET", "topsecret")
	t.Setenv("SAFESLOP_IT_NPMTOK", "npmtok")
	outFile := filepath.Join(ws, "out")

	// The child fails (exit 9) if the .npmrc was not staged, otherwise records
	// the injected secret so the test can confirm env injection.
	script := `test -f "$NPM_CONFIG_USERCONFIG" || exit 9; printf '%s' "$MY_SECRET" > "` + outFile + `"`

	prof := policy.Profile{
		Agent:       "shell",
		Environment: "host",
		Network:     "deny",
		Secrets:     map[string]string{"MY_SECRET": "env:SAFESLOP_IT_SECRET"},
		Credentials: &policy.Credentials{
			Pnpm: []policy.PnpmRegistry{{Host: "registry.npmjs.org", Token: "env:SAFESLOP_IT_NPMTOK"}},
		},
	}

	code, err := runProfile("probe", prof, []string{"/bin/sh", "-c", script}, ws)
	if err != nil {
		t.Fatalf("runProfile: %v", err)
	}
	if code != 0 {
		t.Fatalf("exit code = %d (9 means NPM_CONFIG_USERCONFIG/.npmrc was not staged)", code)
	}
	got, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read out: %v", err)
	}
	if string(got) != "topsecret" {
		t.Fatalf("secret not injected into child env: got %q", string(got))
	}
	if _, err := os.Stat(filepath.Join(ws, ".safeslop", "runtime", "probe")); !os.IsNotExist(err) {
		t.Fatalf("stage dir was not wiped on exit: err=%v", err)
	}
}

func TestDoctorUsesOwnedRuntimeAndCredentialProbes(t *testing.T) {
	d := defaultDependencies()
	opProbed, runtimeProbed := false, false
	d.credsProber = func() creds.Prober {
		return creds.Prober{OpSignedIn: func(context.Context) bool {
			opProbed = true
			return true
		}}
	}
	d.detectRuntime = func(runtime.NetworkPolicy) (runtime.Engine, error) {
		runtimeProbed = true
		return runtime.PodmanEngine{}, nil
	}

	report := doctorReportWithDeps(d)
	if !opProbed || !runtimeProbed {
		t.Fatalf("doctor bypassed owned probes: op=%t runtime=%t", opProbed, runtimeProbed)
	}
	if report["1password-signedin"].(map[string]any)["present"] != true || report["container-runtime"].(map[string]any)["present"] != true {
		t.Fatalf("doctor report did not use probe results: %+v", report)
	}
}

func TestDoctorReportsGkeAuthPlugin(t *testing.T) {
	report := doctorReport()
	if _, ok := report["gke-gcloud-auth-plugin"]; !ok {
		t.Fatalf("doctor must probe gke-gcloud-auth-plugin: %v keys", report)
	}
}

func TestDoctorReportsGh(t *testing.T) {
	if _, ok := doctorReport()["gh"]; !ok {
		t.Fatalf("doctor must probe gh")
	}
}

func TestDoctorReportsPresentWithSameInodeAliases(t *testing.T) {
	const (
		first = "/Applications/OrbStack.app/Contents/MacOS/docker"
		alias = "/usr/local/bin/docker"
	)
	d := defaultDependencies()
	d.doctorHostExec = func() *hostexec.Resolver {
		return hostexec.New(cliFakeHostEnv{
			all: map[string][]string{"docker": {first, alias}},
			sameFile: func(a, b string) (bool, error) {
				return (a == first || a == alias) && (b == first || b == alias), nil
			},
		})
	}

	row := doctorReportWithDeps(d)["docker"].(map[string]any)
	if row["present"] != true || row["path"] != first {
		t.Fatalf("alias-only docker should be present at the first path: %+v", row)
	}
	if got, ok := row["alias_paths"].([]string); !ok || len(got) != 1 || got[0] != alias {
		t.Fatalf("alias_paths not surfaced: %+v", row)
	}
	if _, ok := row["shadowed_paths"]; ok {
		t.Fatalf("alias-only docker must not report a shadow: %+v", row)
	}
}

func TestDoctorReportsAliasesPlusDistinctShadow(t *testing.T) {
	const (
		first  = "/safe/bin/docker"
		alias  = "/usr/local/bin/docker"
		shadow = "/opt/homebrew/bin/docker"
	)
	d := defaultDependencies()
	d.doctorHostExec = func() *hostexec.Resolver {
		return hostexec.New(cliFakeHostEnv{
			all: map[string][]string{"docker": {first, alias, shadow}},
			sameFile: func(a, b string) (bool, error) {
				return (a == first || a == alias) && (b == first || b == alias), nil
			},
		})
	}

	row := doctorReportWithDeps(d)["docker"].(map[string]any)
	if row["present"] != false {
		t.Fatalf("mixed docker paths must not be present: %+v", row)
	}
	if got, ok := row["alias_paths"].([]string); !ok || len(got) != 1 || got[0] != alias {
		t.Fatalf("alias_paths not surfaced: %+v", row)
	}
	if got, ok := row["shadowed_paths"].([]string); !ok || len(got) != 1 || got[0] != shadow {
		t.Fatalf("shadowed_paths not surfaced: %+v", row)
	}
}

func TestDoctorReportsUnverifiedIdentity(t *testing.T) {
	d := defaultDependencies()
	d.doctorHostExec = func() *hostexec.Resolver {
		return hostexec.New(cliFakeHostEnv{
			all: map[string][]string{"docker": {"/safe/bin/docker", "/usr/local/bin/docker"}},
			sameFile: func(string, string) (bool, error) {
				return false, os.ErrPermission
			},
		})
	}

	row := doctorReportWithDeps(d)["docker"].(map[string]any)
	if row["present"] != false || row["identity_unverified"] != true {
		t.Fatalf("identity failure must be unavailable and explicitly unverified: %+v", row)
	}
	if _, ok := row["shadowed_paths"]; ok {
		t.Fatalf("identity failure must not be mislabeled as a shadow: %+v", row)
	}
}

func TestDoctorReportsShadowedHelperWithoutMarkingPresent(t *testing.T) {
	d := defaultDependencies()
	d.doctorHostExec = func() *hostexec.Resolver {
		return hostexec.New(cliFakeHostEnv{path: "/safe/bin:/other/bin", all: map[string][]string{
			"git": {"/safe/bin/git", "/other/bin/git"},
		}})
	}

	row, ok := doctorReportWithDeps(d)["git"].(map[string]any)
	if !ok {
		t.Fatalf("doctor git row has unexpected shape")
	}
	if row["present"] != false || row["path"] != "/safe/bin/git" {
		t.Fatalf("shadowed git should be marked unavailable with winner path: %+v", row)
	}
	all, ok := row["all_paths"].([]string)
	if !ok || len(all) != 2 || all[0] != "/safe/bin/git" || all[1] != "/other/bin/git" {
		t.Fatalf("all_paths not surfaced: %+v", row)
	}
	shadowed, ok := row["shadowed_paths"].([]string)
	if !ok || len(shadowed) != 1 || shadowed[0] != "/other/bin/git" {
		t.Fatalf("shadowed_paths not surfaced: %+v", row)
	}
}

// The pivot narrows the supported coding agents to Claude Code and Pi; doctor must
// probe those and must not regrow probes for the dropped agent CLIs. The dropped
// names are kept out of source here (the denylist guards their reappearance); the
// agentseed/agentargv negative tests prove rejection.
func TestDoctorProbesSupportedAgentsOnly(t *testing.T) {
	report := doctorReport()
	for _, want := range []string{"claude", "pi"} {
		if _, ok := report[want]; !ok {
			t.Fatalf("doctor must probe supported agent %q: %v keys", want, report)
		}
	}
}

func TestServeRemovedFromRoot(t *testing.T) {
	root := newRoot()
	for _, c := range root.Commands() {
		if c.Name() == "serve" {
			t.Fatal("safeslop serve must stay removed with the old UI control plane")
		}
	}
}

func TestLaunchRegistered(t *testing.T) {
	if cmdLaunch().Name() != "launch" {
		t.Fatal("launch command missing")
	}
}

// TestInstallUninstallRemovedFromRoot pins specs/0066 D1: the self-installer surface is gone, so neither
// `safeslop install` nor `safeslop uninstall` may be registered on the root command.
func TestInstallUninstallRemovedFromRoot(t *testing.T) {
	root := newRoot()
	for _, c := range root.Commands() {
		if c.Name() == "install" || c.Name() == "uninstall" {
			t.Fatalf("safeslop %s must stay removed after the ambient-runtime pivot (specs/0066)", c.Name())
		}
	}
}

func TestGcCommandRegistered(t *testing.T) {
	root := newRoot()
	for _, c := range root.Commands() {
		if c.Name() == "gc" {
			return
		}
	}
	t.Fatal("safeslop gc command missing")
}

func TestLaunchProfileRejectsBadName(t *testing.T) {
	_, err := launchProfile("bad; rm -rf ~", "")
	if err == nil || !strings.Contains(err.Error(), "invalid profile") {
		t.Fatalf("malicious profile name must be rejected before any spawn: %v", err)
	}
}
