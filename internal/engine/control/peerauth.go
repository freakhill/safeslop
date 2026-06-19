package control

import (
	"fmt"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

// peerUID returns the uid of the connected peer of a unix socket via LOCAL_PEERCRED (darwin).
func peerUID(c *net.UnixConn) (int, error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return -1, err
	}
	var xucred *unix.Xucred
	var gerr error
	if err := raw.Control(func(fd uintptr) {
		xucred, gerr = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	}); err != nil {
		return -1, err
	}
	if gerr != nil {
		return -1, gerr
	}
	return int(xucred.Uid), nil
}

// peerPID returns the pid of the connected peer via LOCAL_PEERPID (darwin <sys/un.h>). Unlike
// LOCAL_PEERCRED (uid only), this lets us tell the cockpit GUI apart from the sandboxed agent
// reaching back to its jailer (specs/0024 S1b).
func peerPID(c *net.UnixConn) (int, error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return -1, err
	}
	var pid int
	var gerr error
	if err := raw.Control(func(fd uintptr) {
		pid, gerr = unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID)
	}); err != nil {
		return -1, err
	}
	return pid, gerr
}

// ppidOf returns the parent pid of pid via the kernel proc table — a sysctl, not an exec, so it
// works regardless of any fs sandbox and adds no process to the tree.
func ppidOf(pid int) (int, error) {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return -1, err
	}
	return int(kp.Eproc.Ppid), nil
}

// isProcessTreeDescendant reports whether pid is a STRICT descendant of ancestor — walking pid's
// parent chain reaches ancestor. ancestor == pid is NOT a descendant (so the server connecting to
// itself, as in the unit test, is allowed). The walk is bounded so a garbage/cyclic table can't spin.
func isProcessTreeDescendant(ancestor, pid int, ppidOf func(int) (int, error)) (bool, error) {
	cur := pid
	for depth := 0; depth < 64; depth++ {
		parent, err := ppidOf(cur)
		if err != nil {
			return false, err
		}
		if parent == ancestor {
			return true, nil
		}
		if parent <= 1 {
			return false, nil
		}
		cur = parent
	}
	return false, nil
}

// authorizePeer rejects (a) any peer whose uid differs from this process's uid (same-user only), and
// (b) any peer that lives inside a safeslop-spawned process tree — the sandboxed agent reaching back
// to drive its own jailer (specs/0024 S1b). A same-uid peer OUTSIDE our tree (the cockpit GUI) is
// allowed; arbitrary same-uid host malware is the residual that the codesign/audit-token follow-on
// closes (specs/0024 Deferred). Codesign-identity verification needs Security.framework — see
// specs/0012 §2.
func authorizePeer(c *net.UnixConn) error {
	uid, err := peerUID(c)
	if err != nil {
		return fmt.Errorf("peer cred check: %w", err)
	}
	if uid != os.Getuid() {
		return fmt.Errorf("peer uid %d != server uid %d — cross-user control-plane access denied", uid, os.Getuid())
	}
	pid, err := peerPID(c)
	if err != nil {
		return nil // can't determine pid: fall back to the uid gate + the policy trust gate (S1a)
	}
	desc, err := isProcessTreeDescendant(os.Getpid(), pid, ppidOf)
	if err != nil {
		return nil // can't walk the tree: don't break the legit GUI on a transient sysctl error
	}
	if desc {
		return fmt.Errorf("control-plane access from pid %d denied: peer is inside a safeslop-spawned sandbox", pid)
	}
	return nil
}
