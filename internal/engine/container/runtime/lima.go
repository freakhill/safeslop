package runtime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/freakhill/safeslop/internal/engine/install"
)

// LimaBackend provisions a pinned, rootless, hardened vz Linux VM (via the pinned limactl) and exposes a
// docker-compatible socket scoped to the session — never the host's /var/run/docker.sock (specs/0043).
// The full provisioning (owned lima YAML + fail-closed invariant gate + second consent + receipt) lands
// in specs/0044 Phase 3/4; this is the seam + the on-demand engine pins.
type LimaBackend struct {
	dirs install.Dirs
	run  Runner
}

// NewLimaBackend wires the production runner; tests construct LimaBackend{} directly with a fake run.
func NewLimaBackend(dirs install.Dirs) *LimaBackend {
	return &LimaBackend{dirs: dirs, run: defaultRunner}
}

func (*LimaBackend) Name() string { return "lima" }

// StateDir is safeslop's OWNED lima home (not the user's ~/.lima), so the VM + instance state live under
// safeslop and can be receipted/torn down as a unit (specs/0044 Phase 4.3).
func (b *LimaBackend) StateDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "safeslop", "lima")
}

// enginePins are the on-demand container-runtime artifacts: the pinned VM OS image plus the in-guest
// engine bundle, all staged from LOCAL pinned copies at provision so the VM does ZERO internet fetch for
// the engine (specs/0043 graft #1). Verified against the upstream signed releases on 2026-06-22. These
// are installed at first container start behind the consent gate — NOT in install.DesiredState().
func enginePins() []install.Pin {
	return []install.Pin{
		{
			// alpine-lima: a tiny, purpose-built lima guest image, github-hosted + checksummed. It ships an
			// .iso with only a sha512 sidecar (no sha256), so this pin uses SHA512. The .iso boot path under
			// lima v2.1.3 is exercised by the Phase 6 integration test (deferred). Chosen over an Ubuntu cloud
			// image for minimal surface; the engine is staged separately (below), not baked by the distro.
			Name:       "lima-guest-image",
			Kind:       "runtime",
			Format:     install.FormatBlob,
			Version:    "0.2.49", // alpine-lima release (Alpine 3.23.0 guest)
			SHA512:     "7ef845a28cd8d77da8317a2d748456f0861d86d23dc2c51739927f16c634fec21ec90d58edd71202bdd708f81629bcffc4c9c682689bcc34f37668803757d463",
			URL:        "https://github.com/lima-vm/alpine-lima/releases/download/v0.2.49/alpine-lima-std-3.23.0-aarch64.iso",
			Provenance: install.ProvenanceVendor, // matches alpine-lima's published .sha512sum
		},
		{
			Name:       "nerdctl-full",
			Kind:       "runtime",
			Format:     install.FormatBlob,
			Version:    "2.3.3",
			SHA256:     "2322f29f451189dd790b5d7c599b4600c210ff0f2c10244308a8e6a024274066",
			URL:        "https://github.com/containerd/nerdctl/releases/download/v2.3.3/nerdctl-full-2.3.3-linux-arm64.tar.gz",
			Provenance: install.ProvenanceVendor, // matches nerdctl's published SHA256SUMS
		},
		{
			Name:       "cosign",
			Kind:       "runtime",
			Format:     install.FormatBlob,
			Version:    "3.1.1",
			SHA256:     "2ec865872e331c32fd12b08dae15332d3f92c0aa029219589684a4903ca85d11",
			URL:        "https://github.com/sigstore/cosign/releases/download/v3.1.1/cosign-linux-arm64",
			Provenance: install.ProvenanceVendor, // matches cosign's published cosign_checksums.txt
		},
	}
}

// Pins are installed ON DEMAND (gated at first container start), never folded into the base
// install.DesiredState() that `install apply` runs for every user. limactl itself is the small,
// always-useful isolation-tier binary and lives in DesiredState alongside tart.
func (b *LimaBackend) Pins() []install.Pin { return enginePins() }

// Ensure will provision + boot the VM and return the scoped socket. The body (owned YAML render +
// invariant gate + consent + receipt + limactl start) is specs/0044 Phase 3/4; until then it fails
// loudly rather than pretending a runtime exists.
func (b *LimaBackend) Ensure(ctx context.Context, emit func(string)) (string, error) {
	if _, err := os.Stat(filepath.Join(b.dirs.BinDir, "limactl")); err != nil {
		return "", fmt.Errorf("lima backend: limactl not installed (expected at %s/limactl): %w", b.dirs.BinDir, err)
	}
	return "", fmt.Errorf("lima backend: VM provisioning not yet implemented (specs/0044 Phase 3/4)")
}

func (b *LimaBackend) Teardown(ctx context.Context) error { return nil }
