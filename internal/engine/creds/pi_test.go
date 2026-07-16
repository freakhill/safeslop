package creds

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/freakhill/safeslop/internal/engine/policy"
)

var piOAuthPolicy = &policy.PiCreds{Provider: "openai-codex", Model: "gpt-5.6-luna"}

func piOAuthFixture(t *testing.T, now time.Time, body string) (home, auth string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	agent := filepath.Join(home, ".pi", "agent")
	if err := os.MkdirAll(agent, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(home, ".pi"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(agent, 0o700); err != nil {
		t.Fatal(err)
	}
	auth = filepath.Join(agent, "auth.json")
	if err := os.WriteFile(auth, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	oldNow, oldSleep, oldAfterRead := piOAuthNow, piOAuthSleep, piOAuthAfterRead
	piOAuthNow = func() time.Time { return now }
	piOAuthSleep = func(time.Duration) {}
	piOAuthAfterRead = nil
	t.Cleanup(func() {
		piOAuthNow, piOAuthSleep, piOAuthAfterRead = oldNow, oldSleep, oldAfterRead
	})
	return home, auth
}

func validPiOAuthJSON(expires time.Time, access string) string {
	wire := map[string]any{
		"openai-codex": map[string]any{
			"type": "oauth", "access": access, "refresh": "REFRESH_SENTINEL",
			"accountId": "ACCOUNT_SENTINEL", "expires": expires.UnixMilli(),
		},
		"openrouter": map[string]any{"type": "api_key", "key": "OTHER_PROVIDER_SENTINEL"},
	}
	b, _ := json.Marshal(wire)
	return string(b)
}

func stagedPiAuthPath(stage string) string {
	return filepath.Join(stage, "pi", "openai-codex", "auth.json")
}

func TestStagePiOAuthWritesCanonicalAccessOnlySnapshot(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	source := validPiOAuthJSON(now.Add(2*time.Hour), "ACCESS_CANARY")
	_, auth := piOAuthFixture(t, now, source)
	before, err := os.ReadFile(auth)
	if err != nil {
		t.Fatal(err)
	}
	stage := t.TempDir()
	got, err := StagePiOAuth(piOAuthPolicy, stage)
	if err != nil {
		t.Fatalf("StagePiOAuth: %v", err)
	}
	if !got.ExpiresAt.Equal(now.Add(2 * time.Hour)) {
		t.Fatalf("expires = %s", got.ExpiresAt)
	}
	body, err := os.ReadFile(stagedPiAuthPath(stage))
	if err != nil {
		t.Fatal(err)
	}
	want := "{\"openai-codex\":{\"type\":\"api_key\",\"key\":\"ACCESS_CANARY\"}}\n"
	if string(body) != want {
		t.Fatalf("staged auth = %q, want canonical access-only JSON", body)
	}
	for _, forbidden := range []string{"REFRESH_SENTINEL", "ACCOUNT_SENTINEL", "OTHER_PROVIDER_SENTINEL", auth} {
		if strings.Contains(string(body), forbidden) {
			t.Fatalf("staged auth leaked %q: %s", forbidden, body)
		}
	}
	for _, path := range []string{filepath.Join(stage, "pi"), filepath.Join(stage, "pi", "openai-codex")} {
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o700 {
			t.Fatalf("stage dir %s mode = %v err=%v", path, infoMode(info), err)
		}
	}
	if info, err := os.Stat(stagedPiAuthPath(stage)); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("staged auth mode = %v err=%v", infoMode(info), err)
	}
	after, _ := os.ReadFile(auth)
	if string(after) != string(before) {
		t.Fatal("host Pi auth was modified")
	}
}

func infoMode(info os.FileInfo) os.FileMode {
	if info == nil {
		return 0
	}
	return info.Mode().Perm()
}

func TestStagePiOAuthRejectsUnsafeSourcesValueFree(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	valid := validPiOAuthJSON(now.Add(time.Hour), "ACCESS_CANARY")
	tests := []struct {
		name  string
		setup func(t *testing.T, home, auth string)
		code  string
	}{
		{"file-mode", func(t *testing.T, _, auth string) { must(t, os.Chmod(auth, 0o644)) }, PiOAuthSourceUnsafe},
		{"parent-mode", func(t *testing.T, home, _ string) { must(t, os.Chmod(filepath.Join(home, ".pi"), 0o770)) }, PiOAuthSourceUnsafe},
		{"file-symlink", func(t *testing.T, _, auth string) { must(t, os.Remove(auth)); must(t, os.Symlink("other", auth)) }, PiOAuthSourceUnsafe},
		{"hard-link", func(t *testing.T, _, auth string) { must(t, os.Link(auth, auth+".second")) }, PiOAuthSourceUnsafe},
		{"oversize", func(t *testing.T, _, auth string) { must(t, os.WriteFile(auth, make([]byte, (1<<20)+1), 0o600)) }, PiOAuthSourceUnsafe},
		{"auth-directory", func(t *testing.T, _, auth string) { must(t, os.Remove(auth)); must(t, os.Mkdir(auth, 0o600)) }, PiOAuthSourceUnsafe},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			home, auth := piOAuthFixture(t, now, valid)
			tc.setup(t, home, auth)
			_, err := StagePiOAuth(piOAuthPolicy, t.TempDir())
			if PiOAuthErrorCode(err) != tc.code {
				t.Fatalf("error = %T %v, want %s", err, err, tc.code)
			}
			if strings.Contains(err.Error(), home) || strings.Contains(err.Error(), auth) || strings.Contains(err.Error(), "ACCESS_CANARY") {
				t.Fatalf("error leaked source/value: %v", err)
			}
		})
	}
}

func TestStagePiOAuthRejectsParentSymlink(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	home := t.TempDir()
	t.Setenv("HOME", home)
	real := filepath.Join(home, "real-agent")
	must(t, os.MkdirAll(real, 0o700))
	must(t, os.WriteFile(filepath.Join(real, "auth.json"), []byte(validPiOAuthJSON(now.Add(time.Hour), "ACCESS_CANARY")), 0o600))
	must(t, os.Symlink("real-agent", filepath.Join(home, ".pi")))
	oldNow, oldSleep := piOAuthNow, piOAuthSleep
	piOAuthNow, piOAuthSleep = func() time.Time { return now }, func(time.Duration) {}
	t.Cleanup(func() { piOAuthNow, piOAuthSleep = oldNow, oldSleep })
	_, err := StagePiOAuth(piOAuthPolicy, t.TempDir())
	if PiOAuthErrorCode(err) != PiOAuthSourceUnsafe {
		t.Fatalf("parent symlink error = %v", err)
	}
}

func TestStagePiOAuthLockAndStableRead(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	t.Run("lock-held", func(t *testing.T) {
		_, auth := piOAuthFixture(t, now, validPiOAuthJSON(now.Add(time.Hour), "ACCESS_CANARY"))
		must(t, os.Mkdir(auth+".lock", 0o700))
		_, err := StagePiOAuth(piOAuthPolicy, t.TempDir())
		if PiOAuthErrorCode(err) != PiOAuthSourceBusy {
			t.Fatalf("lock error = %v", err)
		}
		if _, statErr := os.Stat(auth + ".lock"); statErr != nil {
			t.Fatalf("safeslop removed Pi lock: %v", statErr)
		}
	})
	t.Run("mutation-retries", func(t *testing.T) {
		_, auth := piOAuthFixture(t, now, validPiOAuthJSON(now.Add(time.Hour), "ACCESS_OLD"))
		piOAuthAfterRead = func(attempt int) {
			if attempt == 0 {
				must(t, os.WriteFile(auth, []byte(validPiOAuthJSON(now.Add(time.Hour), "ACCESS_NEW")), 0o600))
			}
		}
		stage := t.TempDir()
		if _, err := StagePiOAuth(piOAuthPolicy, stage); err != nil {
			t.Fatal(err)
		}
		body, _ := os.ReadFile(stagedPiAuthPath(stage))
		if !strings.Contains(string(body), "ACCESS_NEW") || strings.Contains(string(body), "ACCESS_OLD") {
			t.Fatalf("stable retry staged wrong bytes: %s", body)
		}
	})
	t.Run("replacement-retries", func(t *testing.T) {
		_, auth := piOAuthFixture(t, now, validPiOAuthJSON(now.Add(time.Hour), "ACCESS_OLD"))
		piOAuthAfterRead = func(attempt int) {
			if attempt == 0 {
				must(t, os.Rename(auth, auth+".replaced"))
				must(t, os.WriteFile(auth, []byte(validPiOAuthJSON(now.Add(time.Hour), "ACCESS_NEW")), 0o600))
			}
		}
		stage := t.TempDir()
		if _, err := StagePiOAuth(piOAuthPolicy, stage); err != nil {
			t.Fatal(err)
		}
		body, _ := os.ReadFile(stagedPiAuthPath(stage))
		if !strings.Contains(string(body), "ACCESS_NEW") || strings.Contains(string(body), "ACCESS_OLD") {
			t.Fatalf("replacement retry staged wrong bytes: %s", body)
		}
	})
}

func TestStagePiOAuthUsesLockedTenAttemptRetryBudget(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	_, auth := piOAuthFixture(t, now, validPiOAuthJSON(now.Add(time.Hour), "ACCESS_000"))
	reads := 0
	piOAuthAfterRead = func(attempt int) {
		reads++
		must(t, os.WriteFile(auth, []byte(validPiOAuthJSON(now.Add(time.Hour), fmt.Sprintf("ACCESS_%03d", attempt+1))), 0o600))
	}
	var sleeps []time.Duration
	piOAuthSleep = func(d time.Duration) { sleeps = append(sleeps, d) }
	_, err := StagePiOAuth(piOAuthPolicy, t.TempDir())
	if PiOAuthErrorCode(err) != PiOAuthSourceBusy {
		t.Fatalf("unstable source error = %v", err)
	}
	if reads != 10 || len(sleeps) != 9 {
		t.Fatalf("retry budget reads=%d sleeps=%d, want 10/9", reads, len(sleeps))
	}
	for i, d := range sleeps {
		if d != 50*time.Millisecond {
			t.Fatalf("sleep %d = %s, want 50ms", i, d)
		}
	}
}

func TestStagePiOAuthRejectsMalformedAndExpiryBoundaries(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name, body, code string
	}{
		{"missing-provider", `{"openrouter":{"type":"api_key","key":"x"}}`, PiOAuthProviderMissing},
		{"wrong-type", `{"openai-codex":{"type":"api_key","key":"x"}}`, PiOAuthAuthTypeUnsupported},
		{"duplicate-key", `{"openai-codex":{"type":"oauth","access":"A","access":"B","expires":9999999999999}}`, PiOAuthSourceMalformed},
		{"trailing", validPiOAuthJSON(now.Add(time.Hour), "ACCESS") + `{}`, PiOAuthSourceMalformed},
		{"whitespace-access", validPiOAuthJSON(now.Add(time.Hour), "ACCESS BAD"), PiOAuthSourceMalformed},
		{"control-access", validPiOAuthJSON(now.Add(time.Hour), "ACCESS\x00BAD"), PiOAuthSourceMalformed},
		{"non-ascii-access", validPiOAuthJSON(now.Add(time.Hour), "ACCESS-é"), PiOAuthSourceMalformed},
		{"oversize-access", validPiOAuthJSON(now.Add(time.Hour), strings.Repeat("A", 64*1024+1)), PiOAuthSourceMalformed},
		{"expired", validPiOAuthJSON(now, "ACCESS"), PiOAuthExpired},
		{"exact-headroom", validPiOAuthJSON(now.Add(15*time.Minute), "ACCESS"), PiOAuthNearExpiry},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			piOAuthFixture(t, now, tc.body)
			_, err := StagePiOAuth(piOAuthPolicy, t.TempDir())
			if PiOAuthErrorCode(err) != tc.code {
				t.Fatalf("error = %T %v, want %s", err, err, tc.code)
			}
		})
	}
	t.Run("one-millisecond-over-headroom", func(t *testing.T) {
		piOAuthFixture(t, now, validPiOAuthJSON(now.Add(15*time.Minute+time.Millisecond), "ACCESS"))
		if _, err := StagePiOAuth(piOAuthPolicy, t.TempDir()); err != nil {
			t.Fatalf("headroom+1ms rejected: %v", err)
		}
	})
}

func TestStagePiOAuthRechecksHeadroomBeforeWrite(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	piOAuthFixture(t, now, validPiOAuthJSON(now.Add(20*time.Minute), "ACCESS"))
	calls := 0
	piOAuthNow = func() time.Time {
		calls++
		if calls == 1 {
			return now
		}
		return now.Add(10 * time.Minute)
	}
	stage := t.TempDir()
	_, err := StagePiOAuth(piOAuthPolicy, stage)
	if PiOAuthErrorCode(err) != PiOAuthNearExpiry {
		t.Fatalf("second headroom check error = %v", err)
	}
	if calls < 2 {
		t.Fatalf("clock calls = %d, want at least two", calls)
	}
	if _, statErr := os.Stat(stagedPiAuthPath(stage)); !os.IsNotExist(statErr) {
		t.Fatalf("near-expiry second check wrote auth: %v", statErr)
	}
}

func TestStagePiOAuthMissingAndStageFailure(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	t.Run("missing", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		oldNow, oldSleep := piOAuthNow, piOAuthSleep
		piOAuthNow, piOAuthSleep = func() time.Time { return now }, func(time.Duration) {}
		t.Cleanup(func() { piOAuthNow, piOAuthSleep = oldNow, oldSleep })
		_, err := StagePiOAuth(piOAuthPolicy, t.TempDir())
		if PiOAuthErrorCode(err) != PiOAuthSourceMissing {
			t.Fatalf("missing error = %v", err)
		}
	})
	t.Run("stage-path-is-file", func(t *testing.T) {
		piOAuthFixture(t, now, validPiOAuthJSON(now.Add(time.Hour), "ACCESS"))
		stage := filepath.Join(t.TempDir(), "not-a-dir")
		must(t, os.WriteFile(stage, []byte("x"), 0o600))
		_, err := StagePiOAuth(piOAuthPolicy, stage)
		if PiOAuthErrorCode(err) != PiOAuthStageFailed {
			t.Fatalf("stage failure = %v", err)
		}
	})
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
