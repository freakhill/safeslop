package uninstall

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/freakhill/safeslop/internal/engine/receipt"
)

// applyPathA removes a tool safeslop placed itself, against its receipt. It is an ATOMIC BATCH: it first
// verifies every receipted file (hash-match, symlink-safety, presence) WITHOUT mutating anything, and
// only if the whole set passes does it move the files to the trash in one stamp. So a hash mismatch on
// the last file leaves the first untouched — no half-removed environment (specs/0041 task 3).
//
// Rules:
//   - hash mismatch on a non-self-updating tool  -> abort the whole item (tamper / user edit; surface a diff)
//   - hash mismatch on a SelfUpdating tool        -> ErrNeedsConfirm unless confirmSelfUpdated (claude drifts by design)
//   - file already gone (ENOENT)                  -> skip it, success (rm -f semantics)
//   - receipted path now an external symlink      -> skip it, never follow it out of the prefix
func (e *Engine) applyPathA(item Item, confirmSelfUpdated bool) (Result, error) {
	res := Result{Tool: item.Tool, Kind: RemovePathA}

	// Warn (don't block) if the tool is still running — esp. tart holding VMs.
	for _, f := range item.Files {
		if f.Symlink {
			continue
		}
		if pids := runningInstances(f.Path); len(pids) > 0 {
			res.Notes = append(res.Notes, fmt.Sprintf("%s appears to be running (pids %v) — close it before relying on removal", item.Tool, pids))
		}
	}

	// Phase 1: verify everything, mutate nothing. Collect the set to trash.
	var toTrash []string
	for _, f := range item.Files {
		ok, reason, err := e.checkRemovable(f, item.SelfUpdating, confirmSelfUpdated)
		switch {
		case err != nil:
			return res, err // hard abort — nothing moved yet
		case !ok:
			res.Skipped = append(res.Skipped, f.Path+" ("+reason+")")
		default:
			toTrash = append(toTrash, f.Path)
		}
	}

	if len(toTrash) == 0 {
		return res, nil
	}

	// Phase 2: one atomic batch move to the trash.
	stamp := e.stamp()
	if _, err := moveToTrash(toTrash, stamp); err != nil {
		return res, err
	}
	res.Trashed = toTrash
	res.TrashStamp = stamp
	return res, nil
}

// checkRemovable validates one receipted file. (ok=true) means "move it to trash"; (ok=false, reason)
// means "leave it, here's why"; a non-nil error aborts the whole item.
func (e *Engine) checkRemovable(f receipt.File, selfUpdating, confirmSelfUpdated bool) (ok bool, reason string, err error) {
	fi, statErr := os.Lstat(f.Path)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			return false, "already gone", nil // rm -f semantics
		}
		return false, "", statErr
	}

	// A receipted path that is now a symlink: only safe to remove if it still points INSIDE our prefix.
	// An external symlink (e.g. repointed at a brew binary) is left alone — never follow it to delete the
	// target (specs/0041: symlinks out of the prefix are out of scope).
	if fi.Mode()&os.ModeSymlink != 0 {
		if f.Symlink {
			if e.symlinkEscapes(f.Path) {
				return false, "external symlink — left intact", nil
			}
			return true, "", nil // our own in-prefix symlink (e.g. an app's BinDir link)
		}
		// We recorded a regular file but it's now a symlink — treat as foreign, don't follow it.
		return false, "replaced by a symlink — left intact", nil
	}

	// A bundle dir (an .app) carries no recorded sha — nothing to hash-verify, remove as recorded.
	if f.SHA256 == "" {
		return true, "", nil
	}

	got, herr := hashFile(f.Path)
	if herr != nil {
		return false, "", herr
	}
	if !strings.EqualFold(got, f.SHA256) {
		if selfUpdating {
			if confirmSelfUpdated {
				return true, "", nil // expected drift (e.g. claude), user confirmed
			}
			return false, "", ErrNeedsConfirm
		}
		return false, "", fmt.Errorf("hash mismatch for %s: receipt=%s ondisk=%s (refusing to delete — edited or tampered)", f.Path, f.SHA256, got)
	}
	return true, "", nil
}

// symlinkEscapes reports whether the symlink at p resolves outside the install prefix (BinDir/AppDir).
func (e *Engine) symlinkEscapes(p string) bool {
	target, err := os.Readlink(p)
	if err != nil {
		return true // unreadable → treat as foreign, don't touch
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(p), target)
	}
	target = filepath.Clean(target)
	// BinDir/AppDir/LibDir are all safeslop-owned prefixes: a symlink into any of them is our own (e.g.
	// BinDir/limactl -> LibDir/limactl/bin/limactl for a FormatToolTree install), safe to remove.
	for _, prefix := range []string{e.Dirs.BinDir, e.Dirs.AppDir, e.Dirs.LibDir} {
		if prefix != "" && (target == prefix || strings.HasPrefix(target, prefix+string(os.PathSeparator))) {
			return false
		}
	}
	return true
}

func hashFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}
