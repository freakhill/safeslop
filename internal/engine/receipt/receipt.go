// Package receipt is the host-side install receipt store — the authority for what `safeslop uninstall`
// may remove. The install manifest (install.DesiredState) is fetch *intent*; the filesystem is ground
// truth that drifts (safeslop's own `claude` pin self-updates after install, so its on-disk hash
// diverges by design). So uninstall is driven from a receipt written at install time recording exactly
// what was placed (Path A) or which delegate uninstaller owns the system state (Path B) — never
// reconstructed from the manifest (specs/0040, specs/0041).
//
// The store lives in ~/.config/safeslop/ (outside any agent-writable workspace), mirroring
// internal/engine/trust and internal/engine/userconfig: a versioned JSON file, written with a 0700 dir
// and 0600 file, rewritten whole on every change so a crash can never leave a half-written receipt.
package receipt

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
)

const storeVersion = 1

// File is one filesystem artifact a Path A install placed. SHA256 is the sha of the bytes safeslop
// wrote (empty for a symlink or an .app bundle directory); uninstall recomputes it before unlinking and
// refuses on a mismatch unless the owning tool is SelfUpdating.
type File struct {
	Path    string `json:"path"`
	SHA256  string `json:"sha256,omitempty"`
	Symlink bool   `json:"symlink,omitempty"`
}

// Entry is one installed tool's receipt. Path is "A" (safeslop placed the files itself — own-and-remove)
// or "B" (a verified third-party installer placed system state — delegate-and-verify). Files is set for
// Path A; Uninstall (the delegate's designated uninstaller argv) is set for Path B.
type Entry struct {
	Tool             string   `json:"tool"`
	Path             string   `json:"path"` // "A" | "B"
	Version          string   `json:"version,omitempty"`
	Provenance       string   `json:"provenance,omitempty"`
	SelfUpdating     bool     `json:"self_updating,omitempty"`
	Files            []File   `json:"files,omitempty"`             // Path A: artifacts safeslop placed
	Uninstall        []string `json:"uninstall,omitempty"`         // Path B: designated delegate uninstaller argv
	UninstallVerify  []string `json:"uninstall_verify,omitempty"`  // Path B: post-teardown probe argv (residue check)
	InstallerVersion string   `json:"installer_version,omitempty"` // Path B: version of the installer that ran
}

type storeFile struct {
	Version   int               `json:"version"`
	Entries   map[string]Entry  `json:"entries"`             // tool name -> receipt
	Unmanaged map[string]string `json:"unmanaged,omitempty"` // tool -> path: present on the box but NOT installed by safeslop (negative provenance / audit trail)
}

// Store is an in-memory view of the receipt file plus its on-disk path.
type Store struct {
	path      string
	entries   map[string]Entry
	unmanaged map[string]string
}

// DefaultPath is ~/.config/safeslop/receipts.json (host-side, agent-unreachable).
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "safeslop", "receipts.json"), nil
}

// Load reads the store at path; a missing file is an empty store (not an error).
func Load(path string) (*Store, error) {
	s := &Store{path: path, entries: map[string]Entry{}, unmanaged: map[string]string{}}
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
	if f.Unmanaged != nil {
		s.unmanaged = f.Unmanaged
	}
	return s, nil
}

// persist rewrites the whole file (0700 dir, 0600 file) so a crash can't half-write a receipt.
func (s *Store) persist() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	var unmanaged map[string]string
	if len(s.unmanaged) > 0 {
		unmanaged = s.unmanaged
	}
	b, err := json.MarshalIndent(storeFile{Version: storeVersion, Entries: s.entries, Unmanaged: unmanaged}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, b, 0o600)
}

// Record upserts an entry keyed by e.Tool and persists the store.
func (s *Store) Record(e Entry) error {
	s.entries[e.Tool] = e
	return s.persist()
}

// Get returns the receipt for a tool, if one was recorded.
func (s *Store) Get(tool string) (Entry, bool) {
	e, ok := s.entries[tool]
	return e, ok
}

// All returns every recorded entry, sorted by tool name for deterministic plans.
func (s *Store) All() []Entry {
	out := make([]Entry, 0, len(s.entries))
	for _, e := range s.entries {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Tool < out[j].Tool })
	return out
}

// Remove drops a tool's receipt and persists the store. Removing an absent tool is a no-op success (the
// symmetric reverse of Record — mirrors trust.Revoke). It rewrites the file rather than mutating in
// place, so a crash can't half-remove.
func (s *Store) Remove(tool string) error {
	if _, ok := s.entries[tool]; !ok {
		return nil
	}
	delete(s.entries, tool)
	return s.persist()
}

// NoteUnmanaged records that `tool` exists at `path` but was NOT installed by safeslop (Docker, a
// hand-installed or brew/cask binary). It is the negative-provenance audit trail so a later uninstall
// can explain why it left the tool untouched. Persists the store.
func (s *Store) NoteUnmanaged(tool, path string) error {
	if s.unmanaged == nil {
		s.unmanaged = map[string]string{}
	}
	s.unmanaged[tool] = path
	return s.persist()
}

// Unmanaged returns a copy of the negative-provenance map (tool -> path).
func (s *Store) Unmanaged() map[string]string {
	out := make(map[string]string, len(s.unmanaged))
	for k, v := range s.unmanaged {
		out[k] = v
	}
	return out
}
