//go:build darwin || linux

package hostpath

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

const (
	piOAuthSourceRel      = ".pi/agent/auth.json"
	piOAuthParentRel      = ".pi/agent"
	piOAuthLockName       = "auth.json.lock"
	piOAuthMaxSourceBytes = 1 << 20
	piOAuthReadAttempts   = 10
	piOAuthRetryDelay     = 50 * time.Millisecond
)

var (
	piOAuthSourceSleep     = time.Sleep
	piOAuthSourceAfterRead func(attempt int)
)

var errPiDirectoryUnsafe = errors.New("unsafe Pi OAuth directory")

type piSourceProof struct {
	root   *proofRoot
	parent *pinnedNode
	leaf   *pinnedNode
}

func (p *piSourceProof) close() {
	if p == nil {
		return
	}
	if p.leaf != nil {
		p.leaf.close()
	}
	if p.parent != nil {
		p.parent.close()
	}
	if p.root != nil {
		p.root.close()
	}
}

// ReadPiOAuthSource proves and reads only HOME/.pi/agent/auth.json. It returns
// no path, target, identity, mount, or operating-system detail on failure.
func ReadPiOAuthSource(home string) ([]byte, PiOAuthSourceStatus) {
	if home == "" || !filepath.IsAbs(home) || filepath.Clean(home) != home || !projectionSafetySupported() {
		return nil, PiOAuthSourceUnsafe
	}
	for attempt := 0; attempt < piOAuthReadAttempts; attempt++ {
		body, status, retry := readPiOAuthAttempt(home, attempt)
		if !retry {
			if status != PiOAuthSourceOK {
				zeroBytes(body)
				body = nil
			}
			return body, status
		}
		zeroBytes(body)
		if attempt+1 < piOAuthReadAttempts {
			piOAuthSourceSleep(piOAuthRetryDelay)
		}
	}
	return nil, PiOAuthSourceBusy
}

func readPiOAuthAttempt(home string, attempt int) ([]byte, PiOAuthSourceStatus, bool) {
	original, status, retry := provePiOAuthSource(home, nil)
	if original != nil {
		defer original.close()
	}
	if status != PiOAuthSourceOK {
		return nil, status, retry
	}
	locked, lockStatus := piOAuthLockStatus(original.parent)
	if lockStatus != PiOAuthSourceOK {
		return nil, lockStatus, lockStatus == PiOAuthSourceBusy
	}
	if locked {
		return nil, PiOAuthSourceBusy, true
	}

	before, err := original.leaf.file.Stat()
	if err != nil || !safePiOAuthLeaf(before) {
		return nil, PiOAuthSourceUnsafe, false
	}
	body, err := io.ReadAll(io.LimitReader(original.leaf.file, piOAuthMaxSourceBytes+1))
	if err != nil || len(body) > piOAuthMaxSourceBytes {
		zeroBytes(body)
		return nil, PiOAuthSourceUnsafe, false
	}
	if piOAuthSourceAfterRead != nil {
		piOAuthSourceAfterRead(attempt)
	}
	after, err := original.leaf.file.Stat()
	if err != nil || !samePiOAuthSnapshot(before, after) {
		return body, PiOAuthSourceBusy, true
	}

	freshRoot, err := original.root.clone()
	if err != nil {
		return body, PiOAuthSourceBusy, true
	}
	fresh, status, retry := provePiOAuthSource(home, freshRoot)
	if fresh != nil {
		defer fresh.close()
	}
	if status != PiOAuthSourceOK {
		if status == PiOAuthSourceMissing {
			return body, PiOAuthSourceBusy, true
		}
		return body, status, retry
	}
	freshInfo, err := fresh.leaf.file.Stat()
	if err != nil || !safePiOAuthLeaf(freshInfo) {
		return body, PiOAuthSourceUnsafe, false
	}
	originalLocked, originalLockStatus := piOAuthLockStatus(original.parent)
	freshLocked, freshLockStatus := piOAuthLockStatus(fresh.parent)
	if originalLockStatus != PiOAuthSourceOK || freshLockStatus != PiOAuthSourceOK {
		return body, PiOAuthSourceUnsafe, false
	}
	if originalLocked || freshLocked {
		return body, PiOAuthSourceBusy, true
	}
	if !samePiOAuthSnapshot(after, freshInfo) || !os.SameFile(after, freshInfo) ||
		!original.root.revalidate() || !fresh.root.revalidate() {
		return body, PiOAuthSourceBusy, true
	}
	return body, PiOAuthSourceOK, false
}

func provePiOAuthSource(home string, retained *proofRoot) (*piSourceProof, PiOAuthSourceStatus, bool) {
	root := retained
	var err error
	if root == nil {
		root, err = openProofRoot(home)
		if err != nil {
			status, retry := classifyPiProofError(err)
			return nil, status, retry
		}
	}
	proof := &piSourceProof{root: root}
	if !safePiOAuthDirectory(root.before) {
		return proof, PiOAuthSourceUnsafe, false
	}
	policy := proofPolicy{validateDirectory: func(info os.FileInfo) error {
		if !safePiOAuthDirectory(info) {
			return errPiDirectoryUnsafe
		}
		return nil
	}}
	proof.parent, err = openPinnedPathWithPolicy(root, piOAuthParentRel, policy)
	if err != nil {
		status, retry := classifyPiProofError(err)
		return proof, status, retry
	}
	if proof.parent.dir == nil || !safePiOAuthDirectory(proof.parent.info) {
		return proof, PiOAuthSourceUnsafe, false
	}
	proof.leaf, err = openPinnedPathWithPolicy(root, piOAuthSourceRel, policy)
	if err != nil {
		status, retry := classifyPiProofError(err)
		return proof, status, retry
	}
	if proof.leaf.file == nil || !safePiOAuthLeaf(proof.leaf.info) {
		return proof, PiOAuthSourceUnsafe, false
	}
	return proof, PiOAuthSourceOK, false
}

func classifyPiProofError(err error) (PiOAuthSourceStatus, bool) {
	if errors.Is(err, errPiDirectoryUnsafe) {
		return PiOAuthSourceUnsafe, false
	}
	var proofErr *proofError
	if !errors.As(err, &proofErr) {
		return PiOAuthSourceUnsafe, false
	}
	switch proofErr.failure {
	case proofUnavailable:
		if proofErr.absent && !proofErr.linked {
			return PiOAuthSourceMissing, false
		}
		return PiOAuthSourceUnsafe, false
	case proofChanged:
		return PiOAuthSourceBusy, true
	default:
		return PiOAuthSourceUnsafe, false
	}
}

func piOAuthLockStatus(parent *pinnedNode) (bool, PiOAuthSourceStatus) {
	if parent == nil || parent.dir == nil {
		return false, PiOAuthSourceUnsafe
	}
	_, err := parent.dir.Lstat(piOAuthLockName)
	if errors.Is(err, os.ErrNotExist) {
		return false, PiOAuthSourceOK
	}
	if err != nil {
		return false, PiOAuthSourceUnsafe
	}
	return true, PiOAuthSourceOK
}

func safePiOAuthDirectory(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && safePiOAuthDirectoryMetadata(stat.Uid, uint32(os.Geteuid()), info.Mode())
}

func safePiOAuthLeaf(info os.FileInfo) bool {
	if info == nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 ||
		info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 || info.Size() > piOAuthMaxSourceBytes {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Geteuid()) && stat.Nlink == 1
}

func samePiOAuthSnapshot(a, b os.FileInfo) bool {
	return a != nil && b != nil && os.SameFile(a, b) && a.Size() == b.Size() &&
		a.Mode() == b.Mode() && a.ModTime().Equal(b.ModTime())
}

func zeroBytes(body []byte) {
	for i := range body {
		body[i] = 0
	}
}
