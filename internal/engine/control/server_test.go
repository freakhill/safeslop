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

func TestTrustCallsTrustFn(t *testing.T) {
	got := ""
	s := &server{trustFn: func(cp string) (string, error) { got = cp; return "/abs/safeslop.cue", nil }}
	resp, err := s.Trust(context.Background(), &pb.TrustRequest{ConfigPath: "/repo"})
	if err != nil {
		t.Fatalf("Trust: %v", err)
	}
	if got != "/repo" || resp.TrustedPath != "/abs/safeslop.cue" {
		t.Fatalf("trustFn called with %q, resp=%+v", got, resp)
	}
}

func TestTrustUnwiredErrors(t *testing.T) {
	s := &server{}
	if _, err := s.Trust(context.Background(), &pb.TrustRequest{}); err == nil {
		t.Fatal("unwired Trust must error")
	}
}

func TestListProfilesCallsListFn(t *testing.T) {
	s := &server{listFn: func(cp string) ([]*pb.Profile, error) {
		return []*pb.Profile{{Name: "dev", Agent: "claude", Environment: "sandbox", Network: "deny", Tier: "mistake-guard"}}, nil
	}}
	resp, err := s.ListProfiles(context.Background(), &pb.ListProfilesRequest{})
	if err != nil {
		t.Fatalf("ListProfiles: %v", err)
	}
	if len(resp.Profiles) != 1 || resp.Profiles[0].Tier != "mistake-guard" {
		t.Fatalf("ListProfiles = %+v", resp.Profiles)
	}
}

func TestListProfilesUnwiredErrors(t *testing.T) {
	s := &server{}
	if _, err := s.ListProfiles(context.Background(), &pb.ListProfilesRequest{}); err == nil {
		t.Fatal("unwired ListProfiles must error")
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
