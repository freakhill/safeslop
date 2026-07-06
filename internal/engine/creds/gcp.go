package creds

import (
	"context"
	"fmt"
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

// StageGCP mints a short-lived ADC access token on the host and exposes it only
// through CLOUDSDK_AUTH_ACCESS_TOKEN for the gcloud CLI. The long-lived refresh
// token is never read or written, and no dead token file is staged.
// No revoke (token expires ~1h).
func StageGCP(ctx context.Context, creds *policy.Credentials, stageDir string) ([]string, error) {
	if creds == nil || creds.Gcp == nil {
		return nil, nil
	}
	argv := gcpTokenArgv(creds.Gcp.Scopes)
	cmd, err := hostCommand(ctx, argv, "GCP ADC access token")
	if err != nil {
		return nil, err
	}
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("gcloud print-access-token (is ADC set up? run: gcloud auth application-default login): helper failed")
	}
	tok := strings.TrimSpace(string(out))
	if tok == "" {
		return nil, fmt.Errorf("gcloud returned an empty access token")
	}
	return []string{"CLOUDSDK_AUTH_ACCESS_TOKEN=" + tok}, nil
}
