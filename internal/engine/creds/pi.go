package creds

import (
	"errors"
	"time"

	"github.com/freakhill/safeslop/internal/engine/policy"
	engsession "github.com/freakhill/safeslop/internal/engine/session"
)

const (
	PiOAuthSourceMissing       = "pi_oauth_source_missing"
	PiOAuthSourceUnsafe        = "pi_oauth_source_unsafe"
	PiOAuthSourceBusy          = "pi_oauth_source_busy"
	PiOAuthSourceMalformed     = "pi_oauth_source_malformed"
	PiOAuthProviderMissing     = "pi_oauth_provider_missing"
	PiOAuthAuthTypeUnsupported = "pi_oauth_auth_type_unsupported"
	PiOAuthExpired             = "pi_oauth_expired"
	PiOAuthNearExpiry          = "pi_oauth_near_expiry"
	PiOAuthStageFailed         = "pi_oauth_stage_failed"
)

type PiOAuthStage struct {
	ExpiresAt time.Time
}

type PiOAuthError struct {
	failure engsession.Failure
}

func (e *PiOAuthError) Error() string               { return e.failure.Summary }
func (e *PiOAuthError) Failure() engsession.Failure { return e.failure }
func (e *PiOAuthError) Code() string                { return e.failure.Code }

func newPiOAuthError(code string) *PiOAuthError {
	return &PiOAuthError{failure: engsession.Failure{
		Version: 1, Phase: "credential", Code: code, Required: true,
		Summary: "Pi OAuth access could not be staged.",
		Action:  "Refresh the host Pi login, then start a new session.",
	}}
}

func PiOAuthErrorCode(err error) string {
	var target *PiOAuthError
	if errors.As(err, &target) {
		return target.Code()
	}
	return ""
}

var (
	piOAuthNow       = time.Now
	piOAuthSleep     = time.Sleep
	piOAuthAfterRead func(attempt int)
)

// StagePiOAuth will extract and stage the narrow access-only snapshot specified
// by specs/0113. The RED contract tests land before its implementation.
func StagePiOAuth(_ *policy.PiCreds, _ string) (PiOAuthStage, error) {
	return PiOAuthStage{}, newPiOAuthError(PiOAuthStageFailed)
}
