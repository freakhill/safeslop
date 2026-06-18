package control

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/freakhill/safeslop/internal/engine/control/pb"
)

// startTestServer serves Control on an ephemeral unix socket with a resolver that
// maps any profile to a `cat` session. Returns a connected client + cleanup.
func startTestServer(t *testing.T) (pb.ControlClient, func()) {
	t.Helper()
	dir := t.TempDir()
	addr := dir + "/s.sock"
	ln, err := net.Listen("unix", addr)
	if err != nil {
		t.Fatal(err)
	}
	mgr := NewManager()
	resolve := func(profile, configPath string) (SessionSpec, error) {
		return SessionSpec{Argv: []string{"cat"}}, nil
	}
	gs := grpc.NewServer()
	pb.RegisterControlServer(gs, &server{version: "test", mgr: mgr, resolveFn: resolve})
	go func() { _ = gs.Serve(ln) }()
	conn, err := grpc.NewClient("unix:"+addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	return pb.NewControlClient(conn), func() { conn.Close(); gs.Stop() }
}

func TestOpenAttachRoundTrip(t *testing.T) {
	c, done := startTestServer(t)
	defer done()
	ctx := context.Background()

	open, err := c.OpenSession(ctx, &pb.OpenSessionRequest{Profile: "any", Cols: 80, Rows: 24})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	st, err := c.Attach(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Send(&pb.ClientFrame{Msg: &pb.ClientFrame_AttachSessionId{AttachSessionId: open.SessionId}}); err != nil {
		t.Fatal(err)
	}
	if err := st.Send(&pb.ClientFrame{Msg: &pb.ClientFrame_Input{Input: []byte("ping\n")}}); err != nil {
		t.Fatal(err)
	}
	// read until we see the echo or time out
	deadline := time.Now().Add(3 * time.Second)
	var seen bool
	for time.Now().Before(deadline) && !seen {
		f, err := st.Recv()
		if err != nil {
			t.Fatalf("Recv: %v", err)
		}
		if o := f.GetOutput(); len(o) > 0 && contains(o, []byte("ping")) {
			seen = true
		}
	}
	if !seen {
		t.Fatal("did not see echoed input over Attach")
	}
	_, _ = c.CloseSession(ctx, &pb.CloseSessionRequest{SessionId: open.SessionId})
}

func contains(h, n []byte) bool {
	for i := 0; i+len(n) <= len(h); i++ {
		if string(h[i:i+len(n)]) == string(n) {
			return true
		}
	}
	return false
}
