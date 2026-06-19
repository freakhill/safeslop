package control

import (
	"context"
	"testing"

	"google.golang.org/grpc"

	"github.com/freakhill/safeslop/internal/engine/control/pb"
)

// fakeApplyStream captures InstallApplyEvents sent by the server-streaming RPC.
type fakeApplyStream struct {
	grpc.ServerStream
	ctx  context.Context
	sent []*pb.InstallApplyEvent
}

func (f *fakeApplyStream) Context() context.Context { return f.ctx }
func (f *fakeApplyStream) Send(e *pb.InstallApplyEvent) error {
	f.sent = append(f.sent, e)
	return nil
}

func TestInstallApplyStreamsEvents(t *testing.T) {
	s := &server{
		version: "vTEST",
		installApplyFn: func(emit func(*pb.InstallApplyEvent)) error {
			emit(&pb.InstallApplyEvent{Kind: pb.InstallApplyEvent_START, Tool: "mise"})
			emit(&pb.InstallApplyEvent{Kind: pb.InstallApplyEvent_DONE, Tool: "mise"})
			return nil
		},
	}
	st := &fakeApplyStream{ctx: context.Background()}
	if err := s.InstallApply(&pb.InstallApplyRequest{}, st); err != nil {
		t.Fatalf("InstallApply: %v", err)
	}
	if len(st.sent) != 2 || st.sent[0].Kind != pb.InstallApplyEvent_START || st.sent[1].Kind != pb.InstallApplyEvent_DONE {
		t.Fatalf("unexpected event stream: %+v", st.sent)
	}
}

func TestInstallApplyUnwiredErrors(t *testing.T) {
	s := &server{version: "vTEST"} // installApplyFn nil
	st := &fakeApplyStream{ctx: context.Background()}
	if err := s.InstallApply(&pb.InstallApplyRequest{}, st); err != nil {
		t.Fatalf("InstallApply: %v", err)
	}
	if len(st.sent) != 1 || st.sent[0].Kind != pb.InstallApplyEvent_ERROR {
		t.Fatalf("expected a single ERROR event when unwired, got %+v", st.sent)
	}
}

func TestInstallPlanReturnsActions(t *testing.T) {
	s := &server{version: "vTEST"}
	resp, err := s.InstallPlan(context.Background(), &pb.InstallPlanRequest{})
	if err != nil {
		t.Fatalf("InstallPlan: %v", err)
	}
	_ = resp.Actions // whatever the embedded manifest diffs to; must not error
}
