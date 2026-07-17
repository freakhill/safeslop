package cli

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/freakhill/safeslop/internal/engine/container"
	engsession "github.com/freakhill/safeslop/internal/engine/session"
)

func TestConcurrentSessionGrantAndRevokeSerializeRuntimeAuthority(t *testing.T) {
	for attempt := range 20 {
		t.Run(fmt.Sprintf("attempt-%02d", attempt), exerciseConcurrentSessionGrantAndRevoke)
	}
}

func exerciseConcurrentSessionGrantAndRevoke(t *testing.T) {
	store := engsession.NewStore(t.TempDir())
	sess, err := store.Create("fish", "container", t.TempDir(), time.Date(2026, 7, 17, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	sess, oldGrant, err := engsession.AppendGrant(sess, "old.example.com", 443, time.Date(2026, 7, 17, 0, 0, 1, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	_, runtimeGeneration := seedAppliedRunningSession(t, store, sess)

	var runtimeMu sync.Mutex
	d := installEgressSeams(t,
		func(_ context.Context, candidate engsession.Session, _ []container.SessionGrant) error {
			generation, err := sessionGeneration(candidate)
			if err != nil {
				return err
			}
			runtimeMu.Lock()
			runtimeGeneration = generation
			runtimeMu.Unlock()
			return nil
		},
		func(context.Context, engsession.Session) (container.EgressGeneration, error) {
			runtimeMu.Lock()
			defer runtimeMu.Unlock()
			return runtimeGeneration, nil
		}, nil)

	type operationResult struct {
		name string
		err  error
	}
	start := make(chan struct{})
	results := make(chan operationResult, 2)
	go func() {
		<-start
		_, _, err := grantSessionEgressWithDeps(d, context.Background(), store, sess.ID, "new.example.com", 443, time.Date(2026, 7, 17, 0, 0, 2, 0, time.UTC))
		results <- operationResult{name: "grant", err: err}
	}()
	go func() {
		<-start
		_, err := revokeSessionEgressWithDeps(d, context.Background(), store, sess.ID, oldGrant.ID, time.Date(2026, 7, 17, 0, 0, 3, 0, time.UTC))
		results <- operationResult{name: "revoke", err: err}
	}()
	close(start)
	for range 2 {
		if result := <-results; result.err != nil {
			t.Fatalf("concurrent %s authority mutation: %v", result.name, result.err)
		}
	}

	got, err := store.Get(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.EgressGrants) != 1 || got.EgressGrants[0].Host != "new.example.com" {
		t.Fatalf("serialized grants = %+v, want only new.example.com", got.EgressGrants)
	}
	state := got.EgressRuntimeState()
	generation, err := sessionGeneration(got)
	if err != nil {
		t.Fatal(err)
	}
	if state.Transition != nil || state.AppliedRevision != generation.Revision || state.AppliedHash != generation.Hash {
		t.Fatalf("durable/runtime authority diverged: state=%+v generation=%+v", state, generation)
	}
}

func TestConcurrentSessionRenameAndDismissPreserveBothMutations(t *testing.T) {
	store := engsession.NewStore(t.TempDir())
	sess, err := store.Create("fish", "container", t.TempDir(), time.Date(2026, 7, 17, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		_, err := store.Rename(sess.ID, "concurrent-name", time.Date(2026, 7, 17, 0, 0, 1, 0, time.UTC))
		errs <- err
	}()
	go func() {
		defer wg.Done()
		<-start
		_, _, err := dismissSessionEgress(store, sess.ID, "api.example.com", 443, time.Date(2026, 7, 17, 0, 0, 2, 0, time.UTC))
		errs <- err
	}()
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent mutation failed: %v", err)
		}
	}
	got, err := store.Get(sess.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "concurrent-name" || len(got.EgressAcknowledgements) != 1 {
		t.Fatalf("successful concurrent mutation was lost: %#v", got)
	}
}
