package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	engsession "github.com/freakhill/safeslop/internal/engine/session"
)

// TestStageDirForOutsideWorkspace pins the 0070 B2 fix: the stage dir must never land
// under the agent-writable workspace, must be deterministic (the revoke/wipe paths
// reconstruct it), and must differ per workspace so same-named concurrent runs don't
// collide. The base is created 0700.
func TestSessionStageLayoutUsesIDForNewAndHashForLegacy(t *testing.T) {
	workspace := t.TempDir()
	store := engsession.NewStore(t.TempDir())
	created, err := store.Create("fish", "container", workspace, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	currentDir, err := sessionStageDir(created)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := filepath.Base(currentDir), "session-"+created.ID; got != want {
		t.Fatalf("new session stage = %q, want layout-2 %q", got, want)
	}
	if key, err := sessionReapKey(created); err != nil || key != created.ID {
		t.Fatalf("new session reap key = %q err=%v, want %q", key, err, created.ID)
	}

	legacy := engsession.Session{ID: "sess-legacy", Workspace: workspace}
	legacyDir, err := sessionStageDir(legacy)
	if err != nil {
		t.Fatal(err)
	}
	wantLegacy, err := stageDirFor("session-"+legacy.ID, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if legacyDir != wantLegacy || filepath.Base(legacyDir) == "session-"+legacy.ID {
		t.Fatalf("legacy stage reconstruction changed: got %q want %q", legacyDir, wantLegacy)
	}
	if key, err := sessionReapKey(legacy); err != nil || key != strings.TrimPrefix(filepath.Base(wantLegacy), "session-") {
		t.Fatalf("legacy reap key = %q err=%v", key, err)
	}
}

func TestStageDirForOutsideWorkspace(t *testing.T) {
	ws := t.TempDir()

	got, err := stageDirFor("session-abc", ws)
	if err != nil {
		t.Fatalf("stageDirFor: %v", err)
	}

	// Never inside the workspace tree (the whole point of B2).
	if rel, err := filepath.Rel(ws, got); err == nil && !strings.HasPrefix(rel, "..") {
		t.Fatalf("stage dir %q is inside workspace %q (rel %q)", got, ws, rel)
	}

	// Under the user cache dir, in the safeslop/runtime root.
	cache, err := os.UserCacheDir()
	if err != nil {
		t.Skipf("no user cache dir on this host: %v", err)
	}
	base := filepath.Join(cache, "safeslop", "runtime")
	if filepath.Dir(got) != base {
		t.Errorf("stage dir parent = %q, want %q", filepath.Dir(got), base)
	}
	if !strings.HasPrefix(filepath.Base(got), "session-abc-") {
		t.Errorf("stage dir base = %q, want session-abc-<hash>", filepath.Base(got))
	}

	// Base created 0700 (owner-only) so a peer user can't read staged bearers.
	fi, err := os.Stat(base)
	if err != nil {
		t.Fatalf("stat base: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o700 {
		t.Errorf("stage base perm = %o, want 700", perm)
	}

	// Deterministic for the same (name, ws).
	again, err := stageDirFor("session-abc", ws)
	if err != nil {
		t.Fatalf("stageDirFor (2nd): %v", err)
	}
	if again != got {
		t.Errorf("not deterministic: %q != %q", again, got)
	}

	// Distinct workspace -> distinct stage dir even for the same name.
	other, err := stageDirFor("session-abc", t.TempDir())
	if err != nil {
		t.Fatalf("stageDirFor (other ws): %v", err)
	}
	if other == got {
		t.Errorf("same stage dir for distinct workspaces: %q", got)
	}
}
