package container

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSessionGrantApplyWritesOverlayAndReloadsProxy(t *testing.T) {
	dir := t.TempDir()
	composeFile := filepath.Join(dir, "compose.yml")
	if err := os.WriteFile(composeFile, []byte("services: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "session-grants.conf"), []byte(RenderSessionGrants(nil)), 0o600); err != nil {
		t.Fatal(err)
	}
	eng := newFakeEngine(t, nil)

	if err := ApplySessionGrants(context.Background(), eng, composeFile, dir, []SessionGrant{{Host: "example.com", Port: 443}}); err != nil {
		t.Fatalf("ApplySessionGrants: %v", err)
	}
	eng.assertRan(t, composeCommandKey(t, composeFile, "exec", "-T", "proxy", "squid", "-k", "reconfigure"))
	b, err := os.ReadFile(filepath.Join(dir, "session-grants.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "grant_0_host dstdom_regex -n ^example\\.com$") || !strings.Contains(string(b), "grant_0_port port 443") {
		t.Fatalf("overlay did not contain rendered grant:\n%s", b)
	}
}

func TestSessionGrantApplyRetriesTransientReloadBeforeRollback(t *testing.T) {
	dir := t.TempDir()
	composeFile := filepath.Join(dir, "compose.yml")
	if err := os.WriteFile(composeFile, []byte("services: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := RenderSessionGrants(nil)
	if err := os.WriteFile(filepath.Join(dir, "session-grants.conf"), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	eng := newFakeEngine(t, nil)
	key := composeCommandKey(t, composeFile, "exec", "-T", "proxy", "squid", "-k", "reconfigure")
	eng.fail(key, 42)
	calls := 0
	eng.runHook(key, func() {
		calls++
		if calls == 1 {
			eng.fail(key, 0)
		}
	})

	err := ApplySessionGrants(context.Background(), eng, composeFile, dir, []SessionGrant{{Host: "example.com", Port: 443}})
	if err != nil {
		t.Fatalf("transient bind-visibility reload must retry: %v", err)
	}
	if calls != 2 {
		t.Fatalf("reconfigure calls = %d, want 2", calls)
	}
	body, err := os.ReadFile(filepath.Join(dir, "session-grants.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "^example\\.com$") {
		t.Fatalf("transient reload restored old overlay: %s", body)
	}
}

func TestSessionGrantApplyFailClosedRestoresPreviousOverlay(t *testing.T) {
	dir := t.TempDir()
	composeFile := filepath.Join(dir, "compose.yml")
	if err := os.WriteFile(composeFile, []byte("services: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := RenderSessionGrants([]SessionGrant{{Host: "old.example.com", Port: 443}})
	if err := os.WriteFile(filepath.Join(dir, "session-grants.conf"), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	eng := newFakeEngine(t, nil)
	eng.fail(composeCommandKey(t, composeFile, "exec", "-T", "proxy", "squid", "-k", "reconfigure"), 42)

	err := ApplySessionGrants(context.Background(), eng, composeFile, dir, []SessionGrant{{Host: "new.example.com", Port: 443}})
	if err == nil || !strings.Contains(err.Error(), "reconfigure proxy") {
		t.Fatalf("ApplySessionGrants error = %v, want proxy reconfigure failure", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "session-grants.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != old {
		t.Fatalf("failed reload must restore previous overlay\n--- got ---\n%s\n--- want ---\n%s", b, old)
	}
}

func TestSessionGrantApplyWriteFailureDoesNotReloadOrReplace(t *testing.T) {
	dir := t.TempDir()
	composeFile := filepath.Join(dir, "compose.yml")
	old := RenderSessionGrants(nil)
	if err := os.WriteFile(filepath.Join(dir, "session-grants.conf"), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	eng := newFakeEngine(t, nil)

	err := ApplySessionGrants(context.Background(), eng, composeFile, dir, []SessionGrant{{Host: "example.com", Port: 443}}, WithOverlayTestHook(func(string) error {
		return os.ErrPermission
	}))
	if err == nil || !strings.Contains(err.Error(), "write session grants overlay") {
		t.Fatalf("ApplySessionGrants error = %v, want write failure", err)
	}
	eng.assertNotRan(t, composeCommandKey(t, composeFile, "exec", "-T", "proxy", "squid", "-k", "reconfigure"))
	b, err := os.ReadFile(filepath.Join(dir, "session-grants.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != old {
		t.Fatalf("write failure must leave previous overlay untouched\n--- got ---\n%s\n--- want ---\n%s", b, old)
	}
}
