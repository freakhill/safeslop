package cli

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/container"
	"github.com/freakhill/safeslop/internal/engine/container/runtime"
	engexec "github.com/freakhill/safeslop/internal/engine/exec"
	"github.com/freakhill/safeslop/internal/engine/policy"
	engsession "github.com/freakhill/safeslop/internal/engine/session"
)

// TestRecordSessionBackendPersistsDetectedRuntime pins specs/0066 D7: recordSessionBackend fills
// Session.Backend from the detected ambient runtime's Name(). The detection seam is stubbed so the test
// stays hermetic (no real docker/podman/lima probe).
func TestRecordSessionBackendPersistsDetectedRuntime(t *testing.T) {
	store := engsession.NewStore(filepath.Join(t.TempDir(), "sessions"))
	sess, err := store.Create("claude", "container", t.TempDir(), nowForTest(t))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	d := defaultDependencies()
	d.detectRuntime = func(runtime.NetworkPolicy) (runtime.Engine, error) { return runtime.PodmanEngine{}, nil }
	updated, err := recordSessionBackendWithDeps(d, store, sess)
	if err != nil {
		t.Fatalf("record backend: %v", err)
	}
	if updated.Backend != "podman" {
		t.Fatalf("backend = %q, want podman", updated.Backend)
	}
	stored, err := store.Get(sess.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if stored.Backend != "podman" {
		t.Fatalf("stored backend = %q, want podman", stored.Backend)
	}
}

func TestRecordSessionBackendDoesNotReplacePersistedRuntime(t *testing.T) {
	store := engsession.NewStore(filepath.Join(t.TempDir(), "sessions"))
	sess, err := store.Create("claude", "container", t.TempDir(), nowForTest(t))
	if err != nil {
		t.Fatal(err)
	}
	sess.Backend = "podman"
	if err := store.Save(sess); err != nil {
		t.Fatal(err)
	}
	d := defaultDependencies()
	d.detectRuntime = func(runtime.NetworkPolicy) (runtime.Engine, error) {
		t.Fatal("persisted session must not re-detect an ambient runtime")
		return nil, nil
	}
	d.backendEngine = func(name string) (runtime.Engine, error) {
		if name != "podman" {
			t.Fatalf("backend = %q, want podman", name)
		}
		return runtime.PodmanEngine{}, nil
	}

	updated, err := recordSessionBackendWithDeps(d, store, sess)
	if err != nil {
		t.Fatalf("record backend: %v", err)
	}
	if updated.Backend != "podman" {
		t.Fatalf("backend = %q, want persisted podman", updated.Backend)
	}
}

func TestRecordSessionBackendFailsClosedWhenInitialDetectionFails(t *testing.T) {
	store := engsession.NewStore(filepath.Join(t.TempDir(), "sessions"))
	sess, err := store.Create("claude", "container", t.TempDir(), nowForTest(t))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	d := defaultDependencies()
	d.detectRuntime = func(runtime.NetworkPolicy) (runtime.Engine, error) { return nil, errors.New("runtime unavailable") }
	_, err = recordSessionBackendWithDeps(d, store, sess)
	if !errors.Is(err, ErrSessionBackendUnavailable) {
		t.Fatalf("record backend error = %v, want fixed unavailable backend error", err)
	}
	stored, err := store.Get(sess.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if stored.Backend != "" || stored.RecipeID != "" || stored.Image != "" || stored.Resolved != nil {
		t.Fatalf("failed detection persisted launch state: %+v", stored)
	}
}

func TestSessionLaunchUsesDetectedEngineWithoutSecondProbe(t *testing.T) {
	store := engsession.NewStore(filepath.Join(t.TempDir(), "sessions"))
	sess, err := store.Create("claude", "container", t.TempDir(), nowForTest(t))
	if err != nil {
		t.Fatal(err)
	}
	d := defaultDependencies()
	probes := 0
	d.detectRuntime = func(network runtime.NetworkPolicy) (runtime.Engine, error) {
		probes++
		if network != runtime.PolicyDeny {
			t.Fatalf("runtime policy = %v, want deny gate", network)
		}
		return runtime.PodmanEngine{}, nil
	}
	var launched runtime.Engine
	d.launchContainer = func(_ context.Context, eng runtime.Engine, _ engexec.LaunchSpec, _, _ string, _, _ []string, _ string, _ []string, _ *policy.Projection, _ ...container.SessionGrant) (int, error) {
		launched = eng
		return 0, nil
	}

	updated, eng, err := prepareSessionBackendWithDeps(d, store, sess)
	if err != nil {
		t.Fatalf("prepare session backend: %v", err)
	}
	prof, err := sessionProfile(updated)
	if err != nil {
		t.Fatal(err)
	}
	argv, err := agentArgv(prof)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runProfileCtxWithEngineAndDeps(d, context.Background(), eng, "session-"+sess.ID, prof, argv, updated.Workspace, ""); err != nil {
		t.Fatalf("launch session: %v", err)
	}
	if probes != 1 {
		t.Fatalf("runtime probes = %d, want exactly one", probes)
	}
	if launched != eng || launched.Name() != "podman" {
		t.Fatalf("launch engine = %v, want detected podman engine", launched)
	}
}

func TestSweepManagedOrphansNoopsWhenDockerUnavailable(t *testing.T) {
	t.Setenv("SAFESLOP_STATE_DIR", filepath.Join(t.TempDir(), "state"))
	d := defaultDependencies()
	d.detectRuntime = func(runtime.NetworkPolicy) (runtime.Engine, error) {
		return nil, errors.New("runtime unavailable")
	}
	if err := sweepManagedOrphansWithDeps(d, t.Context()); err != nil {
		t.Fatalf("sweep with no docker should no-op: %v", err)
	}
}

func TestGCUsesDetectedRuntimeAndOwnedGCSeam(t *testing.T) {
	d := defaultDependencies()
	d.detectRuntime = func(runtime.NetworkPolicy) (runtime.Engine, error) { return runtime.PodmanEngine{}, nil }
	called := false
	d.gcImages = func(_ context.Context, eng runtime.Engine, opts container.GCOptions, _ container.GCProtection) ([]string, error) {
		called = true
		if eng.Name() != "podman" || opts.Keep != 2 || opts.Until != "24h" {
			t.Fatalf("gc engine/options = %q/%+v, want podman/keep=2/until=24h", eng.Name(), opts)
		}
		return []string{"local/safeslop-tools:old"}, nil
	}

	out, err := runRootForTestWithDeps(t, t.TempDir(), d, "gc", "--until", "24h", "--keep", "2", "--json")
	if err != nil {
		t.Fatalf("gc: %v", err)
	}
	if !called || !strings.Contains(out, "local/safeslop-tools:old") {
		t.Fatalf("gc did not use owned seam: called=%t output=%s", called, out)
	}
}

func TestGCUnavailableDoesNotRunGC(t *testing.T) {
	d := defaultDependencies()
	d.detectRuntime = func(runtime.NetworkPolicy) (runtime.Engine, error) { return nil, errors.New("unavailable") }
	d.gcImages = func(context.Context, runtime.Engine, container.GCOptions, container.GCProtection) ([]string, error) {
		t.Fatal("GC must not run without a detected runtime")
		return nil, nil
	}

	_, err := runRootForTestWithDeps(t, t.TempDir(), d, "gc")
	if err == nil || !strings.Contains(err.Error(), "cannot gc: container runtime is unavailable") {
		t.Fatalf("gc error = %v, want fixed unavailable runtime error", err)
	}
}

func TestGcHelp(t *testing.T) {
	out, err := runRootForTest(t, t.TempDir(), "gc", "--help")
	if err != nil {
		t.Fatalf("gc --help: %v", err)
	}
	for _, want := range []string{"Garbage-collect", "--keep", "--until"} {
		if !strings.Contains(out, want) {
			t.Fatalf("gc help missing %q:\n%s", want, out)
		}
	}
}
