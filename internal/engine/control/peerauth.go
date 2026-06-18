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

// authorizePeer rejects any peer whose uid differs from this process's uid (same-user only).
// This is the v1 control-plane peer-auth; codesign-identity verification is a follow-on
// (needs Security.framework / CGO — specs/0012 §2).
func authorizePeer(c *net.UnixConn) error {
	uid, err := peerUID(c)
	if err != nil {
		return fmt.Errorf("peer cred check: %w", err)
	}
	if uid != os.Getuid() {
		return fmt.Errorf("peer uid %d != server uid %d — cross-user control-plane access denied", uid, os.Getuid())
	}
	return nil
}
