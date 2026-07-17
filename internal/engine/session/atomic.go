package session

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
)

// atomicHooks is an instance-owned fault seam for the filesystem operations
// whose ordering makes a record commit durable. Production Stores leave it nil.
type atomicHooks struct {
	syncFile func(*os.File) error
	rename   func(string, string) error
	link     func(string, string) error
	syncDir  func(string) error
}

func (h *atomicHooks) fileSync(f *os.File) error {
	if h != nil && h.syncFile != nil {
		return h.syncFile(f)
	}
	return f.Sync()
}

func (h *atomicHooks) replace(oldPath, newPath string) error {
	if h != nil && h.rename != nil {
		return h.rename(oldPath, newPath)
	}
	return os.Rename(oldPath, newPath)
}

func (h *atomicHooks) noReplace(oldPath, newPath string) error {
	if h != nil && h.link != nil {
		return h.link(oldPath, newPath)
	}
	return os.Link(oldPath, newPath)
}

func (h *atomicHooks) directorySync(dir string) error {
	if h != nil && h.syncDir != nil {
		return h.syncDir(dir)
	}
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

// writeRecordAtomic installs complete bytes in the target directory. create
// uses a hard link from the synced temporary file to get portable no-replace
// semantics; replacement uses rename. Temporary names never end in .json and
// therefore can never be mistaken for records by List.
func writeRecordAtomic(path string, data []byte, create bool, hooks *atomicHooks) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".record-tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	removeTemp := true
	defer func() {
		_ = tmp.Close()
		if removeTemp {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := io.Copy(tmp, bytes.NewReader(data)); err != nil {
		return err
	}
	if err := hooks.fileSync(tmp); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	if create {
		if err := hooks.noReplace(tmpPath, path); err != nil {
			return err
		}
		if err := os.Remove(tmpPath); err != nil {
			// The target is already complete, but the directory now has an
			// uncertain extra link. Treat this as an uncertain commit.
			return ErrCommitUncertain
		}
		removeTemp = false
	} else {
		if err := hooks.replace(tmpPath, path); err != nil {
			return err
		}
		removeTemp = false
	}
	if err := hooks.directorySync(dir); err != nil {
		return ErrCommitUncertain
	}
	return nil
}

func removeRecordAtomic(path string, hooks *atomicHooks) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if err := hooks.directorySync(filepath.Dir(path)); err != nil {
		return ErrCommitUncertain
	}
	return nil
}
