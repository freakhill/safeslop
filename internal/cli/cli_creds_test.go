package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/policy"
)

func fakeBin(t *testing.T, dir, name, stdout string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\ncat <<'EOF'\n"+stdout+"\nEOF\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// A host-mode profile with aws+gcp creds stages both into the child's env, and the
// stage (with the GCP token file) is wiped on exit. The child never reads host
// ~/.aws — it gets the short-lived SSO creds we resolved.
func TestRunProfileStagesCloudCredsThenWipes(t *testing.T) {
	binDir := t.TempDir()
	fakeBin(t, binDir, "aws", `{"Version":1,"AccessKeyId":"AKIA","SecretAccessKey":"sek","SessionToken":"tok"}`)
	fakeBin(t, binDir, "gcloud", "ya29.TOKEN")
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	ws := t.TempDir()
	out := filepath.Join(ws, "seen")
	script := `printf '%s\n%s\n' "$AWS_ACCESS_KEY_ID" "$CLOUDSDK_AUTH_ACCESS_TOKEN" > "` + out + `"`

	prof := policy.Profile{
		Agent: "shell", Environment: "host", Network: "deny",
		Credentials: &policy.Credentials{Aws: &policy.AwsSso{Profile: "dev"}, Gcp: &policy.GcpAdc{}},
	}
	code, err := runProfile("cloud", prof, []string{"/bin/sh", "-c", script}, ws)
	if err != nil || code != 0 {
		t.Fatalf("runProfile code=%d err=%v", code, err)
	}
	got, _ := os.ReadFile(out)
	if !strings.Contains(string(got), "AKIA") {
		t.Fatalf("child did not get AWS key: %q", got)
	}
	if !strings.Contains(string(got), "ya29.TOKEN") {
		t.Fatalf("child did not get GCP token: %q", got)
	}
	if _, err := os.Stat(filepath.Join(ws, ".slop", "runtime", "cloud")); !os.IsNotExist(err) {
		t.Fatalf("stage (with gcp token file) not wiped: %v", err)
	}
}
