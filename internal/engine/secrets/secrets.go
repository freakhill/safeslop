// Package secrets resolves secret references at launch time (specs/0001 §7.1).
//
// A reference is either a 1Password URI ("op://vault/item/field"), resolved via
// the `op` CLI, or "env:NAME", read from the launching environment. Resolved
// values are never logged; callers place them only into the child's environment
// or the ephemeral, wiped-on-exit stage.
package secrets

import (
	"context"
	"fmt"
	"os"
	osexec "os/exec"
	"strings"
)

// OpAvailable reports whether the 1Password CLI is on PATH.
func OpAvailable() bool {
	_, err := osexec.LookPath("op")
	return err == nil
}

// OpSignedIn reports whether `op` has an active session (best-effort).
func OpSignedIn(ctx context.Context) bool {
	if !OpAvailable() {
		return false
	}
	return osexec.CommandContext(ctx, "op", "whoami").Run() == nil
}

// Resolve returns the value for a single secret ref.
func Resolve(ctx context.Context, ref string) (string, error) {
	switch {
	case strings.HasPrefix(ref, "env:"):
		name := strings.TrimPrefix(ref, "env:")
		v, ok := os.LookupEnv(name)
		if !ok || v == "" {
			return "", fmt.Errorf("env var %q (from %q) is not set", name, ref)
		}
		return v, nil

	case strings.HasPrefix(ref, "op://"):
		if !OpAvailable() {
			return "", fmt.Errorf("1Password CLI `op` not found on PATH; cannot resolve an op:// secret (install op and run `op signin`)")
		}
		// --no-newline so tokens are not corrupted by a trailing newline. The
		// error is kept generic: op's stderr is not surfaced in case it echoes
		// the reference or value.
		out, err := osexec.CommandContext(ctx, "op", "read", "--no-newline", ref).Output()
		if err != nil {
			return "", fmt.Errorf("op read failed for an op:// secret (is the 1Password app running and signed in?): %w", err)
		}
		return string(out), nil

	default:
		return "", fmt.Errorf("unsupported secret ref %q (want op://... or env:NAME)", ref)
	}
}

// ResolveMap resolves envName->ref into envName->value, preserving the names.
func ResolveMap(ctx context.Context, refs map[string]string) (map[string]string, error) {
	out := make(map[string]string, len(refs))
	for name, ref := range refs {
		v, err := Resolve(ctx, ref)
		if err != nil {
			return nil, fmt.Errorf("secret %s: %w", name, err)
		}
		out[name] = v
	}
	return out, nil
}
