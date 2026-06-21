package control

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"google.golang.org/grpc"

	"github.com/freakhill/safeslop/internal/engine/control/pb"
	"github.com/freakhill/safeslop/internal/engine/install"
)

// socketPath is ~/.safeslop/s.sock — deliberately short (macOS sun_path is 104 bytes;
// ~/Library/Application Support/... + a long username would silently overflow).
func socketPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".safeslop", "s.sock"), nil
}

// Serve binds the UDS (0700 dir / 0600 socket), enforces same-uid peer-auth at Accept time,
// and serves the Control service until the listener is closed. version is reported by Ping;
// launchFn handles Launch RPCs (nil => Launch reports "not wired"); resolveFn maps a profile
// to a SessionSpec for OpenSession (the embedded-cockpit data plane).
func Serve(version string,
	launchFn func(profile, configPath string, emit func(*pb.LaunchEvent)) error,
	resolveFn func(profile, configPath string) (SessionSpec, error),
	trustFn func(configPath string) (string, error),
	listFn func(configPath string) ([]*pb.Profile, error),
	preflightFn func(profile, configPath string) (*pb.PreflightHostLaunchResponse, error),
	untrustFn func(configPath string) (string, error),
) error {
	path, err := socketPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	_ = os.Remove(path) // clear a stale socket from a prior crash
	ln, err := net.Listen("unix", path)
	if err != nil {
		return fmt.Errorf("bind %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return err
	}
	gs := grpc.NewServer()
	pb.RegisterControlServer(gs, NewControlServer(version, launchFn, resolveFn, trustFn, listFn, preflightFn, untrustFn))
	return gs.Serve(peerAuthListener{ln})
}

// NewControlServer builds the Control service implementation with the given engine wiring. Exposed so
// an in-process harness can serve it over a bufconn listener with the REAL fns — the headless analog
// of click-testing every cockpit tab's RPC (see internal/cli cockpit-backend smoke). Production goes
// through Serve, which binds the peer-authed UDS and registers exactly this.
func NewControlServer(version string,
	launchFn func(profile, configPath string, emit func(*pb.LaunchEvent)) error,
	resolveFn func(profile, configPath string) (SessionSpec, error),
	trustFn func(configPath string) (string, error),
	listFn func(configPath string) ([]*pb.Profile, error),
	preflightFn func(profile, configPath string) (*pb.PreflightHostLaunchResponse, error),
	untrustFn func(configPath string) (string, error),
) pb.ControlServer {
	return &server{
		version:        version,
		launchFn:       launchFn,
		mgr:            NewManager(),
		resolveFn:      resolveFn,
		trustFn:        trustFn,
		listFn:         listFn,
		preflightFn:    preflightFn,
		untrustFn:      untrustFn,
		installApplyFn: defaultInstallApply(version),
	}
}

// defaultInstallApply runs the pinned plan over the same HTTP fetcher + dirs the CLI uses,
// translating engine events to the wire enum.
func defaultInstallApply(version string) func(emit func(*pb.InstallApplyEvent)) error {
	return func(emit func(*pb.InstallApplyEvent)) error {
		ctx := context.Background()
		res, err := install.Plan(install.Status(ctx, version), install.DesiredState())
		if err != nil {
			return err
		}
		dirs, err := install.DefaultDirs()
		if err != nil {
			return err
		}
		return install.Apply(ctx, res, dirs, install.HTTPFetcher{}, func(e install.Event) {
			emit(installEventToPB(e))
		})
	}
}

// peerAuthListener rejects cross-uid peers at Accept time (before any RPC is served).
type peerAuthListener struct{ net.Listener }

func (l peerAuthListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	if uc, ok := c.(*net.UnixConn); ok {
		if aerr := authorizePeer(uc); aerr != nil {
			_ = c.Close()
			return l.Accept() // skip the unauthorized peer, keep serving
		}
	}
	return c, nil
}
