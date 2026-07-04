# 0027 — AWS session-policy downscoping (scope-first creds, review S5·2)

**Goal:** Complete the AWS half of the security review's S5 / H5 ("scope-first, decay-second"):
downscope the staged AWS creds to least-privilege so a full-TTL reuse of a leaked token is bounded
to what the task needed — the symmetric companion to the GCP scopes slice (specs/0026).

**What shipped:** `#AwsSso` gains optional `roleArn` + `sessionPolicy` (both required together).
When set, `StageAWS` runs `aws sts assume-role --role-arn <roleArn> --role-session-name safeslop
--policy <sessionPolicy>` *using the SSO creds*, and stages the **downscoped** creds. A session
policy can only narrow, never widen, the role's permissions, so this is purely least-privilege.
Absent => current behavior (the broad SSO role creds).

```cue
credentials: aws: {
	profile:       "my-sso-profile"
	roleArn:       "arn:aws:iam::123456789012:role/safeslop-readonly"
	sessionPolicy: """{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":["s3:GetObject"],"Resource":"*"}]}"""
}
```

**Constraint:** the role must be assumable by your SSO identity (its trust policy must allow the
assume-role). Opt-in, so existing profiles are unchanged.

**Tests:** `internal/engine/creds/aws_test.go` — `awsAssumeRoleArgv` builder, `parseAWSAssumeRole`
(+ empty-error), and `TestStageAWSDownscopesViaAssumeRole` with a branching fake `aws` proving the
staged creds are the downscoped ones (`DOWN`), not the broad SSO ones. `make check` + a `validate`
smoke pass.

**Remaining S5 follow-on:** `credential_process` delivery (keep creds out of the same-uid process
table) — the env channel is a deliberate uniform-across-tiers choice; changing it is a design fork,
not a clean slice. Scope-first (downscoping at mint) is now complete for both AWS and GCP.
