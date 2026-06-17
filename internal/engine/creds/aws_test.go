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

func TestRenderAWSCredsFileHasSessionToken(t *testing.T) {
	got := renderAWSCredsFile(awsCreds{AccessKeyID: "AKIA", SecretAccessKey: "sek", SessionToken: "tok"}, "eu-west-1")
	for _, want := range []string{"[default]", "aws_access_key_id = AKIA", "aws_secret_access_key = sek", "aws_session_token = tok", "region = eu-west-1"} {
		if !strings.Contains(got, want) {
			t.Fatalf("creds file missing %q:\n%s", want, got)
		}
	}
}

func TestStageAWSWritesScopedFileAndEnv(t *testing.T) {
	binDir := t.TempDir()
	fakeBin(t, binDir, "aws", `{"Version":1,"AccessKeyId":"AKIA","SecretAccessKey":"sek","SessionToken":"tok"}`)
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH")) // fake `aws` wins; stub's `cat` resolves from real PATH

	stage := t.TempDir()
	env, err := StageAWS(context.Background(), &policy.Credentials{Aws: &policy.AwsSso{Profile: "dev", Region: "eu-west-1"}}, stage)
	if err != nil {
		t.Fatalf("StageAWS: %v", err)
	}
	credFile := filepath.Join(stage, "aws-credentials")
	fi, err := os.Stat(credFile)
	if err != nil {
		t.Fatalf("staged file: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("perm = %v want 0600", fi.Mode().Perm())
	}
	body, _ := os.ReadFile(credFile)
	if !strings.Contains(string(body), "aws_session_token = tok") {
		t.Fatalf("staged creds wrong:\n%s", body)
	}
	joined := strings.Join(env, " ")
	if !strings.Contains(joined, "AWS_SHARED_CREDENTIALS_FILE="+credFile) || !strings.Contains(joined, "AWS_PROFILE=default") {
		t.Fatalf("env = %v", env)
	}
}

func TestStageAWSNilIsNoop(t *testing.T) {
	env, err := StageAWS(context.Background(), &policy.Credentials{}, t.TempDir())
	if err != nil || env != nil {
		t.Fatalf("nil aws creds must be a no-op: env=%v err=%v", env, err)
	}
}
