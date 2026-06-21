package hostenv

import (
	"errors"
	"strings"
	"testing"
)

func TestParsePathHelper(t *testing.T) {
	out := `PATH="/usr/bin:/bin:/usr/sbin:/sbin"; export PATH;
MANPATH="/usr/share/man"; export MANPATH;`
	if got := parsePathHelper(out); got != "/usr/bin:/bin:/usr/sbin:/sbin" {
		t.Errorf("parsePathHelper=%q", got)
	}
	if got := parsePathHelper("no path here"); got != "" {
		t.Errorf("parsePathHelper(garbage)=%q want empty", got)
	}
}

func TestMergePATH(t *testing.T) {
	got := mergePATH("/opt/homebrew/bin:/usr/bin", "/usr/bin:/usr/local/bin:/opt/homebrew/bin")
	want := "/opt/homebrew/bin:/usr/bin:/usr/local/bin" // primary order kept, new fallback dirs appended, deduped
	if got != want {
		t.Errorf("mergePATH=%q want %q", got, want)
	}
}

func TestEnvLookPath(t *testing.T) {
	e := &Env{
		vars:   map[string]string{"PATH": "/opt/homebrew/bin:/usr/bin"},
		isExec: func(p string) bool { return p == "/opt/homebrew/bin/claude" || p == "/usr/bin/git" },
	}
	if got, ok := e.LookPath("claude"); !ok || got != "/opt/homebrew/bin/claude" {
		t.Errorf("LookPath(claude)=%q,%v", got, ok)
	}
	if got, ok := e.LookPath("git"); !ok || got != "/usr/bin/git" {
		t.Errorf("LookPath(git)=%q,%v", got, ok)
	}
	if _, ok := e.LookPath("nonesuch"); ok {
		t.Error("LookPath(nonesuch) should miss")
	}
	// an explicit path is checked directly, not searched on PATH
	if _, ok := e.LookPath("/usr/bin/git"); !ok {
		t.Error("LookPath of an absolute existing exec should hit")
	}
}

// fakeReconstructor builds a reconstructor with controllable seams and a runShell call counter.
func fakeReconstructor(environ []string, shellOut string, shellErr error, key *string, calls *int) *reconstructor {
	return &reconstructor{
		environ:      func() []string { return environ },
		homeDir:      func() (string, error) { return "/Users/test", nil },
		username:     func() string { return "test" },
		resolveShell: func(string) (string, error) { return "/bin/zsh", nil },
		newMarker:    func() string { return "MARK" },
		runShell: func(string, []string) (string, error) {
			*calls++
			return shellOut, shellErr
		},
		pathHelper: func() string { return "" },
		stat:       statAll(0o755),
		mtimeKey:   func(string) string { return *key },
		isExec:     func(string) bool { return true },
	}
}

func TestReconstructRichEnvUsesCurrent(t *testing.T) {
	calls := 0
	key := "k"
	r := fakeReconstructor(
		[]string{"SHELL=/bin/zsh", "PATH=/opt/homebrew/bin:/usr/bin", "FOO=bar"},
		"", nil, &key, &calls)
	env := r.get()
	if env.Source != "current" {
		t.Errorf("Source=%q want current", env.Source)
	}
	if calls != 0 {
		t.Errorf("rich env must not spawn a shell (calls=%d)", calls)
	}
	if v, _ := env.Get("FOO"); v != "bar" {
		t.Errorf("current env should carry FOO, got %q", v)
	}
}

func TestReconstructMinimalCaptureSuccess(t *testing.T) {
	calls := 0
	key := "k"
	out := "MARK\n" +
		"PATH=/opt/homebrew/bin:/usr/bin\n" +
		"HOME=/Users/test\n" +
		"AWS_SECRET_ACCESS_KEY=shhh\n" +
		"MARK\n"
	r := fakeReconstructor([]string{"PATH=/usr/bin:/bin"}, out, nil, &key, &calls)
	env := r.get()
	if calls != 1 {
		t.Fatalf("expected exactly one shell capture, got %d", calls)
	}
	if env.Source != "shell:zsh" {
		t.Errorf("Source=%q want shell:zsh", env.Source)
	}
	if !strings.Contains(env.PATH(), "/opt/homebrew/bin") {
		t.Errorf("PATH missing captured brew dir: %q", env.PATH())
	}
	if !strings.Contains(env.PATH(), "/usr/local/bin") {
		t.Errorf("PATH should also include the fallback floor (/usr/local/bin): %q", env.PATH())
	}
	// The reconstructed env is intentionally RICH (it is the host_discovery_env). The firewall that
	// keeps these out of the sandbox lives in cli.childEnv, not here.
	if v, ok := env.Get("AWS_SECRET_ACCESS_KEY"); !ok || v != "shhh" {
		t.Errorf("host_discovery_env should carry the captured credential (got %q,%v)", v, ok)
	}
}

func TestReconstructMinimalCaptureFailFallsBack(t *testing.T) {
	calls := 0
	key := "k"
	r := fakeReconstructor([]string{"PATH=/usr/bin:/bin"}, "", errors.New("shell exploded"), &key, &calls)
	env := r.get()
	if env.Source != "fallback" {
		t.Errorf("Source=%q want fallback", env.Source)
	}
	if !strings.Contains(env.PATH(), "/opt/homebrew/bin") {
		t.Errorf("fallback PATH must include hardcoded brew dir: %q", env.PATH())
	}
}

func TestReconstructCachesUntilMtimeChanges(t *testing.T) {
	calls := 0
	key := "k1"
	out := "MARK\nPATH=/opt/homebrew/bin:/usr/bin\nMARK\n"
	r := fakeReconstructor([]string{"PATH=/usr/bin:/bin"}, out, nil, &key, &calls)

	r.get()
	r.get()
	if calls != 1 {
		t.Fatalf("second get with unchanged mtime must use cache (calls=%d)", calls)
	}
	key = "k2" // simulate an rc-file edit
	r.get()
	if calls != 2 {
		t.Fatalf("changed mtime must invalidate the cache (calls=%d)", calls)
	}
}
