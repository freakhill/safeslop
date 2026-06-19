// Package gitguard fingerprints the part of a git repo that the *host* will
// execute on its next git command — the executable hooks under .git/hooks and
// .git/config (which can point hooksPath/fsmonitor/filters/aliases at arbitrary
// commands). safeslop runs the agent against a writable repo, so a prompt-injected
// or malicious agent can plant a hook or a config directive that runs on the
// host the next time *you* run git there (specs/0024 review S3, specs/0025).
//
// This package only DETECTS such changes (a before/after Snapshot + Diff) so the
// caller can warn; it never blocks the agent's legitimate git use. Prevention is
// a follow-on (specs/0025 Deferred).
package gitguard

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// State is a fingerprint of a repo's git exec-surface at one instant.
type State struct {
	configHash string            // sha256 of .git/config ("" when absent)
	hooks      map[string]string // executable, non-.sample hook name -> sha256 of contents
}

// Snapshot fingerprints repoRoot's git exec-surface. A missing or non-directory
// .git (e.g. a worktree pointer file) yields an empty, stable State and no error,
// so callers can snapshot unconditionally.
func Snapshot(repoRoot string) (State, error) {
	st := State{hooks: map[string]string{}}
	gitdir := filepath.Join(repoRoot, ".git")
	info, err := os.Stat(gitdir)
	if err != nil || !info.IsDir() {
		return st, nil // no .git dir (or a worktree pointer file): nothing to fingerprint
	}

	if b, err := os.ReadFile(filepath.Join(gitdir, "config")); err == nil {
		st.configHash = hashBytes(b)
	} else if !os.IsNotExist(err) {
		return State{}, err
	}

	entries, err := os.ReadDir(filepath.Join(gitdir, "hooks"))
	if err != nil {
		if os.IsNotExist(err) {
			return st, nil
		}
		return State{}, err
	}
	for _, e := range entries {
		if e.IsDir() || strings.HasSuffix(e.Name(), ".sample") {
			continue // git never runs *.sample
		}
		fi, err := e.Info()
		if err != nil {
			return State{}, err
		}
		if fi.Mode()&0o111 == 0 {
			continue // not executable: git won't run it
		}
		b, err := os.ReadFile(filepath.Join(gitdir, "hooks", e.Name()))
		if err != nil {
			return State{}, err
		}
		st.hooks[e.Name()] = hashBytes(b)
	}
	return st, nil
}

// Diff returns human-readable descriptions of every exec-surface change from s to
// after (config changed, or an executable hook added/modified). Removals are not
// an execution risk and are not reported. The result is sorted and is nil when
// nothing relevant changed.
func (s State) Diff(after State) []string {
	var out []string
	if s.configHash != after.configHash {
		out = append(out, ".git/config (hooksPath / fsmonitor / filters / aliases can run commands)")
	}
	for name, h := range after.hooks {
		if s.hooks[name] != h {
			out = append(out, ".git/hooks/"+name)
		}
	}
	sort.Strings(out)
	return out
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
