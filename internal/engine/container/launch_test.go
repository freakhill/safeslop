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
