package creds

import (
	"context"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"

	"github.com/freakhill/safeslop/internal/engine/policy"
)

// gcpTokenArgv builds the ADC access-token call. When scopes are declared they are
// passed as a comma-joined --scopes to downscope the minted token (scope-first
// least-privilege, specs/0026 S5); empty scopes keep ADC's default (broad) scopes.
func gcpTokenArgv(scopes []string) []string {
	argv := []string{"gcloud", "auth", "application-default", "print-access-token"}
	if len(scopes) > 0 {
		argv = append(argv, "--scopes="+strings.Join(scopes, ","))
	}
	return argv
}

// StageGCP mints a short-lived ADC access token on the host and stages ONLY that
// token (the long-lived refresh token is never read or written), exposing it via
// CLOUDSDK_AUTH_ACCESS_TOKEN for the gcloud CLI. No revoke (token expires ~1h).
func StageGCP(ctx context.Context, creds *policy.Credentials, stageDir string) ([]string, error) {
	if creds == nil || creds.Gcp == nil {
		return nil, nil
	}
	argv := gcpTokenArgv(creds.Gcp.Scopes)
	out, err := osexec.CommandContext(ctx, argv[0], argv[1:]...).Output()
	if err != nil {
		return nil, fmt.Errorf("gcloud print-access-token (is ADC set up? run: gcloud auth application-default login): %w", err)
	}
	tok := strings.TrimSpace(string(out))
	if tok == "" {
		return nil, fmt.Errorf("gcloud returned an empty access token")
	}
	if err := os.MkdirAll(stageDir, 0o700); err != nil {
		return nil, err
	}
	tokFile := filepath.Join(stageDir, "gcp-access-token")
	if err := os.WriteFile(tokFile, []byte(tok+"\n"), 0o600); err != nil {
		return nil, err
	}
	return []string{"CLOUDSDK_AUTH_ACCESS_TOKEN=" + tok}, nil
}
