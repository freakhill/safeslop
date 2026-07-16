package container

import engsession "github.com/freakhill/safeslop/internal/engine/session"

const NetworkProxyUnavailable = "network_proxy_unavailable"

var runtimeFailureText = map[string][2]string{
	NetworkProxyUnavailable: {
		"The required network proxy did not become ready.",
		"Check the container runtime, then retry the session.",
	},
}

// RuntimeFailure is an engine-owned, value-free boundary failure. It deliberately
// excludes command output, runtime paths, and wrapped OS errors so session status
// can persist it safely.
type RuntimeFailure struct {
	failure engsession.Failure
}

func (e *RuntimeFailure) Error() string               { return e.failure.Summary }
func (e *RuntimeFailure) Failure() engsession.Failure { return e.failure }

func newRuntimeFailure(code string) *RuntimeFailure {
	text, ok := runtimeFailureText[code]
	if !ok {
		text = [2]string{"The container boundary did not become ready.", "Check the container runtime, then retry the session."}
	}
	return &RuntimeFailure{failure: engsession.Failure{
		Version:  1,
		Phase:    "network",
		Code:     code,
		Required: true,
		Summary:  text[0],
		Action:   text[1],
	}}
}
