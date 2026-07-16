package container

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/policy"
)

func requireProjectionCode(t *testing.T, err error, want string) {
	t.Helper()
	var projectionErr *ProjectionError
	if !errors.As(err, &projectionErr) {
		t.Fatalf("error %v is not a ProjectionError", err)
	}
	if got := projectionErr.Failure().Code; got != want {
		t.Fatalf("projection code = %q, want %q", got, want)
	}
}

func TestProjectionContainerFacadeSnapshotsThroughSharedProof(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".zshrc"), []byte("facade-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	stage := t.TempDir()
	manifest, err := SnapshotProjection(home, stage, policy.Projection{Items: []policy.ProjectionItem{{
		Source: "~/.zshrc", Label: "zsh",
	}}})
	if err != nil {
		t.Fatal(err)
	}
	mounts := manifest.PresentMounts()
	if len(mounts) != 1 || mounts[0].Target != ".zshrc" || mounts[0].Status != projPresent {
		t.Fatalf("container projection facade manifest = %+v", manifest.Items)
	}
	if !strings.HasPrefix(mounts[0].Host, filepath.Join(stage, "projection-snapshots")+string(filepath.Separator)) {
		t.Fatalf("container facade returned live source: %q", mounts[0].Host)
	}
}

func TestProjectionContainerFacadePreservesTypedFailure(t *testing.T) {
	home := t.TempDir()
	_, err := SnapshotProjection(home, t.TempDir(), policy.Projection{Items: []policy.ProjectionItem{{
		Source: "/etc/passwd", Optional: func() *bool { value := false; return &value }(),
	}}})
	var projectionErr *ProjectionError
	if !errors.As(err, &projectionErr) {
		t.Fatalf("container facade error = %T %v", err, err)
	}
	if projectionErr.Failure().Code != ProjectionTargetOutsideRoot || strings.Contains(err.Error(), home) {
		t.Fatalf("container facade changed value-free failure: %+v", projectionErr.Failure())
	}
}
