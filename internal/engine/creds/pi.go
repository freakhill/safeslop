package creds

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/freakhill/safeslop/internal/engine/policy"
	engsession "github.com/freakhill/safeslop/internal/engine/session"
	"golang.org/x/sys/unix"
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

	piOAuthMaxSourceBytes = 1 << 20
	piOAuthMaxAccessBytes = 64 << 10
	piOAuthReadAttempts   = 10
	piOAuthRetryDelay     = 50 * time.Millisecond
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

var (
	piOAuthNow       = time.Now
	piOAuthSleep     = time.Sleep
	piOAuthAfterRead func(attempt int)
)

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
	homeFD, err := openDirectoryAt(unix.AT_FDCWD, home)
	if err != nil {
		return nil, classifyPiOAuthOpenError(err)
	}
	defer unix.Close(homeFD)
	piFD, err := openDirectoryAt(homeFD, ".pi")
	if err != nil {
		return nil, classifyPiOAuthOpenError(err)
	}
	defer unix.Close(piFD)
	if !safePiOAuthDirectory(piFD) {
		return nil, newPiOAuthError(PiOAuthSourceUnsafe)
	}
	agentFD, err := openDirectoryAt(piFD, "agent")
	if err != nil {
		return nil, classifyPiOAuthOpenError(err)
	}
	defer unix.Close(agentFD)
	if !safePiOAuthDirectory(agentFD) {
		return nil, newPiOAuthError(PiOAuthSourceUnsafe)
	}

	for attempt := 0; attempt < piOAuthReadAttempts; attempt++ {
		locked, lockErr := piOAuthLockExists(agentFD)
		if lockErr != nil {
			return nil, newPiOAuthError(PiOAuthSourceUnsafe)
		}
		if locked {
			if attempt+1 < piOAuthReadAttempts {
				piOAuthSleep(piOAuthRetryDelay)
				continue
			}
			return nil, newPiOAuthError(PiOAuthSourceBusy)
		}

		body, stable, readErr := readStablePiOAuthFile(agentFD, attempt)
		if readErr != nil {
			return nil, readErr
		}
		if stable {
			return body, nil
		}
		if attempt+1 < piOAuthReadAttempts {
			piOAuthSleep(piOAuthRetryDelay)
		}
	}
	return nil, newPiOAuthError(PiOAuthSourceBusy)
}

func openDirectoryAt(parentFD int, name string) (int, error) {
	return unix.Openat(parentFD, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
}

func classifyPiOAuthOpenError(err error) error {
	if errors.Is(err, unix.ENOENT) {
		return newPiOAuthError(PiOAuthSourceMissing)
	}
	return newPiOAuthError(PiOAuthSourceUnsafe)
}

func safePiOAuthDirectory(fd int) bool {
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return false
	}
	return stat.Mode&unix.S_IFMT == unix.S_IFDIR && stat.Mode&0o077 == 0 && stat.Uid == uint32(os.Geteuid())
}

func ownedByCurrentUser(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Geteuid())
}

func singleLink(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Nlink == 1
}

func piOAuthLockExists(agentFD int) (bool, error) {
	var stat unix.Stat_t
	err := unix.Fstatat(agentFD, "auth.json.lock", &stat, unix.AT_SYMLINK_NOFOLLOW)
	if errors.Is(err, unix.ENOENT) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func readStablePiOAuthFile(agentFD, attempt int) ([]byte, bool, error) {
	fd, err := unix.Openat(agentFD, "auth.json", unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, false, classifyPiOAuthOpenError(err)
	}
	file := os.NewFile(uintptr(fd), "")
	if file == nil {
		unix.Close(fd)
		return nil, false, newPiOAuthError(PiOAuthSourceUnsafe)
	}
	defer file.Close()

	before, err := file.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Mode().Perm()&0o077 != 0 ||
		!ownedByCurrentUser(before) || !singleLink(before) || before.Size() > piOAuthMaxSourceBytes {
		return nil, false, newPiOAuthError(PiOAuthSourceUnsafe)
	}
	body, err := io.ReadAll(io.LimitReader(file, piOAuthMaxSourceBytes+1))
	if err != nil || len(body) > piOAuthMaxSourceBytes {
		return nil, false, newPiOAuthError(PiOAuthSourceUnsafe)
	}
	if piOAuthAfterRead != nil {
		piOAuthAfterRead(attempt)
	}
	after, err := file.Stat()
	if err != nil {
		return nil, false, newPiOAuthError(PiOAuthSourceUnsafe)
	}
	freshFD, err := unix.Openat(agentFD, "auth.json", unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, false, classifyPiOAuthOpenError(err)
	}
	fresh := os.NewFile(uintptr(freshFD), "")
	if fresh == nil {
		unix.Close(freshFD)
		return nil, false, newPiOAuthError(PiOAuthSourceUnsafe)
	}
	freshInfo, statErr := fresh.Stat()
	fresh.Close()
	if statErr != nil {
		return nil, false, newPiOAuthError(PiOAuthSourceUnsafe)
	}
	locked, lockErr := piOAuthLockExists(agentFD)
	if lockErr != nil {
		return nil, false, newPiOAuthError(PiOAuthSourceUnsafe)
	}
	stable := !locked && samePiOAuthSnapshot(before, after) && samePiOAuthSnapshot(after, freshInfo) && os.SameFile(after, freshInfo)
	return body, stable, nil
}

func samePiOAuthSnapshot(a, b os.FileInfo) bool {
	return a.Size() == b.Size() && a.Mode() == b.Mode() && a.ModTime().Equal(b.ModTime())
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
