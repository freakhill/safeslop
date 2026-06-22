package uninstall

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/freakhill/safeslop/internal/engine/install"
)

// applyPathB delegates teardown of system state to the tool's own designated uninstaller — safeslop never
// hand-rolls removal of /nix, a daemon, or synthetic.conf. It:
//  1. re-verifies the delegate binary before running it (codesign, for an on-disk absolute path);
//  2. runs the delegate, FAIL-CLOSED on a non-zero exit (the caller must then halt the whole uninstall);
//  3. does NOT trust exit 0 — runs the receipted residue probe and treats the tool reappearing in its
//     output as "teardown incomplete" (nix-installer has shipped "successful" uninstalls leaving a stale
//     APFS volume / synthetic.conf entry that breaks the next install).
func (e *Engine) applyPathB(ctx context.Context, item Item) (Result, error) {
	res := Result{Tool: item.Tool, Kind: DelegatePathB}
	if len(item.Delegate) == 0 {
		return res, fmt.Errorf("uninstall %s: receipt has no delegate uninstaller", item.Tool)
	}

	// 1. Resolve + re-verify the delegate.
	bin := item.Delegate[0]
	argv := append([]string(nil), item.Delegate...)
	if filepath.IsAbs(bin) {
		if err := e.codesign(ctx, bin); err != nil {
			if errors.Is(err, install.ErrCodesignUnavailable) {
				res.Notes = append(res.Notes, "codesign unavailable — skipped delegate re-verification")
			} else {
				return res, fmt.Errorf("uninstall %s: delegate %s failed codesign re-verification: %w", item.Tool, bin, err)
			}
		}
	} else {
		resolved, err := exec.LookPath(bin)
		if err != nil {
			return res, fmt.Errorf("uninstall %s: delegate uninstaller %q not found on PATH: %w", item.Tool, bin, err)
		}
		argv[0] = resolved
		res.Notes = append(res.Notes, "delegate is a PATH command ("+bin+") — codesign re-verification skipped")
	}

	// 2. Run the delegate, fail-closed on its exit code.
	out, code, err := e.Run(ctx, argv)
	if err != nil {
		return res, fmt.Errorf("uninstall %s: running delegate: %w", item.Tool, err)
	}
	if code != 0 {
		return res, fmt.Errorf("uninstall %s: delegate exited %d (halting; not proceeding to other tools):\n%s", item.Tool, code, strings.TrimSpace(out))
	}

	// 3. Verify the teardown actually happened — don't trust exit 0.
	if residue, err := e.verifyTeardown(ctx, item); err != nil {
		return res, err
	} else if residue != "" {
		return res, fmt.Errorf("uninstall %s: delegate exited 0 but teardown is incomplete — %s", item.Tool, residue)
	}
	res.Notes = append(res.Notes, "delegate succeeded; teardown verified")
	return res, nil
}

// codesign calls the injected verifier (install.VerifyCodesign in production).
func (e *Engine) codesign(ctx context.Context, path string) error {
	if e.Codesign != nil {
		return e.Codesign(ctx, path)
	}
	return install.VerifyCodesign(ctx, path)
}

// verifyTeardown runs the receipted residue probe (e.g. `diskutil apfs list` for nix) and reports a
// non-empty description if the tool's name still appears in the output — a generic, data-driven residue
// signal that needs no per-tool grep token. No probe (rustup) → nothing to verify.
func (e *Engine) verifyTeardown(ctx context.Context, item Item) (residue string, err error) {
	if len(item.Verify) == 0 {
		return "", nil
	}
	out, _, runErr := e.Run(ctx, item.Verify)
	if runErr != nil {
		// A probe we cannot run is not proof of clean teardown — surface it rather than assume success.
		return "", fmt.Errorf("uninstall %s: residue probe %v failed to run: %w", item.Tool, item.Verify, runErr)
	}
	if strings.Contains(strings.ToLower(out), strings.ToLower(item.Tool)) {
		return fmt.Sprintf("%q still appears in `%s` output", item.Tool, strings.Join(item.Verify, " ")), nil
	}
	return "", nil
}
