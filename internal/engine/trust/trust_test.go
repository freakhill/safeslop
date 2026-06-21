package trust

import (
	"path/filepath"
	"testing"
)

func TestCheckUntrustedThenApproveThenChanged(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "trust.json")
	pol := "/repo/safeslop.cue"
	v1 := []byte("profiles: { dev: { agent: \"claude\" } }")

	s, err := Load(storePath) // missing file -> empty store
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Check(pol, v1); got != Untrusted {
		t.Fatalf("fresh policy should be Untrusted, got %v", got)
	}
	if err := s.Approve(pol, v1); err != nil {
		t.Fatal(err)
	}

	// reload from disk: approval must persist
	s2, err := Load(storePath)
	if err != nil {
		t.Fatal(err)
	}
	if got := s2.Check(pol, v1); got != Trusted {
		t.Fatalf("approved policy should be Trusted, got %v", got)
	}
	// the agent edits the policy -> bytes differ -> Changed (not silently honored)
	if got := s2.Check(pol, []byte("profiles: { dev: { network: \"allow\" } }")); got != Changed {
		t.Fatalf("edited policy should be Changed, got %v", got)
	}
}

func TestHashStable(t *testing.T) {
	if Hash([]byte("x")) != Hash([]byte("x")) || Hash([]byte("x")) == Hash([]byte("y")) {
		t.Fatal("Hash must be deterministic and content-sensitive")
	}
}

func TestRevokeReturnsToUntrusted(t *testing.T) {
	dir := t.TempDir()
	store := &Store{path: filepath.Join(dir, "trust.json"), entries: map[string]string{}}
	policy := []byte("safeslop: {version: 1}")
	abs := "/repo/safeslop.cue"

	if err := store.Approve(abs, policy); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if store.Check(abs, policy) != Trusted {
		t.Fatalf("precondition: want Trusted after Approve")
	}
	if err := store.Revoke(abs); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if got := store.Check(abs, policy); got != Untrusted {
		t.Errorf("after Revoke = %v, want Untrusted", got)
	}
	// Revoking an absent entry is a no-op success (idempotent).
	if err := store.Revoke(abs); err != nil {
		t.Errorf("second Revoke should be a no-op, got %v", err)
	}
	// The removal persisted: a fresh Load no longer trusts it.
	reloaded, err := Load(store.path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if reloaded.Check(abs, policy) != Untrusted {
		t.Errorf("revocation did not persist")
	}
}
