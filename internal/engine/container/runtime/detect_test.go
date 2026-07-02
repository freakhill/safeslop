package runtime

import (
	"context"
	"os/exec"
	"testing"
)

// fakeDetector builds a detector whose PATH lookup, capability probe, and env reads are all injected, so
// Detect's precedence + probes + egress gate are exercised without ever shelling a real docker/podman/lima.
// onPath drives lookPath; probeOK drives the capability probe's exit code (keyed by argv[0], which is the
// runtime binary). A runtime is "available" only when it is BOTH on PATH and its probe returns 0.
func fakeDetector(onPath, probeOK map[string]bool, env map[string]string) detector {
	return detector{
		lookPath: func(name string) (string, error) {
			if onPath[name] {
				return "/usr/bin/" + name, nil
			}
			return "", exec.ErrNotFound
		},
		run: func(_ context.Context, argv []string) (string, int, error) {
			if probeOK[argv[0]] {
				return "", 0, nil
			}
			return "probe failed", 1, nil
		},
		getenv: func(k string) string { return env[k] },
	}
}

// set is shorthand for the on-PATH / probe-OK maps.
func set(names ...string) map[string]bool {
	m := map[string]bool{}
	for _, n := range names {
		m[n] = true
	}
	return m
}

func TestDetectPrecedenceDockerBeatsPodmanAndLima(t *testing.T) {
	d := fakeDetector(set("docker", "podman", "lima"), set("docker", "podman", "lima"), nil)
	eng, err := detect(d, PolicyAllow)
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if eng.Name() != "docker" {
		t.Fatalf("precedence: want docker first, got %q", eng.Name())
	}
}

func TestDetectPrecedenceFallsToPodmanThenLima(t *testing.T) {
	// No docker → podman wins.
	eng, err := detect(fakeDetector(set("podman", "lima"), set("podman", "lima"), nil), PolicyAllow)
	if err != nil || eng.Name() != "podman" {
		t.Fatalf("no docker: want podman, got %q (err %v)", engName(eng), err)
	}
	// No docker/podman → lima wins.
	eng, err = detect(fakeDetector(set("lima"), set("lima"), nil), PolicyAllow)
	if err != nil || eng.Name() != "lima" {
		t.Fatalf("only lima: want lima, got %q (err %v)", engName(eng), err)
	}
}

// A docker CLI on PATH with no reachable daemon fails its `docker compose version` probe and must be
// treated as not-available (detection moves on), never "docker selected but broken" (D3).
func TestDetectDaemonDownIsNotAvailable(t *testing.T) {
	d := fakeDetector(set("docker", "podman"), set("podman"), nil) // docker on PATH, probe fails
	eng, err := detect(d, PolicyAllow)
	if err != nil || eng.Name() != "podman" {
		t.Fatalf("docker daemon down: want fall-through to podman, got %q (err %v)", engName(eng), err)
	}
}

func TestDetectNoneFailsClosed(t *testing.T) {
	if _, err := detect(fakeDetector(nil, nil, nil), PolicyAllow); err == nil {
		t.Fatal("no runtime present must fail closed, got nil error")
	}
}

func TestDetectEnvOverrideSelectsNamedRuntime(t *testing.T) {
	// docker is present and preferred by precedence, but the override pins podman.
	d := fakeDetector(set("docker", "podman"), set("docker", "podman"), map[string]string{"SAFESLOP_CONTAINER_RUNTIME": "podman"})
	eng, err := detect(d, PolicyAllow)
	if err != nil || eng.Name() != "podman" {
		t.Fatalf("override: want podman, got %q (err %v)", engName(eng), err)
	}
}

func TestDetectEnvOverrideUnknownRuntimeErrors(t *testing.T) {
	d := fakeDetector(set("docker"), set("docker"), map[string]string{"SAFESLOP_CONTAINER_RUNTIME": "containerd"})
	if _, err := detect(d, PolicyAllow); err == nil {
		t.Fatal("an unknown SAFESLOP_CONTAINER_RUNTIME must error, not silently ignore")
	}
}

// An override naming a runtime whose probe fails is a hard error — never a silent fallback to another
// runtime, even when one is available (S4: "use exactly this or fail").
func TestDetectEnvOverrideFailsClosedNeverFallsBack(t *testing.T) {
	d := fakeDetector(set("docker", "podman"), set("docker"), map[string]string{"SAFESLOP_CONTAINER_RUNTIME": "podman"}) // podman probe fails, docker works
	eng, err := detect(d, PolicyAllow)
	if err == nil {
		t.Fatalf("override podman with a failing probe must error, not fall back to %q", engName(eng))
	}
}

func TestDetectDenyAllowsVerifiedDocker(t *testing.T) {
	d := fakeDetector(set("docker"), set("docker"), nil)
	eng, err := detect(d, PolicyDeny)
	if err != nil || eng.Name() != "docker" {
		t.Fatalf("deny+docker must pass the egress gate, got %q (err %v)", engName(eng), err)
	}
}

// The fail-closed egress gate: a deny profile must NOT launch on an unverified rootless runtime (D6).
func TestDetectDenyRejectsUnverifiedRootless(t *testing.T) {
	for _, rt := range []string{"podman", "lima"} {
		if _, err := detect(fakeDetector(set(rt), set(rt), nil), PolicyDeny); err == nil {
			t.Fatalf("deny tier must fail closed on unverified %s, got nil error", rt)
		}
	}
}

// SAFESLOP_ALLOW_UNVERIFIED_RUNTIME=1 is the single explicit opt-in that opens the deny-tier gate.
func TestDetectDenyUnverifiedOpensWithOptIn(t *testing.T) {
	for _, rt := range []string{"podman", "lima"} {
		d := fakeDetector(set(rt), set(rt), map[string]string{"SAFESLOP_ALLOW_UNVERIFIED_RUNTIME": "1"})
		eng, err := detect(d, PolicyDeny)
		if err != nil || eng.Name() != rt {
			t.Fatalf("opt-in must open the gate for %s, got %q (err %v)", rt, engName(eng), err)
		}
	}
}

// Teardown paths pass PolicyAllow, which must never be gated: `down`/sweep/reap must be able to detect an
// unverified rootless runtime in order to CLEAN UP after it (the gate applies only to launching deny).
func TestDetectTeardownPolicyIsNotGated(t *testing.T) {
	for _, rt := range []string{"podman", "lima"} {
		eng, err := detect(fakeDetector(set(rt), set(rt), nil), PolicyAllow)
		if err != nil || eng.Name() != rt {
			t.Fatalf("teardown (PolicyAllow) must detect unverified %s for cleanup, got %q (err %v)", rt, engName(eng), err)
		}
	}
}

func engName(e Engine) string {
	if e == nil {
		return "<nil>"
	}
	return e.Name()
}
