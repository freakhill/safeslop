//go:build darwin

package session

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func processStartTokenOS(pid int) (string, bool) {
	if pid <= 0 {
		return "", false
	}
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || kp == nil || kp.Proc.P_pid != int32(pid) {
		return "", false
	}
	tv := kp.Proc.P_starttime
	if tv.Sec == 0 && tv.Usec == 0 {
		return "", false
	}
	return fmt.Sprintf("darwin:%d.%06d", tv.Sec, tv.Usec), true
}
