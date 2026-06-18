package control

import (
	"context"

	"github.com/freakhill/safeslop/internal/engine/control/pb"
)

// server implements pb.ControlServer. Launch delegation is wired in Serve via launchFn.
type server struct {
	pb.UnimplementedControlServer
	version  string
	launchFn func(profile, configPath string, emit func(*pb.LaunchEvent)) error
}

func (s *server) Ping(_ context.Context, _ *pb.PingRequest) (*pb.PingResponse, error) {
	return &pb.PingResponse{Version: s.version}, nil
}

func (s *server) Launch(req *pb.LaunchRequest, stream pb.Control_LaunchServer) error {
	emit := func(e *pb.LaunchEvent) { _ = stream.Send(e) }
	if s.launchFn == nil {
		emit(&pb.LaunchEvent{Kind: pb.LaunchEvent_ERROR, Message: "launch not wired"})
		return nil
	}
	return s.launchFn(req.Profile, req.ConfigPath, emit)
}
