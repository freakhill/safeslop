package vm

import (
	"context"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"time"
)

// vmSSHKeyOpEnv names a 1Password secret reference (op://vault/item/field). When
// set, the vm boundary reads the VM's SSH private key just-in-time at launch
// instead of requiring a key file on disk: the key is materialized into a transient
// 0600 file that SAFESLOP_VM_SSH_KEY then points at, so the existing ssh/scp path
// uses it unchanged, and the file is wiped when the run returns.
//
// The reference should request the OpenSSH format, e.g.
//
//	op://homelab-infra/safeslop-base-vm/private key?ssh-format=openssh
//
// `op read` defaults to PKCS#8 ("-----BEGIN PRIVATE KEY-----"), which ssh cannot
// use. When SAFESLOP_VM_SSH_KEY_OP is set it takes precedence over any plain
// SAFESLOP_VM_SSH_KEY file path.
const vmSSHKeyOpEnv = "SAFESLOP_VM_SSH_KEY_OP"

// readOpRef reads a 1Password reference via the op CLI. It is a seam for tests. No
// --no-newline: an OpenSSH private key needs its trailing newline, and the value is
// never logged. The read is bounded so a hung/`signin`-needed op fails rather than
// blocking a launch (especially a detached supervisor with no terminal to prompt).
var readOpRef = func(ctx context.Context, ref string) ([]byte, error) {
	if _, err := osexec.LookPath("op"); err != nil {
		return nil, fmt.Errorf("1Password CLI `op` not found on PATH; cannot resolve %s (install op and run `op signin`)", vmSSHKeyOpEnv)
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	out, err := osexec.CommandContext(ctx, "op", "read", ref).Output()
	if err != nil {
		return nil, fmt.Errorf("op read failed for the vm ssh key (is the 1Password app running and signed in?): %w", err)
	}
	return out, nil
}

// stageVMSSHKey, when SAFESLOP_VM_SSH_KEY_OP is set, JIT-reads that reference into a
// transient 0600 key file and points SAFESLOP_VM_SSH_KEY at it for the ssh/scp
// invocations. It returns a cleanup that wipes the key. When the env is unset it is
// a no-op (with a no-op cleanup) that leaves any existing SAFESLOP_VM_SSH_KEY file
// path untouched. The key file lives in its own temp dir, NOT the scp'd stage dir,
// so the VM's own access key never lands inside the guest.
func stageVMSSHKey(ctx context.Context) (func(), error) {
	ref := os.Getenv(vmSSHKeyOpEnv)
	if ref == "" {
		return func() {}, nil
	}
	key, err := readOpRef(ctx, ref)
	if err != nil {
		return nil, err
	}
	if len(key) == 0 {
		return nil, fmt.Errorf("%s resolved to an empty key", vmSSHKeyOpEnv)
	}
	if key[len(key)-1] != '\n' {
		key = append(key, '\n') // ssh refuses a key file without a trailing newline
	}
	dir, err := os.MkdirTemp("", "safeslop-vmkey")
	if err != nil {
		return nil, err
	}
	keyPath := filepath.Join(dir, "id")
	if err := os.WriteFile(keyPath, key, 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	_ = os.Setenv("SAFESLOP_VM_SSH_KEY", keyPath)
	return func() { _ = os.RemoveAll(dir) }, nil
}
