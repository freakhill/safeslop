package cli

import (
	"context"
	"slices"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/policy"
)

func TestStageProfileResolvesEnvSecret(t *testing.T) {
	t.Setenv("TEST_SAFESLOP_SECRET", "s3cr3t")
	prof := policy.Profile{Secrets: map[string]string{"FOO": "env:TEST_SAFESLOP_SECRET"}}
	secretEnv, pathEnv, err := stageProfile(context.Background(), prof, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(secretEnv, "FOO=s3cr3t") {
		t.Fatalf("secretEnv missing the resolved secret: %v", secretEnv)
	}
	if len(pathEnv) != 0 {
		t.Fatalf("no credentials → pathEnv must be empty: %v", pathEnv)
	}
}
