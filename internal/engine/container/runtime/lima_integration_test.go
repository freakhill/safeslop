//go:build integration

// End-to-end proof that the lima backend boots a pinned, rootless container engine and runs a
// digest-pinned container in it — on a REAL vz VM (specs/0044 Phase 6). Gated two ways: the `integration`
// build tag keeps it out of the normal `go test ./...`, and limaAvailable() skips it off darwin/arm64.
// Run with `make test-integration` on a darwin/arm64 host (it downloads the pinned limactl + the three
// engine blobs, boots the VM, provisions the rootless engine offline, and runs a container).
//
// It mirrors the specs/0041 VM idempotency test: install the pins → Ensure boots → run a digest-pinned
// container over the in-guest nerdctl engine and assert its token → Teardown → assert the instance is
// gone. The engine is rootless nerdctl INSIDE the guest, so the host's docker socket is never used.
package runtime

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/freakhill/safeslop/internal/engine/install"
)

func limaAvailable(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("lima vz backend requires darwin/arm64")
	}
}

func TestLimaEnsureRunsPinnedContainer(t *testing.T) {
	limaAvailable(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	// Isolate everything under temp dirs (incl. a temp LIMA_HOME via $HOME) so the test never touches a
	// real user instance or the real ~/.config/safeslop. The HOME must be SHORT: lima's instance ssh.sock
	// path (LIMA_HOME/<inst>/ssh.sock.<digits>) must fit UNIX_PATH_MAX=104, and the default deep t.TempDir()
	// under /private/var/folders blows past it. A real ~/.config/safeslop/lima path is well within the limit.
	home, err := os.MkdirTemp("/tmp", "ss")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	t.Setenv("HOME", home)
	dirs := install.Dirs{
		BinDir:      t.TempDir(),
		AppDir:      t.TempDir(),
		TmpDir:      t.TempDir(),
		CacheDir:    t.TempDir(),
		LibDir:      t.TempDir(), // limactl is a FormatToolTree (needs its share/ tree)
		ReceiptPath: t.TempDir() + "/receipts.json",
	}

	// Install the pinned limactl + the three engine blobs (.iso, nerdctl-full, cosign) for real.
	var pins []install.Pin
	for _, p := range install.DesiredState() {
		if p.Name == "limactl" {
			pins = append(pins, p)
		}
	}
	if len(pins) != 1 {
		t.Fatalf("expected exactly one limactl pin in DesiredState, got %d", len(pins))
	}
	pins = append(pins, enginePins()...)
	plan, err := install.Plan(install.State{}, pins)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if err := install.Apply(ctx, plan, dirs, install.HTTPFetcher{}, nil); err != nil {
		t.Fatalf("install limactl + engine blobs: %v", err)
	}

	// A workspace under /tmp (short, non-home so the mount-isolation gate allows it) — mounted writable.
	// Resolve symlinks (/tmp -> /private/tmp on macOS) so the path the test bind-mounts + reads matches
	// the resolved path Ensure mounts into the guest (the identity-mount holds for the resolved path).
	ws, err := os.MkdirTemp("/tmp", "ws")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(ws) })
	if real, err := filepath.EvalSymlinks(ws); err == nil {
		ws = real
	}

	b := NewLimaBackend(dirs)
	defer b.Teardown(context.Background()) // reclaim the VM even if a later step fails

	eng, err := b.Ensure(ctx, ws, func(s string) { t.Logf("ensure: %s", s) })
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if eng.Name() != "nerdctl" {
		t.Fatalf("lima backend must run in-guest nerdctl (never the host docker socket), got %q", eng.Name())
	}

	const img = "alpine@sha256:4bcff63911fcb4448bd4fdacec207030997caf25e9bea4045fa6c8c44de311d1"

	// 1. The rootless engine executes a digest-pinned container.
	const token = "SAFESLOP_LIMA_E2E_OK"
	var out bytes.Buffer
	c := eng.Command(ctx, "run", "--rm", img, "echo", token)
	c.Stdout, c.Stderr = &out, &out
	if err := c.Run(); err != nil {
		t.Fatalf("nerdctl run: %v\n%s", err, out.String())
	}
	if !bytes.Contains(out.Bytes(), []byte(token)) {
		t.Fatalf("container did not print the token; output:\n%s", out.String())
	}

	// 2. The workspace is mounted writable at its identity path: a container write reaches the host repo.
	if err := eng.Command(ctx, "run", "--rm", "-v", ws+":"+ws, img, "sh", "-c", "echo hostbound > "+ws+"/from_guest").Run(); err != nil {
		t.Fatalf("workspace bind-mount run: %v", err)
	}
	if bb, err := os.ReadFile(ws + "/from_guest"); err != nil || !bytes.Contains(bb, []byte("hostbound")) {
		t.Fatalf("container write did not reach the host workspace: %v %q", err, bb)
	}

	// 3. SECURITY: a container on the backend's --internal network has NO direct egress (the agent's only
	// path out is the squid proxy). This is the regression test for the rootless-nerdctl egress finding.
	netName := eng.InternalNetwork()
	if netName == "" {
		t.Fatal("lima engine must define an --internal network for egress isolation")
	}
	_ = eng.Command(ctx, "network", "create", "--internal", netName).Run() // idempotent
	var egr bytes.Buffer
	probe := eng.Command(ctx, "run", "--rm", "--network", netName, img,
		"sh", "-c", "nc -z -w5 1.1.1.1 443 && echo REACHED || echo BLOCKED")
	probe.Stdout, probe.Stderr = &egr, &egr
	_ = probe.Run()
	if !bytes.Contains(egr.Bytes(), []byte("BLOCKED")) || bytes.Contains(egr.Bytes(), []byte("REACHED")) {
		t.Fatalf("EGRESS LEAK: agent on the --internal net reached the internet directly; output:\n%s", egr.String())
	}

	// Teardown removes the instance; a second Ensure-less existence check proves it is gone.
	if err := b.Teardown(ctx); err != nil {
		t.Fatalf("Teardown: %v", err)
	}
	limactl := dirs.BinDir + "/limactl"
	if b.instanceExists(ctx, limactl, b.limaHome()) {
		t.Fatal("instance still present after Teardown")
	}
}
