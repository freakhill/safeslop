package uninstall

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// trashManifest maps each trashed file back to its original path, so a rollback can restore it.
type trashManifest struct {
	Stamp   string            `json:"stamp"`
	Entries map[string]string `json:"entries"` // trashed absolute path -> original absolute path
}

const trashManifestName = "manifest.json"

// TrashDir is ~/.local/share/safeslop/trash. It sits under $HOME like ~/.local/bin (BinDir), so the same
// APFS volume backs both and `os.Rename` from BinDir into the trash is atomic (no cross-device copy).
func TrashDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "safeslop", "trash"), nil
}

// moveToTrash renames each path into trash/<stamp>/ under a flattened, collision-safe layout and writes a
// manifest mapping trashed->original. It is the recoverable alternative to unlinking — Path A is the tier
// that can be rolled back (MSI transactional-rollback precedent). Returns the stamp dir.
func moveToTrash(paths []string, stamp string) (stampDir string, err error) {
	base, err := TrashDir()
	if err != nil {
		return "", err
	}
	stampDir = filepath.Join(base, stamp)
	if err := os.MkdirAll(stampDir, 0o700); err != nil {
		return "", err
	}
	man := trashManifest{Stamp: stamp, Entries: map[string]string{}}
	for i, p := range paths {
		// Flatten with an index prefix so two files of the same basename don't collide in the stamp dir.
		dst := filepath.Join(stampDir, fmt.Sprintf("%03d_%s", i, filepath.Base(p)))
		if err := os.Rename(p, dst); err != nil {
			return "", fmt.Errorf("trash %s: %w", p, err)
		}
		man.Entries[dst] = p
	}
	if err := writeManifest(stampDir, man); err != nil {
		return "", err
	}
	return stampDir, nil
}

func writeManifest(stampDir string, man trashManifest) error {
	b, err := json.MarshalIndent(man, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(stampDir, trashManifestName), b, 0o600)
}

// Rollback restores the files from a trash stamp (the newest if stamp is ""). It refuses to clobber: if
// an original path already exists again, that file is left in the trash and a clear error is returned.
func Rollback(stamp string) (restored []string, err error) {
	base, err := TrashDir()
	if err != nil {
		return nil, err
	}
	if stamp == "" {
		stamp, err = newestStamp(base)
		if err != nil {
			return nil, err
		}
	}
	stampDir := filepath.Join(base, stamp)
	man, err := readManifest(stampDir)
	if err != nil {
		return nil, err
	}
	// Sort for deterministic restore order.
	trashed := make([]string, 0, len(man.Entries))
	for t := range man.Entries {
		trashed = append(trashed, t)
	}
	sort.Strings(trashed)
	for _, t := range trashed {
		orig := man.Entries[t]
		if _, statErr := os.Lstat(orig); statErr == nil {
			return restored, fmt.Errorf("refusing to clobber %s — it exists again; left %s in trash", orig, t)
		}
		if err := os.MkdirAll(filepath.Dir(orig), 0o755); err != nil {
			return restored, err
		}
		if err := os.Rename(t, orig); err != nil {
			return restored, fmt.Errorf("restore %s: %w", orig, err)
		}
		restored = append(restored, orig)
	}
	return restored, nil
}

// Prune deletes trash stamps older than ttl and returns the count removed. A stamp with no parseable
// timestamp is left alone (never delete what we can't date).
func Prune(ttl time.Duration, now time.Time) (int, error) {
	base, err := TrashDir()
	if err != nil {
		return 0, err
	}
	ents, err := os.ReadDir(base)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	removed := 0
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		ts, perr := time.Parse(stampLayout, e.Name())
		if perr != nil {
			continue
		}
		if now.Sub(ts) > ttl {
			if err := os.RemoveAll(filepath.Join(base, e.Name())); err != nil {
				return removed, err
			}
			removed++
		}
	}
	return removed, nil
}

// stampLayout is the trash-stamp time format (UTC, filesystem-safe, lexically sortable).
const stampLayout = "20060102T150405Z"

func (e *Engine) stamp() string { return e.now().UTC().Format(stampLayout) }

func newestStamp(base string) (string, error) {
	ents, err := os.ReadDir(base)
	if err != nil {
		return "", err
	}
	var stamps []string
	for _, en := range ents {
		if en.IsDir() && strings.HasSuffix(en.Name(), "Z") {
			stamps = append(stamps, en.Name())
		}
	}
	if len(stamps) == 0 {
		return "", fmt.Errorf("no trash to roll back")
	}
	sort.Strings(stamps) // lexical == chronological for stampLayout
	return stamps[len(stamps)-1], nil
}

func readManifest(stampDir string) (trashManifest, error) {
	var man trashManifest
	b, err := os.ReadFile(filepath.Join(stampDir, trashManifestName))
	if err != nil {
		return man, err
	}
	if err := json.Unmarshal(b, &man); err != nil {
		return man, err
	}
	return man, nil
}
