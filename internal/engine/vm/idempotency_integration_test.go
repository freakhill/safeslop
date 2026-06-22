//go:build integration

// install->uninstall->install idempotency proof on a REAL tart VM (specs/0041 task 6). Gated two ways:
// the `integration` build tag keeps it out of the normal `go test ./...` gate, and Available() skips it
// when tart is absent. Run it with `make test-integration` on a darwin/arm64 host that has tart.
//
// It boots a clean macOS guest, scp's the freshly-built binary in, then asserts the full round-trip:
// `install apply` places the toolchain + writes a receipt, `uninstall apply --yes` removes it leaving no
// safeslop residue, and `install apply` again reproduces the exact same `which uv` — i.e. the loop is
// idempotent. Assertions focus on uv (the lightest Path A tool); install apply only downloads + places
// files (no Path A tool is executed at install time), so the full manifest is safe to drive in a VM.
//
// Path B (nix) idempotency is intentionally out of scope here: there is no CLI verb to install a
// verified-installer tool yet, and exercising it would create + destroy a real /nix APFS volume + daemon
// on the agent. Add it once a `safeslop install <verified-tool>` path exists.
package vm

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInstallUninstallInstallIdempotent(t *testing.T) {
	if !Available() {
		t.Skip("tart unavailable — skipping VM idempotency test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	if err := EnsureBase(ctx); err != nil {
		t.Fatalf("EnsureBase: %v", err)
	}
	const profile = "idem"
	// Defer teardown BEFORE the error check: CloneAndBoot can fail after the VM is already booted (e.g.
	// an SSH-reachability timeout), and Destroy keys off the profile name, not the IP — so this reclaims
	// a half-booted VM too. Fresh ctx so teardown runs even if the test ctx has timed out.
	ip, err := CloneAndBoot(ctx, profile)
	defer Destroy(context.Background(), profile)
	if err != nil {
		t.Fatalf("CloneAndBoot: %v", err)
	}

	// Build the binary (agent is darwin/arm64, same as the guest) and scp it in.
	bin := filepath.Join(t.TempDir(), "safeslop")
	if out, err := exec.CommandContext(ctx, "go", "build", "-o", bin, "github.com/freakhill/safeslop/cmd/safeslop").CombinedOutput(); err != nil {
		t.Fatalf("build safeslop: %v\n%s", err, out)
	}
	if err := osCommand(ctx, scpArgv(ip, bin, "/Users/"+sshUser+"/safeslop")).Run(); err != nil {
		t.Fatalf("scp binary into guest: %v", err)
	}

	sshRun := func(remote string) (string, error) {
		argv := sshArgv(ip, false, "zsh", "-lc", remote)
		out, err := exec.CommandContext(ctx, argv[0], argv[1:]...).CombinedOutput()
		return string(out), err
	}
	guestSays := func(remote string) string { out, _ := sshRun(remote); return strings.TrimSpace(out) }
	uvPresent := func() bool { return guestSays("test -x ~/.local/bin/uv && echo yes || echo no") == "yes" }

	// 1. install — places the toolchain + writes the receipt.
	if out, err := sshRun("~/safeslop install apply"); err != nil {
		t.Fatalf("install apply: %v\n%s", err, out)
	}
	if !uvPresent() {
		t.Fatal("uv missing after install apply")
	}
	if guestSays("test -f ~/.config/safeslop/receipts.json && echo yes || echo no") != "yes" {
		t.Fatal("receipt not written after install")
	}
	whichBefore := guestSays("command -v uv || true")

	// 2. uninstall — removes it, leaves no safeslop residue.
	if out, err := sshRun("~/safeslop uninstall apply --yes"); err != nil {
		t.Fatalf("uninstall apply: %v\n%s", err, out)
	}
	if uvPresent() {
		t.Fatal("uv still present after uninstall")
	}
	if residue := guestSays("launchctl print system 2>/dev/null | grep -i safeslop || true"); residue != "" {
		t.Fatalf("safeslop launchd residue after uninstall: %s", residue)
	}

	// 3. reinstall — idempotent: uv resolves to the same path it did the first time.
	if out, err := sshRun("~/safeslop install apply"); err != nil {
		t.Fatalf("reinstall apply: %v\n%s", err, out)
	}
	if !uvPresent() {
		t.Fatal("uv missing after reinstall")
	}
	if whichAfter := guestSays("command -v uv || true"); whichAfter != whichBefore {
		t.Fatalf("`which uv` changed across the loop: before=%q after=%q", whichBefore, whichAfter)
	}
}
