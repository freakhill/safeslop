package hostenv

import (
	"io/fs"
	"reflect"
	"testing"
)

// statAll builds a statFunc that returns a fixed mode for every path, and never errors.
func statAll(mode fs.FileMode) statFunc {
	return func(string) (fs.FileMode, error) { return mode, nil }
}

// statMap returns per-path modes; paths absent from the map report a not-exist error.
func statMap(m map[string]fs.FileMode) statFunc {
	return func(p string) (fs.FileMode, error) {
		if mode, ok := m[p]; ok {
			return mode, nil
		}
		return 0, fs.ErrNotExist
	}
}

func TestIsGUIMinimal(t *testing.T) {
	cases := []struct {
		name    string
		environ []string
		want    bool
	}{
		{"finder stripped (no SHELL, no brew)", []string{"PATH=/usr/bin:/bin"}, true},
		{"shell set but PATH lacks brew", []string{"SHELL=/bin/zsh", "PATH=/usr/bin:/bin"}, true},
		{"no shell but brew on PATH", []string{"PATH=/opt/homebrew/bin:/usr/bin"}, true},
		{"rich terminal env", []string{"SHELL=/bin/zsh", "PATH=/opt/homebrew/bin:/usr/bin:/bin"}, false},
		{"rich intel terminal env", []string{"SHELL=/bin/bash", "PATH=/usr/local/bin:/usr/bin"}, false},
	}
	for _, c := range cases {
		if got := isGUIMinimal(c.environ); got != c.want {
			t.Errorf("%s: isGUIMinimal=%v want %v", c.name, got, c.want)
		}
	}
}

func TestParseMarkerEnv(t *testing.T) {
	const marker = "SAFESLOP-7f3a"
	out := "neofetch banner line\n" +
		"another noise line: tip = run foo\n" +
		marker + "\n" +
		"PATH=/opt/homebrew/bin:/usr/bin\n" +
		"HOME=/Users/jojo\n" +
		"FOO=bar\n" +
		"this is a continuation of foo\n" + // no '=' → folds into FOO
		"EMPTY=\n" +
		marker + "\n" +
		"trailing MOTD garbage\n"
	got, err := parseMarkerEnv(out, marker)
	if err != nil {
		t.Fatalf("parseMarkerEnv error: %v", err)
	}
	want := map[string]string{
		"PATH":  "/opt/homebrew/bin:/usr/bin",
		"HOME":  "/Users/jojo",
		"FOO":   "bar\nthis is a continuation of foo",
		"EMPTY": "",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseMarkerEnv\n got=%#v\nwant=%#v", got, want)
	}
}

func TestParseMarkerEnvMissingMarkers(t *testing.T) {
	if _, err := parseMarkerEnv("no markers here\nPATH=/x\n", "SAFESLOP-zzz"); err == nil {
		t.Error("expected error when markers are absent (so caller can fall back)")
	}
}

func TestFilterPATH(t *testing.T) {
	in := "/opt/homebrew/bin:relative/dir::/has/../dotdot:/tmp/ww:/usr/bin:/opt/homebrew/bin"
	stat := statMap(map[string]fs.FileMode{
		"/opt/homebrew/bin": 0o755,
		"/tmp/ww":           0o777, // world-writable → rejected
		"/usr/bin":          0o755,
	})
	got := filterPATH(in, stat)
	want := "/opt/homebrew/bin:/usr/bin" // drop relative, empty, '..', world-writable; dedupe
	if got != want {
		t.Errorf("filterPATH=%q want %q", got, want)
	}
}

func TestFilterPATHKeepsAbsentDirs(t *testing.T) {
	// An absolute, ..-free dir that does not exist is harmless for LookPath — keep it.
	in := "/opt/homebrew/bin:/does/not/exist/yet"
	got := filterPATH(in, statMap(map[string]fs.FileMode{"/opt/homebrew/bin": 0o755}))
	want := "/opt/homebrew/bin:/does/not/exist/yet"
	if got != want {
		t.Errorf("filterPATH=%q want %q", got, want)
	}
}

func TestSanitize(t *testing.T) {
	in := map[string]string{
		"DYLD_INSERT_LIBRARIES": "/evil.dylib",
		"DYLD_LIBRARY_PATH":     "/x",
		"LD_PRELOAD":            "/y",
		"MULTI":                 "line1\nline2", // multiline → dropped
		"NUL":                   "a\x00b",       // NUL → dropped
		"FOO":                   "bar",
		"PATH":                  "/opt/homebrew/bin:/tmp/ww:/usr/bin",
	}
	stat := statMap(map[string]fs.FileMode{
		"/opt/homebrew/bin": 0o755,
		"/tmp/ww":           0o777,
		"/usr/bin":          0o755,
	})
	got := sanitize(in, stat)
	want := map[string]string{
		"FOO":  "bar",
		"PATH": "/opt/homebrew/bin:/usr/bin",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("sanitize\n got=%#v\nwant=%#v", got, want)
	}
}

func TestHardcodedDirs(t *testing.T) {
	dirs := hardcodedDirs("/Users/jojo")
	must := []string{
		"/opt/homebrew/bin",
		"/usr/local/bin",
		"/Users/jojo/.local/bin",
		"/Users/jojo/.cargo/bin",
		"/Users/jojo/.local/share/mise/shims",
		"/Users/jojo/.asdf/shims",
	}
	have := map[string]bool{}
	for _, d := range dirs {
		have[d] = true
	}
	for _, m := range must {
		if !have[m] {
			t.Errorf("hardcodedDirs missing %q (got %v)", m, dirs)
		}
	}
}

func TestShellArgvUsesSeparateFlags(t *testing.T) {
	// Combined "-ilc" breaks fish's getopt parsing; separate flags work across zsh/bash/fish.
	got := shellArgv("INNER")
	want := []string{"-l", "-i", "-c", "INNER"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("shellArgv=%v want %v", got, want)
	}
}

func TestIsFishShell(t *testing.T) {
	if !isFishShell("/opt/homebrew/bin/fish") {
		t.Error("expected /opt/homebrew/bin/fish to be detected as fish")
	}
	if isFishShell("/bin/zsh") {
		t.Error("zsh misdetected as fish")
	}
}
