package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/policy"
)

// TestRunProfileStagesSecretsAndNpmrcThenWipes exercises the SP2 lifecycle end
// to end on the host environment: secrets are resolved + injected into the
// child env, a pnpm .npmrc is staged and pointed at via NPM_CONFIG_USERCONFIG,
// and the stage dir is wiped on exit. Hermetic — env: refs only, no live op.
func TestRunProfileStagesSecretsAndNpmrcThenWipes(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("SLOP_IT_SECRET", "topsecret")
	t.Setenv("SLOP_IT_NPMTOK", "npmtok")
	outFile := filepath.Join(ws, "out")

	// The child fails (exit 9) if the .npmrc was not staged, otherwise records
	// the injected secret so the test can confirm env injection.
	script := `test -f "$NPM_CONFIG_USERCONFIG" || exit 9; printf '%s' "$MY_SECRET" > "` + outFile + `"`

	prof := policy.Profile{
		Agent:       "shell",
		Environment: "host",
		Network:     "deny",
		Secrets:     map[string]string{"MY_SECRET": "env:SLOP_IT_SECRET"},
		Credentials: &policy.Credentials{
			Pnpm: []policy.PnpmRegistry{{Host: "registry.npmjs.org", Token: "env:SLOP_IT_NPMTOK"}},
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
	if _, err := os.Stat(filepath.Join(ws, ".slop", "runtime", "probe")); !os.IsNotExist(err) {
		t.Fatalf("stage dir was not wiped on exit: err=%v", err)
	}
}

func TestDoctorReportsGkeAuthPlugin(t *testing.T) {
	report := doctorReport()
	if _, ok := report["gke-gcloud-auth-plugin"]; !ok {
		t.Fatalf("doctor must probe gke-gcloud-auth-plugin: %v keys", report)
	}
}

func TestRunProfileKubeVMGuarded(t *testing.T) {
	prof := policy.Profile{
		Agent:       "claude",
		Environment: "vm",
		Network:     "allow",
		Credentials: &policy.Credentials{Kube: &policy.KubeCluster{Eks: &policy.EksCluster{Name: "prod"}}},
	}
	_, err := runProfile("deploy", prof, []string{"claude"}, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "vm") {
		t.Fatalf("expected a vm-unsupported guard error for kube creds, got: %v", err)
	}
}
