package creds

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/policy"
)

func TestGcpTokenArgv(t *testing.T) {
	got := gcpTokenArgv(nil)
	want := []string{"gcloud", "auth", "application-default", "print-access-token"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("argv = %v", got)
	}
}

func TestGcpTokenArgvScopes(t *testing.T) {
	// scope-first: declared scopes downscope the minted token (specs/0026 S5).
	got := gcpTokenArgv([]string{
		"https://www.googleapis.com/auth/devstorage.read_only",
		"https://www.googleapis.com/auth/bigquery.readonly",
	})
	joined := strings.Join(got, " ")
	want := "--scopes=https://www.googleapis.com/auth/devstorage.read_only,https://www.googleapis.com/auth/bigquery.readonly"
	if !strings.Contains(joined, want) {
		t.Fatalf("scoped argv must carry comma-joined --scopes, got %v", got)
	}
}

func TestStageGCPExposesAccessTokenOnlyInEnv(t *testing.T) {
	binDir := t.TempDir()
	fakeBin(t, binDir, "gcloud", "ya29.ACCESS_TOKEN_VALUE") // fakeBin from aws_test.go (same package)
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	stage := filepath.Join(t.TempDir(), "stage")
	env, err := StageGCP(context.Background(), &policy.Credentials{Gcp: &policy.GcpAdc{}}, stage)
	if err != nil {
		t.Fatalf("StageGCP: %v", err)
	}
	if !strings.Contains(strings.Join(env, " "), "CLOUDSDK_AUTH_ACCESS_TOKEN=ya29.ACCESS_TOKEN_VALUE") {
		t.Fatalf("env = %v", env)
	}
	tokFile := filepath.Join(stage, "gcp-access-token")
	if _, err := os.Stat(tokFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("GCP access token must not be staged in a dead file; stat err=%v", err)
	}
	if _, err := os.Stat(stage); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("StageGCP should not create a stage dir for env-only delivery; stat err=%v", err)
	}
}

func TestStageGCPNilIsNoop(t *testing.T) {
	env, err := StageGCP(context.Background(), &policy.Credentials{}, t.TempDir())
	if err != nil || env != nil {
		t.Fatalf("nil gcp creds must be a no-op: env=%v err=%v", env, err)
	}
}
