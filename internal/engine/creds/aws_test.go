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

func TestAwsAssumeRoleArgv(t *testing.T) {
	got := awsAssumeRoleArgv("arn:aws:iam::123:role/downscope", `{"Version":"2012-10-17","Statement":[]}`)
	joined := strings.Join(got, " ")
	for _, want := range []string{"sts assume-role", "--role-arn arn:aws:iam::123:role/downscope", "--role-session-name safeslop", "--policy", "--output json"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("assume-role argv missing %q: %v", want, got)
		}
	}
}

func TestParseAWSAssumeRole(t *testing.T) {
	out := `{"Credentials":{"AccessKeyId":"AKIA2","SecretAccessKey":"sek2","SessionToken":"tok2","Expiration":"2026-06-17T12:00:00Z"}}`
	c, err := parseAWSAssumeRole([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	if c.AccessKeyID != "AKIA2" || c.SecretAccessKey != "sek2" || c.SessionToken != "tok2" {
		t.Fatalf("parsed = %+v", c)
	}
}

func TestParseAWSAssumeRoleEmptyErrors(t *testing.T) {
	if _, err := parseAWSAssumeRole([]byte(`{"Credentials":{}}`)); err == nil {
		t.Fatal("expected error when assume-role returns no credentials")
	}
}

// TestStageAWSDownscopesViaAssumeRole: with roleArn+sessionPolicy, the staged creds are the
// downscoped assume-role creds, not the broad SSO creds (specs/0027 S5·2).
func TestStageAWSDownscopesViaAssumeRole(t *testing.T) {
	binDir := t.TempDir()
	// A fake `aws` that branches on the subcommand ($2): export-credentials then assume-role.
	script := `#!/bin/sh
if [ "$2" = "export-credentials" ]; then
  echo '{"Version":1,"AccessKeyId":"SSO","SecretAccessKey":"ssosek","SessionToken":"ssotok"}'
elif [ "$2" = "assume-role" ]; then
  echo '{"Credentials":{"AccessKeyId":"DOWN","SecretAccessKey":"downsek","SessionToken":"downtok"}}'
fi
`
	if err := os.WriteFile(filepath.Join(binDir, "aws"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	env, err := StageAWS(context.Background(), &policy.Credentials{Aws: &policy.AwsSso{
		Profile:       "dev",
		RoleArn:       "arn:aws:iam::123:role/downscope",
		SessionPolicy: `{"Version":"2012-10-17","Statement":[]}`,
	}}, t.TempDir())
	if err != nil {
		t.Fatalf("StageAWS: %v", err)
	}
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "AWS_ACCESS_KEY_ID=DOWN") {
		t.Fatalf("expected downscoped creds, got %v", env)
	}
	if strings.Contains(joined, "SSO") {
		t.Fatalf("broad SSO creds must be replaced by the downscoped ones: %v", env)
	}
}
