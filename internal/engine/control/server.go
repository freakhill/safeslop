package control

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/freakhill/safeslop/internal/engine/control/pb"
)

// server implements pb.ControlServer. Launch delegation is wired in Serve via launchFn;
// session RPCs are backed by mgr + the profile->SessionSpec resolver resolveFn.
type server struct {
	pb.UnimplementedControlServer
	version   string
	launchFn  func(profile, configPath string, emit func(*pb.LaunchEvent)) error
	mgr       *Manager
	resolveFn func(profile, configPath string) (SessionSpec, error)
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
