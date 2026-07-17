// Package workspace resolves the single intentionally writable host directory
// carried by a launch. It is deliberately separate from hostpath: workspace is
// pathname authority selected by policy, while hostpath proves read-only files
// through retained descriptors.
package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	ErrInvalid     = errors.New("workspace path is invalid")
	ErrUnavailable = errors.New("workspace is not an existing directory")
	ErrOverlap     = errors.New("workspace and runtime stage overlap")
)

// Candidate applies origin semantics and returns an absolute clean pathname.
// It does not require the target to exist; readiness evaluation uses it to
// classify a missing workspace without changing how launch later resolves it.
func Candidate(raw, policyPath, invocationDir string) (string, error) {
	if err := validateText(raw); err != nil {
		return "", err
	}
	if invocationDir == "" {
		var err error
		invocationDir, err = os.Getwd()
		if err != nil {
			return "", ErrUnavailable
		}
	}
	if err := validateText(invocationDir); err != nil {
		return "", err
	}
	base := invocationDir
	path := raw
	if path == "" {
		path = invocationDir
	} else if !filepath.IsAbs(path) && policyPath != "" {
		if err := validateText(policyPath); err != nil {
			return "", err
		}
		base = filepath.Dir(policyPath)
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(base, path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", ErrInvalid
	}
	if err := validateText(abs); err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

// Resolve applies policy-relative/invocation-relative origin semantics, requires
// an existing directory, and returns its absolute symlink-free pathname.
func Resolve(raw, policyPath, invocationDir string) (string, error) {
	candidate, err := Candidate(raw, policyPath, invocationDir)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(candidate)
	if err != nil || !info.IsDir() {
		return "", ErrUnavailable
	}
	real, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", ErrUnavailable
	}
	if !filepath.IsAbs(real) {
		return "", ErrInvalid
	}
	if err := validateText(real); err != nil {
		return "", err
	}
	info, err = os.Stat(real)
	if err != nil || !info.IsDir() {
		return "", ErrUnavailable
	}
	return filepath.Clean(real), nil
}

// ResolveAbsolute revalidates an already-resolved launch boundary immediately
// before materialization. Relative values fail rather than being silently
// rebased against the container engine's working directory.
func ResolveAbsolute(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", ErrInvalid
	}
	return Resolve(path, "", string(filepath.Separator))
}

// RequireDisjoint rejects either tree containing the other. A workspace that
// contains the credential/proxy stage would make the nominally read-only stage
// reachable through /workspace; a workspace inside the stage aliases it.
func RequireDisjoint(workspacePath, stagePath string) error {
	workspacePath, err := ResolveAbsolute(workspacePath)
	if err != nil {
		return err
	}
	stagePath, err = ResolveAbsolute(stagePath)
	if err != nil {
		return err
	}
	return RequireDisjointPaths(workspacePath, stagePath)
}

// RequireDisjointPaths performs the same containment proof before a stage is
// created, so credential staging can reject an alias before writing any bearer
// files. Callers pass already-origin-resolved absolute paths; final launch still
// revalidates both existing directories with RequireDisjoint.
func RequireDisjointPaths(workspacePath, stagePath string) error {
	if !filepath.IsAbs(workspacePath) || !filepath.IsAbs(stagePath) {
		return ErrInvalid
	}
	if err := validateText(workspacePath); err != nil {
		return err
	}
	if err := validateText(stagePath); err != nil {
		return err
	}
	workspacePath = canonicalizeExistingPrefix(filepath.Clean(workspacePath))
	stagePath = canonicalizeExistingPrefix(filepath.Clean(stagePath))
	if contains(workspacePath, stagePath) || contains(stagePath, workspacePath) {
		return ErrOverlap
	}
	return nil
}

func canonicalizeExistingPrefix(path string) string {
	current := path
	var suffix []string
	for {
		if real, err := filepath.EvalSymlinks(current); err == nil {
			for i := len(suffix) - 1; i >= 0; i-- {
				real = filepath.Join(real, suffix[i])
			}
			return filepath.Clean(real)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return path
		}
		suffix = append(suffix, filepath.Base(current))
		current = parent
	}
}

func contains(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func validateText(path string) error {
	if !utf8.ValidString(path) {
		return ErrInvalid
	}
	for _, r := range path {
		if r == 0 || unicode.In(r, unicode.Cc, unicode.Cf, unicode.Zl, unicode.Zp) {
			return fmt.Errorf("%w: control or format character", ErrInvalid)
		}
	}
	return nil
}
