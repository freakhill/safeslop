package creds

import (
	"strings"
	"testing"
)

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
