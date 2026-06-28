package container

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// withRepoLock serializes staging/reconcile across concurrent safeslop invocations on the same
// repo via an advisory flock on <repo>/.safeslop/lock. The lock is released when fn returns.
func withRepoLock(repo string, fn func() error) error {
	if err := os.MkdirAll(filepath.Join(repo, ".safeslop"), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(repo, ".safeslop", "lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}

// buildLockDir is the host directory holding per-recipe build locks. It is GLOBAL (not repo-scoped):
// the image cache is shared across repos, so two repos building the same recipe must contend on the
// same lock. SAFESLOP_STATE_DIR overrides the root (tests set it for hermeticity); otherwise it is
// the user cache dir (build locks are cache-like, ephemeral state).
func buildLockDir() string {
	root := os.Getenv("SAFESLOP_STATE_DIR")
	if root == "" {
		if c, err := os.UserCacheDir(); err == nil {
			root = filepath.Join(c, "safeslop")
		} else {
			root = filepath.Join(os.TempDir(), "safeslop")
		}
	}
	return filepath.Join(root, "build")
}

// withBuildLock serializes builds of one recipe id across concurrent safeslop invocations via an
// advisory flock on buildLockDir()/<id>.lock, so a recipe is built at most once even when several
// runs race (the existing withRepoLock only wraps Reconcile, not the build). Callers double-check
// imageExists inside fn after acquiring (see ensureImage). id is sanitized into a filename.
func withBuildLock(id string, fn func() error) error {
	dir := buildLockDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	safe := strings.NewReplacer("/", "_", ":", "_").Replace(id)
	f, err := os.OpenFile(filepath.Join(dir, safe+".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}
