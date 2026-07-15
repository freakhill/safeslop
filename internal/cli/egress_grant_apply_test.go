package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/container"
	engexec "github.com/freakhill/safeslop/internal/engine/exec"
	"github.com/freakhill/safeslop/internal/engine/policy"
	engsession "github.com/freakhill/safeslop/internal/engine/session"
)

func TestSessionGrantApplyRunningSavesAfterOverlaySuccessNoProfileMutation(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "safeslop.cue"), []byte("package safeslop\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SAFESLOP_STATE_DIR", t.TempDir())
	store := sessionStore()
	sess, err := store.Create("pi", "container", ws, nowForTest(t))
	if err != nil {
		t.Fatal(err)
	}
	sess.Status = engsession.StatusRunning
	sess.PID = os.Getpid()
	if err := store.Save(sess); err != nil {
		t.Fatal(err)
	}
	seedSessionOverlayForTest(t, sess)
	var applied []container.SessionGrant
	oldApply := applySessionGrantOverlay
	applySessionGrantOverlay = func(_ context.Context, got engsession.Session, desired []container.SessionGrant) error {
		if got.ID != sess.ID || len(desired) != 1 || desired[0].Host != "example.com" || desired[0].Port != 443 {
			t.Fatalf("unexpected overlay apply input: session=%s grants=%+v", got.ID, desired)
		}
		applied = append(applied, desired...)
		return nil
	}
	t.Cleanup(func() { applySessionGrantOverlay = oldApply })

	if _, _, err := grantSessionEgress(context.Background(), store, sess.ID, "example.com", 443, nowForTest(t)); err != nil {
		t.Fatalf("grantSessionEgress: %v", err)
	}
	if len(applied) != 1 {
		t.Fatalf("overlay was not applied: %+v", applied)
	}
	stored, err := store.Get(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.EgressGrants) != 1 || stored.EgressGrants[0].Host != "example.com" || stored.GrantRevision != 1 {
		t.Fatalf("stored grants = %+v rev=%d", stored.EgressGrants, stored.GrantRevision)
	}
	cue, err := os.ReadFile(filepath.Join(ws, "safeslop.cue"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(cue), "example.com") {
		t.Fatalf("grant must not mutate profile CUE: %s", cue)
	}
}

func TestSessionGrantApplyFailClosedDoesNotSave(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("SAFESLOP_STATE_DIR", t.TempDir())
	store := sessionStore()
	sess, err := store.Create("pi", "container", ws, nowForTest(t))
	if err != nil {
		t.Fatal(err)
	}
	sess.Status = engsession.StatusRunning
	sess.PID = os.Getpid()
	if err := store.Save(sess); err != nil {
		t.Fatal(err)
	}
	seedSessionOverlayForTest(t, sess)
	oldApply := applySessionGrantOverlay
	applySessionGrantOverlay = func(context.Context, engsession.Session, []container.SessionGrant) error {
		return errors.New("ProxyReload failed")
	}
	t.Cleanup(func() { applySessionGrantOverlay = oldApply })

	if _, _, err := grantSessionEgress(context.Background(), store, sess.ID, "example.com", 443, nowForTest(t)); err == nil {
		t.Fatal("grantSessionEgress with failed overlay unexpectedly succeeded")
	}
	stored, err := store.Get(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.EgressGrants) != 0 || stored.GrantRevision != 0 {
		t.Fatalf("failed overlay must not save grants, got %+v rev=%d", stored.EgressGrants, stored.GrantRevision)
	}
}

func TestSessionGrantApplyPreservesPersistentSnapshotOnGrantAndRevoke(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("SAFESLOP_STATE_DIR", t.TempDir())
	store := sessionStore()
	sess, err := store.Create("pi", "container", ws, nowForTest(t))
	if err != nil {
		t.Fatal(err)
	}
	sess.Status = engsession.StatusRunning
	sess.PersistentEgress = []policy.PersistentEgressRule{{FQDN: "always.example.com", Port: 443}}
	if err := store.Save(sess); err != nil {
		t.Fatal(err)
	}
	seedSessionOverlayForTest(t, sess)

	var applies [][]container.SessionGrant
	oldApply := applySessionGrantOverlay
	applySessionGrantOverlay = func(_ context.Context, _ engsession.Session, desired []container.SessionGrant) error {
		applies = append(applies, append([]container.SessionGrant(nil), desired...))
		return nil
	}
	t.Cleanup(func() { applySessionGrantOverlay = oldApply })

	updated, grant, err := grantSessionEgress(context.Background(), store, sess.ID, "now.example.com", 443, nowForTest(t))
	if err != nil {
		t.Fatalf("grantSessionEgress: %v", err)
	}
	if _, err := revokeSessionEgress(context.Background(), store, updated.ID, grant.ID, nowForTest(t)); err != nil {
		t.Fatalf("revokeSessionEgress: %v", err)
	}
	if len(applies) != 2 {
		t.Fatalf("overlay applies = %#v, want grant and revoke", applies)
	}
	for _, want := range []struct {
		at int
		n  int
	}{{0, 2}, {1, 1}} {
		if len(applies[want.at]) != want.n || applies[want.at][0].Host != "always.example.com" || applies[want.at][0].Port != 443 {
			t.Fatalf("overlay %d = %#v, persistent snapshot must remain first", want.at, applies[want.at])
		}
	}
	stored, err := store.Get(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.PersistentEgress) != 1 || stored.PersistentEgress[0].FQDN != "always.example.com" || len(stored.EgressGrants) != 0 {
		t.Fatalf("stored session = %+v, persistent snapshot must survive overlay mutations", stored)
	}
}

func TestSessionGrantApplyLaunchThreadsStoredGrants(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("SAFESLOP_STATE_DIR", t.TempDir())
	store := sessionStore()
	sess, err := store.Create("shell", "container", ws, nowForTest(t))
	if err != nil {
		t.Fatal(err)
	}
	sess.PersistentEgress = []policy.PersistentEgressRule{{FQDN: "always.example.com", Port: 443}}
	sess, _, err = engsession.AppendGrant(sess, "example.com", 443, nowForTest(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(sess); err != nil {
		t.Fatal(err)
	}

	prof := policy.Profile{Agent: "shell", Environment: "container", Network: "deny", Workspace: ws}
	argv, err := agentArgv(prof)
	if err != nil {
		t.Fatal(err)
	}
	var got []container.SessionGrant
	oldLaunch := containerLaunch
	containerLaunch = func(_ context.Context, _ engexec.LaunchSpec, _, _ string, _, _ []string, _ string, _ []string, _ *policy.Projection, grants ...container.SessionGrant) (int, error) {
		got = append([]container.SessionGrant(nil), grants...)
		return 0, nil
	}
	t.Cleanup(func() { containerLaunch = oldLaunch })

	if _, err := runProfileCtx(context.Background(), "session-"+sess.ID, prof, argv, ws); err != nil {
		t.Fatalf("runProfileCtx: %v", err)
	}
	if len(got) != 2 || got[0].Host != "always.example.com" || got[0].Port != 443 || got[1].Host != "example.com" || got[1].Port != 443 {
		t.Fatalf("launch grants = %+v, want persistent always.example.com:443 then session example.com:443", got)
	}
}

func TestGrantRevokeRunningSavesAfterOverlaySuccess(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("SAFESLOP_STATE_DIR", t.TempDir())
	store := sessionStore()
	sess, err := store.Create("pi", "container", ws, nowForTest(t))
	if err != nil {
		t.Fatal(err)
	}
	sess.Status = engsession.StatusRunning
	sess.PID = os.Getpid()
	sess, g, err := engsession.AppendGrant(sess, "example.com", 443, nowForTest(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(sess); err != nil {
		t.Fatal(err)
	}
	seedSessionOverlayForTest(t, sess)
	oldApply := applySessionGrantOverlay
	applySessionGrantOverlay = func(_ context.Context, _ engsession.Session, desired []container.SessionGrant) error {
		if len(desired) != 0 {
			t.Fatalf("revoke overlay desired grants = %+v, want empty", desired)
		}
		return nil
	}
	t.Cleanup(func() { applySessionGrantOverlay = oldApply })

	if _, err := revokeSessionEgress(context.Background(), store, sess.ID, g.ID, nowForTest(t)); err != nil {
		t.Fatalf("revokeSessionEgress: %v", err)
	}
	stored, err := store.Get(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.EgressGrants) != 0 || stored.GrantRevision != 2 {
		t.Fatalf("revoke stored grants=%+v rev=%d, want none rev=2", stored.EgressGrants, stored.GrantRevision)
	}
}

func TestSessionGrantCreatedSessionSavesWithoutProxyReload(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("SAFESLOP_STATE_DIR", t.TempDir())
	store := sessionStore()
	sess, err := store.Create("pi", "container", ws, nowForTest(t))
	if err != nil {
		t.Fatal(err)
	}
	oldApply := applySessionGrantOverlay
	applySessionGrantOverlay = func(context.Context, engsession.Session, []container.SessionGrant) error {
		t.Fatal("created session must not reload a non-running proxy")
		return nil
	}
	t.Cleanup(func() { applySessionGrantOverlay = oldApply })

	if _, _, err := grantSessionEgress(context.Background(), store, sess.ID, "example.com", 443, nowForTest(t)); err != nil {
		t.Fatalf("grantSessionEgress on created session: %v", err)
	}
	stored, err := store.Get(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.EgressGrants) != 1 || stored.GrantRevision != 1 {
		t.Fatalf("created session grant not saved: %+v rev=%d", stored.EgressGrants, stored.GrantRevision)
	}
}

func TestSessionGrantRejectsNetworkAllowBeforeApply(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("SAFESLOP_STATE_DIR", t.TempDir())
	store := sessionStore()
	sess, err := store.Create("pi", "container", ws, nowForTest(t))
	if err != nil {
		t.Fatal(err)
	}
	sess.Network = "allow"
	if err := store.Save(sess); err != nil {
		t.Fatal(err)
	}
	oldApply := applySessionGrantOverlay
	applySessionGrantOverlay = func(context.Context, engsession.Session, []container.SessionGrant) error {
		t.Fatal("network allow must be rejected before overlay apply")
		return nil
	}
	t.Cleanup(func() { applySessionGrantOverlay = oldApply })

	if _, _, err := grantSessionEgress(context.Background(), store, sess.ID, "example.com", 443, nowForTest(t)); err != engsession.ErrSessionNotGrantable {
		t.Fatalf("network allow grant error = %v, want ErrSessionNotGrantable", err)
	}
	stored, err := store.Get(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.EgressGrants) != 0 || stored.GrantRevision != 0 {
		t.Fatalf("network allow rejection mutated session: %+v rev=%d", stored.EgressGrants, stored.GrantRevision)
	}
}

func seedSessionOverlayForTest(t *testing.T, sess engsession.Session) string {
	t.Helper()
	stageDir, err := sessionStageDir(sess)
	if err != nil {
		t.Fatalf("session stage dir: %v", err)
	}
	if err := os.MkdirAll(stageDir, 0o700); err != nil {
		t.Fatalf("mkdir stage dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stageDir, "compose.yml"), []byte("services: {}\n"), 0o600); err != nil {
		t.Fatalf("write compose: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stageDir, "session-grants.conf"), []byte(container.RenderSessionGrants(nil)), 0o600); err != nil {
		t.Fatalf("write grants: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(stageDir) })
	return stageDir
}
