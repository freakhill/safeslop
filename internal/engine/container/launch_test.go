package container

import (
	"context"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/exec"
)

func TestLaunchRejectsWhenUnavailable(t *testing.T) {
	t.Setenv("PATH", "")
	_, err := Launch(context.Background(), exec.LaunchSpec{Argv: []string{"fish"}}, t.TempDir(), "deny", nil, t.TempDir())
	if err == nil {
		t.Fatal("expected error when docker unavailable")
	}
}

func TestPrepareSessionRejectsWhenUnavailable(t *testing.T) {
	t.Setenv("PATH", "")
	_, cleanup, err := PrepareSession(context.Background(), []string{"fish"}, t.TempDir(), "deny", nil, t.TempDir())
	if err == nil {
		t.Fatal("expected error when docker unavailable")
	}
	if cleanup == nil {
		t.Fatal("cleanup must never be nil")
	}
	cleanup() // must be safe to call on the error path
}
