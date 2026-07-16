//go:build linux

package container

import (
	"os"

	"golang.org/x/sys/unix"
)

func projectionSafetySupported() bool { return true }

func fileMountID(file *os.File) (uint64, bool) {
	var stat unix.Statx_t
	if err := unix.Statx(int(file.Fd()), "", unix.AT_EMPTY_PATH|unix.AT_SYMLINK_NOFOLLOW, unix.STATX_MNT_ID, &stat); err != nil {
		return 0, false
	}
	if stat.Mask&unix.STATX_MNT_ID == 0 {
		return 0, false
	}
	return stat.Mnt_id, true
}
