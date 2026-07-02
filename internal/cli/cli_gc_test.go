package cli

import (
	"path/filepath"
	"strings"
	"testing"

	engsession "github.com/freakhill/safeslop/internal/engine/session"
)

// TestRecordSessionBackendPersistsDetectedRuntime pins specs/0066 D7: recordSessionBackend fills
// Session.Backend from the detected ambient runtime's Name(). The detection seam is stubbed so the test
// stays hermetic (no real docker/podman/lima probe).
func TestRecordSessionBackendPersistsDetectedRuntime(t *testing.T) {
	store := engsession.NewStore(filepath.Join(t.TempDir(), "sessions"))
	sess, err := store.Create("claude", "container", t.TempDir(), nowForTest(t))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	orig := detectEngineName
	t.Cleanup(func() { detectEngineName = orig })
	detectEngineName = func() string { return "podman" }
	updated, err := recordSessionBackend(store, sess)
	if err != nil {
		t.Fatalf("record backend: %v", err)
	}
	if updated.Backend != "podman" {
		t.Fatalf("backend = %q, want podman", updated.Backend)
	}
	stored, err := store.Get(sess.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if stored.Backend != "podman" {
		t.Fatalf("stored backend = %q, want podman", stored.Backend)
	}
}

func TestRecordSessionBackendPersistsRecipeAnchorsForAdHocContainerSession(t *testing.T) {
	store := engsession.NewStore(filepath.Join(t.TempDir(), "sessions"))
	sess, err := store.Create("claude", "container", t.TempDir(), nowForTest(t))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	orig := detectEngineName
	t.Cleanup(func() { detectEngineName = orig })
	detectEngineName = func() string { return "" } // hermetic: exercise the recipe-anchor path without a runtime probe
	updated, err := recordSessionBackend(store, sess)
	if err != nil {
		t.Fatalf("record backend: %v", err)
	}
	if updated.RecipeID == "" || updated.Image == "" || updated.Resolved == nil {
		t.Fatalf("recipe anchors not persisted: %+v", updated)
	}
	if !strings.HasPrefix(updated.Image, "local/safeslop-tools:") {
		t.Fatalf("image = %q, want managed tools image", updated.Image)
	}
	stored, err := store.Get(sess.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if stored.RecipeID != updated.RecipeID || stored.Image != updated.Image || stored.Resolved == nil {
		t.Fatalf("stored anchors mismatch: stored=%+v updated=%+v", stored, updated)
	}
}

func TestSweepManagedOrphansNoopsWhenDockerUnavailable(t *testing.T) {
	t.Setenv("PATH", "")
	t.Setenv("SAFESLOP_STATE_DIR", filepath.Join(t.TempDir(), "state"))
	if err := sweepManagedOrphans(t.Context()); err != nil {
		t.Fatalf("sweep with no docker should no-op: %v", err)
	}
}

func TestGcHelp(t *testing.T) {
	out, err := runRootForTest(t, t.TempDir(), "gc", "--help")
	if err != nil {
		t.Fatalf("gc --help: %v", err)
	}
	for _, want := range []string{"Garbage-collect", "--keep", "--until"} {
		if !strings.Contains(out, want) {
			t.Fatalf("gc help missing %q:\n%s", want, out)
		}
	}
}
