package creds

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/policy"
)

// fakeBin writes an executable shell stub named `name` into dir that prints `stdout`.
func fakeBin(t *testing.T, dir, name, stdout string) {
	t.Helper()
	p := filepath.Join(dir, name)
	script := "#!/bin/sh\ncat <<'EOF'\n" + stdout + "\nEOF\n"
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestAwsExportArgv(t *testing.T) {
	got := awsExportArgv("dev-admin")
	want := []string{"aws", "configure", "export-credentials", "--profile", "dev-admin", "--format", "process"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("argv = %v", got)
	}
}

func TestParseAWSProcessCreds(t *testing.T) {
	// the documented shape of `aws configure export-credentials --format process`
	out := `{"Version":1,"AccessKeyId":"AKIA","SecretAccessKey":"sek","SessionToken":"tok","Expiration":"2026-06-17T12:00:00Z"}`
	c, err := parseAWSProcessCreds([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	if c.AccessKeyID != "AKIA" || c.SecretAccessKey != "sek" || c.SessionToken != "tok" {
		t.Fatalf("parsed = %+v", c)
	}
}

func TestParseAWSProcessCredsEmptyErrors(t *testing.T) {
	if _, err := parseAWSProcessCreds([]byte(`{"Version":1}`)); err == nil {
		t.Fatal("expected error when no access key (SSO expired)")
	}
}

func TestAwsEnv(t *testing.T) {
	full := strings.Join(awsEnv(awsCreds{AccessKeyID: "AKIA", SecretAccessKey: "sek", SessionToken: "tok"}, "eu-west-1"), "\n")
	for _, want := range []string{"AWS_ACCESS_KEY_ID=AKIA", "AWS_SECRET_ACCESS_KEY=sek", "AWS_SESSION_TOKEN=tok", "AWS_DEFAULT_REGION=eu-west-1"} {
		if !strings.Contains(full, want) {
			t.Fatalf("env missing %q:\n%s", want, full)
		}
	}
	// no session token / no region → only the two required vars.
	if got := awsEnv(awsCreds{AccessKeyID: "A", SecretAccessKey: "S"}, ""); len(got) != 2 {
		t.Fatalf("want 2 vars when no token/region, got %v", got)
	}
}

func TestStageAWSReturnsEnv(t *testing.T) {
	binDir := t.TempDir()
	fakeBin(t, binDir, "aws", `{"Version":1,"AccessKeyId":"AKIA","SecretAccessKey":"sek","SessionToken":"tok"}`)
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH")) // fake `aws` wins; stub's `cat` resolves from real PATH

	env, err := StageAWS(context.Background(), &policy.Credentials{Aws: &policy.AwsSso{Profile: "dev", Region: "eu-west-1"}}, t.TempDir())
	if err != nil {
		t.Fatalf("StageAWS: %v", err)
	}
	joined := strings.Join(env, "\n")
	for _, want := range []string{"AWS_ACCESS_KEY_ID=AKIA", "AWS_SECRET_ACCESS_KEY=sek", "AWS_SESSION_TOKEN=tok", "AWS_DEFAULT_REGION=eu-west-1"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("env missing %q: %v", want, env)
		}
	}
}

func TestStageAWSNilIsNoop(t *testing.T) {
	env, err := StageAWS(context.Background(), &policy.Credentials{}, t.TempDir())
	if err != nil || env != nil {
		t.Fatalf("nil aws creds must be a no-op: env=%v err=%v", env, err)
	}
}
