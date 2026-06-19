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
