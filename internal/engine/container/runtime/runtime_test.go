package runtime

import (
	"context"
	"strings"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/install"
)

func TestSelectFallsToLimaWithoutDocker(t *testing.T) {
	t.Setenv("PATH", "") // no docker resolvable
	if b := Select(false, install.Dirs{}); b.Name() != "lima" {
		t.Fatalf("with no ambient docker, Select(false) should pick lima, got %q", b.Name())
	}
	if b := Select(true, install.Dirs{}); b.Name() != "lima" {
		t.Fatalf("Select(true) must always pick lima, got %q", b.Name())
	}
}

func TestSystemBackendErrsWithoutDocker(t *testing.T) {
	t.Setenv("PATH", "")
	_, err := (&SystemBackend{}).Ensure(context.Background(), nil)
	if err == nil {
		t.Fatal("SystemBackend.Ensure must error when no docker is on PATH")
	}
}

func TestSystemBackendOwnsNothing(t *testing.T) {
	s := &SystemBackend{}
	if s.Pins() != nil {
		t.Fatal("SystemBackend must declare no pins (it manages nothing)")
	}
	if s.StateDir() != "" {
		t.Fatal("SystemBackend must own no state dir")
	}
}

// TestEnginePinsAreFailClosed runs the install fail-closed validator over LimaBackend's on-demand pins,
// so the verified engine blobs hold the same fully-pinned contract as install.DesiredState().
func TestEnginePinsAreFailClosed(t *testing.T) {
	pins := NewLimaBackend(install.Dirs{}).Pins()
	if len(pins) < 2 {
		t.Fatalf("expected the engine bundle + cosign pins, got %d", len(pins))
	}
	if err := install.ValidateDesired(pins); err != nil {
		t.Fatalf("LimaBackend engine pins must be fail-closed valid: %v", err)
	}
	for _, p := range pins {
		if p.Format != install.FormatBlob {
			t.Errorf("engine pin %q must be FormatBlob (non-executable guest artifact), got %q", p.Name, p.Format)
		}
	}
}

func TestLimaStateDirUnderSafeslop(t *testing.T) {
	sd := NewLimaBackend(install.Dirs{}).StateDir()
	if !strings.HasSuffix(sd, "/.config/safeslop/lima") {
		t.Fatalf("lima state dir must be safeslop-owned, got %q", sd)
	}
}

func TestLimaEnsureFailsLoudWithoutLimactl(t *testing.T) {
	// BinDir with no limactl → Ensure must error, never silently claim a runtime.
	_, err := NewLimaBackend(install.Dirs{BinDir: t.TempDir()}).Ensure(context.Background(), nil)
	if err == nil {
		t.Fatal("lima Ensure must error when limactl is absent")
	}
}
