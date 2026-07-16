package hostpath

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const maxProofLinks = 40

func proofLinkCountSafe(dereferences int) bool {
	return dereferences >= 0 && dereferences <= maxProofLinks
}

func sameProofMount(rootMount, nodeMount uint64, known bool) bool {
	return known && rootMount != 0 && rootMount == nodeMount
}

type proofFailure uint8

const (
	proofUnavailable proofFailure = iota + 1
	proofOutsideRoot
	proofSymlinkLoop
	proofChanged
	proofUnsupported
	proofUnsafe
	proofSourceType
)

type proofError struct {
	failure proofFailure
	absent  bool
	linked  bool
}

func (e *proofError) Error() string { return "host path proof failed" }

type proofRoot struct {
	path    string
	root    *os.Root
	before  os.FileInfo
	mountID uint64
	edges   []proofEdge
}

type proofEdge struct {
	parent     *os.Root
	name       string
	before     os.FileInfo
	linkTarget string
	link       bool
}

type pinnedNode struct {
	file      *os.File
	dir       *os.Root
	info      os.FileInfo
	canonical string
	mountID   uint64
}

// proofMountID is injected by package tests to model mount-instance changes.
// Production always points at the supported platform's descriptor query.
var proofMountID = fileMountID

func openProofRoot(path string) (*proofRoot, error) {
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, &proofError{failure: proofUnavailable, absent: errors.Is(err, os.ErrNotExist)}
	}
	before, err := root.Stat(".")
	if err != nil || !before.IsDir() {
		_ = root.Close()
		return nil, &proofError{failure: proofUnsupported}
	}
	mountID, ok := mountIDForRoot(root)
	if !ok || mountID == 0 {
		_ = root.Close()
		return nil, &proofError{failure: proofUnsupported}
	}
	return &proofRoot{path: filepath.Clean(path), root: root, before: before, mountID: mountID}, nil
}

func (r *proofRoot) clone() (*proofRoot, error) {
	if r == nil || r.root == nil {
		return nil, &proofError{failure: proofChanged}
	}
	root, err := r.root.OpenRoot(".")
	if err != nil {
		return nil, &proofError{failure: proofChanged}
	}
	before, err := root.Stat(".")
	mountID, mountOK := mountIDForRoot(root)
	if err != nil || !os.SameFile(r.before, before) || !mountOK || mountID != r.mountID {
		_ = root.Close()
		return nil, &proofError{failure: proofChanged}
	}
	return &proofRoot{path: r.path, root: root, before: before, mountID: mountID}, nil
}

func (r *proofRoot) close() {
	if r == nil {
		return
	}
	for _, edge := range r.edges {
		_ = edge.parent.Close()
	}
	if r.root != nil {
		_ = r.root.Close()
	}
}

func (r *proofRoot) retainEdge(parent *os.Root, name string, before os.FileInfo, linkTarget string, link bool) error {
	hold, err := parent.OpenRoot(".")
	if err != nil {
		return &proofError{failure: proofChanged}
	}
	r.edges = append(r.edges, proofEdge{parent: hold, name: name, before: before, linkTarget: linkTarget, link: link})
	return nil
}

func (r *proofRoot) revalidate() bool {
	if r == nil || r.root == nil {
		return false
	}
	after, err := r.root.Stat(".")
	if err != nil || !stableInfo(r.before, after) {
		return false
	}
	mountID, ok := mountIDForRoot(r.root)
	if !ok || mountID != r.mountID {
		return false
	}
	for _, edge := range r.edges {
		after, err := edge.parent.Lstat(edge.name)
		if err != nil || !stableInfo(edge.before, after) {
			return false
		}
		if edge.link {
			target, err := edge.parent.Readlink(edge.name)
			if err != nil || target != edge.linkTarget {
				return false
			}
			continue
		}
		if after.IsDir() {
			dir, err := edge.parent.OpenRoot(edge.name)
			if err != nil {
				return false
			}
			opened, statErr := dir.Stat(".")
			edgeMount, mountOK := mountIDForRoot(dir)
			_ = dir.Close()
			if statErr != nil || !os.SameFile(after, opened) || !mountOK || edgeMount != r.mountID {
				return false
			}
			continue
		}
		file, err := edge.parent.Open(edge.name)
		if err != nil {
			return false
		}
		opened, statErr := file.Stat()
		edgeMount, mountOK := proofMountID(file)
		_ = file.Close()
		if statErr != nil || !os.SameFile(after, opened) || !mountOK || edgeMount != r.mountID {
			return false
		}
	}
	return true
}

func (n *pinnedNode) close() {
	if n.file != nil {
		_ = n.file.Close()
	}
	if n.dir != nil {
		_ = n.dir.Close()
	}
}

func mountIDForRoot(root *os.Root) (uint64, bool) {
	file, err := root.Open(".")
	if err != nil {
		return 0, false
	}
	defer file.Close()
	return proofMountID(file)
}

type proofPolicy struct {
	validateCanonical func(string) error
	validateDirectory func(os.FileInfo) error
	afterReadlink     func()
}

func openPinnedPath(root *proofRoot, rel string, validate func(string) error, afterReadlink func()) (*pinnedNode, error) {
	return openPinnedPathWithPolicy(root, rel, proofPolicy{validateCanonical: validate, afterReadlink: afterReadlink})
}

func openPinnedPathWithPolicy(root *proofRoot, rel string, policy proofPolicy) (*pinnedNode, error) {
	rel = filepath.Clean(rel)
	if rel == "." {
		return nil, &proofError{failure: proofSourceType}
	}
	parts := splitRel(rel)
	visited := map[string]bool{}
	for links := 0; ; {
		current := root.root
		prefix := ""
		var opened []*os.Root
		reset := false
		for i, part := range parts {
			info, err := current.Lstat(part)
			if err != nil {
				closeRoots(opened)
				if errors.Is(err, os.ErrNotExist) {
					return nil, &proofError{failure: proofUnavailable, absent: true, linked: links > 0}
				}
				if errors.Is(err, os.ErrPermission) {
					return nil, &proofError{failure: proofUnavailable}
				}
				return nil, &proofError{failure: proofChanged}
			}
			canonical := filepath.Join(prefix, part)
			if info.Mode()&os.ModeSymlink != 0 {
				if visited[canonical] || !proofLinkCountSafe(links+1) {
					closeRoots(opened)
					return nil, &proofError{failure: proofSymlinkLoop}
				}
				visited[canonical] = true
				links++
				target, err := current.Readlink(part)
				if err != nil {
					closeRoots(opened)
					return nil, &proofError{failure: proofChanged}
				}
				if policy.afterReadlink != nil {
					policy.afterReadlink()
				}
				afterLink, statErr := current.Lstat(part)
				targetAfter, readErr := current.Readlink(part)
				if statErr != nil || readErr != nil || afterLink.Mode()&os.ModeSymlink == 0 || !os.SameFile(info, afterLink) || target != targetAfter {
					closeRoots(opened)
					return nil, &proofError{failure: proofChanged}
				}
				if err := root.retainEdge(current, part, afterLink, target, true); err != nil {
					closeRoots(opened)
					return nil, err
				}
				var next string
				if filepath.IsAbs(target) {
					relTarget, ok := strictAbsoluteTarget(root.path, target)
					if !ok {
						closeRoots(opened)
						return nil, &proofError{failure: proofOutsideRoot}
					}
					next = filepath.Join(relTarget, filepath.Join(parts[i+1:]...))
				} else {
					next = filepath.Clean(filepath.Join(filepath.Dir(canonical), target, filepath.Join(parts[i+1:]...)))
				}
				if escapesRoot(next) {
					closeRoots(opened)
					return nil, &proofError{failure: proofOutsideRoot}
				}
				if policy.validateCanonical != nil {
					if err := policy.validateCanonical(filepath.Join(root.path, next)); err != nil {
						closeRoots(opened)
						return nil, err
					}
				}
				parts = splitRel(next)
				closeRoots(opened)
				reset = true
				break
			}
			last := i == len(parts)-1
			if last {
				if info.IsDir() {
					dir, err := current.OpenRoot(part)
					if err != nil {
						closeRoots(opened)
						return nil, &proofError{failure: proofChanged}
					}
					after, err := dir.Stat(".")
					mountID, mountOK := mountIDForRoot(dir)
					if err != nil || !os.SameFile(info, after) || !mountOK || mountID != root.mountID {
						_ = dir.Close()
						closeRoots(opened)
						if !mountOK {
							return nil, &proofError{failure: proofUnsupported}
						}
						return nil, &proofError{failure: proofUnsafe}
					}
					if policy.validateDirectory != nil {
						if err := policy.validateDirectory(after); err != nil {
							_ = dir.Close()
							closeRoots(opened)
							return nil, err
						}
					}
					if err := root.retainEdge(current, part, after, "", false); err != nil {
						_ = dir.Close()
						closeRoots(opened)
						return nil, err
					}
					closeRoots(opened)
					return &pinnedNode{dir: dir, info: after, canonical: filepath.Join(root.path, canonical), mountID: mountID}, nil
				}
				if !info.Mode().IsRegular() {
					closeRoots(opened)
					return nil, &proofError{failure: proofSourceType}
				}
				file, err := current.Open(part)
				if err != nil {
					closeRoots(opened)
					if errors.Is(err, os.ErrPermission) {
						return nil, &proofError{failure: proofUnavailable}
					}
					return nil, &proofError{failure: proofChanged}
				}
				after, err := file.Stat()
				mountID, mountOK := proofMountID(file)
				if err != nil || !os.SameFile(info, after) || !mountOK || mountID != root.mountID {
					_ = file.Close()
					closeRoots(opened)
					if !mountOK {
						return nil, &proofError{failure: proofUnsupported}
					}
					return nil, &proofError{failure: proofUnsafe}
				}
				if err := root.retainEdge(current, part, after, "", false); err != nil {
					_ = file.Close()
					closeRoots(opened)
					return nil, err
				}
				closeRoots(opened)
				return &pinnedNode{file: file, info: after, canonical: filepath.Join(root.path, canonical), mountID: mountID}, nil
			}
			if !info.IsDir() {
				closeRoots(opened)
				return nil, &proofError{failure: proofSourceType}
			}
			nextRoot, err := current.OpenRoot(part)
			if err != nil {
				closeRoots(opened)
				return nil, &proofError{failure: proofChanged}
			}
			after, err := nextRoot.Stat(".")
			mountID, mountOK := mountIDForRoot(nextRoot)
			if err != nil || !os.SameFile(info, after) || !mountOK || mountID != root.mountID {
				_ = nextRoot.Close()
				closeRoots(opened)
				if !mountOK {
					return nil, &proofError{failure: proofUnsupported}
				}
				return nil, &proofError{failure: proofUnsafe}
			}
			if policy.validateDirectory != nil {
				if err := policy.validateDirectory(after); err != nil {
					_ = nextRoot.Close()
					closeRoots(opened)
					return nil, err
				}
			}
			if err := root.retainEdge(current, part, after, "", false); err != nil {
				_ = nextRoot.Close()
				closeRoots(opened)
				return nil, err
			}
			opened = append(opened, nextRoot)
			current = nextRoot
			prefix = canonical
		}
		if !reset {
			return nil, &proofError{failure: proofChanged}
		}
	}
}

func openDirectNode(parent *os.Root, parentMount uint64, canonical, name string) (*pinnedNode, error) {
	info, err := parent.Lstat(name)
	if err != nil {
		return nil, &proofError{failure: proofChanged}
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, &proofError{failure: proofUnsafe}
	}
	if info.IsDir() {
		dir, err := parent.OpenRoot(name)
		if err != nil {
			return nil, &proofError{failure: proofChanged}
		}
		after, err := dir.Stat(".")
		mountID, mountOK := mountIDForRoot(dir)
		if err != nil || !os.SameFile(info, after) || !mountOK || mountID != parentMount {
			_ = dir.Close()
			if !mountOK {
				return nil, &proofError{failure: proofUnsupported}
			}
			return nil, &proofError{failure: proofUnsafe}
		}
		return &pinnedNode{dir: dir, info: after, canonical: canonical, mountID: mountID}, nil
	}
	if !info.Mode().IsRegular() {
		return nil, &proofError{failure: proofUnsafe}
	}
	file, err := parent.Open(name)
	if err != nil {
		return nil, &proofError{failure: proofChanged}
	}
	after, err := file.Stat()
	mountID, mountOK := proofMountID(file)
	if err != nil || !os.SameFile(info, after) || !mountOK || mountID != parentMount {
		_ = file.Close()
		if !mountOK {
			return nil, &proofError{failure: proofUnsupported}
		}
		return nil, &proofError{failure: proofUnsafe}
	}
	return &pinnedNode{file: file, info: after, canonical: canonical, mountID: mountID}, nil
}

func closeRoots(roots []*os.Root) {
	for i := len(roots) - 1; i >= 0; i-- {
		_ = roots[i].Close()
	}
}

// strictAbsoluteTarget converts only raw POSIX absolute targets that are exact
// lexical proper descendants of the retained root. It performs no filesystem
// access or normalization.
func strictAbsoluteTarget(root, target string) (string, bool) {
	if root == "/" || !strings.HasPrefix(root, "/") || strings.HasSuffix(root, "/") {
		return "", false
	}
	for _, part := range strings.Split(root[1:], "/") {
		if part == "" || part == "." || part == ".." {
			return "", false
		}
	}
	prefix := root + "/"
	if !strings.HasPrefix(target, prefix) {
		return "", false
	}
	parts := strings.Split(target[len(prefix):], "/")
	if len(parts) == 0 {
		return "", false
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", false
		}
	}
	return strings.Join(parts, "/"), true
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
