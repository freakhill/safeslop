package container

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/freakhill/safeslop/internal/engine/policy"
)

// Projection status values recorded per expanded file (specs/0096 T1 FLO verdict). Present sources
// get a bind mount + a copy step; absent/unreadable optional sources are recorded for operator
// legibility but mounted/copied only when present.
const (
	projPresent           = "present"
	projSkippedAbsent     = "skipped-absent"
	projSkippedUnreadable = "skipped-unreadable"
)

// ProjectionMount is one resolved per-file host source staged read-only into the container at an
// opaque path and copied into the ephemeral home by the entrypoint (specs/0096). Host is the
// absolute, symlink-component-free host source; Container is the /safeslop/projected/<id> bind
// target (empty for skipped entries, which appear in the manifest for legibility but are not
// mounted); Target is the destination relative to /home/agent.
type ProjectionMount struct {
	Host      string `json:"host"`
	Container string `json:"staging,omitempty"`
	Target    string `json:"target"`
	Optional  bool   `json:"optional"`
	Label     string `json:"label,omitempty"`
	Status    string `json:"status"`
}

// ProjectionManifest is the JSON record written to /safeslop/runtime/projection.json so the
// operator (and tests) can inspect exactly which host files were projected and how. It is live host
// filesystem state, NOT content-pinned by the builtin profile hash (specs/0096 ayo lesson #10).
type ProjectionManifest struct {
	Items []ProjectionMount `json:"items"`
}

// MarshalJSON renders the manifest compactly as the items array when populated.
func (m ProjectionManifest) MarshalJSON() ([]byte, error) {
	type plain ProjectionManifest
	return json.Marshal(plain(m))
}

// projectionRoots are the allowed host roots a projected source may resolve under. $HOME and
// $XDG_CONFIG_HOME (defaulting to $HOME/.config). Anything escaping both is rejected.
func projectionRoots(home string) (string, string) {
	home = filepath.Clean(home)
	xdg := os.Getenv("XDG_CONFIG_HOME")
	if xdg == "" || !filepath.IsAbs(xdg) {
		xdg = filepath.Join(home, ".config")
	}
	return home, filepath.Clean(xdg)
}

// ResolveProjection expands a profile's projection against the host filesystem into per-file mount
// entries plus a manifest. It enforces the FLO resolver laws (specs/research/2026-07-12-safe-home-
// projection-flo.md §"Excluded sources" + resolver law #7): reject broad $HOME, credential/cache
// roots, symlink components, path escapes, duplicate targets, and non-regular files. Optional absent
// or unreadable sources skip legibly; required ones fail closed; a resolver-law violation fails
// closed regardless of the optional flag.
func ResolveProjection(home string, proj policy.Projection) (ProjectionManifest, error) {
	home = filepath.Clean(home)
	if !filepath.IsAbs(home) {
		return ProjectionManifest{}, errors.New("projection: home must be absolute")
	}
	_, xdg := projectionRoots(home)
	excluded := excludedRoots(home, xdg)
	manifest := ProjectionManifest{}
	seenTarget := map[string]bool{}

	for _, item := range proj.Items {
		entries, err := resolveItem(home, xdg, excluded, item)
		if err != nil {
			return ProjectionManifest{}, err
		}
		for _, e := range entries {
			if e.Status != projPresent {
				// Skipped entries are recorded for legibility (operator sees "fish config not found")
				// but get no mount; duplicate-target tracking only applies to mounted targets.
				manifest.Items = append(manifest.Items, e)
				continue
			}
			if seenTarget[e.Target] {
				return ProjectionManifest{}, fmt.Errorf("projection: duplicate target %q (from %s)", e.Target, e.Host)
			}
			seenTarget[e.Target] = true
			manifest.Items = append(manifest.Items, e)
		}
	}

	// Assign opaque staging ids in manifest order so mounts are deterministic and topology-free
	// (specs/0096 ayo lesson #4: opaque /safeslop/projected/<id>, never host-mirrored paths).
	id := 0
	for i := range manifest.Items {
		if manifest.Items[i].Status != projPresent {
			continue
		}
		manifest.Items[i].Container = fmt.Sprintf("/safeslop/projected/%d", id)
		id++
	}
	return manifest, nil
}

// PresentMounts returns only the present (bind-mounted) entries, in id order, for compose rendering.
func (m ProjectionManifest) PresentMounts() []ProjectionMount {
	out := make([]ProjectionMount, 0)
	for _, it := range m.Items {
		if it.Status == projPresent {
			out = append(out, it)
		}
	}
	return out
}

// resolveItem expands one policy.ProjectionItem into 0+ entries, applying the resolver laws.
func resolveItem(home, xdg string, excluded []string, item policy.ProjectionItem) ([]ProjectionMount, error) {
	optional := item.Optional == nil || *item.Optional
	label := item.Label
	kind := item.Kind
	if kind == "" {
		kind = "file"
	}
	abs, err := expandSource(home, item.Source)
	if err != nil {
		return nil, err
	}
	if lawErr := projectionLaw(abs, home, xdg, excluded); lawErr != nil {
		// Resolver-law violations fail closed even for optional sources (FLO resolver law #7).
		return nil, lawErr
	}

	switch kind {
	case "file":
		e, status, err := resolveFile(home, xdg, abs, optional)
		if err != nil {
			return nil, err
		}
		if e == nil {
			return []ProjectionMount{{Host: abs, Target: targetFromAbs(home, xdg, abs), Optional: optional, Label: label, Status: status}}, nil
		}
		e.Label = label
		e.Target = targetFromAbs(home, xdg, abs)
		return []ProjectionMount{*e}, nil
	case "dir":
		return resolveDir(home, xdg, abs, optional, label, excluded)
	case "glob":
		return resolveGlob(home, xdg, excluded, abs, optional, label)
	default:
		return nil, fmt.Errorf("projection: unknown kind %q", kind)
	}
}

// expandSource resolves a ~/ prefix to home and cleans the path. It does NOT follow symlinks
// (symlink-component rejection happens in projectionLaw); EvalSymlinks is intentionally avoided so a
// symlinked source is rejected, not silently followed (specs/0096 ayo lesson #5).
func expandSource(home, src string) (string, error) {
	src = filepath.Clean(strings.ReplaceAll(src, "\x00", ""))
	switch {
	case src == "~":
		return home, nil
	case strings.HasPrefix(src, "~/"):
		return filepath.Join(home, src[2:]), nil
	case filepath.IsAbs(src):
		return src, nil
	default:
		return "", fmt.Errorf("projection: source must start with ~/ or be absolute (got %q)", src)
	}
}

// projectionLaw enforces the host-independent resolver laws: the source must stay under an allowed
// root (no path escape) and must not name a broad/credential/cache source. Symlink-component and
// non-regular-file checks happen per expanded file in resolveFile/resolveDir (they need Lstat).
func projectionLaw(abs, home, xdg string, excluded []string) error {
	if abs == home || abs == xdg {
		return fmt.Errorf("projection: broad root rejected (%s); use specific files", abs)
	}
	under := func(root string) bool { return abs == root || strings.HasPrefix(abs, root+string(filepath.Separator)) }
	if !under(home) && !under(xdg) {
		return fmt.Errorf("projection: source escapes $HOME/$XDG_CONFIG_HOME: %s", abs)
	}
	for _, ex := range excluded {
		if abs == ex || strings.HasPrefix(abs, ex+string(filepath.Separator)) {
			return fmt.Errorf("projection: excluded source %s is credential/cache/host-config authority (specs/0096)", abs)
		}
	}
	// ~/.cargo/credentials* — a glob of short-lived/long-lived cargo tokens, matched by basename.
	if filepath.Dir(abs) == filepath.Join(home, ".cargo") && strings.HasPrefix(filepath.Base(abs), "credentials") {
		return fmt.Errorf("projection: excluded source %s is cargo credentials (specs/0096)", abs)
	}
	return nil
}

// resolveFile validates one regular file. It returns a present entry, or a nil entry + a skipped
// status for optional absent/unreadable sources. Required absent/unreadable sources return an error
// (fail closed). A symlinked source or non-regular file is a resolver-law violation (error) for both
// optional and required items.
func resolveFile(home, xdg, abs string, optional bool) (*ProjectionMount, string, error) {
	li, err := os.Lstat(abs)
	if errors.Is(err, os.ErrNotExist) {
		if optional {
			return nil, projSkippedAbsent, nil
		}
		return nil, "", fmt.Errorf("projection: required source absent: %s", abs)
	}
	if err != nil {
		// Unreadable/stat-failed: treat like unreadable. Optional skips, required fails.
		if optional {
			return nil, projSkippedUnreadable, nil
		}
		return nil, "", fmt.Errorf("projection: required source unreadable: %s: %w", abs, err)
	}
	if li.Mode()&os.ModeSymlink != 0 {
		return nil, "", fmt.Errorf("projection: symlink source rejected (TOCTOU risk): %s", abs)
	}
	if !li.Mode().IsRegular() {
		return nil, "", fmt.Errorf("projection: non-regular file rejected (use kind: dir or glob): %s", abs)
	}
	if hasSymlinkPrefix(abs, home, xdg) {
		return nil, "", fmt.Errorf("projection: symlink component in path rejected: %s", abs)
	}
	if !readable(abs) {
		if optional {
			return nil, projSkippedUnreadable, nil
		}
		return nil, "", fmt.Errorf("projection: required source unreadable: %s", abs)
	}
	return &ProjectionMount{Host: abs, Target: "", Status: projPresent, Optional: optional}, projPresent, nil
}

// resolveDir walks a directory and emits one entry per regular file it contains. Symlinked entries
// inside the dir are skipped (not followed); a symlinked dir source itself is rejected by resolveFile
// semantics applied to the dir. Optional absent dir skips; required absent dir fails.
func resolveDir(home, xdg, abs string, optional bool, label string, _ []string) ([]ProjectionMount, error) {
	li, err := os.Lstat(abs)
	if errors.Is(err, os.ErrNotExist) {
		if optional {
			return []ProjectionMount{{Host: abs, Target: targetFromAbs(home, xdg, abs) + "/", Optional: optional, Label: label, Status: projSkippedAbsent}}, nil
		}
		return nil, fmt.Errorf("projection: required dir absent: %s", abs)
	}
	if err != nil {
		if optional {
			return []ProjectionMount{{Host: abs, Target: targetFromAbs(home, xdg, abs) + "/", Optional: optional, Label: label, Status: projSkippedUnreadable}}, nil
		}
		return nil, fmt.Errorf("projection: required dir unreadable: %s: %w", abs, err)
	}
	if li.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("projection: symlink dir source rejected: %s", abs)
	}
	if !li.IsDir() {
		return nil, fmt.Errorf("projection: kind:dir source is not a directory: %s", abs)
	}
	if hasSymlinkPrefix(abs, home, xdg) {
		return nil, fmt.Errorf("projection: symlink component in dir path rejected: %s", abs)
	}
	var out []ProjectionMount
	walkErr := filepath.WalkDir(abs, func(path string, d os.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if d.IsDir() {
			// Do not descend into symlinked subdirs (avoid following host symlinks).
			if path != abs {
				if li2, e := os.Lstat(path); e == nil && li2.Mode()&os.ModeSymlink != 0 {
					return filepath.SkipDir
				}
			}
			return nil
		}
		info, e := d.Info()
		if e != nil {
			return nil // skip unreadable entries
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil // skip symlinks and special files inside the corpus
		}
		if !readable(path) {
			return nil
		}
		out = append(out, ProjectionMount{
			Host:     path,
			Target:   targetFromAbs(home, xdg, path),
			Optional: optional,
			Label:    label,
			Status:   projPresent,
		})
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("projection: walk %s: %w", abs, walkErr)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Target < out[j].Target })
	if len(out) == 0 && !optional {
		return nil, fmt.Errorf("projection: required dir has no projectable files: %s", abs)
	}
	return out, nil
}

// resolveGlob expands a filepath.Match pattern under the host and emits one entry per regular file
// match. Non-regular/symlink matches are skipped; zero matches is treated like an absent source.
func resolveGlob(home, xdg string, _ []string, pattern string, optional bool, label string) ([]ProjectionMount, error) {
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("projection: bad glob %q: %w", pattern, err)
	}
	var out []ProjectionMount
	for _, m := range matches {
		li, e := os.Lstat(m)
		if e != nil {
			continue
		}
		if li.Mode()&os.ModeSymlink != 0 || !li.Mode().IsRegular() {
			continue
		}
		if !readable(m) {
			continue
		}
		out = append(out, ProjectionMount{Host: m, Target: targetFromAbs(home, xdg, m), Optional: optional, Label: label, Status: projPresent})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Target < out[j].Target })
	if len(out) == 0 {
		st := projSkippedAbsent
		if optional {
			return []ProjectionMount{{Host: pattern, Target: "", Optional: optional, Label: label, Status: st}}, nil
		}
		return nil, fmt.Errorf("projection: required glob matched nothing: %s", pattern)
	}
	return out, nil
}

// hasSymlinkPrefix walks each path component from its allowed root and reports whether any component
// is a symlink (Lstat, no follow). This is the MVP conservative subset of fd-bind/O_NOFOLLOW
// anti-TOCTOU hardening (specs/0096 ayo lesson #5): existing symlink components are rejected; an
// absent tail is left to the absent/required logic.
func hasSymlinkPrefix(abs, home, xdg string) bool {
	root := ""
	switch {
	case home != "" && (abs == home || strings.HasPrefix(abs, home+string(filepath.Separator))):
		root = home
	case xdg != "" && (abs == xdg || strings.HasPrefix(abs, xdg+string(filepath.Separator))):
		root = xdg
	default:
		return false
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return false
	}
	cur := root
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		if part == "" || part == "." {
			continue
		}
		cur = filepath.Join(cur, part)
		if li, e := os.Lstat(cur); e == nil && li.Mode()&os.ModeSymlink != 0 {
			return true
		}
	}
	return false
}

// targetFromAbs derives the destination under /home/agent: the path relative to $HOME (preserving the
// ~/.pi, ~/.config tree), or relative to $XDG_CONFIG_HOME mirrored under .config.
func targetFromAbs(home, xdg, abs string) string {
	if r, err := filepath.Rel(home, abs); err == nil && !strings.HasPrefix(r, "..") {
		return filepath.ToSlash(r)
	}
	if r, err := filepath.Rel(xdg, abs); err == nil && !strings.HasPrefix(r, "..") {
		return filepath.ToSlash(filepath.Join(".config", r))
	}
	return filepath.ToSlash(abs)
}

// readable reports whether the host user can open the file for reading (best-effort proxy for the
// container uid 1000 read; the entrypoint re-checks at copy time and skips failures — specs/0096 ayo
// lesson #9: backend UID mapping may still make a readable host file unreadable in-container).
func readable(abs string) bool {
	f, err := os.Open(abs)
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}

// excludedRoots lists the absolute host paths that are hard-rejected as projection sources
// (specs/0096 FLO "Excluded sources"): credential dirs, cloud CLI config, safeslop's own
// state/cache/account dirs, browser/cookie/keychain state, and raw git config.
func excludedRoots(home, xdg string) []string {
	return []string{
		filepath.Join(home, ".ssh"),
		filepath.Join(home, ".aws"),
		filepath.Join(home, ".kube"),
		filepath.Join(home, ".docker"),
		filepath.Join(home, ".gnupg"),
		filepath.Join(home, ".npmrc"),
		filepath.Join(home, ".pypirc"),
		filepath.Join(home, ".gitconfig"),
		filepath.Join(home, ".config", "git"),
		filepath.Join(home, ".config", "gcloud"),
		filepath.Join(home, ".config", "safeslop"),
		filepath.Join(home, ".cache", "safeslop"),
		filepath.Join(home, "Library", "Caches", "safeslop"),
		filepath.Join(home, "Library", "Cookies"),
		filepath.Join(home, "Library", "Keychains"),
		filepath.Join(xdg, "gcloud"),
		filepath.Join(xdg, "safeslop"),
		filepath.Join(xdg, "git"),
	}
}
