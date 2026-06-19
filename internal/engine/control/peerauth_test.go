package control

import (
	"net"
	"os"
	"testing"
)

func TestPeerUIDOnUnixSocket(t *testing.T) {
	// A real unix socket: both ends are this process, so the peer uid == our uid.
	dir := t.TempDir()
	addr := dir + "/s.sock"
	ln, err := net.Listen("unix", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := ln.Accept()
		if err != nil {
			t.Errorf("accept: %v", err)
			return
		}
		defer conn.Close()
		uid, err := peerUID(conn.(*net.UnixConn))
		if err != nil {
			t.Errorf("peerUID: %v", err)
			return
		}
		if uid != os.Getuid() {
			t.Errorf("peer uid = %d, want %d", uid, os.Getuid())
		}
		if err := authorizePeer(conn.(*net.UnixConn)); err != nil {
			t.Errorf("same-uid peer must authorize: %v", err)
		}
	}()

	c, err := net.Dial("unix", addr)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	<-done
}

func TestIsProcessTreeDescendant(t *testing.T) {
	// fake tree: serve(100) -> sandbox-exec(200) -> agent(300); gui(50) -> launchd(1)
	parents := map[int]int{300: 200, 200: 100, 100: 1, 50: 1}
	ppidOf := func(pid int) (int, error) { return parents[pid], nil }

	// the sandboxed agent IS a descendant of serve -> must be detected (rejected upstream)
	if d, _ := isProcessTreeDescendant(100, 300, ppidOf); !d {
		t.Error("agent (300) must be detected as a descendant of serve (100)")
	}
	// the cockpit GUI is NOT a descendant of serve -> allowed
	if d, _ := isProcessTreeDescendant(100, 50, ppidOf); d {
		t.Error("gui (50) must NOT be a descendant of serve (100)")
	}
	// serve connecting to itself is not a STRICT descendant -> allowed (the same-process test case)
	if d, _ := isProcessTreeDescendant(100, 100, ppidOf); d {
		t.Error("serve (100) must not be its own descendant")
	}
}
