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
