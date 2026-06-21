package cli

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/freakhill/safeslop/internal/engine/control"
	"github.com/freakhill/safeslop/internal/engine/control/pb"
)

// TestCockpitBackendSmoke drives the REAL Control gRPC server — the same service the SwiftUI cockpit
// talks to — over an in-memory bufconn, wired with the real engine fns (cockpitListProfiles /
// cockpitTrust / resolveSession). It is the headless analog of click-testing the three tabs, and the
// SwiftUI app's missing `--audit`: it verifies the backend of Launch (ListProfiles + honest tiers),
// Installs (ListTools catalog), and Create (ValidatePolicy good/bad + ListPresets), plus Ping /
// InstallPlan / Trust. No window server; runs in `make check` / CI.
func TestCockpitBackendSmoke(t *testing.T) {
	// Seed a trusted multi-profile repo (h/s/c/v across all four isolation tiers). trustPolicy also
	// repoints HOME at an isolated tempdir, so the trust store never touches the real ~/.config.
	dir := t.TempDir()
	cuePath := filepath.Join(dir, "safeslop.cue")
	if err := os.WriteFile(cuePath, []byte(resolverCue), 0o644); err != nil {
		t.Fatal(err)
	}
	trustPolicy(t, cuePath)

	cl, stop := startCockpitBackend(t)
	defer stop()
	ctx := context.Background()

	// Ping — liveness + version.
	if pong, err := cl.Ping(ctx, &pb.PingRequest{}); err != nil || pong.Version != "vSMOKE" {
		t.Fatalf("Ping = %+v err=%v", pong, err)
	}

	// Launch tab — ListProfiles returns the four seeded profiles, each carrying an honest tier.
	lp, err := cl.ListProfiles(ctx, &pb.ListProfilesRequest{ConfigPath: cuePath})
	if err != nil {
		t.Fatalf("ListProfiles: %v", err)
	}
	gotEnv := map[string]string{}
	for _, p := range lp.Profiles {
		gotEnv[p.Name] = p.Environment
		if p.Tier == "" {
			t.Errorf("profile %q rendered with no isolation tier (the GUI shows a blank ecusson)", p.Name)
		}
	}
	for name, env := range map[string]string{"h": "host", "s": "sandbox", "c": "container", "v": "vm"} {
		if gotEnv[name] != env {
			t.Errorf("ListProfiles[%q] env=%q want %q (got all: %v)", name, gotEnv[name], env, gotEnv)
		}
	}

	// Create tab — ValidatePolicy rejects broken CUE...
	bad, err := cl.ValidatePolicy(ctx, &pb.ValidatePolicyRequest{CueText: "package safeslop\nsafeslop: {{{ not valid"})
	if err != nil {
		t.Fatalf("ValidatePolicy(bad): transport err %v", err)
	}
	if bad.Valid || bad.Error == "" {
		t.Errorf("ValidatePolicy accepted broken CUE (valid=%v error=%q)", bad.Valid, bad.Error)
	}
	// ...and parses good CUE into profiles tagged with tier + arbiter risk (the live Create preview).
	good, err := cl.ValidatePolicy(ctx, &pb.ValidatePolicyRequest{CueText: resolverCue})
	if err != nil {
		t.Fatalf("ValidatePolicy(good): %v", err)
	}
	if !good.Valid || len(good.Profiles) != 4 {
		t.Fatalf("ValidatePolicy(good) valid=%v profiles=%d (want 4)", good.Valid, len(good.Profiles))
	}
	for _, p := range good.Profiles {
		if p.RiskHeadline == "" || p.RiskLevel == "" {
			t.Errorf("profile %q missing arbiter risk summary (headline=%q level=%q)", p.Name, p.RiskHeadline, p.RiskLevel)
		}
	}

	// Create tab — ListPresets returns the bundled stdlib (the Presets menu).
	pr, err := cl.ListPresets(ctx, &pb.ListPresetsRequest{})
	if err != nil || len(pr.GetPresets()) == 0 {
		t.Fatalf("ListPresets = %d presets, err=%v", len(pr.GetPresets()), err)
	}

	// Installs tab — ListTools(catalogOnly) returns the full catalog instantly. catalogOnly skips the
	// host-dependent detection pass, so the assertion is deterministic while still exercising the RPC.
	lt, err := cl.ListTools(ctx, &pb.ListToolsRequest{CatalogOnly: true})
	if err != nil || len(lt.GetTools()) < 15 {
		t.Fatalf("ListTools(catalogOnly) = %d tools, err=%v", len(lt.GetTools()), err)
	}
	for _, ts := range lt.Tools {
		if ts.Source != "unknown" { // catalogOnly defers detection — every tool is "?" until detected
			t.Errorf("catalogOnly tool %q should defer detection (source=%q)", ts.Name, ts.Source)
		}
	}

	// Installs tab — InstallPlan resolves against the host install state without erroring.
	if _, err := cl.InstallPlan(ctx, &pb.InstallPlanRequest{}); err != nil {
		t.Fatalf("InstallPlan: %v", err)
	}

	// Trust records host approval and echoes the absolute trusted path (the trust sheet's action).
	tr, err := cl.Trust(ctx, &pb.TrustRequest{ConfigPath: cuePath})
	if err != nil || tr.TrustedPath == "" {
		t.Fatalf("Trust = %+v err=%v", tr, err)
	}
}

// startCockpitBackend serves the real Control implementation over an in-memory bufconn and returns a
// connected client plus a stop func. Same wiring as `safeslop serve`, minus the UDS/peer-auth bind
// (the client and server share this process, so peer-auth is moot — it has its own test).
func startCockpitBackend(t *testing.T) (pb.ControlClient, func()) {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer()
	pb.RegisterControlServer(gs, control.NewControlServer("vSMOKE",
		nil, // launchFn: the Launch RPC has real side effects and is out of scope for the smoke.
		resolveSession, cockpitTrust, cockpitListProfiles,
	))
	go func() { _ = gs.Serve(lis) }()

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		gs.Stop()
		t.Fatalf("dial bufconn: %v", err)
	}
	return pb.NewControlClient(conn), func() {
		_ = conn.Close()
		gs.Stop()
	}
}
