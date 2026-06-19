// Package trust is a host-side approval store for per-repo safeslop.cue policies. The policy file
// lives inside the agent-writable workspace, so a sandboxed agent could rewrite its own policy and
// a cloned repo ships its own — therefore `safeslop run` is gated on an explicit, host-recorded
// approval of the policy's exact bytes (specs/0022; ayo specs/0012 §10.5 H2). The store lives in
// ~/.config/safeslop/ (outside any workspace, agent-unreachable), mirroring internal/engine/userconfig.
package trust

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
)

// Status is a policy's trust state relative to the store.
type Status int

const (
	Trusted   Status = iota // recorded, and the bytes still hash to the approved value
	Untrusted               // no record for this path (never approved)
	Changed                 // recorded, but the bytes hash differs (edited since approval)
)

func (s Status) String() string {
	switch s {
	case Trusted:
		return "trusted"
	case Changed:
		return "changed"
	default:
		return "untrusted"
	}
}

const storeVersion = 1

type storeFile struct {
	Version int               `json:"version"`
	Entries map[string]string `json:"entries"` // absolute policy path -> approved sha256 hex
}

// Store is an in-memory view of the trust file plus its on-disk path.
type Store struct {
	path    string
	entries map[string]string
}

// DefaultPath is ~/.config/safeslop/trust.json (host-side, agent-unreachable).
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "safeslop", "trust.json"), nil
}

// Load reads the store at path; a missing file is an empty store (not an error).
func Load(path string) (*Store, error) {
	s := &Store{path: path, entries: map[string]string{}}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	var f storeFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, err
	}
	if f.Entries != nil {
		s.entries = f.Entries
	}
	return s, nil
}

// Hash is the sha256 hex of the policy bytes — the approval token.
func Hash(policyBytes []byte) string {
	sum := sha256.Sum256(policyBytes)
	return hex.EncodeToString(sum[:])
}

// Check reports the trust status of policyBytes for the policy at absPath.
func (s *Store) Check(absPath string, policyBytes []byte) Status {
	want, ok := s.entries[absPath]
	if !ok {
		return Untrusted
	}
	if want != Hash(policyBytes) {
		return Changed
	}
	return Trusted
}

// Approve records policyBytes' hash for absPath and persists the store (0700 dir, 0600 file).
func (s *Store) Approve(absPath string, policyBytes []byte) error {
	s.entries[absPath] = Hash(policyBytes)
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(storeFile{Version: storeVersion, Entries: s.entries}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, b, 0o600)
}
