package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/container"
	runtimepkg "github.com/freakhill/safeslop/internal/engine/container/runtime"
	engexec "github.com/freakhill/safeslop/internal/engine/exec"
	"github.com/freakhill/safeslop/internal/engine/policy"
	engsession "github.com/freakhill/safeslop/internal/engine/session"
)

func installEgressSeams(t *testing.T,
	apply func(context.Context, engsession.Session, []container.SessionGrant) error,
	inspect func(context.Context, engsession.Session) (container.EgressGeneration, error),
	teardown func(engsession.Session) error,
) *dependencies {
	t.Helper()
	d := defaultDependencies()
	if apply != nil {
		d.applyEgressOverlay = apply
	}
	if inspect != nil {
		d.inspectEgress = inspect
	}
	if teardown != nil {
		d.teardownEgress = teardown
	}
	return d
}

type scriptedEgressRecordTx struct {
	current engsession.Session
	errors  []error
	commits int
}

func (tx *scriptedEgressRecordTx) Session() engsession.Session { return tx.current }

func (tx *scriptedEgressRecordTx) Commit(candidate engsession.Session) error {
	tx.commits++
	if len(tx.errors) > 0 {
		err := tx.errors[0]
		tx.errors = tx.errors[1:]
		if err != nil {
			return err
		}
	}
	tx.current = candidate
	return nil
}

func TestFailClosedEgressRetriesFailureStateCommit(t *testing.T) {
	for _, tc := range []struct {
		name        string
		teardownErr error
		wantStatus  string
	}{
		{name: "teardown unproven", teardownErr: errors.New("teardown incomplete"), wantStatus: engsession.StatusRunning},
		{name: "teardown proven", wantStatus: engsession.StatusStopped},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := installEgressSeams(t, nil, nil, func(engsession.Session) error { return tc.teardownErr })
			tx := &scriptedEgressRecordTx{
				current: engsession.Session{ID: "sess-test", Status: engsession.StatusRunning},
				errors:  []error{errors.New("transient record write"), nil},
			}
			if err := failClosedEgressWithDeps(d, tx, nil); !errors.Is(err, ErrEgressAuthorityUncertain) {
				t.Fatalf("failClosedEgress error = %v", err)
			}
			if tx.commits != 2 || tx.current.Status != tc.wantStatus || tx.current.LastFailure == nil || tx.current.LastFailure.Code != "network_authority_uncertain" {
				t.Fatalf("failure state was not retried: commits=%d session=%+v", tx.commits, tx.current)
			}
		})
	}
}

func seedAppliedRunningSession(t *testing.T, store engsession.Store, sess engsession.Session) (engsession.Session, container.EgressGeneration) {
	t.Helper()
	sess.Status, sess.PID = engsession.StatusRunning, 4242
	generation, err := sessionGeneration(sess)
	if err != nil {
		t.Fatal(err)
	}
	sess.SetEgressRuntimeState(engsession.EgressRuntimeState{AppliedRevision: generation.Revision, AppliedHash: generation.Hash})
	if err := store.Save(sess); err != nil {
		t.Fatal(err)
	}
	stored, err := store.Get(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	return stored, generation
}

func TestSessionGrantWidenPersistsUpperBoundBeforeRuntimeAck(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "safeslop.cue"), []byte("package safeslop\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := engsession.NewStore(t.TempDir())
	sess, err := store.Create("pi", "container", ws, nowForTest(t))
	if err != nil {
		t.Fatal(err)
	}
	_, runtimeGeneration := seedAppliedRunningSession(t, store, sess)
	applyCalls := 0
	d := installEgressSeams(t,
		func(_ context.Context, candidate engsession.Session, desired []container.SessionGrant) error {
			applyCalls++
			if len(desired) != 1 || desired[0].Host != "example.com" {
				t.Fatalf("candidate grants = %+v", desired)
			}
			durable, err := store.Get(sess.ID)
			if err != nil {
				t.Fatal(err)
			}
			state := durable.EgressRuntimeState()
			if len(durable.EgressGrants) != 1 || state.Transition == nil || state.Transition.Direction != engsession.EgressDirectionWiden {
				t.Fatalf("runtime widened before durable upper bound: session=%+v state=%+v", durable, state)
			}
			runtimeGeneration, err = sessionGeneration(candidate)
			return err
		},
		func(context.Context, engsession.Session) (container.EgressGeneration, error) {
			return runtimeGeneration, nil
		},
		func(engsession.Session) error { t.Fatal("successful widen tore down the session"); return nil },
	)

	updated, _, err := grantSessionEgressWithDeps(d, context.Background(), store, sess.ID, "example.com", 443, nowForTest(t))
	if err != nil {
		t.Fatalf("grantSessionEgress: %v", err)
	}
	if applyCalls != 1 || len(updated.EgressGrants) != 1 || updated.Status != engsession.StatusRunning {
		t.Fatalf("updated session = %+v applyCalls=%d", updated, applyCalls)
	}
	state := updated.EgressRuntimeState()
	if state.Transition != nil || state.AppliedRevision != updated.GrantRevision || state.AppliedHash != runtimeGeneration.Hash {
		t.Fatalf("final applied state = %+v, runtime=%+v", state, runtimeGeneration)
	}
	cue, err := os.ReadFile(filepath.Join(ws, "safeslop.cue"))
	if err != nil || strings.Contains(string(cue), "example.com") {
		t.Fatalf("grant mutated profile policy: %s err=%v", cue, err)
	}
}

func TestSessionGrantUncertainApplyTearsDownBeforeCompensating(t *testing.T) {
	store := engsession.NewStore(t.TempDir())
	sess, err := store.Create("pi", "container", t.TempDir(), nowForTest(t))
	if err != nil {
		t.Fatal(err)
	}
	_, runtimeGeneration := seedAppliedRunningSession(t, store, sess)
	teardowns := 0
	d := installEgressSeams(t,
		func(context.Context, engsession.Session, []container.SessionGrant) error {
			return container.ErrEgressGenerationUncertain
		},
		func(context.Context, engsession.Session) (container.EgressGeneration, error) {
			return runtimeGeneration, nil
		},
		func(current engsession.Session) error {
			teardowns++
			durable, err := store.Get(current.ID)
			if err != nil || len(durable.EgressGrants) != 1 {
				t.Fatalf("durable upper bound narrowed before teardown: %+v err=%v", durable, err)
			}
			return nil
		},
	)
	if _, _, err := grantSessionEgressWithDeps(d, context.Background(), store, sess.ID, "example.com", 443, nowForTest(t)); !errors.Is(err, ErrEgressAuthorityUncertain) {
		t.Fatalf("grant error = %v, want ErrEgressAuthorityUncertain", err)
	}
	stored, err := store.Get(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if teardowns != 1 || stored.Status != engsession.StatusStopped || len(stored.EgressGrants) != 0 || stored.GrantRevision != 0 {
		t.Fatalf("failed widen compensation = %+v teardowns=%d", stored, teardowns)
	}
	if stored.LastFailure == nil || stored.LastFailure.Code != "network_authority_uncertain" {
		t.Fatalf("missing fixed uncertainty failure: %+v", stored.LastFailure)
	}
}

func TestSessionGrantTeardownFailurePersistsRunningUncertaintyMarker(t *testing.T) {
	store := engsession.NewStore(t.TempDir())
	sess, err := store.Create("pi", "container", t.TempDir(), nowForTest(t))
	if err != nil {
		t.Fatal(err)
	}
	_, runtimeGeneration := seedAppliedRunningSession(t, store, sess)
	d := installEgressSeams(t,
		func(context.Context, engsession.Session, []container.SessionGrant) error {
			return container.ErrEgressGenerationUncertain
		},
		func(context.Context, engsession.Session) (container.EgressGeneration, error) {
			return runtimeGeneration, nil
		},
		func(engsession.Session) error { return errors.New("teardown incomplete") },
	)
	if _, _, err := grantSessionEgressWithDeps(d, context.Background(), store, sess.ID, "example.com", 443, nowForTest(t)); !errors.Is(err, ErrEgressAuthorityUncertain) {
		t.Fatalf("grant error = %v", err)
	}
	stored, err := store.Get(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != engsession.StatusRunning || stored.LastFailure == nil || stored.LastFailure.Code != "network_authority_uncertain" || stored.EgressRuntimeState().Transition == nil {
		t.Fatalf("incomplete teardown marker = %+v state=%+v", stored, stored.EgressRuntimeState())
	}
}

func TestSessionEgressUncertaintyMarkerBlocksRecoveryUntilTeardown(t *testing.T) {
	store := engsession.NewStore(t.TempDir())
	sess, err := store.Create("pi", "container", t.TempDir(), nowForTest(t))
	if err != nil {
		t.Fatal(err)
	}
	_, runtimeGeneration := seedAppliedRunningSession(t, store, sess)
	applyCalls, teardowns := 0, 0
	d := installEgressSeams(t,
		func(_ context.Context, candidate engsession.Session, _ []container.SessionGrant) error {
			applyCalls++
			var generationErr error
			runtimeGeneration, generationErr = sessionGeneration(candidate)
			if generationErr != nil {
				return generationErr
			}
			return container.ErrEgressGenerationUncertain
		},
		func(context.Context, engsession.Session) (container.EgressGeneration, error) {
			return runtimeGeneration, nil
		},
		func(engsession.Session) error {
			teardowns++
			return errors.New("teardown incomplete")
		},
	)
	if _, _, err := grantSessionEgressWithDeps(d, context.Background(), store, sess.ID, "example.com", 443, nowForTest(t)); !errors.Is(err, ErrEgressAuthorityUncertain) {
		t.Fatalf("first grant error = %v", err)
	}
	if _, _, err := grantSessionEgressWithDeps(d, context.Background(), store, sess.ID, "example.com", 443, nowForTest(t)); !errors.Is(err, ErrEgressAuthorityUncertain) {
		t.Fatalf("recovery grant error = %v, want terminal uncertainty", err)
	}
	stored, err := store.Get(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if applyCalls != 1 || teardowns != 2 || stored.Status != engsession.StatusRunning || stored.LastFailure == nil || stored.LastFailure.Code != "network_authority_uncertain" || stored.EgressRuntimeState().Transition == nil {
		t.Fatalf("unsafe uncertainty recovery: session=%+v state=%+v applies=%d teardowns=%d", stored, stored.EgressRuntimeState(), applyCalls, teardowns)
	}
}

func TestSessionRevokeNarrowsRuntimeBeforeDurableAuthority(t *testing.T) {
	store := engsession.NewStore(t.TempDir())
	sess, err := store.Create("pi", "container", t.TempDir(), nowForTest(t))
	if err != nil {
		t.Fatal(err)
	}
	sess, grant, err := engsession.AppendGrant(sess, "example.com", 443, nowForTest(t))
	if err != nil {
		t.Fatal(err)
	}
	_, runtimeGeneration := seedAppliedRunningSession(t, store, sess)
	d := installEgressSeams(t,
		func(_ context.Context, candidate engsession.Session, desired []container.SessionGrant) error {
			if len(desired) != 0 {
				t.Fatalf("narrow candidate = %+v, want no session grants", desired)
			}
			durable, err := store.Get(sess.ID)
			if err != nil {
				t.Fatal(err)
			}
			state := durable.EgressRuntimeState()
			if len(durable.EgressGrants) != 1 || state.Transition == nil || state.Transition.Direction != engsession.EgressDirectionNarrow {
				t.Fatalf("durable authority narrowed before runtime ACK: session=%+v state=%+v", durable, state)
			}
			runtimeGeneration, err = sessionGeneration(candidate)
			return err
		},
		func(context.Context, engsession.Session) (container.EgressGeneration, error) {
			return runtimeGeneration, nil
		},
		nil,
	)
	updated, err := revokeSessionEgressWithDeps(d, context.Background(), store, sess.ID, grant.ID, nowForTest(t))
	if err != nil {
		t.Fatalf("revokeSessionEgress: %v", err)
	}
	state := updated.EgressRuntimeState()
	if len(updated.EgressGrants) != 0 || updated.GrantRevision != 2 || state.Transition != nil || state.AppliedHash != runtimeGeneration.Hash {
		t.Fatalf("final narrow state = %+v runtime=%+v", updated, runtimeGeneration)
	}
}

func TestSessionRevokeApplyFailureRestoresOldDurableGeneration(t *testing.T) {
	store := engsession.NewStore(t.TempDir())
	sess, err := store.Create("pi", "container", t.TempDir(), nowForTest(t))
	if err != nil {
		t.Fatal(err)
	}
	sess, grant, err := engsession.AppendGrant(sess, "example.com", 443, nowForTest(t))
	if err != nil {
		t.Fatal(err)
	}
	_, runtimeGeneration := seedAppliedRunningSession(t, store, sess)
	calls := 0
	d := installEgressSeams(t,
		func(_ context.Context, candidate engsession.Session, _ []container.SessionGrant) error {
			calls++
			if calls == 1 {
				return container.ErrEgressGenerationUncertain
			}
			var err error
			runtimeGeneration, err = sessionGeneration(candidate)
			return err
		},
		func(context.Context, engsession.Session) (container.EgressGeneration, error) {
			return runtimeGeneration, nil
		},
		func(engsession.Session) error { t.Fatal("proved restore should not tear down"); return nil },
	)
	if _, err := revokeSessionEgressWithDeps(d, context.Background(), store, sess.ID, grant.ID, nowForTest(t)); err == nil {
		t.Fatal("revoke unexpectedly succeeded after candidate apply failure")
	}
	stored, err := store.Get(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	state := stored.EgressRuntimeState()
	if calls != 2 || stored.Status != engsession.StatusRunning || len(stored.EgressGrants) != 1 || state.Transition != nil || state.AppliedHash != runtimeGeneration.Hash {
		t.Fatalf("restore state = %+v runtime=%+v calls=%d", stored, runtimeGeneration, calls)
	}
}

func TestRecoverInterruptedWidenReappliesDurableCandidate(t *testing.T) {
	store := engsession.NewStore(t.TempDir())
	sess, err := store.Create("pi", "container", t.TempDir(), nowForTest(t))
	if err != nil {
		t.Fatal(err)
	}
	oldGeneration, err := sessionGeneration(sess)
	if err != nil {
		t.Fatal(err)
	}
	sess, _, err = engsession.AppendGrant(sess, "example.com", 443, nowForTest(t))
	if err != nil {
		t.Fatal(err)
	}
	candidateGeneration, err := sessionGeneration(sess)
	if err != nil {
		t.Fatal(err)
	}
	sess.Status = engsession.StatusRunning
	sess.SetEgressRuntimeState(engsession.EgressRuntimeState{AppliedRevision: oldGeneration.Revision, AppliedHash: oldGeneration.Hash, Transition: &engsession.EgressTransition{
		Direction: engsession.EgressDirectionWiden, CandidateRevision: candidateGeneration.Revision, CandidateHash: candidateGeneration.Hash, CandidateGrants: sess.EgressGrants,
	}})
	if err := store.Save(sess); err != nil {
		t.Fatal(err)
	}
	runtimeGeneration := oldGeneration
	d := installEgressSeams(t,
		func(_ context.Context, candidate engsession.Session, _ []container.SessionGrant) error {
			var err error
			runtimeGeneration, err = sessionGeneration(candidate)
			return err
		},
		func(context.Context, engsession.Session) (container.EgressGeneration, error) {
			return runtimeGeneration, nil
		}, nil,
	)
	if _, err := store.WithLocked(sess.ID, func(tx *engsession.RecordTx) error {
		return recoverRunningSessionEgressWithDeps(d, context.Background(), tx)
	}); err != nil {
		t.Fatalf("recover widen: %v", err)
	}
	stored, err := store.Get(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if state := stored.EgressRuntimeState(); state.Transition != nil || state.AppliedHash != candidateGeneration.Hash || runtimeGeneration != candidateGeneration {
		t.Fatalf("recovered widen state=%+v runtime=%+v", state, runtimeGeneration)
	}
}

func TestRecoverInterruptedNarrowCommitsAcknowledgedCandidate(t *testing.T) {
	store := engsession.NewStore(t.TempDir())
	sess, err := store.Create("pi", "container", t.TempDir(), nowForTest(t))
	if err != nil {
		t.Fatal(err)
	}
	sess, _, err = engsession.AppendGrant(sess, "example.com", 443, nowForTest(t))
	if err != nil {
		t.Fatal(err)
	}
	oldGeneration, err := sessionGeneration(sess)
	if err != nil {
		t.Fatal(err)
	}
	candidate := sess
	candidate.EgressGrants = nil
	candidate.GrantRevision++
	candidateGeneration, err := sessionGeneration(candidate)
	if err != nil {
		t.Fatal(err)
	}
	sess.Status = engsession.StatusRunning
	sess.SetEgressRuntimeState(engsession.EgressRuntimeState{AppliedRevision: oldGeneration.Revision, AppliedHash: oldGeneration.Hash, Transition: &engsession.EgressTransition{
		Direction: engsession.EgressDirectionNarrow, CandidateRevision: candidateGeneration.Revision, CandidateHash: candidateGeneration.Hash,
	}})
	if err := store.Save(sess); err != nil {
		t.Fatal(err)
	}
	runtimeGeneration := candidateGeneration
	d := installEgressSeams(t,
		func(context.Context, engsession.Session, []container.SessionGrant) error {
			t.Fatal("an exactly acknowledged narrow candidate must not be widened again")
			return nil
		},
		func(context.Context, engsession.Session) (container.EgressGeneration, error) {
			return runtimeGeneration, nil
		}, nil,
	)
	if _, err := store.WithLocked(sess.ID, func(tx *engsession.RecordTx) error {
		return recoverRunningSessionEgressWithDeps(d, context.Background(), tx)
	}); err != nil {
		t.Fatalf("recover narrow: %v", err)
	}
	stored, err := store.Get(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if state := stored.EgressRuntimeState(); len(stored.EgressGrants) != 0 || stored.GrantRevision != candidate.GrantRevision || state.Transition != nil || state.AppliedHash != candidateGeneration.Hash || runtimeGeneration != candidateGeneration {
		t.Fatalf("recovered narrow session=%+v state=%+v runtime=%+v", stored, state, runtimeGeneration)
	}
}

func TestLegacyRunningSessionBootstrapsSameAuthorityBeforeGrant(t *testing.T) {
	store := engsession.NewStore(t.TempDir())
	sess, err := store.Create("pi", "container", t.TempDir(), nowForTest(t))
	if err != nil {
		t.Fatal(err)
	}
	sess.Status = engsession.StatusRunning
	if err := store.Save(sess); err != nil {
		t.Fatal(err)
	}
	var applies []container.EgressGeneration
	runtimeGeneration := container.EgressGeneration{}
	d := installEgressSeams(t,
		func(_ context.Context, candidate engsession.Session, _ []container.SessionGrant) error {
			generation, err := sessionGeneration(candidate)
			if err == nil {
				applies = append(applies, generation)
				runtimeGeneration = generation
			}
			return err
		},
		func(context.Context, engsession.Session) (container.EgressGeneration, error) {
			return runtimeGeneration, errors.New("legacy proxy has no generation")
		}, nil,
	)
	if _, _, err := grantSessionEgressWithDeps(d, context.Background(), store, sess.ID, "example.com", 443, nowForTest(t)); err != nil {
		t.Fatalf("grant after bootstrap: %v", err)
	}
	if len(applies) != 2 || applies[0].Revision != 0 || applies[1].Revision != 1 {
		t.Fatalf("bootstrap/apply generations = %+v, want unchanged 0 then widened 1", applies)
	}
}

func TestSessionGrantApplyPreservesPersistentSnapshotOnGrantAndRevoke(t *testing.T) {
	store := engsession.NewStore(t.TempDir())
	sess, err := store.Create("pi", "container", t.TempDir(), nowForTest(t))
	if err != nil {
		t.Fatal(err)
	}
	sess.PersistentEgress = []policy.PersistentEgressRule{{FQDN: "always.example.com", Port: 443}}
	_, runtimeGeneration := seedAppliedRunningSession(t, store, sess)
	var applies [][]container.SessionGrant
	d := installEgressSeams(t,
		func(_ context.Context, candidate engsession.Session, desired []container.SessionGrant) error {
			applies = append(applies, append([]container.SessionGrant(nil), desired...))
			var err error
			runtimeGeneration, err = sessionGeneration(candidate)
			return err
		},
		func(context.Context, engsession.Session) (container.EgressGeneration, error) {
			return runtimeGeneration, nil
		}, nil,
	)
	updated, grant, err := grantSessionEgressWithDeps(d, context.Background(), store, sess.ID, "now.example.com", 443, nowForTest(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := revokeSessionEgressWithDeps(d, context.Background(), store, updated.ID, grant.ID, nowForTest(t)); err != nil {
		t.Fatal(err)
	}
	if len(applies) != 2 || len(applies[0]) != 2 || len(applies[1]) != 1 || applies[0][0].Host != "always.example.com" || applies[1][0].Host != "always.example.com" {
		t.Fatalf("persistent authority drifted across applies: %#v", applies)
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
	d := defaultDependencies()
	d.store = store
	d.detectRuntime = func(runtimepkg.NetworkPolicy) (runtimepkg.Engine, error) { return runtimepkg.HostDockerEngine{}, nil }
	d.launchContainer = func(_ context.Context, _ runtimepkg.Engine, _ engexec.LaunchSpec, _, _ string, _, _ []string, _ string, _ []string, _ *policy.Projection, grants ...container.SessionGrant) (int, error) {
		got = append([]container.SessionGrant(nil), grants...)
		return 0, nil
	}
	if _, err := runProfileCtxWithDeps(d, context.Background(), "session-"+sess.ID, prof, argv, ws, ""); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Host != "always.example.com" || got[1].Host != "example.com" {
		t.Fatalf("launch grants = %+v", got)
	}
}

func TestSessionGrantCreatedSessionSavesWithoutProxyReplacement(t *testing.T) {
	store := engsession.NewStore(t.TempDir())
	sess, err := store.Create("pi", "container", t.TempDir(), nowForTest(t))
	if err != nil {
		t.Fatal(err)
	}
	d := installEgressSeams(t, func(context.Context, engsession.Session, []container.SessionGrant) error {
		t.Fatal("created session must not replace a proxy")
		return nil
	}, nil, nil)
	if _, _, err := grantSessionEgressWithDeps(d, context.Background(), store, sess.ID, "example.com", 443, nowForTest(t)); err != nil {
		t.Fatal(err)
	}
	stored, err := store.Get(sess.ID)
	if err != nil || len(stored.EgressGrants) != 1 || stored.GrantRevision != 1 {
		t.Fatalf("created session grant = %+v err=%v", stored, err)
	}
}

func TestSessionGrantRejectsNetworkAllowBeforeApply(t *testing.T) {
	store := engsession.NewStore(t.TempDir())
	sess, err := store.Create("pi", "container", t.TempDir(), nowForTest(t))
	if err != nil {
		t.Fatal(err)
	}
	sess.Network = "allow"
	if err := store.Save(sess); err != nil {
		t.Fatal(err)
	}
	d := installEgressSeams(t, func(context.Context, engsession.Session, []container.SessionGrant) error {
		t.Fatal("network allow must be rejected before proxy replacement")
		return nil
	}, nil, nil)
	if _, _, err := grantSessionEgressWithDeps(d, context.Background(), store, sess.ID, "example.com", 443, nowForTest(t)); err != engsession.ErrSessionNotGrantable {
		t.Fatalf("grant error = %v", err)
	}
}
