package cli

import (
	"sync"
	"testing"
	"time"

	engsession "github.com/freakhill/safeslop/internal/engine/session"
)

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
