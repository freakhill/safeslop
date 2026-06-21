package control

import (
	"context"
	"sort"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/freakhill/safeslop/internal/engine/control/pb"
	"github.com/freakhill/safeslop/internal/engine/install"
	"github.com/freakhill/safeslop/internal/engine/policy"
	"github.com/freakhill/safeslop/internal/engine/tools"
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
	preflightFn    func(profile, configPath string) (*pb.PreflightHostLaunchResponse, error)
	untrustFn      func(configPath string) (string, error)
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

// PreflightHostLaunch authors the per-launch host comprehension gate (specs/0030): the fixed honesty
// headline, a live-state scope line, and the shuffled consent rows (>=1 true host statement + >=1 false
// cross-tier decoy). Host-tier ONLY, and a pure read — nothing is recorded, so the gate re-draws every
// entry (consent is per-launch, never persisted). The peer is uid/process-tree-checked at Accept.
func (s *server) PreflightHostLaunch(_ context.Context, req *pb.PreflightHostLaunchRequest) (*pb.PreflightHostLaunchResponse, error) {
	if s.preflightFn == nil {
		return nil, status.Errorf(codes.Unimplemented, "preflight host launch not wired")
	}
	return s.preflightFn(req.Profile, req.ConfigPath)
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

// Untrust removes the host-side approval of the safeslop.cue at req.ConfigPath (trust.Store.Revoke), so
// the next ListProfiles reports it untrusted and a subsequent launch re-gates through the trust sheet —
// the symmetric reverse of Trust (ayo Actionable #3). Revoke removes privilege, so it needs no biometric
// (specs/0030 risk-proportional rule). The peer is uid/process-tree-checked at Accept.
func (s *server) Untrust(_ context.Context, req *pb.UntrustRequest) (*pb.UntrustResponse, error) {
	if s.untrustFn == nil {
		return nil, status.Errorf(codes.Unimplemented, "untrust not wired")
	}
	abs, err := s.untrustFn(req.ConfigPath)
	if err != nil {
		return nil, err
	}
	return &pb.UntrustResponse{UntrustedPath: abs}, nil
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

// ListTools returns the Installs-tab catalog with read-only detection (internal/engine/tools). A
// present tool is never installable, so the GUI can't ask safeslop to clobber an existing install.
// The peer is uid/process-tree-checked at Accept, so a sandboxed agent can't enumerate the host here.
func (s *server) ListTools(_ context.Context, req *pb.ListToolsRequest) (*pb.ListToolsResponse, error) {
	statuses := tools.DetectAll()
	if req.CatalogOnly {
		statuses = tools.CatalogStatuses() // instant first paint, detection deferred
	}
	out := &pb.ListToolsResponse{}
	for _, st := range statuses {
		ts := &pb.ToolStatus{
			Name: st.Tool.Name, Category: st.Tool.Category, Note: st.Tool.Note,
			Present: st.Present, Source: st.Source, Path: st.Path, Installable: st.Installable(),
			ShadowedPaths: st.ShadowedPaths,
			Precautions:   tools.Precautions(st), // hover tooltip — set for every tool
		}
		if ts.Installable {
			pv := tools.InstallPreview(st) // the consent gate's preview (specs/0037)
			ts.InstallHint = pv.Command
			ts.Verification = string(pv.Verification)
			ts.SourceUrl = pv.SourceURL
			ts.Sha256 = pv.SHA256
			ts.PinnedVersion = pv.Version
			ts.NeedsConsent = pv.NeedsConsent
		}
		out.Tools = append(out.Tools, ts)
	}
	return out, nil
}

// InstallTool installs ONE missing catalog tool by name, streaming output lines. tools.InstallByName
// refuses present tools (no-clobber). The command runs on the host as the user; the peer is already
// uid/process-tree-checked at Accept, so a sandboxed agent can't trigger host installs.
func (s *server) InstallTool(req *pb.InstallToolRequest, stream pb.Control_InstallToolServer) error {
	emit := func(line string) {
		_ = stream.Send(&pb.InstallToolEvent{Kind: pb.InstallToolEvent_LINE, Line: line})
	}
	if err := tools.InstallByName(req.Name, emit); err != nil {
		_ = stream.Send(&pb.InstallToolEvent{Kind: pb.InstallToolEvent_ERROR, Line: err.Error()})
		return nil
	}
	_ = stream.Send(&pb.InstallToolEvent{Kind: pb.InstallToolEvent_DONE})
	return nil
}

// ValidatePolicy vets unsaved CUE text from the editor (policy.LoadBytes) and returns either a
// cue-vet error or the parsed profiles, each tagged with tier + arbiter risk — the Create tab's live
// feedback loop. Pure parsing; no host mutation.
func (s *server) ValidatePolicy(_ context.Context, req *pb.ValidatePolicyRequest) (*pb.ValidatePolicyResponse, error) {
	cfg, err := policy.LoadBytes([]byte(req.CueText))
	if err != nil {
		return &pb.ValidatePolicyResponse{Valid: false, Error: err.Error()}, nil
	}
	resp := &pb.ValidatePolicyResponse{Valid: true}
	names := make([]string, 0, len(cfg.Profiles))
	for n := range cfg.Profiles {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		prof := cfg.Profiles[n]
		env := prof.Environment
		if env == "" {
			env = "sandbox"
		}
		tier, note := policy.EnvTier(env)
		risk := policy.RiskSummary(prof)
		resp.Profiles = append(resp.Profiles, &pb.Profile{
			Name: n, Agent: prof.Agent, Environment: env, Network: prof.Network,
			Tier: tier, TierNote: note,
			RiskHeadline: risk.Headline, RiskLevel: risk.Level, RiskLines: risk.Lines,
			TechStack: policy.TechStack(prof),
			RiskAxes:  RiskAxesPB(prof),
		})
	}
	return resp, nil
}

// ListPresets returns the bundled premade policies (policy.Presets) for the Create tab's stdlib picker.
func (s *server) ListPresets(_ context.Context, _ *pb.ListPresetsRequest) (*pb.ListPresetsResponse, error) {
	out := &pb.ListPresetsResponse{}
	for _, p := range policy.Presets() {
		out.Presets = append(out.Presets, &pb.Preset{Name: p.Name, Summary: p.Description, Cue: p.CUE})
	}
	return out, nil
}

// RiskAxesPB maps a profile's policy-level RiskAxes onto the wire type, so cockpitListProfiles and
// ValidatePolicy emit the same per-axis restriction status (single source of truth: policy.RiskAxes).
func RiskAxesPB(p policy.Profile) []*pb.RiskAxis {
	axes := policy.RiskAxes(p)
	out := make([]*pb.RiskAxis, 0, len(axes))
	for _, a := range axes {
		out = append(out, &pb.RiskAxis{Name: a.Name, Value: a.Value, Restricted: a.Restricted, Severity: a.Severity})
	}
	return out
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
