package creds

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/freakhill/safeslop/internal/engine/hostpath"
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

	piOAuthMaxAccessBytes = 64 << 10
	piOAuthMinHeadroom    = 15 * time.Minute
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
	summary := "Pi OAuth access could not be staged."
	action := "Refresh the host Pi login, then start a new session."
	switch code {
	case PiOAuthSourceMissing:
		summary = "The host Pi authentication store is unavailable."
		action = "Log in with host Pi, then start a new session."
	case PiOAuthSourceUnsafe:
		summary = "The host Pi authentication store failed safety checks."
		action = "Repair its ownership, permissions, links, or file type, then retry."
	case PiOAuthSourceBusy:
		summary = "The host Pi authentication store is being updated."
		action = "Wait for the host Pi update to finish, then retry."
	case PiOAuthSourceMalformed:
		summary = "The host Pi authentication store is malformed."
	case PiOAuthProviderMissing:
		summary = "The required host Pi OAuth provider is not logged in."
	case PiOAuthAuthTypeUnsupported:
		summary = "The required host Pi provider is not using OAuth."
		action = "Log in to the required provider with host Pi OAuth, then retry."
	case PiOAuthExpired:
		summary = "The host Pi OAuth access has expired."
	case PiOAuthNearExpiry:
		summary = "The host Pi OAuth access expires too soon for a new session."
	case PiOAuthStageFailed:
		summary = "The Pi OAuth access snapshot could not be written."
		action = "Check the local staging area, then start a new session."
	}
	return &PiOAuthError{failure: engsession.Failure{
		Version: 1, Phase: "credential", Code: code, Required: true,
		Summary: summary, Action: action,
	}}
}

func PiOAuthErrorCode(err error) string {
	var target *PiOAuthError
	if errors.As(err, &target) {
		return target.Code()
	}
	return ""
}

var piOAuthNow = time.Now

// StagePiOAuth snapshots only the selected provider's access bearer into a
// synthetic Pi auth file. It never copies refresh/account metadata or writes to
// the host store.
func StagePiOAuth(pi *policy.PiCreds, stageDir string) (PiOAuthStage, error) {
	if pi == nil || pi.Provider != "openai-codex" || pi.Model != "gpt-5.6-luna" {
		return PiOAuthStage{}, newPiOAuthError(PiOAuthStageFailed)
	}
	body, err := readPiOAuthSource()
	if err != nil {
		return PiOAuthStage{}, err
	}
	defer zeroPiOAuthBytes(body)
	access, expiresAt, err := parsePiOAuthAccess(body, piOAuthNow())
	if err != nil {
		return PiOAuthStage{}, err
	}
	// Recheck immediately before materializing the snapshot so work done during
	// safe read/parse cannot consume the minimum launch headroom unnoticed.
	if err := validatePiOAuthExpiry(expiresAt, piOAuthNow()); err != nil {
		return PiOAuthStage{}, err
	}
	if err := writePiOAuthSnapshot(stageDir, pi.Provider, access); err != nil {
		return PiOAuthStage{}, newPiOAuthError(PiOAuthStageFailed)
	}
	return PiOAuthStage{ExpiresAt: expiresAt}, nil
}

func readPiOAuthSource() ([]byte, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil, newPiOAuthError(PiOAuthSourceMissing)
	}
	body, status := hostpath.ReadPiOAuthSource(home)
	switch status {
	case hostpath.PiOAuthSourceOK:
		return body, nil
	case hostpath.PiOAuthSourceMissing:
		return nil, newPiOAuthError(PiOAuthSourceMissing)
	case hostpath.PiOAuthSourceBusy:
		return nil, newPiOAuthError(PiOAuthSourceBusy)
	default:
		return nil, newPiOAuthError(PiOAuthSourceUnsafe)
	}
}

func parsePiOAuthAccess(body []byte, now time.Time) (string, time.Time, error) {
	value, err := decodePiOAuthJSON(body)
	if err != nil {
		return "", time.Time{}, newPiOAuthError(PiOAuthSourceMalformed)
	}
	root, ok := value.(map[string]any)
	if !ok {
		return "", time.Time{}, newPiOAuthError(PiOAuthSourceMalformed)
	}
	rawProvider, ok := root["openai-codex"]
	if !ok {
		return "", time.Time{}, newPiOAuthError(PiOAuthProviderMissing)
	}
	provider, ok := rawProvider.(map[string]any)
	if !ok {
		return "", time.Time{}, newPiOAuthError(PiOAuthSourceMalformed)
	}
	authType, ok := provider["type"].(string)
	if !ok {
		return "", time.Time{}, newPiOAuthError(PiOAuthSourceMalformed)
	}
	if authType != "oauth" {
		return "", time.Time{}, newPiOAuthError(PiOAuthAuthTypeUnsupported)
	}
	access, ok := provider["access"].(string)
	if !ok || !safePiOAuthAccess(access) {
		return "", time.Time{}, newPiOAuthError(PiOAuthSourceMalformed)
	}
	expiresNumber, ok := provider["expires"].(json.Number)
	if !ok {
		return "", time.Time{}, newPiOAuthError(PiOAuthSourceMalformed)
	}
	expiresMillis, err := expiresNumber.Int64()
	if err != nil || expiresMillis <= 0 {
		return "", time.Time{}, newPiOAuthError(PiOAuthSourceMalformed)
	}
	expiresAt := time.UnixMilli(expiresMillis)
	if err := validatePiOAuthExpiry(expiresAt, now); err != nil {
		return "", time.Time{}, err
	}
	return access, expiresAt, nil
}

func safePiOAuthAccess(access string) bool {
	if access == "" || len(access) > piOAuthMaxAccessBytes {
		return false
	}
	for i := range len(access) {
		// The locked MVP accepts bounded printable ASCII only. This excludes all
		// whitespace, controls (including DEL), and Unicode before staging.
		if access[i] < 0x21 || access[i] > 0x7e {
			return false
		}
	}
	return true
}

func validatePiOAuthExpiry(expiresAt, now time.Time) error {
	if !expiresAt.After(now) {
		return newPiOAuthError(PiOAuthExpired)
	}
	if !expiresAt.After(now.Add(piOAuthMinHeadroom)) {
		return newPiOAuthError(PiOAuthNearExpiry)
	}
	return nil
}

func decodePiOAuthJSON(body []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	value, err := decodePiOAuthValue(decoder)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("trailing JSON value")
		}
		return nil, err
	}
	return value, nil
}

func decodePiOAuthValue(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delim, isDelim := token.(json.Delim)
	if !isDelim {
		return token, nil
	}
	switch delim {
	case '{':
		object := make(map[string]any)
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, errors.New("non-string object key")
			}
			if _, duplicate := object[key]; duplicate {
				return nil, fmt.Errorf("duplicate object key")
			}
			value, err := decodePiOAuthValue(decoder)
			if err != nil {
				return nil, err
			}
			object[key] = value
		}
		if end, err := decoder.Token(); err != nil || end != json.Delim('}') {
			return nil, errors.New("unterminated object")
		}
		return object, nil
	case '[':
		array := make([]any, 0)
		for decoder.More() {
			value, err := decodePiOAuthValue(decoder)
			if err != nil {
				return nil, err
			}
			array = append(array, value)
		}
		if end, err := decoder.Token(); err != nil || end != json.Delim(']') {
			return nil, errors.New("unterminated array")
		}
		return array, nil
	default:
		return nil, errors.New("unexpected JSON delimiter")
	}
}

func writePiOAuthSnapshot(stageDir, provider, access string) error {
	providerDir := filepath.Join(stageDir, "pi", provider)
	if err := os.MkdirAll(providerDir, 0o700); err != nil {
		return err
	}
	for _, dir := range []string{filepath.Join(stageDir, "pi"), providerDir} {
		if err := os.Chmod(dir, 0o700); err != nil {
			return err
		}
	}
	payload := struct {
		OpenAICodex struct {
			Type string `json:"type"`
			Key  string `json:"key"`
		} `json:"openai-codex"`
	}{}
	payload.OpenAICodex.Type = "api_key"
	payload.OpenAICodex.Key = access
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	body = append(body, '\n')
	defer zeroPiOAuthBytes(body)

	tmp, err := os.CreateTemp(providerDir, ".auth.json.tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, filepath.Join(providerDir, "auth.json")); err != nil {
		return err
	}
	keep = true
	return nil
}

func zeroPiOAuthBytes(buf []byte) {
	for i := range buf {
		buf[i] = 0
	}
}
