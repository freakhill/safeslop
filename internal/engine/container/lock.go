package container

import (
	"os"
	"path/filepath"
	"syscall"
)

// withRepoLock serializes staging/reconcile across concurrent slop invocations on the same
// repo via an advisory flock on <repo>/.slop/lock. The lock is released when fn returns.
func withRepoLock(repo string, fn func() error) error {
	if err := os.MkdirAll(filepath.Join(repo, ".slop"), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(repo, ".slop", "lock"), os.O_CREATE|os.O_RDWR, 0o600)
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
