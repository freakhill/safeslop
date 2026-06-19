package control

import (
	"context"
	"strings"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/control/pb"
)

func TestServerPing(t *testing.T) {
	s := &server{version: "vTEST"}
	resp, err := s.Ping(context.Background(), &pb.PingRequest{})
	if err != nil || resp.Version != "vTEST" {
		t.Fatalf("Ping = %+v err=%v", resp, err)
	}
}

func TestSocketPathIsShort(t *testing.T) {
	p, err := socketPath()
	if err != nil {
		t.Fatal(err)
	}
	if len(p) >= 104 {
		t.Fatalf("socket path %q exceeds the 104-byte sun_path limit (%d)", p, len(p))
	}
	if !strings.HasSuffix(p, "/.safeslop/s.sock") {
		t.Fatalf("socket path = %q, want ~/.safeslop/s.sock", p)
	}
}
