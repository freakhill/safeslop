package cli

import (
	"testing"

	"github.com/freakhill/safeslop/internal/engine/container"
	engsession "github.com/freakhill/safeslop/internal/engine/session"
)

// TestSessionReapKeyMatchesLaunchLabel guards specs/0074 Bug 1: the boundary reap must address the
// exact safeslop.session label the launch path stamps — SessionIDFromStageDir(stageDir), which
// carries stageDirFor's fnv(ws) suffix — not the bare session id. Reaping by the bare id silently
// misses the hash-suffixed label, so a detached `session stop` leaks its containers.
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
