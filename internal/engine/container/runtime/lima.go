package runtime

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/freakhill/safeslop/internal/engine/install"
)

// copyFile is the cross-device fallback for stageEngine's hard-link (the CacheDir blobs and LIMA_HOME may
// live on different filesystems). It preserves nothing but the bytes — staged artifacts are read-only.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// instanceName is the single lima instance safeslop owns (per-host, not per-repo — the VM is the
// engine, scoped by its rootless guest + the per-run workspace mount, not by one VM per project).
const instanceName = "safeslop"

// LimaBackend provisions a pinned, rootless, hardened vz Linux VM (via the pinned limactl) and hands
// back a LimaNerdctlEngine that runs nerdctl INSIDE the guest — never the host's docker socket
// (specs/0043). The owned lima YAML + fail-closed invariant gate + on-demand engine pins all live here.
type LimaBackend struct {
	dirs install.Dirs
	run  Runner
}

// NewLimaBackend wires the production runner; tests construct LimaBackend{} directly with a fake run.
func NewLimaBackend(dirs install.Dirs) *LimaBackend {
	return &LimaBackend{dirs: dirs, run: defaultRunner}
}

func (*LimaBackend) Name() string { return "lima" }

// StateDir is safeslop's OWNED lima state root (not the user's ~/.lima): the VM + instance state, the
// engine staging dir, and the rendered config all live under it and are receipted/torn down as a unit
// (specs/0044 Phase 4.3). It is NOT LIMA_HOME itself — see limaHome.
func (b *LimaBackend) StateDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "safeslop", "lima")
}

// limaHome is LIMA_HOME — a SUBDIR of StateDir holding only lima's own instance dirs. It must contain
// nothing else: lima treats every subdir of LIMA_HOME as an instance, so the engine-stage dir and the
// rendered instance config are deliberately kept OUTSIDE it (a config file inside LIMA_HOME makes lima
// mis-resolve the instance and fatal on a missing lima.yaml — found live 2026-06-22).
func (b *LimaBackend) limaHome() string {
	sd := b.StateDir()
	if sd == "" {
		return ""
	}
	return filepath.Join(sd, "home")
}

// enginePins are the on-demand container-runtime artifacts: the pinned VM OS image plus the in-guest
// engine bundle, all staged from LOCAL pinned copies at provision so the VM does ZERO internet fetch for
// the engine (specs/0043 graft #1). Verified against the upstream signed releases on 2026-06-22, and the
// alpine .iso boot + nerdctl-full musl-exec + rootless bring-up were validated live the same day. These
// are installed at first container start behind the consent gate — NOT in install.DesiredState().
func enginePins() []install.Pin {
	return []install.Pin{
		{
			// alpine-lima: a tiny, purpose-built lima guest image, github-hosted + checksummed. It ships an
			// .iso with only a sha512 sidecar (no sha256), so this pin uses SHA512. The .iso boots under lima
			// v2.1.3 (validated 2026-06-22 — this pin is byte-identical to lima's own _images/alpine-iso.yaml).
			// Chosen over an Ubuntu cloud image for minimal surface; the engine is staged separately (below).
			Name:       imageBlobName,
			Kind:       "runtime",
			Format:     install.FormatBlob,
			Version:    "0.2.49", // alpine-lima release (Alpine 3.23.0 guest)
			SHA512:     imageSHA512,
			URL:        "https://github.com/lima-vm/alpine-lima/releases/download/v0.2.49/alpine-lima-std-3.23.0-aarch64.iso",
			Provenance: install.ProvenanceVendor, // matches alpine-lima's published .sha512sum
		},
		{
			// nerdctl-full bundles containerd/nerdctl/runc/rootlesskit/buildkit — all musl-static, verified
			// to exec on the Alpine guest (2026-06-22). Staged to /safeslop-engine and untarred into
			// /usr/local at provision; rootless containerd then comes up via the bundled containerd-rootless.sh
			// (no systemd needed — Alpine is OpenRC).
			Name:       engineBlobName,
			Kind:       "runtime",
			Format:     install.FormatBlob,
			Version:    "2.3.3",
			SHA256:     "2322f29f451189dd790b5d7c599b4600c210ff0f2c10244308a8e6a024274066",
			URL:        "https://github.com/containerd/nerdctl/releases/download/v2.3.3/nerdctl-full-2.3.3-linux-arm64.tar.gz",
			Provenance: install.ProvenanceVendor, // matches nerdctl's published SHA256SUMS
		},
		{
			Name:       cosignBlobName,
			Kind:       "runtime",
			Format:     install.FormatBlob,
			Version:    "3.1.1",
			SHA256:     "2ec865872e331c32fd12b08dae15332d3f92c0aa029219589684a4903ca85d11",
			URL:        "https://github.com/sigstore/cosign/releases/download/v3.1.1/cosign-linux-arm64",
			Provenance: install.ProvenanceVendor, // matches cosign's published cosign_checksums.txt
		},
	}
}

// Blob names = the install.Pin names = the filenames installBlob places under CacheDir. imageSHA512 is
// the alpine-lima .iso digest (the render-time image digest and the install-time verification share it).
const (
	imageBlobName  = "lima-guest-image"
	engineBlobName = "nerdctl-full"
	cosignBlobName = "cosign"
	imageSHA512    = "7ef845a28cd8d77da8317a2d748456f0861d86d23dc2c51739927f16c634fec21ec90d58edd71202bdd708f81629bcffc4c9c682689bcc34f37668803757d463"
)

// Pins are installed ON DEMAND (gated at first container start), never folded into the base
// install.DesiredState() that `install apply` runs for every user. limactl itself is the small,
// always-useful isolation-tier binary and lives in DesiredState alongside tart.
func (b *LimaBackend) Pins() []install.Pin { return enginePins() }

// Ensure idempotently boots the pinned, rootless VM and returns a LimaNerdctlEngine. On first call it
// renders the owned YAML (which runs the fail-closed invariant gate), stages the engine bundle from the
// CacheDir blobs, and `limactl start`s the instance; on every call it (re)starts a stopped instance and
// brings rootless containerd up (idempotent), then waits for the engine to answer. The full flow was
// validated live on 2026-06-22 (see specs/0044). It fails loud rather than pretending a runtime exists.
func (b *LimaBackend) Ensure(ctx context.Context, emit func(string)) (Engine, error) {
	if emit == nil {
		emit = func(string) {}
	}
	limactl := filepath.Join(b.dirs.BinDir, "limactl")
	if _, err := os.Stat(limactl); err != nil {
		return nil, fmt.Errorf("lima backend: limactl not installed (expected at %s): %w", limactl, err)
	}
	home := b.limaHome()
	if home == "" {
		return nil, fmt.Errorf("lima backend: cannot resolve LIMA_HOME (no user home)")
	}
	if err := os.MkdirAll(home, 0o755); err != nil {
		return nil, fmt.Errorf("lima backend: create LIMA_HOME: %w", err)
	}
	uid := os.Getuid()
	eng := LimaNerdctlEngine{Limactl: limactl, Instance: instanceName, UID: uid, LimaHome: home}

	if b.instanceExists(ctx, limactl, home) {
		emit("starting the existing safeslop lima VM")
		if out, code, err := b.runLimactl(ctx, limactl, home, "start", instanceName); err != nil || code != 0 {
			return nil, limaErr("start existing instance failed", out, code, err)
		}
	} else {
		emit("provisioning the pinned safeslop lima VM (first run)")
		if err := b.create(ctx, limactl, home, emit); err != nil {
			return nil, err
		}
	}

	emit("bringing up the rootless container engine")
	if err := b.bringUpEngine(ctx, limactl, home, uid); err != nil {
		return nil, err
	}
	if err := b.waitEngineReady(ctx, eng); err != nil {
		return nil, err
	}
	return eng, nil
}

// create renders the owned, hardened YAML (gated by assertLimaInvariants inside renderLimaYAML), stages
// the engine bundle from the installed CacheDir blobs, writes the config, and starts a fresh instance.
func (b *LimaBackend) create(ctx context.Context, limactl, home string, emit func(string)) error {
	stage, err := b.stageEngine()
	if err != nil {
		return fmt.Errorf("lima backend: stage engine bundle: %w", err)
	}
	image := filepath.Join(b.dirs.CacheDir, imageBlobName)
	if _, err := os.Stat(image); err != nil {
		return fmt.Errorf("lima backend: pinned VM image not installed (expected at %s — run the gated engine install): %w", image, err)
	}
	user := os.Getenv("USER")
	if user == "" {
		return fmt.Errorf("lima backend: cannot resolve $USER for the rootless subuid grant")
	}
	cfg, err := renderLimaYAML(LimaConfigInput{
		ImageLocation: image,
		ImageDigest:   "sha512:" + imageSHA512,
		ImageArch:     "aarch64",
		EngineStage:   stage,
		User:          user,
	})
	if err != nil {
		return err // invariant gate failure — never start a non-conforming VM
	}
	// The config MUST be written outside LIMA_HOME: a YAML inside LIMA_HOME makes lima treat its parent
	// as an instance dir and fatal on a missing lima.yaml (found live 2026-06-22). lima copies it into the
	// instance dir at start, so the temp file is disposable.
	tmp := b.dirs.TmpDir
	if tmp == "" {
		tmp = os.TempDir()
	}
	cfgFile, err := os.CreateTemp(tmp, "safeslop-lima-*.yaml")
	if err != nil {
		return err
	}
	cfgPath := cfgFile.Name()
	cfgFile.Close()
	defer os.Remove(cfgPath)
	if err := os.WriteFile(cfgPath, cfg, 0o600); err != nil {
		return err
	}
	out, code, err := b.runLimactl(ctx, limactl, home, "start", "--tty=false", "--name="+instanceName, cfgPath)
	if err != nil || code != 0 {
		return limaErr("limactl start failed", out, code, err)
	}
	return nil
}

// stageEngine assembles the read-only engine-staging dir under LIMA_HOME with the filenames the provision
// script reads (nerdctl-full.tar.gz, cosign), hard-linked from the installed CacheDir blobs (cheap; copy
// fallback across filesystems). The dir is mounted RO at /safeslop-engine — the only non-workspace mount.
func (b *LimaBackend) stageEngine() (string, error) {
	stage := filepath.Join(b.StateDir(), "engine-stage")
	if err := os.MkdirAll(stage, 0o755); err != nil {
		return "", err
	}
	links := map[string]string{
		engineBlobName: "nerdctl-full.tar.gz",
		cosignBlobName: "cosign",
	}
	for blob, staged := range links {
		src := filepath.Join(b.dirs.CacheDir, blob)
		if _, err := os.Stat(src); err != nil {
			return "", fmt.Errorf("pinned %s not installed (expected at %s): %w", blob, src, err)
		}
		dst := filepath.Join(stage, staged)
		_ = os.Remove(dst)
		if err := os.Link(src, dst); err != nil {
			if err := copyFile(src, dst); err != nil { // cross-device fallback
				return "", err
			}
		}
	}
	return stage, nil
}

// bringUpEngine creates the XDG runtime dir (root; /run is tmpfs so this runs on every boot) and launches
// rootless containerd DETACHED via containerd-rootless.sh — no systemd, idempotent via a pgrep guard. The
// detached process persists across limactl-shell sessions (validated 2026-06-22).
func (b *LimaBackend) bringUpEngine(ctx context.Context, limactl, home string, uid int) error {
	script := fmt.Sprintf(`set -eu
sudo install -d -o "$(id -un)" -g "$(id -gn)" -m 0700 /run/user/%d
export XDG_RUNTIME_DIR=/run/user/%d
export PATH=/usr/local/bin:$PATH
if ! pgrep -x containerd >/dev/null 2>&1; then
  setsid sh -c "containerd-rootless.sh > /tmp/safeslop-containerd-rootless.log 2>&1" </dev/null >/dev/null 2>&1 &
fi`, uid, uid)
	out, code, err := b.runLimactl(ctx, limactl, home, "shell", instanceName, "sh", "-c", script)
	if err != nil || code != 0 {
		return limaErr("rootless engine bring-up failed", out, code, err)
	}
	return nil
}

// waitEngineReady polls `nerdctl info` (through the injected Runner) until the rootless daemon answers
// (it is launched detached, so it races the first command). Bounded; a context cancel aborts immediately.
func (b *LimaBackend) waitEngineReady(ctx context.Context, eng LimaNerdctlEngine) error {
	for i := 0; i < 20; i++ {
		if _, code, err := b.run(ctx, eng.argv("info")); err == nil && code == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return fmt.Errorf("lima backend: rootless engine did not become ready (see /tmp/safeslop-containerd-rootless.log in the guest)")
}

// instanceExists reports whether `limactl list` already knows the safeslop instance.
func (b *LimaBackend) instanceExists(ctx context.Context, limactl, home string) bool {
	out, code, err := b.runLimactl(ctx, limactl, home, "list", "-q")
	if err != nil || code != 0 {
		return false
	}
	for _, line := range strings.Fields(out) {
		if line == instanceName {
			return true
		}
	}
	return false
}

// runLimactl runs limactl with LIMA_HOME pointed at safeslop's owned home, through the injected Runner.
func (b *LimaBackend) runLimactl(ctx context.Context, limactl, home string, args ...string) (string, int, error) {
	argv := append([]string{"env", "LIMA_HOME=" + home, limactl}, args...)
	return b.run(ctx, argv)
}

// limaErr renders a limactl failure with the captured output. The Runner reports a non-zero exit as
// (output, code, nil), so a bare `%w` of err would print "%!w(<nil>)"; surface the code + lima's stderr.
func limaErr(what, out string, code int, err error) error {
	if err != nil {
		return fmt.Errorf("lima backend: %s: %w\n%s", what, err, out)
	}
	return fmt.Errorf("lima backend: %s (exit %d)\n%s", what, code, strings.TrimSpace(out))
}

// Teardown stops and deletes the safeslop instance (idempotent — a missing instance is success). The
// owned LIMA_HOME dir + staged blobs are reaped by uninstall against the receipt (specs/0044 Phase 4.3).
func (b *LimaBackend) Teardown(ctx context.Context) error {
	limactl := filepath.Join(b.dirs.BinDir, "limactl")
	if _, err := os.Stat(limactl); err != nil {
		return nil // nothing installed → nothing to tear down
	}
	home := b.limaHome()
	if !b.instanceExists(ctx, limactl, home) {
		return nil
	}
	if _, _, err := b.runLimactl(ctx, limactl, home, "stop", "-f", instanceName); err != nil {
		return err
	}
	if out, code, err := b.runLimactl(ctx, limactl, home, "delete", "-f", instanceName); err != nil || code != 0 {
		return limaErr("delete instance failed", out, code, err)
	}
	return nil
}
