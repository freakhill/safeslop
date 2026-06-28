// Package jsoncontract defines the versioned JSON envelope shared by the
// safeslop Go CLI and the Emacs frontend (specs/0049). Both sides parse the same
// golden fixtures under testdata/, so this package is the single source of truth
// for the wire shape and the append-only error-code registry.
package jsoncontract

// ErrorCode is a stable, machine-readable code for an envelope error or warning.
//
// The v1 set is append-only: existing codes must never be renamed or removed, so
// that older Emacs clients keep recognizing them. New codes may be added.
type ErrorCode string

// v1 error codes. Keep this list and allErrorCodes in sync; the test enforces it.
const (
	CodeInvalidArgument        ErrorCode = "INVALID_ARGUMENT"
	CodeSchemaUnsupported      ErrorCode = "SCHEMA_UNSUPPORTED"
	CodeSchemaViolation        ErrorCode = "SCHEMA_VIOLATION"
	CodeNotFound               ErrorCode = "NOT_FOUND"
	CodeConflict               ErrorCode = "CONFLICT"
	CodePermissionDenied       ErrorCode = "PERMISSION_DENIED"
	CodeAuthRequired           ErrorCode = "AUTH_REQUIRED"
	CodeCredentialRevoked      ErrorCode = "CREDENTIAL_REVOKED"
	CodeCredentialRevokeFailed ErrorCode = "CREDENTIAL_REVOKE_FAILED"
	CodePolicyDenied           ErrorCode = "POLICY_DENIED"
	CodeNetworkDenied          ErrorCode = "NETWORK_DENIED"
	CodeRuntimeUnavailable     ErrorCode = "RUNTIME_UNAVAILABLE"
	CodeToolUnavailable        ErrorCode = "TOOL_UNAVAILABLE"
	CodeAgentUnsupported       ErrorCode = "AGENT_UNSUPPORTED"
	CodeSessionNotFound        ErrorCode = "SESSION_NOT_FOUND"
	CodeSessionAlreadyRunning  ErrorCode = "SESSION_ALREADY_RUNNING"
	CodeSessionNotRunning      ErrorCode = "SESSION_NOT_RUNNING"
	CodeSessionStopped         ErrorCode = "SESSION_STOPPED"
	CodeSessionCancelled       ErrorCode = "SESSION_CANCELLED"
	CodePTYUnavailable         ErrorCode = "PTY_UNAVAILABLE"
	CodeTimeout                ErrorCode = "TIMEOUT"
	CodeRateLimited            ErrorCode = "RATE_LIMITED"
	CodeIOError                ErrorCode = "IO_ERROR"
	CodeInternal               ErrorCode = "INTERNAL"
)

// allErrorCodes is the canonical ordered v1 registry.
var allErrorCodes = []ErrorCode{
	CodeInvalidArgument,
	CodeSchemaUnsupported,
	CodeSchemaViolation,
	CodeNotFound,
	CodeConflict,
	CodePermissionDenied,
	CodeAuthRequired,
	CodeCredentialRevoked,
	CodeCredentialRevokeFailed,
	CodePolicyDenied,
	CodeNetworkDenied,
	CodeRuntimeUnavailable,
	CodeToolUnavailable,
	CodeAgentUnsupported,
	CodeSessionNotFound,
	CodeSessionAlreadyRunning,
	CodeSessionNotRunning,
	CodeSessionStopped,
	CodeSessionCancelled,
	CodePTYUnavailable,
	CodeTimeout,
	CodeRateLimited,
	CodeIOError,
	CodeInternal,
}

// AllErrorCodes returns a copy of the v1 error-code registry in canonical order.
func AllErrorCodes() []ErrorCode {
	out := make([]ErrorCode, len(allErrorCodes))
	copy(out, allErrorCodes)
	return out
}

// IsValidCode reports whether code is a known v1 error code.
func IsValidCode(code ErrorCode) bool {
	for _, c := range allErrorCodes {
		if c == code {
			return true
		}
	}
	return false
}

// PTYUnavailable returns the canonical PTY_UNAVAILABLE error envelope. `session
// run` emits it when no usable controlling terminal is available, so the caller
// switches to the JSONL status fallback named in details.fallback. It is
// retryable: a later attach from a real terminal can still succeed. This is the
// single source of the wire shape, pinned to error-pty-unavailable.golden.json by
// the contract tests (specs/0050 PR4).
func PTYUnavailable() Envelope {
	return Error(NewMessage(
		CodePTYUnavailable,
		"interactive PTY is unavailable; use status JSONL fallback",
		true,
		map[string]any{"fallback": "status-jsonl"},
	))
}
