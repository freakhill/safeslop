package container

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type fakeEngine struct {
	t       *testing.T
	outputs map[string]string
	mu      sync.Mutex
	runs    []string
}

func newFakeEngine(t *testing.T, outputs map[string]string) *fakeEngine {
	t.Helper()
	return &fakeEngine{t: t, outputs: outputs}
}

func (f *fakeEngine) Name() string { return "fake" }

func (f *fakeEngine) Argv(args ...string) []string { return append([]string{"fake-engine"}, args...) }

func (f *fakeEngine) InternalNetwork() string { return "" }

func (f *fakeEngine) Command(ctx context.Context, args ...string) *exec.Cmd {
	key := strings.Join(args, " ")
	f.mu.Lock()
	f.runs = append(f.runs, key)
	f.mu.Unlock()
	out := f.outputs[key]
	return fakeCommand(ctx, out)
}

func (f *fakeEngine) assertRan(t *testing.T, want string) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, got := range f.runs {
		if got == want {
			return
		}
	}
	t.Fatalf("engine command %q not run; ran: %v", want, f.runs)
}

func (f *fakeEngine) assertNotRan(t *testing.T, unwanted string) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, got := range f.runs {
		if got == unwanted || strings.HasPrefix(got, unwanted+" ") {
			t.Fatalf("engine command %q unexpectedly run; ran: %v", unwanted, f.runs)
		}
	}
}

func fakeCommand(ctx context.Context, stdout string) *exec.Cmd {
	script := filepath.Join(os.TempDir(), "safeslop-fake-engine-helper.sh")
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestFakeEngineHelper", "--", script)
	cmd.Env = append(os.Environ(), "SAFESLOP_FAKE_ENGINE_HELPER=1", "SAFESLOP_FAKE_ENGINE_STDOUT="+stdout)
	return cmd
}

func TestFakeEngineHelper(t *testing.T) {
	if os.Getenv("SAFESLOP_FAKE_ENGINE_HELPER") != "1" {
		return
	}
	fmt.Print(os.Getenv("SAFESLOP_FAKE_ENGINE_STDOUT"))
	os.Exit(0)
}
