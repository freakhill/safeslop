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
	t        *testing.T
	outputs  map[string]string
	failures map[string]int
	onRun    map[string]func()
	mu       sync.Mutex
	runs     []string
}

func composeCommandKey(t testing.TB, composeFile string, args ...string) string {
	t.Helper()
	argv, err := composeProjectArgs(composeFile, args...)
	if err != nil {
		t.Fatalf("composeProjectArgs: %v", err)
	}
	return strings.Join(argv, " ")
}

func newFakeEngine(t *testing.T, outputs map[string]string) *fakeEngine {
	t.Helper()
	return &fakeEngine{t: t, outputs: outputs, failures: map[string]int{}, onRun: map[string]func(){}}
}

func (f *fakeEngine) fail(key string, code int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failures[key] = code
}

func (f *fakeEngine) runHook(key string, hook func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.onRun[key] = hook
}

func (f *fakeEngine) Name() string { return "fake" }

func (f *fakeEngine) Argv(args ...string) []string { return append([]string{"fake-engine"}, args...) }

func (f *fakeEngine) InternalNetwork() string { return "" }

func (f *fakeEngine) Command(ctx context.Context, args ...string) *exec.Cmd {
	key := strings.Join(args, " ")
	f.mu.Lock()
	f.runs = append(f.runs, key)
	code := f.failures[key]
	hook := f.onRun[key]
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	out := f.outputs[key]
	return fakeCommand(ctx, out, code)
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

func fakeCommand(ctx context.Context, stdout string, exitCode int) *exec.Cmd {
	script := filepath.Join(os.TempDir(), "safeslop-fake-engine-helper.sh")
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestFakeEngineHelper", "--", script)
	cmd.Env = append(os.Environ(), "SAFESLOP_FAKE_ENGINE_HELPER=1", "SAFESLOP_FAKE_ENGINE_STDOUT="+stdout, fmt.Sprintf("SAFESLOP_FAKE_ENGINE_EXIT=%d", exitCode))
	return cmd
}

func TestFakeEngineHelper(t *testing.T) {
	if os.Getenv("SAFESLOP_FAKE_ENGINE_HELPER") != "1" {
		return
	}
	fmt.Print(os.Getenv("SAFESLOP_FAKE_ENGINE_STDOUT"))
	if os.Getenv("SAFESLOP_FAKE_ENGINE_EXIT") != "" {
		var code int
		_, _ = fmt.Sscanf(os.Getenv("SAFESLOP_FAKE_ENGINE_EXIT"), "%d", &code)
		os.Exit(code)
	}
	os.Exit(0)
}
