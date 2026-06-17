package creds

import (
	"encoding/json"
	"fmt"
	"strings"
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
