package cli

import (
	"errors"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/container"
	runtimepkg "github.com/freakhill/safeslop/internal/engine/container/runtime"
	engsession "github.com/freakhill/safeslop/internal/engine/session"
)

// TestSessionReapKeyMatchesLaunchLabel guards specs/0074 Bug 1: the boundary reap must address the
// exact safeslop.session label the launch path stamps — SessionIDFromStageDir(stageDir), which
// carries stageDirFor's fnv(ws) suffix — not the bare session id. Reaping by the bare id silently
// misses the hash-suffixed label, so a detached `session stop` leaks its containers.
func TestSessionEngineUsesPersistedBackend(t *testing.T) {
	d := defaultDependencies()
	d.detectRuntime = func(runtimepkg.NetworkPolicy) (runtimepkg.Engine, error) {
		t.Fatal("session operation must not re-detect an ambient runtime")
		return nil, nil
	}
	d.backendEngine = func(name string) (runtimepkg.Engine, error) {
		if name != "podman" {
			t.Fatalf("persisted backend = %q, want podman", name)
		}
		return runtimepkg.PodmanEngine{}, nil
	}

	eng, err := d.engineForSession(engsession.Session{Environment: "container", Backend: "podman"})
	if err != nil || eng.Name() != "podman" {
		t.Fatalf("session engine = %v, %v; want persisted podman", eng, err)
	}
}

func TestSessionRuntimeOperationsFailClosedWhenBackendUnavailable(t *testing.T) {
	d := defaultDependencies()
	d.backendEngine = func(string) (runtimepkg.Engine, error) { return nil, errors.New("podman unavailable") }
	sess := engsession.Session{ID: "sess-test", Environment: "container", Backend: "podman", Workspace: t.TempDir()}

	if _, err := d.engineForSession(sess); !errors.Is(err, ErrSessionBackendUnavailable) {
		t.Fatalf("engine error = %v, want fixed backend error", err)
	}
	if err := d.applyEgressOverlay(t.Context(), sess, nil); !errors.Is(err, ErrSessionBackendUnavailable) {
		t.Fatalf("apply error = %v, want fixed backend error", err)
	}
	if _, err := d.inspectEgress(t.Context(), sess); !errors.Is(err, ErrSessionBackendUnavailable) {
		t.Fatalf("inspect error = %v, want fixed backend error", err)
	}
	if _, err := d.observeEgress(t.Context(), sess); !errors.Is(err, ErrSessionBackendUnavailable) {
		t.Fatalf("observe error = %v, want fixed backend error", err)
	}
	if err := sessionReapBoundaryWithDeps(d, sess); !errors.Is(err, ErrSessionBackendUnavailable) {
		t.Fatalf("reap error = %v, want fixed backend error", err)
	}
}

func TestSessionReapKeyMatchesLaunchLabel(t *testing.T) {
	sess := engsession.Session{ID: "sess-abc", Workspace: t.TempDir()}

	stageDir, err := stageDirFor("session-"+sess.ID, sess.Workspace)
	if err != nil {
		t.Fatalf("stageDirFor: %v", err)
	}
	launchLabel := container.SessionIDFromStageDir(stageDir)

	key, err := sessionReapKey(sess)
	if err != nil {
		t.Fatalf("sessionReapKey: %v", err)
	}
	if key != launchLabel {
		t.Fatalf("reap key %q != launch label %q (detached stop would leak)", key, launchLabel)
	}
	if launchLabel == sess.ID {
		t.Fatalf("precondition broken: launch label == bare id; test cannot catch the bug")
	}
}
