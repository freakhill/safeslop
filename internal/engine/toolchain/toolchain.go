// Package toolchain layers a pinned tool environment (mise/nix) onto any slop environment.
// Wrap is pure: it transforms the agent argv so the toolchain is provisioned, or replaces it
// with a mise task / nix app. Enabling mise/nix inside each environment is the caller's job.
package toolchain

import "os/exec"

// Wrap transforms agentArgv per the toolchain. With run set, it returns the mise task / nix app
// (the agent is not launched). Without run, it wraps the agent so the pinned toolchain is on
// PATH. kind "none"/"" is a passthrough (returns agentArgv unchanged).
func Wrap(kind, run string, agentArgv []string) []string {
	switch kind {
	case "mise":
		if run != "" {
			return []string{"mise", "run", run}
		}
		return append([]string{"mise", "exec", "--"}, agentArgv...)
	case "nix":
		if run != "" {
			return []string{"nix", "run", run}
		}
		return append([]string{"nix", "develop", "-c"}, agentArgv...)
	default:
		return agentArgv
	}
}

// Wraps reports whether kind is a real toolchain (mise/nix) that Wrap will transform.
func Wraps(kind string) bool { return kind == "mise" || kind == "nix" }

// Available reports whether the kind's CLI is on the host PATH (for slop doctor / host runs).
func Available(kind string) bool {
	if !Wraps(kind) {
		return false
	}
	_, err := exec.LookPath(kind)
	return err == nil
}
