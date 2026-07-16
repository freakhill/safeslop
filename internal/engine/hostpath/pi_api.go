package hostpath

import "os"

func safePiOAuthDirectoryMetadata(uid, currentUID uint32, mode os.FileMode) bool {
	return mode.IsDir() && uid == currentUID && mode.Perm()&0o022 == 0
}

// PiOAuthSourceStatus is the closed, value-free result of proving the one fixed
// host Pi OAuth source below HOME.
type PiOAuthSourceStatus uint8

const (
	PiOAuthSourceOK PiOAuthSourceStatus = iota
	PiOAuthSourceMissing
	PiOAuthSourceUnsafe
	PiOAuthSourceBusy
)
