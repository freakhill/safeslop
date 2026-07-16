package container

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/freakhill/safeslop/internal/engine/policy"
	engsession "github.com/freakhill/safeslop/internal/engine/session"
)

const (
	projPresent           = "present"
	projSkippedAbsent     = "skipped-absent"
	projSkippedUnreadable = "skipped-unreadable"
	projSkippedNonRegular = "skipped-nonregular"
)

const (
	ProjectionTargetOutsideRoot = "projection_target_outside_root"
	ProjectionTargetExcluded    = "projection_target_excluded"
	ProjectionSymlinkLoop       = "projection_symlink_loop"
	ProjectionUnsafeDescendant  = "projection_unsafe_descendant"
	ProjectionSourceType        = "projection_source_type"
	ProjectionSnapshotChanged   = "projection_snapshot_changed"
	ProjectionSafetyUnsupported = "projection_safety_unsupported"
	ProjectionRequiredAbsent    = "projection_required_absent"
)

var projectionFailureText = map[string][2]string{
	ProjectionTargetOutsideRoot: {"Config projection leaves its approved home root.", "Keep its symlink target inside home."},
	ProjectionTargetExcluded:    {"Config projection points to an excluded credential or cache path.", "Remove that link from the projected config path."},
	ProjectionSymlinkLoop:       {"Config projection contains a symlink loop.", "Repair the symlink chain and retry."},
	ProjectionUnsafeDescendant:  {"Config projection contains an unsafe nested entry.", "Remove nested links, special files, or mount crossings."},
	ProjectionSourceType:        {"Config projection is not a regular file or safe directory.", "Use a regular config file or directory."},
	ProjectionSnapshotChanged:   {"Config changed during safe projection.", "Stop concurrent changes and retry."},
	ProjectionSafetyUnsupported: {"This platform cannot safely project this symlink layout.", "Use a non-symlinked config path or a project profile without projection."},
	ProjectionRequiredAbsent:    {"A required builtin config source is unavailable.", "Restore the required source and retry."},
}

// ProjectionError is deliberately value-free. The internal resolver never exposes raw target
// paths or OS errors through Error or Failure; callers may persist Failure safely in a session.
type ProjectionError struct {
	failure engsession.Failure
}

func (e *ProjectionError) Error() string               { return e.failure.Summary }
func (e *ProjectionError) Failure() engsession.Failure { return e.failure }

// ProjectionMount is one private snapshot mounted read-only into the container. Host always names
// engine-owned staging storage for present entries; it never names the live projection source.
type ProjectionMount struct {
	Host      string `json:"host"`
	Container string `json:"staging,omitempty"`
	Target    string `json:"target"`
	Optional  bool   `json:"optional"`
	Label     string `json:"label,omitempty"`
	Status    string `json:"status"`
}

type ProjectionManifest struct {
	Items []ProjectionMount `json:"items"`
}

func (m ProjectionManifest) MarshalJSON() ([]byte, error) {
	type plain ProjectionManifest
	return json.Marshal(plain(m))
}

func (m ProjectionManifest) PresentMounts() []ProjectionMount {
	out := make([]ProjectionMount, 0)
	for _, it := range m.Items {
		if it.Status == projPresent {
			out = append(out, it)
		}
	}
	return out
}

func projectionRoots(home string) (string, string) {
	home = filepath.Clean(home)
	xdg := os.Getenv("XDG_CONFIG_HOME")
	if xdg == "" || !filepath.IsAbs(xdg) {
		xdg = filepath.Join(home, ".config")
	}
	return home, filepath.Clean(xdg)
}

// projectionAfterOpen is a test-only barrier. Tests replace a source pathname immediately after
// its descriptor is pinned; production leaves it nil. Snapshot bytes must still come from the fd.
var projectionAfterOpen func(source string)
var projectionAfterReadlink func(source string)

// projectionAfterGlobLstat is a test-only barrier between candidate classification and open.
var projectionAfterGlobLstat func(source, name string)

// projectionMountID is injected in tests to model a same-device mount crossing without requiring
// privileged mount operations. Production uses the OS-specific descriptor query.
var projectionMountID = fileMountID

type projectionResolver struct {
	home        string
	xdg         string
	excluded    []string
	homeRoot    *os.Root
	homeInfo    os.FileInfo
	homeMount   uint64
	xdgRoot     *os.Root
	xdgInfo     os.FileInfo
	xdgMount    uint64
	snapshotTmp string
	item        policy.ProjectionItem
	optional    bool
	files       []sourceProof
	dirs        []dirProof
}

type pinnedNode struct {
	file      *os.File
	dir       *os.Root
	info      os.FileInfo
	canonical string
	mountID   uint64
}

type sourceProof struct {
	file   *os.File
	before os.FileInfo
	digest [sha256.Size]byte
}

type dirProof struct {
	dir    *os.Root
	before os.FileInfo
}

func (n *pinnedNode) close() {
	if n.file != nil {
		_ = n.file.Close()
	}
	if n.dir != nil {
		_ = n.dir.Close()
	}
}

type unavailableError struct{ absent bool }

func (e unavailableError) Error() string { return "projection source unavailable" }

func mountIDForRoot(root *os.Root) (uint64, bool) {
	file, err := root.Open(".")
	if err != nil {
		return 0, false
	}
	defer file.Close()
	return projectionMountID(file)
}

// SnapshotProjection resolves sources through a descriptor-pinned os.Root walk and publishes a
// complete private snapshot below stageDir. Only returned snapshot paths may be mounted.
func SnapshotProjection(home, stageDir string, proj policy.Projection) (manifest ProjectionManifest, err error) {
	home = filepath.Clean(home)
	if !filepath.IsAbs(home) || !projectionSafetySupported() {
		return ProjectionManifest{}, newProjectionError(ProjectionSafetyUnsupported, policy.ProjectionItem{}, home, true)
	}
	if err := os.MkdirAll(stageDir, 0o700); err != nil {
		return ProjectionManifest{}, newProjectionError(ProjectionSafetyUnsupported, policy.ProjectionItem{}, home, true)
	}
	if err := os.Chmod(stageDir, 0o700); err != nil {
		return ProjectionManifest{}, newProjectionError(ProjectionSafetyUnsupported, policy.ProjectionItem{}, home, true)
	}

	finalDir := filepath.Join(stageDir, "projection-snapshots")
	_ = os.RemoveAll(finalDir)
	tmpDir, err := os.MkdirTemp(stageDir, ".projection-snapshots-")
	if err != nil {
		return ProjectionManifest{}, newProjectionError(ProjectionSafetyUnsupported, policy.ProjectionItem{}, home, true)
	}
	if err := os.Chmod(tmpDir, 0o700); err != nil {
		_ = os.RemoveAll(tmpDir)
		return ProjectionManifest{}, newProjectionError(ProjectionSafetyUnsupported, policy.ProjectionItem{}, home, true)
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(tmpDir)
			_ = os.RemoveAll(finalDir)
		}
	}()

	_, xdg := projectionRoots(home)
	r := &projectionResolver{home: home, xdg: xdg, excluded: excludedRoots(home, xdg), snapshotTmp: tmpDir}
	r.homeRoot, err = os.OpenRoot(home)
	if err != nil {
		return ProjectionManifest{}, newProjectionError(ProjectionSafetyUnsupported, policy.ProjectionItem{}, home, true)
	}
	defer r.homeRoot.Close()
	r.homeInfo, err = r.homeRoot.Stat(".")
	if err != nil || !r.homeInfo.IsDir() {
		return ProjectionManifest{}, newProjectionError(ProjectionSafetyUnsupported, policy.ProjectionItem{}, home, true)
	}
	if r.homeMount, _ = mountIDForRoot(r.homeRoot); r.homeMount == 0 {
		return ProjectionManifest{}, newProjectionError(ProjectionSafetyUnsupported, policy.ProjectionItem{}, home, true)
	}
	defer r.closeProofs()

	// An external XDG root is approved independently and opened only if an item actually names it;
	// an unrelated missing XDG directory must not break a ~/ projection.
	needsXDG := false
	if !underRoot(xdg, home) {
		for _, item := range proj.Items {
			if abs, expandErr := expandSource(home, item.Source); expandErr == nil && underRoot(abs, xdg) {
				needsXDG = true
				break
			}
		}
	}
	if needsXDG {
		r.xdgRoot, err = os.OpenRoot(xdg)
		if err != nil {
			return ProjectionManifest{}, newProjectionError(ProjectionSafetyUnsupported, policy.ProjectionItem{}, xdg, true)
		}
		defer r.xdgRoot.Close()
		r.xdgInfo, err = r.xdgRoot.Stat(".")
		if err != nil || !r.xdgInfo.IsDir() {
			return ProjectionManifest{}, newProjectionError(ProjectionSafetyUnsupported, policy.ProjectionItem{}, xdg, true)
		}
		if r.xdgMount, _ = mountIDForRoot(r.xdgRoot); r.xdgMount == 0 {
			return ProjectionManifest{}, newProjectionError(ProjectionSafetyUnsupported, policy.ProjectionItem{}, xdg, true)
		}
	}

	seenTarget := map[string]bool{}
	for _, item := range proj.Items {
		r.item = item
		r.optional = item.Optional == nil || *item.Optional
		entries, itemErr := r.snapshotItem(item)
		if itemErr != nil {
			return ProjectionManifest{}, itemErr
		}
		for _, entry := range entries {
			if entry.Status == projPresent {
				if seenTarget[entry.Target] {
					return ProjectionManifest{}, r.fail(ProjectionSourceType)
				}
				seenTarget[entry.Target] = true
			}
			manifest.Items = append(manifest.Items, entry)
		}
	}

	if err := r.verifyProofs(); err != nil {
		return ProjectionManifest{}, err
	}
	if err := os.Rename(tmpDir, finalDir); err != nil {
		return ProjectionManifest{}, r.fail(ProjectionSnapshotChanged)
	}
	published = true
	for i := range manifest.Items {
		if manifest.Items[i].Status != projPresent {
			continue
		}
		manifest.Items[i].Host = filepath.Join(finalDir, filepath.Base(manifest.Items[i].Host))
		manifest.Items[i].Container = fmt.Sprintf("/safeslop/projected/%d", iPresent(manifest.Items, i))
	}
	return manifest, nil
}

func iPresent(items []ProjectionMount, through int) int {
	n := 0
	for i := 0; i < through; i++ {
		if items[i].Status == projPresent {
			n++
		}
	}
	return n
}

func (r *projectionResolver) snapshotItem(item policy.ProjectionItem) ([]ProjectionMount, error) {
	kind := item.Kind
	if kind == "" {
		kind = "file"
	}
	abs, err := expandSource(r.home, item.Source)
	if err != nil {
		return nil, r.fail(ProjectionTargetOutsideRoot)
	}
	if code := projectionLawCode(abs, r.home, r.xdg, r.excluded); code != "" {
		return nil, r.fail(code)
	}
	if kind == "glob" {
		return r.snapshotGlob(abs)
	}
	root, rootMount, rel, ok := r.rootFor(abs)
	if !ok {
		return nil, r.fail(ProjectionTargetOutsideRoot)
	}
	node, err := r.openPinned(root, rootMount, rel)
	if err != nil {
		return r.unavailableOrFailure(abs, kind, err)
	}
	defer node.close()

	target := targetFromAbs(r.home, r.xdg, abs)
	switch kind {
	case "file":
		if node.file == nil || !node.info.Mode().IsRegular() {
			return nil, r.fail(ProjectionSourceType)
		}
		entry, err := r.snapshotFile(node, target, item.Label)
		if err != nil {
			return nil, err
		}
		return []ProjectionMount{entry}, nil
	case "dir":
		if node.dir == nil || !node.info.IsDir() {
			return nil, r.fail(ProjectionSourceType)
		}
		var out []ProjectionMount
		if err := r.snapshotDir(node.dir, node.info, node.mountID, node.canonical, target, item.Label, &out); err != nil {
			return nil, err
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Target < out[j].Target })
		if len(out) == 0 && !r.optional {
			return nil, r.fail(ProjectionRequiredAbsent)
		}
		return out, nil
	default:
		return nil, r.fail(ProjectionSourceType)
	}
}

func (r *projectionResolver) unavailableOrFailure(abs, kind string, err error) ([]ProjectionMount, error) {
	var unavailable unavailableError
	if !errors.As(err, &unavailable) {
		return nil, err
	}
	if !r.optional {
		return nil, r.fail(ProjectionRequiredAbsent)
	}
	target := targetFromAbs(r.home, r.xdg, abs)
	if kind == "dir" {
		target += "/"
	}
	status := projSkippedUnreadable
	if unavailable.absent {
		status = projSkippedAbsent
	}
	return []ProjectionMount{{Host: displaySource(r.home, abs), Target: target, Optional: true, Label: r.item.Label, Status: status}}, nil
}

func (r *projectionResolver) snapshotGlob(pattern string) ([]ProjectionMount, error) {
	dirAbs, basePattern := filepath.Split(pattern)
	dirAbs = filepath.Clean(dirAbs)
	if strings.ContainsAny(dirAbs, "*?[") || basePattern == "" {
		return nil, r.fail(ProjectionSourceType)
	}
	if code := projectionLawCode(dirAbs, r.home, r.xdg, r.excluded); code != "" {
		return nil, r.fail(code)
	}
	root, rootMount, rel, ok := r.rootFor(dirAbs)
	if !ok {
		return nil, r.fail(ProjectionTargetOutsideRoot)
	}
	node, err := r.openPinned(root, rootMount, rel)
	if err != nil {
		return r.unavailableOrFailure(pattern, "glob", err)
	}
	defer node.close()
	if node.dir == nil {
		return nil, r.fail(ProjectionSourceType)
	}
	before := node.info
	entries, err := node.dir.Open(".")
	if err != nil {
		return nil, r.fail(ProjectionSnapshotChanged)
	}
	names, readErr := entries.ReadDir(-1)
	_ = entries.Close()
	if readErr != nil {
		return nil, r.fail(ProjectionSnapshotChanged)
	}
	sort.Slice(names, func(i, j int) bool { return names[i].Name() < names[j].Name() })
	var out []ProjectionMount
	skippedNonRegular := false
	for _, de := range names {
		matched, matchErr := filepath.Match(basePattern, de.Name())
		if matchErr != nil {
			return nil, r.fail(ProjectionSourceType)
		}
		if !matched {
			continue
		}
		classified, statErr := node.dir.Lstat(de.Name())
		if statErr != nil {
			return nil, r.fail(ProjectionSnapshotChanged)
		}
		if !classified.Mode().IsRegular() {
			if !r.optional {
				return nil, r.fail(ProjectionUnsafeDescendant)
			}
			skippedNonRegular = true
			continue
		}
		if projectionAfterGlobLstat != nil {
			projectionAfterGlobLstat(r.item.Source, de.Name())
		}
		child, childErr := r.openDirect(node.dir, node.mountID, filepath.Join(node.canonical, de.Name()), de.Name())
		if childErr != nil {
			return nil, childErr
		}
		if child.file == nil || !stableInfo(classified, child.info) {
			child.close()
			return nil, r.fail(ProjectionSnapshotChanged)
		}
		target := targetFromAbs(r.home, r.xdg, filepath.Join(dirAbs, de.Name()))
		entry, copyErr := r.snapshotFile(child, target, r.item.Label)
		child.close()
		if copyErr != nil {
			return nil, copyErr
		}
		out = append(out, entry)
	}
	after, statErr := node.dir.Stat(".")
	if statErr != nil || !stableInfo(before, after) {
		return nil, r.fail(ProjectionSnapshotChanged)
	}
	if err := r.retainDirProof(node.dir, after); err != nil {
		return nil, err
	}
	if len(out) == 0 && !r.optional {
		return nil, r.fail(ProjectionRequiredAbsent)
	}
	if skippedNonRegular {
		out = append(out, ProjectionMount{Host: displaySource(r.home, pattern), Optional: true, Label: r.item.Label, Status: projSkippedNonRegular})
	}
	if len(out) == 0 {
		return []ProjectionMount{{Host: displaySource(r.home, pattern), Optional: true, Label: r.item.Label, Status: projSkippedAbsent}}, nil
	}
	return out, nil
}

func (r *projectionResolver) snapshotDir(dir *os.Root, before os.FileInfo, mountID uint64, canonical, targetBase, label string, out *[]ProjectionMount) error {
	f, err := dir.Open(".")
	if err != nil {
		return r.fail(ProjectionSnapshotChanged)
	}
	entries, err := f.ReadDir(-1)
	_ = f.Close()
	if err != nil {
		return r.fail(ProjectionSnapshotChanged)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, de := range entries {
		childCanonical := filepath.Join(canonical, de.Name())
		if projectionLawCode(childCanonical, r.home, r.xdg, r.excluded) == ProjectionTargetExcluded {
			return r.fail(ProjectionUnsafeDescendant)
		}
		child, childErr := r.openDirect(dir, mountID, childCanonical, de.Name())
		if childErr != nil {
			return childErr
		}
		childTarget := filepath.ToSlash(filepath.Join(targetBase, de.Name()))
		if child.dir != nil {
			childErr = r.snapshotDir(child.dir, child.info, child.mountID, child.canonical, childTarget, label, out)
			child.close()
			if childErr != nil {
				return childErr
			}
			continue
		}
		if child.file == nil || !child.info.Mode().IsRegular() {
			child.close()
			return r.fail(ProjectionUnsafeDescendant)
		}
		entry, copyErr := r.snapshotFile(child, childTarget, label)
		child.close()
		if copyErr != nil {
			return copyErr
		}
		*out = append(*out, entry)
	}
	after, err := dir.Stat(".")
	if err != nil || !stableInfo(before, after) {
		return r.fail(ProjectionSnapshotChanged)
	}
	return r.retainDirProof(dir, after)
}

func (r *projectionResolver) snapshotFile(node *pinnedNode, target, label string) (ProjectionMount, error) {
	before, err := node.file.Stat()
	if err != nil || !before.Mode().IsRegular() {
		return ProjectionMount{}, r.fail(ProjectionSnapshotChanged)
	}
	if projectionAfterOpen != nil {
		projectionAfterOpen(r.item.Source)
	}
	name := fmt.Sprintf("%06d", nextSnapshotIndex(r.snapshotTmp))
	partial := filepath.Join(r.snapshotTmp, "."+name+".partial")
	dst, err := os.OpenFile(partial, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o444)
	if err != nil {
		return ProjectionMount{}, r.fail(ProjectionSnapshotChanged)
	}
	h := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(dst, h), node.file)
	closeErr := dst.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(partial)
		return ProjectionMount{}, r.fail(ProjectionSnapshotChanged)
	}
	after, err := node.file.Stat()
	if err != nil || !stableInfo(before, after) {
		_ = os.Remove(partial)
		return ProjectionMount{}, r.fail(ProjectionSnapshotChanged)
	}
	if _, err := node.file.Seek(0, io.SeekStart); err != nil {
		_ = os.Remove(partial)
		return ProjectionMount{}, r.fail(ProjectionSnapshotChanged)
	}
	verify := sha256.New()
	if _, err := io.Copy(verify, node.file); err != nil || !equalDigest(h.Sum(nil), verify.Sum(nil)) {
		_ = os.Remove(partial)
		return ProjectionMount{}, r.fail(ProjectionSnapshotChanged)
	}
	if err := os.Chmod(partial, 0o444); err != nil {
		_ = os.Remove(partial)
		return ProjectionMount{}, r.fail(ProjectionSnapshotChanged)
	}
	published := filepath.Join(r.snapshotTmp, name)
	if err := os.Rename(partial, published); err != nil {
		_ = os.Remove(partial)
		return ProjectionMount{}, r.fail(ProjectionSnapshotChanged)
	}
	var digest [sha256.Size]byte
	copy(digest[:], h.Sum(nil))
	r.files = append(r.files, sourceProof{file: node.file, before: after, digest: digest})
	node.file = nil // the proof owns this descriptor until the complete snapshot is published
	return ProjectionMount{Host: published, Target: target, Optional: r.optional, Label: label, Status: projPresent}, nil
}

func nextSnapshotIndex(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), ".") {
			n++
		}
	}
	return n
}

func equalDigest(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := range a {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}

func stableInfo(before, after os.FileInfo) bool {
	return after != nil && os.SameFile(before, after) && before.Size() == after.Size() && before.Mode() == after.Mode() && before.ModTime() == after.ModTime()
}

func (r *projectionResolver) verifyProofs() error {
	for _, proof := range r.files {
		after, err := proof.file.Stat()
		if err != nil || !stableInfo(proof.before, after) {
			return r.fail(ProjectionSnapshotChanged)
		}
		if _, err := proof.file.Seek(0, io.SeekStart); err != nil {
			return r.fail(ProjectionSnapshotChanged)
		}
		h := sha256.New()
		if _, err := io.Copy(h, proof.file); err != nil || !equalDigest(proof.digest[:], h.Sum(nil)) {
			return r.fail(ProjectionSnapshotChanged)
		}
	}
	for _, proof := range r.dirs {
		after, err := proof.dir.Stat(".")
		if err != nil || !stableInfo(proof.before, after) {
			return r.fail(ProjectionSnapshotChanged)
		}
	}
	return nil
}

func (r *projectionResolver) closeProofs() {
	for _, proof := range r.files {
		_ = proof.file.Close()
	}
	for _, proof := range r.dirs {
		_ = proof.dir.Close()
	}
}

func (r *projectionResolver) retainDirProof(dir *os.Root, before os.FileInfo) error {
	hold, err := dir.OpenRoot(".")
	if err != nil {
		return r.fail(ProjectionSnapshotChanged)
	}
	after, err := hold.Stat(".")
	if err != nil || !stableInfo(before, after) {
		_ = hold.Close()
		return r.fail(ProjectionSnapshotChanged)
	}
	r.dirs = append(r.dirs, dirProof{dir: hold, before: after})
	return nil
}

func (r *projectionResolver) openPinned(root *os.Root, rootMount uint64, rel string) (*pinnedNode, error) {
	rel = filepath.Clean(rel)
	if rel == "." {
		return nil, r.fail(ProjectionSourceType)
	}
	parts := splitRel(rel)
	visited := map[string]bool{}
	for links := 0; ; {
		current := root
		prefix := ""
		var opened []*os.Root
		reset := false
		for i, part := range parts {
			info, err := current.Lstat(part)
			if err != nil {
				closeRoots(opened)
				if errors.Is(err, os.ErrNotExist) {
					return nil, unavailableError{absent: true}
				}
				if errors.Is(err, os.ErrPermission) {
					return nil, unavailableError{}
				}
				return nil, r.fail(ProjectionSnapshotChanged)
			}
			canonical := filepath.Join(prefix, part)
			if info.Mode()&os.ModeSymlink != 0 {
				if visited[canonical] || links >= 40 {
					closeRoots(opened)
					return nil, r.fail(ProjectionSymlinkLoop)
				}
				visited[canonical] = true
				links++
				target, err := current.Readlink(part)
				if err != nil {
					closeRoots(opened)
					return nil, r.fail(ProjectionSnapshotChanged)
				}
				if projectionAfterReadlink != nil {
					projectionAfterReadlink(r.item.Source)
				}
				afterLink, statErr := current.Lstat(part)
				targetAfter, readErr := current.Readlink(part)
				if statErr != nil || readErr != nil || afterLink.Mode()&os.ModeSymlink == 0 || !os.SameFile(info, afterLink) || target != targetAfter {
					closeRoots(opened)
					return nil, r.fail(ProjectionSnapshotChanged)
				}
				if filepath.IsAbs(target) {
					closeRoots(opened)
					return nil, r.fail(ProjectionTargetOutsideRoot)
				}
				next := filepath.Clean(filepath.Join(filepath.Dir(canonical), target, filepath.Join(parts[i+1:]...)))
				if escapesRoot(next) {
					closeRoots(opened)
					return nil, r.fail(ProjectionTargetOutsideRoot)
				}
				if code := projectionLawCode(filepath.Join(root.Name(), next), r.home, r.xdg, r.excluded); code != "" {
					closeRoots(opened)
					return nil, r.fail(code)
				}
				parts = splitRel(next)
				closeRoots(opened)
				reset = true
				break
			}
			last := i == len(parts)-1
			if last {
				if info.IsDir() {
					d, err := current.OpenRoot(part)
					if err != nil {
						closeRoots(opened)
						return nil, r.fail(ProjectionSnapshotChanged)
					}
					after, err := d.Stat(".")
					mountID, mountOK := mountIDForRoot(d)
					if err != nil || !os.SameFile(info, after) || !mountOK || mountID != rootMount {
						_ = d.Close()
						closeRoots(opened)
						if !mountOK {
							return nil, r.fail(ProjectionSafetyUnsupported)
						}
						return nil, r.fail(ProjectionUnsafeDescendant)
					}
					closeRoots(opened)
					return &pinnedNode{dir: d, info: after, canonical: filepath.Join(root.Name(), canonical), mountID: mountID}, nil
				}
				if !info.Mode().IsRegular() {
					closeRoots(opened)
					return nil, r.fail(ProjectionSourceType)
				}
				f, err := current.Open(part)
				if err != nil {
					closeRoots(opened)
					if errors.Is(err, os.ErrPermission) {
						return nil, unavailableError{}
					}
					return nil, r.fail(ProjectionSnapshotChanged)
				}
				after, err := f.Stat()
				mountID, mountOK := projectionMountID(f)
				if err != nil || !os.SameFile(info, after) || !mountOK || mountID != rootMount {
					_ = f.Close()
					closeRoots(opened)
					if !mountOK {
						return nil, r.fail(ProjectionSafetyUnsupported)
					}
					return nil, r.fail(ProjectionUnsafeDescendant)
				}
				closeRoots(opened)
				return &pinnedNode{file: f, info: after, canonical: filepath.Join(root.Name(), canonical), mountID: mountID}, nil
			}
			if !info.IsDir() {
				closeRoots(opened)
				return nil, r.fail(ProjectionSourceType)
			}
			nextRoot, err := current.OpenRoot(part)
			if err != nil {
				closeRoots(opened)
				return nil, r.fail(ProjectionSnapshotChanged)
			}
			after, err := nextRoot.Stat(".")
			mountID, mountOK := mountIDForRoot(nextRoot)
			if err != nil || !os.SameFile(info, after) || !mountOK || mountID != rootMount {
				_ = nextRoot.Close()
				closeRoots(opened)
				if !mountOK {
					return nil, r.fail(ProjectionSafetyUnsupported)
				}
				return nil, r.fail(ProjectionUnsafeDescendant)
			}
			opened = append(opened, nextRoot)
			current = nextRoot
			prefix = canonical
		}
		if !reset {
			return nil, r.fail(ProjectionSnapshotChanged)
		}
	}
}

func (r *projectionResolver) openDirect(parent *os.Root, parentMount uint64, canonical, name string) (*pinnedNode, error) {
	info, err := parent.Lstat(name)
	if err != nil {
		return nil, r.fail(ProjectionSnapshotChanged)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, r.fail(ProjectionUnsafeDescendant)
	}
	if info.IsDir() {
		dir, err := parent.OpenRoot(name)
		if err != nil {
			return nil, r.fail(ProjectionSnapshotChanged)
		}
		after, err := dir.Stat(".")
		mountID, mountOK := mountIDForRoot(dir)
		if err != nil || !os.SameFile(info, after) || !mountOK || mountID != parentMount {
			_ = dir.Close()
			if !mountOK {
				return nil, r.fail(ProjectionSafetyUnsupported)
			}
			return nil, r.fail(ProjectionUnsafeDescendant)
		}
		return &pinnedNode{dir: dir, info: after, canonical: canonical, mountID: mountID}, nil
	}
	if !info.Mode().IsRegular() {
		return nil, r.fail(ProjectionUnsafeDescendant)
	}
	f, err := parent.Open(name)
	if err != nil {
		return nil, r.fail(ProjectionSnapshotChanged)
	}
	after, err := f.Stat()
	mountID, mountOK := projectionMountID(f)
	if err != nil || !os.SameFile(info, after) || !mountOK || mountID != parentMount {
		_ = f.Close()
		if !mountOK {
			return nil, r.fail(ProjectionSafetyUnsupported)
		}
		return nil, r.fail(ProjectionUnsafeDescendant)
	}
	return &pinnedNode{file: f, info: after, canonical: canonical, mountID: mountID}, nil
}

func closeRoots(roots []*os.Root) {
	for i := len(roots) - 1; i >= 0; i-- {
		_ = roots[i].Close()
	}
}

func splitRel(rel string) []string {
	var out []string
	for _, part := range strings.Split(filepath.Clean(rel), string(filepath.Separator)) {
		if part != "" && part != "." {
			out = append(out, part)
		}
	}
	return out
}

func escapesRoot(rel string) bool {
	return rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (r *projectionResolver) rootFor(abs string) (*os.Root, uint64, string, bool) {
	if underRoot(abs, r.home) {
		rel, err := filepath.Rel(r.home, abs)
		return r.homeRoot, r.homeMount, rel, err == nil && !escapesRoot(rel)
	}
	if r.xdgRoot != nil && underRoot(abs, r.xdg) {
		rel, err := filepath.Rel(r.xdg, abs)
		return r.xdgRoot, r.xdgMount, rel, err == nil && !escapesRoot(rel)
	}
	return nil, 0, "", false
}

func (r *projectionResolver) fail(code string) error {
	return newProjectionError(code, r.item, r.home, !r.optional)
}

func newProjectionError(code string, item policy.ProjectionItem, home string, required bool) *ProjectionError {
	text, ok := projectionFailureText[code]
	if !ok {
		code = ProjectionSafetyUnsupported
		text = projectionFailureText[code]
	}
	label := item.Label
	if label == "" {
		label = "config"
	}
	return &ProjectionError{failure: engsession.Failure{
		Version: 1, Phase: "projection", Code: code,
		Projection: "builtin." + label, Source: displaySource(home, item.Source), Required: required,
		Summary: text[0], Action: text[1],
	}}
}

func expandSource(home, src string) (string, error) {
	if strings.ContainsRune(src, 0) {
		return "", errors.New("invalid projection source")
	}
	src = filepath.Clean(src)
	switch {
	case src == "~":
		return home, nil
	case strings.HasPrefix(src, "~/"):
		return filepath.Join(home, src[2:]), nil
	case filepath.IsAbs(src):
		return src, nil
	default:
		return "", errors.New("projection source must be rooted")
	}
}

func projectionLawCode(abs, home, xdg string, excluded []string) string {
	if abs == home || abs == xdg {
		return ProjectionSourceType
	}
	if !underRoot(abs, home) && !underRoot(abs, xdg) {
		return ProjectionTargetOutsideRoot
	}
	for _, ex := range excluded {
		if underRoot(abs, ex) {
			return ProjectionTargetExcluded
		}
	}
	if filepath.Dir(abs) == filepath.Join(home, ".cargo") && strings.HasPrefix(filepath.Base(abs), "credentials") {
		return ProjectionTargetExcluded
	}
	return ""
}

func underRoot(path, root string) bool {
	path, root = filepath.Clean(path), filepath.Clean(root)
	return path == root || strings.HasPrefix(path, root+string(filepath.Separator))
}

func targetFromAbs(home, xdg, abs string) string {
	if r, err := filepath.Rel(home, abs); err == nil && !escapesRoot(r) {
		return filepath.ToSlash(r)
	}
	if r, err := filepath.Rel(xdg, abs); err == nil && !escapesRoot(r) {
		return filepath.ToSlash(filepath.Join(".config", r))
	}
	return ""
}

func displaySource(home, src string) string {
	if src == "" {
		return "~"
	}
	if strings.HasPrefix(src, "~/") || src == "~" {
		return filepath.ToSlash(filepath.Clean(src))
	}
	if filepath.IsAbs(src) && underRoot(src, home) {
		rel, err := filepath.Rel(home, src)
		if err == nil && !escapesRoot(rel) {
			return "~/" + filepath.ToSlash(rel)
		}
	}
	return "~"
}

func excludedRoots(home, xdg string) []string {
	return []string{
		filepath.Join(home, ".ssh"), filepath.Join(home, ".aws"), filepath.Join(home, ".kube"),
		filepath.Join(home, ".docker"), filepath.Join(home, ".gnupg"), filepath.Join(home, ".npmrc"),
		filepath.Join(home, ".pypirc"), filepath.Join(home, ".gitconfig"), filepath.Join(home, ".config", "git"),
		filepath.Join(home, ".config", "gcloud"), filepath.Join(home, ".config", "safeslop"),
		filepath.Join(home, ".cache", "safeslop"), filepath.Join(home, "Library", "Caches", "safeslop"),
		filepath.Join(home, "Library", "Cookies"), filepath.Join(home, "Library", "Keychains"),
		filepath.Join(xdg, "gcloud"), filepath.Join(xdg, "safeslop"), filepath.Join(xdg, "git"),
	}
}
