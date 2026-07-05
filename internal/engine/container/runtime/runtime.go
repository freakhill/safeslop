// Package runtime detects and drives an AMBIENT, user-provided container runtime for the container tier
// (internal/engine/container). safeslop no longer installs, upgrades, or manages any runtime (specs/0066
// dropped the self-installer + the safeslop-managed lima VM). It detects one of docker / podman / a
// user-managed lima on PATH and drives it through the Engine seam; it never provisions one.
package runtime

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/freakhill/safeslop/internal/engine/hostexec"
)

// NetworkPolicy is the egress posture Detect gates on. It mirrors a profile's `network:` field: a deny
// profile needs a runtime whose no-egress network is proven, so Detect fail-closes on an unverified
// rootless runtime (D6); an allow profile intends egress, so any detected runtime is fine.
type NetworkPolicy int

const (
	// PolicyAllow means egress is intended (network: allow). Also the non-gating policy every TEARDOWN
	// path uses (down / sweep / reap): cleaning up must work on ANY detected runtime, verified or not —
	// the deny-tier gate applies only to LAUNCHING a deny profile, never to teardown.
	PolicyAllow NetworkPolicy = iota
	// PolicyDeny means no-egress is required (network: deny). Detect refuses an unverified rootless
	// runtime unless SAFESLOP_ALLOW_UNVERIFIED_RUNTIME=1.
	PolicyDeny
)

// Runner runs an argv and returns combined output + exit code; injected so unit tests never shell a real
// docker/podman/lima. A non-zero exit is reported as (output, code, nil); a failure to launch is err.
type Runner func(ctx context.Context, argv []string) (output string, exitCode int, err error)

func defaultRunner(ctx context.Context, argv []string) (string, int, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Path = argv[0]
	cmd.Args[0] = argv[0]
	cmd.Env = hostexec.Default().EnvFor(hostexec.EnvRuntime)
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code, err = ee.ExitCode(), nil
		} else {
			code = -1
		}
	}
	return buf.String(), code, err
}

// detector holds the injected seams (PATH lookup, command runner, env reader) so Detect's precedence,
// capability probes, and egress gate are all unit-testable without shelling a real runtime.
type detector struct {
	resolveRuntime func(string) (string, error)
	run            Runner
	getenv         func(string) string
}

// Detect runs the ambient-runtime selection (specs/0066 D3/D4/D6) with production seams and returns a
// ready, zero-config Engine or a fail-closed error. policy gates the deny tier (see NetworkPolicy).
func Detect(policy NetworkPolicy) (Engine, error) {
	resolver := hostexec.Default()
	return detect(detector{
		resolveRuntime: func(name string) (string, error) {
			res, err := resolver.Resolve(hostexec.RuntimeSpec(name, "container runtime"))
			if err != nil {
				return "", err
			}
			return res.Path, nil
		},
		run:    defaultRunner,
		getenv: os.Getenv,
	}, policy)
}

// candidates is the fixed auto-detect precedence: docker → podman → lima (D3). docker wins when present
// because it is the only deny-verified runtime today and matches every existing OrbStack/Docker user.
func candidates() []Engine { return []Engine{HostDockerEngine{}, PodmanEngine{}, LimaEngine{}} }

// engineByName maps a SAFESLOP_CONTAINER_RUNTIME override value to its Engine.
func engineByName(name string) (Engine, bool) {
	for _, e := range candidates() {
		if e.Name() == name {
			return e, true
		}
	}
	return nil, false
}

func detect(d detector, policy NetworkPolicy) (Engine, error) {
	// 1. Explicit override: use EXACTLY this runtime or fail — never silently fall back to another (S4).
	if name := d.getenv("SAFESLOP_CONTAINER_RUNTIME"); name != "" {
		eng, ok := engineByName(name)
		if !ok {
			return nil, fmt.Errorf("SAFESLOP_CONTAINER_RUNTIME=%q is not a known runtime; choose one of docker, podman, lima", name)
		}
		resolved, ok, err := d.available(eng)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("SAFESLOP_CONTAINER_RUNTIME=%q was selected but its capability probe failed (CLI absent, daemon down, or an unusable lima instance/template); an override means use exactly this runtime or fail", name)
		}
		return d.gate(resolved, policy)
	}

	// 2. Auto-detect in fixed precedence: first whose CLI + working compose capability is present wins.
	for _, eng := range candidates() {
		resolved, ok, err := d.available(eng)
		if err != nil {
			return nil, err
		}
		if ok {
			return d.gate(resolved, policy)
		}
	}

	// 3. None present/working → fail closed with an actionable error naming all three (D3).
	return nil, fmt.Errorf("no working container runtime found: safeslop needs one of docker, podman, or lima on PATH with a working compose (install OrbStack/Docker Desktop, `brew install podman`, or `brew install lima` and start an instance), or set SAFESLOP_CONTAINER_RUNTIME to name one explicitly")
}

// available runs the per-runtime capability probe (D4): CLI on sanitized PATH is necessary but not sufficient.
func (d detector) available(eng Engine) (Engine, bool, error) {
	path, err := d.resolveRuntime(eng.Name())
	if err != nil {
		if errors.Is(err, hostexec.ErrNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
	resolved := engineWithPath(eng, path)
	switch resolved.(type) {
	case HostDockerEngine:
		return resolved, d.probeDocker(resolved), nil
	case PodmanEngine:
		return resolved, d.probePodman(resolved), nil
	case LimaEngine:
		return resolved, d.probeLima(resolved), nil
	default:
		return nil, false, nil
	}
}

// probeDocker: `docker compose version` succeeds — which also implies a reachable daemon, so a `docker`
// CLI on PATH with no running daemon fails here and is treated as not-available (never "docker selected
// but broken"). Mirrors container.Available().
func (d detector) probeDocker(eng Engine) bool {
	_, code, err := d.run(context.Background(), eng.Argv("compose", "version"))
	return err == nil && code == 0
}

// probePodman: NOT merely `podman compose version` — `podman compose` may delegate to podman-compose
// (Python), docker-compose v1, or the v2 plugin, each with different external-network semantics (B2). The
// probe asserts the external-network split actually parses: render a minimal compose referencing an
// `external: true` network and run `podman compose -f <file> config`; reject podman if it does not.
func (d detector) probePodman(eng Engine) bool {
	f, err := os.CreateTemp("", "safeslop-podman-probe-*.yml")
	if err != nil {
		return false
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(externalNetworkProbeCompose); err != nil {
		f.Close()
		return false
	}
	f.Close()
	_, code, err := d.run(context.Background(), eng.Argv("compose", "-f", f.Name(), "config"))
	return err == nil && code == 0
}

// probeLima: `lima nerdctl info` must succeed — a lima on the docker template (no containerd/nerdctl) or
// with no started instance FAILS here, so detection fails closed rather than mis-driving it (S5).
func (d detector) probeLima(eng Engine) bool {
	_, code, err := d.run(context.Background(), eng.Argv("info"))
	return err == nil && code == 0
}

// externalNetworkProbeCompose is the minimal compose the podman probe renders: a service on a network
// declared `external: true`. If `podman compose config` parses it, podman's compose supports the
// external-network split safeslop's `--internal` egress topology relies on.
const externalNetworkProbeCompose = `services:
  probe:
    image: alpine
    networks: [internal]
networks:
  internal:
    external: true
    name: ` + internalNetworkName + `
`

// gate is the fail-closed deny-tier egress capability gate (D6). For a deny profile it refuses a rootless
// runtime whose no-egress guarantee is not live-validated (podman, lima — until D8 records a passing
// acceptance), unless the operator sets SAFESLOP_ALLOW_UNVERIFIED_RUNTIME=1 (the single, logged opt-in).
// It is never reached for teardown, which always passes PolicyAllow.
func (d detector) gate(eng Engine, policy NetworkPolicy) (Engine, error) {
	if policy != PolicyDeny || verifiedForDeny(eng) {
		return eng, nil // egress intended, or the runtime's no-egress network is proven
	}
	if d.getenv("SAFESLOP_ALLOW_UNVERIFIED_RUNTIME") == "1" {
		fmt.Fprintf(os.Stderr, "safeslop: WARNING: runtime %q is not egress-verified for the deny tier; launching anyway because SAFESLOP_ALLOW_UNVERIFIED_RUNTIME=1 (its no-egress isolation is unproven — see specs/0066 D8)\n", eng.Name())
		return eng, nil
	}
	return nil, fmt.Errorf("runtime %q is not egress-verified for deny-tier: a network:deny profile requires a runtime whose no-egress network is proven, and only docker/OrbStack qualify today. Run the live-validation acceptance (specs/0066 D8), use docker, or set SAFESLOP_ALLOW_UNVERIFIED_RUNTIME=1 to accept the risk", eng.Name())
}

// verifiedForDeny reports whether an engine's no-egress guarantee is recorded as live-validated for the
// deny tier. Only docker/OrbStack qualify today (rootful/VM-backed, so compose's inline `internal: true`
// truly isolates egress); podman + lima join the list only after D8 records a passing acceptance run.
func verifiedForDeny(eng Engine) bool {
	_, ok := eng.(HostDockerEngine)
	return ok
}
