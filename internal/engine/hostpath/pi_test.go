//go:build darwin || linux

package hostpath

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func piSourceFixture(t *testing.T, body string) (home, auth string) {
	t.Helper()
	home = t.TempDir()
	agent := filepath.Join(home, ".pi", "agent")
	if err := os.MkdirAll(agent, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{filepath.Join(home, ".pi"), agent} {
		if err := os.Chmod(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	auth = filepath.Join(agent, "auth.json")
	if err := os.WriteFile(auth, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	oldSleep, oldAfterRead := piOAuthSourceSleep, piOAuthSourceAfterRead
	piOAuthSourceSleep = func(time.Duration) {}
	piOAuthSourceAfterRead = nil
	t.Cleanup(func() {
		piOAuthSourceSleep, piOAuthSourceAfterRead = oldSleep, oldAfterRead
	})
	return home, auth
}

func TestPiOAuthHostPathRetriesMutationAndReplacement(t *testing.T) {
	t.Run("mutation", func(t *testing.T) {
		home, auth := piSourceFixture(t, "old")
		piOAuthSourceAfterRead = func(attempt int) {
			if attempt == 0 {
				mustHostPath(t, os.WriteFile(auth, []byte("new"), 0o600))
			}
		}
		body, status := ReadPiOAuthSource(home)
		if status != PiOAuthSourceOK || string(body) != "new" {
			t.Fatalf("mutation retry = %q/%v, want new/ok", body, status)
		}
	})
	t.Run("replacement", func(t *testing.T) {
		home, auth := piSourceFixture(t, "old")
		piOAuthSourceAfterRead = func(attempt int) {
			if attempt == 0 {
				mustHostPath(t, os.Rename(auth, auth+".old"))
				mustHostPath(t, os.WriteFile(auth, []byte("new"), 0o600))
			}
		}
		body, status := ReadPiOAuthSource(home)
		if status != PiOAuthSourceOK || string(body) != "new" {
			t.Fatalf("replacement retry = %q/%v, want new/ok", body, status)
		}
	})
	t.Run("link-target-change", func(t *testing.T) {
		home, auth := piSourceFixture(t, "old")
		agent := filepath.Dir(auth)
		oldTarget := filepath.Join(agent, "old-auth")
		newTarget := filepath.Join(agent, "new-auth")
		mustHostPath(t, os.Rename(auth, oldTarget))
		mustHostPath(t, os.WriteFile(newTarget, []byte("new"), 0o600))
		mustHostPath(t, os.Symlink("old-auth", auth))
		piOAuthSourceAfterRead = func(attempt int) {
			if attempt == 0 {
				mustHostPath(t, os.Remove(auth))
				mustHostPath(t, os.Symlink("new-auth", auth))
			}
		}
		body, status := ReadPiOAuthSource(home)
		if status != PiOAuthSourceOK || string(body) != "new" {
			t.Fatalf("link retry = %q/%v, want new/ok", body, status)
		}
	})
}

func TestPiOAuthHostPathUsesTenAttemptBudget(t *testing.T) {
	home, auth := piSourceFixture(t, "value-000")
	reads := 0
	piOAuthSourceAfterRead = func(attempt int) {
		reads++
		mustHostPath(t, os.WriteFile(auth, []byte(fmt.Sprintf("value-%03d", attempt+1)), 0o600))
	}
	var sleeps []time.Duration
	piOAuthSourceSleep = func(delay time.Duration) { sleeps = append(sleeps, delay) }
	body, status := ReadPiOAuthSource(home)
	if status != PiOAuthSourceBusy || body != nil {
		t.Fatalf("unstable source = %q/%v, want nil/busy", body, status)
	}
	if reads != 10 || len(sleeps) != 9 {
		t.Fatalf("retry budget reads=%d sleeps=%d, want 10/9", reads, len(sleeps))
	}
	for i, delay := range sleeps {
		if delay != 50*time.Millisecond {
			t.Fatalf("sleep %d = %s, want 50ms", i, delay)
		}
	}
}

func TestPiOAuthHostPathKeepsLockAtLexicalSibling(t *testing.T) {
	home, auth := piSourceFixture(t, "access")
	target := filepath.Join(home, "store", "auth")
	mustHostPath(t, os.MkdirAll(filepath.Dir(target), 0o755))
	mustHostPath(t, os.Rename(auth, target))
	mustHostPath(t, os.Symlink(target, auth))
	lock := auth + ".lock"
	mustHostPath(t, os.Mkdir(lock, 0o700))
	body, status := ReadPiOAuthSource(home)
	if status != PiOAuthSourceBusy || body != nil {
		t.Fatalf("held lexical lock = %q/%v, want nil/busy", body, status)
	}
	if _, err := os.Lstat(lock); err != nil {
		t.Fatalf("proof removed host lock: %v", err)
	}
}

func TestPiOAuthHostPathRejectsUnsafeAncestryLeafAndMount(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, home, auth string)
	}{
		{"group-writable-ancestry", func(t *testing.T, home, _ string) {
			mustHostPath(t, os.Chmod(filepath.Join(home, ".pi"), 0o775))
		}},
		{"other-writable-ancestry", func(t *testing.T, home, _ string) {
			mustHostPath(t, os.Chmod(filepath.Join(home, ".pi", "agent"), 0o707))
		}},
		{"leaf-0644", func(t *testing.T, _, auth string) { mustHostPath(t, os.Chmod(auth, 0o644)) }},
		{"leaf-0700", func(t *testing.T, _, auth string) { mustHostPath(t, os.Chmod(auth, 0o700)) }},
		{"leaf-hardlink", func(t *testing.T, _, auth string) { mustHostPath(t, os.Link(auth, auth+".second")) }},
		{"leaf-oversize", func(t *testing.T, _, auth string) {
			mustHostPath(t, os.WriteFile(auth, make([]byte, piOAuthMaxSourceBytes+1), 0o600))
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			home, auth := piSourceFixture(t, "access")
			tc.setup(t, home, auth)
			body, status := ReadPiOAuthSource(home)
			if status != PiOAuthSourceUnsafe || body != nil {
				t.Fatalf("unsafe source = %q/%v, want nil/unsafe", body, status)
			}
		})
	}

	t.Run("different-mount-instance", func(t *testing.T) {
		home, _ := piSourceFixture(t, "access")
		original := proofMountID
		rootMount, haveRoot := uint64(0), false
		proofMountID = func(file *os.File) (uint64, bool) {
			id, ok := original(file)
			if !ok {
				return 0, false
			}
			if !haveRoot {
				rootMount, haveRoot = id, true
			}
			if strings.HasSuffix(filepath.ToSlash(file.Name()), "/auth.json") {
				return rootMount + 1, true
			}
			return rootMount, true
		}
		t.Cleanup(func() { proofMountID = original })
		body, status := ReadPiOAuthSource(home)
		if status != PiOAuthSourceUnsafe || body != nil {
			t.Fatalf("cross-mount source = %q/%v, want nil/unsafe", body, status)
		}
	})
}

func mustHostPath(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
