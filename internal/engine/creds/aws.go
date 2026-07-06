package creds

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/freakhill/safeslop/internal/engine/hostexec"
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

// awsEnv renders the short-lived creds as the standard AWS SDK/CLI env vars.
// Env (not a credentials file) so the same values work in host AND inside
// container/vm without path remapping; the run's secret channel keeps them out of
// `docker inspect`/`ps`.
func awsEnv(c awsCreds, region string) []string {
	env := []string{
		"AWS_ACCESS_KEY_ID=" + c.AccessKeyID,
		"AWS_SECRET_ACCESS_KEY=" + c.SecretAccessKey,
	}
	if c.SessionToken != "" {
		env = append(env, "AWS_SESSION_TOKEN="+c.SessionToken)
	}
	if region != "" {
		env = append(env, "AWS_DEFAULT_REGION="+region)
	}
	return env
}

// StageAWS resolves the profile's SSO creds on the host (short-lived) and returns
// them as AWS env vars. No revoke: the creds expire (~1h) and there is nothing
// staged to wipe beyond the env — decay-first.
func StageAWS(ctx context.Context, creds *policy.Credentials, _ string) ([]string, error) {
	if creds == nil || creds.Aws == nil {
		return nil, nil
	}
	argv := awsExportArgv(creds.Aws.Profile)
	cmd, err := hostCommand(ctx, argv, "AWS SSO credentials")
	if err != nil {
		return nil, err
	}
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("aws export-credentials (profile %q; is `aws sso login` current?): helper failed", creds.Aws.Profile)
	}
	c, err := parseAWSProcessCreds(out)
	if err != nil {
		return nil, err
	}
	// Optional scope-first downscope (specs/0027 S5·2): assume RoleArn with an inline session
	// policy using the SSO creds, so the staged creds are bounded to least-privilege even at full
	// TTL. Both fields required together; the role must be assumable by the SSO identity.
	if creds.Aws.RoleArn != "" && creds.Aws.SessionPolicy != "" {
		c, err = assumeRoleDownscope(ctx, c, creds.Aws.RoleArn, creds.Aws.SessionPolicy)
		if err != nil {
			return nil, err
		}
	}
	return awsEnv(c, creds.Aws.Region), nil
}

// awsAssumeRoleArgv builds the `aws sts assume-role` call that downscopes via an inline session
// policy (a session policy can only narrow, never widen, the role's permissions).
func awsAssumeRoleArgv(roleArn, sessionPolicy string) []string {
	return []string{
		"aws", "sts", "assume-role",
		"--role-arn", roleArn,
		"--role-session-name", "safeslop",
		"--policy", sessionPolicy,
		"--output", "json",
	}
}

type awsAssumeRoleResp struct {
	Credentials awsCreds `json:"Credentials"`
}

func parseAWSAssumeRole(out []byte) (awsCreds, error) {
	var r awsAssumeRoleResp
	if err := json.Unmarshal(out, &r); err != nil {
		return awsCreds{}, fmt.Errorf("parse aws assume-role: %w", err)
	}
	if r.Credentials.AccessKeyID == "" || r.Credentials.SecretAccessKey == "" {
		return awsCreds{}, fmt.Errorf("aws assume-role returned no credentials")
	}
	return r.Credentials, nil
}

// assumeRoleDownscope runs `aws sts assume-role` under hostexec's AWS helper env allowlist,
// injecting only base's freshly minted SSO creds so ambient host AWS authority is not inherited.
func assumeRoleDownscope(ctx context.Context, base awsCreds, roleArn, sessionPolicy string) (awsCreds, error) {
	argv := awsAssumeRoleArgv(roleArn, sessionPolicy)
	cmd, err := hostCommand(ctx, argv, "AWS assume-role downscope")
	if err != nil {
		return awsCreds{}, err
	}
	extra := []string{
		"AWS_ACCESS_KEY_ID=" + base.AccessKeyID,
		"AWS_SECRET_ACCESS_KEY=" + base.SecretAccessKey,
	}
	if base.SessionToken != "" {
		extra = append(extra, "AWS_SESSION_TOKEN="+base.SessionToken)
	}
	cmd.Env = hostexec.AppendEnv(cmd.Env, extra...)
	out, err := cmd.Output()
	if err != nil {
		return awsCreds{}, fmt.Errorf("aws sts assume-role (role %q; is it assumable by your SSO identity?): helper failed", roleArn)
	}
	return parseAWSAssumeRole(out)
}
