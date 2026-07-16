//go:build darwin

package hostpath

import (
	"os"
	"syscall"
)

func projectionSafetySupported() bool { return true }

func fileMountID(file *os.File) (uint64, bool) {
	var stat syscall.Statfs_t
	if err := syscall.Fstatfs(int(file.Fd()), &stat); err != nil {
		return 0, false
	}
	return uint64(uint32(stat.Fsid.Val[0]))<<32 | uint64(uint32(stat.Fsid.Val[1])), true
}
