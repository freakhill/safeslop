package control

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/freakhill/safeslop/internal/engine/control/pb"
	"github.com/freakhill/safeslop/internal/engine/install"
)

// server implements pb.ControlServer. Launch delegation is wired in Serve via launchFn;
// session RPCs are backed by mgr + the profile->SessionSpec resolver resolveFn; install apply
// is wired via installApplyFn.
type server struct {
	pb.UnimplementedControlServer
	version        string
	launchFn       func(profile, configPath string, emit func(*pb.LaunchEvent)) error
	mgr            *Manager
	resolveFn      func(profile, configPath string) (SessionSpec, error)
	trustFn        func(configPath string) (string, error)
	listFn         func(configPath string) ([]*pb.Profile, error)
	installApplyFn func(emit func(*pb.InstallApplyEvent)) error
}

// ListProfiles returns the profiles declared in the safeslop.cue at req.ConfigPath, each carrying
// its honest isolation tier (from policy.EnvTier) so the GUI renders one source of truth. Listing
// is inspection (like `validate`/`list`), so it is not trust-gated; the peer is uid/process-tree-
// checked at Accept (peerauth.go).
func (s *server) ListProfiles(_ context.Context, req *pb.ListProfilesRequest) (*pb.ListProfilesResponse, error) {
	if s.listFn == nil {
		return nil, status.Errorf(codes.Unimplemented, "list profiles not wired")
	}
	profs, err := s.listFn(req.ConfigPath)
	if err != nil {
		return nil, err
	}
	return &pb.ListProfilesResponse{Profiles: profs}, nil
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

func (s *server) OpenSession(_ context.Context, req *pb.OpenSessionRequest) (*pb.OpenSessionResponse, error) {
	spec, err := s.resolveFn(req.Profile, req.ConfigPath)
	if err != nil {
		return nil, err
	}
	id, err := s.mgr.Open(spec)
	if err != nil {
		return nil, err
	}
	if req.Cols > 0 && req.Rows > 0 {
		if sess, ok := s.mgr.Get(id); ok {
			_ = sess.Resize(uint16(req.Cols), uint16(req.Rows))
		}
	}
	return &pb.OpenSessionResponse{SessionId: id}, nil
}

func (s *server) Attach(stream pb.Control_AttachServer) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	id := first.GetAttachSessionId()
	sess, ok := s.mgr.Get(id)
	if !ok {
		return status.Errorf(codes.NotFound, "unknown session %q", id)
	}
	// output pump: PTY -> client
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, er := sess.Read(buf)
			if n > 0 {
				_ = stream.Send(&pb.ServerFrame{Msg: &pb.ServerFrame_Output{Output: append([]byte(nil), buf[:n]...)}})
			}
			if er != nil {
				return
			}
		}
	}()
	// notify on exit
	go func() {
		<-sess.Exited()
		_ = stream.Send(&pb.ServerFrame{Msg: &pb.ServerFrame_Exited{Exited: &pb.Exited{ExitCode: int32(sess.Code())}}})
	}()
	// input loop: client -> PTY / resize
	for {
		f, err := stream.Recv()
		if err != nil {
			return nil // client hung up
		}
		switch m := f.Msg.(type) {
		case *pb.ClientFrame_Input:
			_, _ = sess.Write(m.Input)
		case *pb.ClientFrame_Resize:
			_ = sess.Resize(uint16(m.Resize.Cols), uint16(m.Resize.Rows))
		}
	}
}

func (s *server) CloseSession(_ context.Context, req *pb.CloseSessionRequest) (*pb.CloseSessionResponse, error) {
	s.mgr.Close(req.SessionId)
	return &pb.CloseSessionResponse{}, nil
}

// Trust records host-side approval of the safeslop.cue at req.ConfigPath so a subsequent
// OpenSession passes the fail-closed trust gate (specs/0024 S1a). The peer is already
// uid- and process-tree-checked at Accept (peerauth.go), so a sandboxed agent can't call this.
func (s *server) Trust(_ context.Context, req *pb.TrustRequest) (*pb.TrustResponse, error) {
	if s.trustFn == nil {
		return nil, status.Errorf(codes.Unimplemented, "trust not wired")
	}
	abs, err := s.trustFn(req.ConfigPath)
	if err != nil {
		return nil, err
	}
	return &pb.TrustResponse{TrustedPath: abs}, nil
}

func (s *server) InstallPlan(_ context.Context, _ *pb.InstallPlanRequest) (*pb.InstallPlanResponse, error) {
	st := install.Status(context.Background(), s.version)
	res, err := install.Plan(st, install.DesiredState())
	if err != nil {
		return nil, err
	}
	out := &pb.InstallPlanResponse{}
	for _, a := range res.Actions {
		out.Actions = append(out.Actions, &pb.InstallAction{
			Name: a.Name, Kind: string(a.Kind), Current: a.Current, Desired: a.Desired,
		})
	}
	return out, nil
}

func (s *server) InstallApply(_ *pb.InstallApplyRequest, stream pb.Control_InstallApplyServer) error {
	emit := func(e *pb.InstallApplyEvent) { _ = stream.Send(e) }
	if s.installApplyFn == nil {
		emit(&pb.InstallApplyEvent{Kind: pb.InstallApplyEvent_ERROR, Msg: "install apply not wired"})
		return nil
	}
	return s.installApplyFn(emit)
}

// installEventToPB maps a pb-free install.Event onto the wire enum.
func installEventToPB(e install.Event) *pb.InstallApplyEvent {
	k := pb.InstallApplyEvent_PROGRESS
	switch e.Kind {
	case install.EventStart:
		k = pb.InstallApplyEvent_START
	case install.EventDone:
		k = pb.InstallApplyEvent_DONE
	case install.EventError:
		k = pb.InstallApplyEvent_ERROR
	}
	return &pb.InstallApplyEvent{Kind: k, Tool: e.Tool, Msg: e.Msg}
}
