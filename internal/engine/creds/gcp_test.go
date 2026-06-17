package creds

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/policy"
)

func TestGcpTokenArgv(t *testing.T) {
	got := gcpTokenArgv()
	want := []string{"gcloud", "auth", "application-default", "print-access-token"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("argv = %v", got)
	}
}

func TestStageGCPStagesAccessTokenOnly(t *testing.T) {
	binDir := t.TempDir()
	fakeBin(t, binDir, "gcloud", "ya29.ACCESS_TOKEN_VALUE") // fakeBin from aws_test.go (same package)
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	stage := t.TempDir()
	env, err := StageGCP(context.Background(), &policy.Credentials{Gcp: &policy.GcpAdc{}}, stage)
	if err != nil {
		t.Fatalf("StageGCP: %v", err)
	}
	tokFile := filepath.Join(stage, "gcp-access-token")
	body, err := os.ReadFile(tokFile)
	if err != nil {
		t.Fatalf("token file: %v", err)
	}
	if strings.TrimSpace(string(body)) != "ya29.ACCESS_TOKEN_VALUE" {
		t.Fatalf("token body = %q", body)
	}
	if strings.Contains(string(body), "refresh_token") {
		t.Fatal("refresh_token must never be staged")
	}
	if fi, _ := os.Stat(tokFile); fi.Mode().Perm() != 0o600 {
		t.Fatalf("token file not 0600")
	}
	if !strings.Contains(strings.Join(env, " "), "CLOUDSDK_AUTH_ACCESS_TOKEN=ya29.ACCESS_TOKEN_VALUE") {
		t.Fatalf("env = %v", env)
	}
}

func TestStageGCPNilIsNoop(t *testing.T) {
	env, err := StageGCP(context.Background(), &policy.Credentials{}, t.TempDir())
	if err != nil || env != nil {
		t.Fatalf("nil gcp creds must be a no-op: env=%v err=%v", env, err)
	}
}
