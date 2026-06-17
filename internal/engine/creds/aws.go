package creds

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"

	"github.com/freakhill/safeslop/internal/engine/policy"
)

type awsCreds struct {
	AccessKeyID     string `json:"AccessKeyId"`
	SecretAccessKey string `json:"SecretAccessKey"`
	SessionToken    string `json:"SessionToken"`
}

// awsExportArgv builds the `aws configure export-credentials` call that resolves
// an SSO profile to short-lived role creds (process JSON on stdout).
func awsExportArgv(profile string) []string {
	return []string{"aws", "configure", "export-credentials", "--profile", profile, "--format", "process"}
}

func parseAWSProcessCreds(out []byte) (awsCreds, error) {
	var c awsCreds
	if err := json.Unmarshal(out, &c); err != nil {
		return awsCreds{}, fmt.Errorf("parse aws export-credentials: %w", err)
	}
	if c.AccessKeyID == "" || c.SecretAccessKey == "" {
		return awsCreds{}, fmt.Errorf("aws export-credentials returned no key (SSO session expired? run: aws sso login)")
	}
	return c, nil
}

func renderAWSCredsFile(c awsCreds, region string) string {
	var b strings.Builder
	b.WriteString("[default]\n")
	fmt.Fprintf(&b, "aws_access_key_id = %s\n", c.AccessKeyID)
	fmt.Fprintf(&b, "aws_secret_access_key = %s\n", c.SecretAccessKey)
	if c.SessionToken != "" {
		fmt.Fprintf(&b, "aws_session_token = %s\n", c.SessionToken)
	}
	if region != "" {
		fmt.Fprintf(&b, "region = %s\n", region)
	}
	return b.String()
}

// StageAWS resolves the profile's SSO creds on the host (short-lived), writes a
// 0600 credentials file into stageDir, and returns env pointing the agent at it.
// No revoke: the creds expire (~1h) and stageDir is wiped on exit (decay-first).
func StageAWS(ctx context.Context, creds *policy.Credentials, stageDir string) ([]string, error) {
	if creds == nil || creds.Aws == nil {
		return nil, nil
	}
	argv := awsExportArgv(creds.Aws.Profile)
	out, err := osexec.CommandContext(ctx, argv[0], argv[1:]...).Output()
	if err != nil {
		return nil, fmt.Errorf("aws export-credentials (profile %q; is `aws sso login` current?): %w", creds.Aws.Profile, err)
	}
	c, err := parseAWSProcessCreds(out)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(stageDir, 0o700); err != nil {
		return nil, err
	}
	credFile := filepath.Join(stageDir, "aws-credentials")
	if err := os.WriteFile(credFile, []byte(renderAWSCredsFile(c, creds.Aws.Region)), 0o600); err != nil {
		return nil, err
	}
	return []string{"AWS_SHARED_CREDENTIALS_FILE=" + credFile, "AWS_PROFILE=default"}, nil
}
