package creds

import (
	"context"
	"fmt"
	"time"

	"github.com/freakhill/safeslop/internal/engine/policy"
	"github.com/freakhill/safeslop/internal/engine/userconfig"
)

// StartGithubCredentialLease attaches the host-only renewal loop to an already staged App-token
// batch. It reloads account links only in host memory for renewal; neither links nor private keys
// cross the run boundary. PAT credentials deliberately have no renewal path.
func StartGithubCredentialLease(stagedAt time.Time, cfg *policy.Credentials, stageDir string, onChange func(LeaseSnapshot)) (*Lease, error) {
	if cfg == nil || cfg.Github == nil || cfg.Github.Mode == "pat" {
		return nil, nil
	}
	expiresAt, ok, err := GithubCredsExpiry(stageDir)
	if err != nil || !ok {
		return nil, err
	}
	var horizon *time.Time
	if cfg.Github.Ttl != "" {
		ttl, err := time.ParseDuration(cfg.Github.Ttl)
		if err != nil || ttl <= 0 {
			return nil, fmt.Errorf("github credential lease: invalid ttl")
		}
		h := stagedAt.Add(ttl).UTC()
		horizon = &h
	}
	accountsPath, err := userconfig.DefaultAccountsPath()
	if err != nil {
		return nil, err
	}
	accounts, err := userconfig.LoadAccounts(accountsPath)
	if err != nil {
		return nil, err
	}
	return StartLease(LeaseConfig{
		ExpiresAt: expiresAt,
		Horizon:   horizon,
		OnChange:  onChange,
		Renew: func(ctx context.Context) (time.Time, error) {
			if _, err := StageGithub(ctx, cfg, stageDir, accounts); err != nil {
				return time.Time{}, err
			}
			exp, ok, err := GithubCredsExpiry(stageDir)
			if err != nil || !ok {
				return time.Time{}, fmt.Errorf("github credential renewal produced no expiry")
			}
			return exp, nil
		},
	})
}
