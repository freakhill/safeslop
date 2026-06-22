package install

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
)

// codesignPath is the macOS code-signing tool. It is a fixed system path (not resolved via PATH) so a
// PATH-injected "codesign" can't subvert the check.
const codesignPath = "/usr/bin/codesign"

// ErrCodesignUnavailable is returned by VerifyCodesign when /usr/sbin/codesign is absent (a non-darwin
// host, or a stripped image). Callers decide whether a missing verifier is fatal for their context;
// uninstall records it as a skipped check rather than failing open silently (specs/0041).
var ErrCodesignUnavailable = errors.New("install: codesign unavailable on this host")

// VerifyCodesign runs `codesign --verify --strict <path>` and returns nil only if the signature is
// intact. It is execution-time re-verification of a delegate uninstaller binary (Path B) immediately
// before running it with the user's privileges — a tampered uninstaller's blast radius is "everything
// the invoking user can delete", so re-validating is cheap insurance (specs/0040).
//
// This is a plain process exec of the system codesign tool — deliberately NOT the in-process
// Security.framework peer-auth deferred in internal/engine/control/peerauth.go; the two are unrelated.
func VerifyCodesign(ctx context.Context, path string) error {
	if _, err := os.Stat(codesignPath); err != nil {
		return ErrCodesignUnavailable
	}
	cmd := exec.CommandContext(ctx, codesignPath, "--verify", "--strict", path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("install: codesign verify failed for %s: %w: %s", path, err, out)
	}
	return nil
}
