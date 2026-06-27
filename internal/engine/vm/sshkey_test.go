package vm

import (
	"context"
	"fmt"
	"os"
	"testing"
)

// TestStageVMSSHKeyFromOp proves the JIT path: with SAFESLOP_VM_SSH_KEY_OP set, the
// op reference is read into a transient 0600 file (trailing newline ensured) that
// SAFESLOP_VM_SSH_KEY then points at, and cleanup wipes it. The op read is seamed.
func TestStageVMSSHKeyFromOp(t *testing.T) {
	t.Setenv("SAFESLOP_VM_SSH_KEY", "")
	t.Setenv(vmSSHKeyOpEnv, "op://homelab-infra/safeslop-base-vm/private key?ssh-format=openssh")
	want := "-----BEGIN OPENSSH PRIVATE KEY-----\nFAKE\n-----END OPENSSH PRIVATE KEY-----" // no trailing newline on purpose
	old := readOpRef
	readOpRef = func(_ context.Context, _ string) ([]byte, error) { return []byte(want), nil }
	defer func() { readOpRef = old }()

	cleanup, err := stageVMSSHKey(context.Background())
	if err != nil {
		t.Fatalf("stageVMSSHKey: %v", err)
	}
	keyPath := os.Getenv("SAFESLOP_VM_SSH_KEY")
	if keyPath == "" {
		t.Fatal("SAFESLOP_VM_SSH_KEY was not pointed at the staged key")
	}
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("stat staged key: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("staged key perms = %v, want 0600", perm)
	}
	got, _ := os.ReadFile(keyPath)
	if string(got) != want+"\n" {
		t.Fatalf("staged key = %q, want the op value with a trailing newline", got)
	}
	cleanup()
	if _, err := os.Stat(keyPath); !os.IsNotExist(err) {
		t.Fatalf("cleanup did not wipe the staged key (stat err = %v)", err)
	}
}

// TestStageVMSSHKeyNoOpRefIsNoop proves the default path is untouched: with no op
// reference set, a pre-existing SAFESLOP_VM_SSH_KEY file path is left as-is.
func TestStageVMSSHKeyNoOpRefIsNoop(t *testing.T) {
	t.Setenv(vmSSHKeyOpEnv, "")
	t.Setenv("SAFESLOP_VM_SSH_KEY", "/pre/existing/key")
	cleanup, err := stageVMSSHKey(context.Background())
	if err != nil {
		t.Fatalf("stageVMSSHKey: %v", err)
	}
	defer cleanup()
	if got := os.Getenv("SAFESLOP_VM_SSH_KEY"); got != "/pre/existing/key" {
		t.Fatalf("no-op path changed SAFESLOP_VM_SSH_KEY to %q", got)
	}
}

// TestStageVMSSHKeyOpReadError proves an op failure surfaces (launch must not
// silently fall back to no key).
func TestStageVMSSHKeyOpReadError(t *testing.T) {
	t.Setenv(vmSSHKeyOpEnv, "op://v/i/f")
	old := readOpRef
	readOpRef = func(_ context.Context, _ string) ([]byte, error) { return nil, fmt.Errorf("boom") }
	defer func() { readOpRef = old }()
	if _, err := stageVMSSHKey(context.Background()); err == nil {
		t.Fatal("want error when op read fails")
	}
}
