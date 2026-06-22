package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/install"
)

func TestLimaNerdctlEngineArgv(t *testing.T) {
	e := LimaNerdctlEngine{Limactl: "/b/limactl", Instance: "safeslop", UID: 501, LimaHome: "/h/lima"}
	got := strings.Join(e.Argv("run", "--rm", "img"), " ")
	want := "env LIMA_HOME=/h/lima /b/limactl shell safeslop env XDG_RUNTIME_DIR=/run/user/501 " +
		"PATH=/usr/local/bin:/usr/bin:/bin:/sbin:/usr/sbin nerdctl run --rm img"
	if got != want {
		t.Fatalf("engine argv\n got: %s\nwant: %s", got, want)
	}
}

// recRunner records every argv and answers "instance absent" + "everything succeeds" so Ensure's
// orchestration (create → bring up → ready) can be unit-tested without booting a VM.
type recRunner struct {
	calls    [][]string
	listSays string // what `limactl list -q` returns
}

func (r *recRunner) run(_ context.Context, argv []string) (string, int, error) {
	r.calls = append(r.calls, argv)
	if joined := strings.Join(argv, " "); strings.Contains(joined, "list -q") {
		return r.listSays, 0, nil
	}
	return "", 0, nil
}

func (r *recRunner) saw(substr string) bool {
	for _, c := range r.calls {
		if strings.Contains(strings.Join(c, " "), substr) {
			return true
		}
	}
	return false
}

// ensureFixture lays out a fake BinDir (limactl) + CacheDir (the three pinned blobs) + HOME so the
// non-VM parts of Ensure (stat checks, staging, StateDir) run for real.
func ensureFixture(t *testing.T) install.Dirs {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USER", "tester")
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "limactl"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cache := t.TempDir()
	for _, blob := range []string{imageBlobName, engineBlobName, cosignBlobName} {
		if err := os.WriteFile(filepath.Join(cache, blob), []byte("blob"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return install.Dirs{BinDir: bin, CacheDir: cache, TmpDir: t.TempDir()}
}

func TestEnsureCreatesThenBringsUp(t *testing.T) {
	dirs := ensureFixture(t)
	r := &recRunner{listSays: ""} // empty → instance absent → create path
	b := &LimaBackend{dirs: dirs, run: r.run}

	eng, err := b.Ensure(context.Background(), nil)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if eng == nil || eng.Name() != "nerdctl" {
		t.Fatalf("want a nerdctl engine, got %+v", eng)
	}
	if !r.saw("start --tty=false --name=" + instanceName) {
		t.Error("absent instance must be created with limactl start --name")
	}
	if !r.saw("containerd-rootless.sh") {
		t.Error("rootless engine must be brought up")
	}
	if !r.saw("nerdctl info") {
		t.Error("readiness must be probed via nerdctl info")
	}
	// The staged bundle must carry the exact filenames the provision script reads.
	stage := filepath.Join(b.StateDir(), "engine-stage")
	for _, f := range []string{"nerdctl-full.tar.gz", "cosign"} {
		if _, err := os.Stat(filepath.Join(stage, f)); err != nil {
			t.Errorf("engine stage missing %s: %v", f, err)
		}
	}
}

func TestEnsureStartsExistingInstance(t *testing.T) {
	dirs := ensureFixture(t)
	r := &recRunner{listSays: instanceName} // present → start-existing path, no --name create
	b := &LimaBackend{dirs: dirs, run: r.run}

	if _, err := b.Ensure(context.Background(), nil); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if r.saw("--name=" + instanceName) {
		t.Error("an existing instance must not be re-created with --name")
	}
	if !r.saw("start " + instanceName) {
		t.Error("an existing instance must be started by name")
	}
}

func TestEnsureFailsWhenImageNotInstalled(t *testing.T) {
	dirs := ensureFixture(t)
	_ = os.Remove(filepath.Join(dirs.CacheDir, imageBlobName)) // pinned image not installed
	r := &recRunner{}
	b := &LimaBackend{dirs: dirs, run: r.run}
	if _, err := b.Ensure(context.Background(), nil); err == nil {
		t.Fatal("Ensure must fail closed when the pinned VM image blob is absent")
	}
}

func TestTeardownIdempotentWhenAbsent(t *testing.T) {
	dirs := ensureFixture(t)
	r := &recRunner{listSays: ""} // instance absent
	b := &LimaBackend{dirs: dirs, run: r.run}
	if err := b.Teardown(context.Background()); err != nil {
		t.Fatalf("Teardown of an absent instance must be a no-op success: %v", err)
	}
	if r.saw("delete") {
		t.Error("must not delete an instance that does not exist")
	}
}
