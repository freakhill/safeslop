package runtime

import (
	"fmt"
	"path/filepath"
	"strings"

	yaml "go.yaml.in/yaml/v3"
)

// limaConfig is the subset of a lima instance YAML safeslop OWNS and writes itself (never `limactl
// create` defaults). Marshaled to the instance config; the invariant gate (assertLimaInvariants)
// re-parses the bytes and fails closed unless every hardening property holds (specs/0043/0044).
//
// Schema is pinned to lima 2.1.3 (the limactl pin): rosetta lives under vmOpts.vz.rosetta — the
// top-level `rosetta:` key is DEPRECATED in 2.x and `limactl validate` warns on it (verified live on
// 2026-06-22 against the pinned binary).
type limaConfig struct {
	VMType     string          `yaml:"vmType"`
	VMOpts     limaVMOpts      `yaml:"vmOpts"`
	Images     []limaImage     `yaml:"images"`
	MountType  string          `yaml:"mountType"`
	Mounts     []limaMount     `yaml:"mounts"`
	Networks   []limaNetwork   `yaml:"networks"`
	SSH        limaSSH         `yaml:"ssh"`
	Containerd limaContainerd  `yaml:"containerd"`
	Provision  []limaProvision `yaml:"provision"`
}

type limaVMOpts struct {
	VZ limaVZ `yaml:"vz"`
}
type limaVZ struct {
	Rosetta limaRosetta `yaml:"rosetta"`
}
type limaRosetta struct {
	Enabled bool `yaml:"enabled"`
	BinFmt  bool `yaml:"binfmt"`
}
type limaImage struct {
	Location string `yaml:"location"`
	Arch     string `yaml:"arch"`
	Digest   string `yaml:"digest,omitempty"`
}
type limaMount struct {
	Location   string `yaml:"location"`
	MountPoint string `yaml:"mountPoint,omitempty"` // guest path; lima otherwise mounts at the identity path
	Writable   bool   `yaml:"writable"`
}

// engineStageMountPoint is where the read-only engine staging dir appears in the guest — the path the
// provision script reads the pinned bundle from. Validated live 2026-06-22.
const engineStageMountPoint = "/safeslop-engine"

type limaNetwork struct {
	Lima string `yaml:"lima,omitempty"`
}
type limaSSH struct {
	ForwardAgent bool `yaml:"forwardAgent"`
}
type limaContainerd struct {
	System bool `yaml:"system"`
	User   bool `yaml:"user"`
}
type limaProvision struct {
	Mode   string `yaml:"mode"`
	Script string `yaml:"script"`
}

// LimaConfigInput is what render needs from the backend: the local pinned image (CacheDir path +
// digest), the read-only engine-staging dir (holds the pinned nerdctl-full/cosign tarballs), the guest
// login user (for the rootless subuid/subgid grant), and an optional opted-in workspace to mount writable.
type LimaConfigInput struct {
	ImageLocation string // absolute local path under CacheDir (the pinned .iso) — never a URL
	ImageDigest   string // "sha512:<hex>" | "sha256:<hex>"
	ImageArch     string // "aarch64"
	EngineStage   string // absolute local dir mounted READ-ONLY; the pinned engine tarballs live here
	User          string // guest login user (= host user); gets the rootless subuid/subgid range
	Workspace     string // optional; "" = no workspace mount (default)
}

// engineProvisionScript is the first-boot (mode:system, root) setup, validated live on 2026-06-22
// against the pinned alpine-lima .iso under lima 2.1.3. It does NOT start the engine — bringing the
// rootless daemon up is Ensure's idempotent job (via containerd-rootless.sh, which needs no systemd;
// Alpine is OpenRC). This script only stages the engine + its OS prerequisites:
//
//   - the pinned engine bundle (nerdctl-full = containerd/nerdctl/runc/rootlesskit/buildkit, all
//     musl-static and verified to exec on Alpine) is untarred from the READ-ONLY /safeslop-engine
//     mount — the engine is NEVER fetched over the network (graft #1);
//   - cosign (pinned) is installed for the image-signature policy;
//   - the rootless OS prerequisites that the minimal Alpine guest lacks (fuse-overlayfs for the
//     rootless snapshotter, shadow-subids for newuidmap/newgidmap) come from Alpine's
//     CRYPTOGRAPHICALLY-SIGNED apk repositories — a verified supply channel (apk-tools checks the
//     package signature against /etc/apk/keys), categorically unlike an unverified `curl | sh`. The
//     invariant gate permits `apk` for exactly this reason while still banning curl/wget/http fetches.
//     (Staging these as pinned offline .apk blobs is a later hardening; noted in specs/0044.)
//   - subuid/subgid ranges let rootless containerd map a uid range (newuidmap).
func engineProvisionScript(user string) string {
	return fmt.Sprintf(`#!/bin/sh
set -eu
# Pinned engine bundle from the read-only staged dir (the engine is never fetched over the network).
tar -C /usr/local -xzf /safeslop-engine/nerdctl-full.tar.gz
install -m 0755 /safeslop-engine/cosign /usr/local/bin/cosign
# Rootless OS prerequisites from Alpine's SIGNED repos (a verified channel, not an unverified fetch).
apk add --no-progress fuse-overlayfs shadow-subids
# Subordinate id ranges so rootless containerd can map a uid range (newuidmap/newgidmap).
echo "%s:100000:65536" > /etc/subuid
echo "%s:100000:65536" > /etc/subgid
`, user, user)
}

// renderLimaYAML produces the owned, hardened instance config. mountType virtiofs; the engine-staging
// dir is mounted READ-ONLY (the only non-workspace mount — safe, holds only pinned artifacts); $HOME is
// never mounted; networks empty (vz user-mode NAT). Lima's own containerd is left OFF (system:false,
// user:false) because lima's built-in containerd installer does not support Alpine — safeslop brings up
// rootless containerd itself from the staged bundle (validated live 2026-06-22).
func renderLimaYAML(in LimaConfigInput) ([]byte, error) {
	cfg := limaConfig{
		VMType:     "vz",
		VMOpts:     limaVMOpts{VZ: limaVZ{Rosetta: limaRosetta{Enabled: true, BinFmt: true}}},
		Images:     []limaImage{{Location: in.ImageLocation, Arch: in.ImageArch, Digest: in.ImageDigest}},
		MountType:  "virtiofs",
		Mounts:     []limaMount{{Location: in.EngineStage, MountPoint: engineStageMountPoint, Writable: false}}, // read-only engine staging at /safeslop-engine
		Networks:   []limaNetwork{},
		SSH:        limaSSH{ForwardAgent: false},
		Containerd: limaContainerd{System: false, User: false}, // lima manages none; safeslop runs rootless itself
		Provision:  []limaProvision{{Mode: "system", Script: engineProvisionScript(in.User)}},
	}
	if in.Workspace != "" {
		cfg.Mounts = append(cfg.Mounts, limaMount{Location: in.Workspace, Writable: true})
	}
	b, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("render lima yaml: %w", err)
	}
	if err := assertLimaInvariants(b); err != nil {
		return nil, err // never hand back a config that fails the gate
	}
	return b, nil
}

// assertLimaInvariants fails closed unless the rendered config holds every hardening property. It runs
// before any `limactl start` and re-runs after a limactl version bump, so a schema drift trips at start,
// not at escape time (mirrors install.ValidateDesired / slop-pinning).
func assertLimaInvariants(yamlBytes []byte) error {
	var cfg limaConfig
	if err := yaml.Unmarshal(yamlBytes, &cfg); err != nil {
		return fmt.Errorf("lima invariants: unparseable config: %w", err)
	}
	if cfg.VMType != "vz" {
		return fmt.Errorf("lima invariants: vmType must be vz, got %q", cfg.VMType)
	}
	// Lima must NOT run a rootful system containerd. (user is left false too — lima's containerd
	// installer doesn't support Alpine — and safeslop brings up rootless containerd itself; the
	// rootless guarantee is enforced at bring-up, via containerd-rootless.sh, not by this field.)
	if cfg.Containerd.System {
		return fmt.Errorf("lima invariants: lima must not run a rootful system containerd (containerd.system must be false)")
	}
	if len(cfg.Networks) != 0 {
		return fmt.Errorf("lima invariants: networks must be empty (user-mode NAT only; no root socket_vmnet)")
	}
	if len(cfg.Images) == 0 {
		return fmt.Errorf("lima invariants: no image declared")
	}
	for _, img := range cfg.Images {
		if img.Location == "" || strings.Contains(img.Location, "://") {
			return fmt.Errorf("lima invariants: image location must be a local path, got %q", img.Location)
		}
		if img.Digest == "" {
			return fmt.Errorf("lima invariants: image %q must carry a digest", img.Location)
		}
	}
	for _, m := range cfg.Mounts {
		if isForbiddenMount(m.Location) {
			return fmt.Errorf("lima invariants: forbidden mount location %q ($HOME / root / home are never mounted)", m.Location)
		}
	}
	for _, p := range cfg.Provision {
		// The ENGINE must come from the read-only staged mount, never the network. `apk` is permitted
		// (Alpine's signed repos — a verified channel; see engineProvisionScript), but unverified fetch
		// tokens are banned outright.
		for _, bad := range []string{"curl", "wget", "apt-get", "apt ", "get.docker.com", "http://", "https://"} {
			if strings.Contains(p.Script, bad) {
				return fmt.Errorf("lima invariants: provision script must not fetch over the network (found %q)", bad)
			}
		}
		if strings.Contains(p.Script, "tar ") && !strings.Contains(p.Script, "/safeslop-engine/") {
			return fmt.Errorf("lima invariants: provision must install the engine from the staged /safeslop-engine mount, not elsewhere")
		}
	}
	return nil
}

// isForbiddenMount reports whether a mount location is a broad/home path that must never be shared into
// the guest (the reverse-sshfs $HOME-mount escape). A read-only engine-staging dir or a scoped workspace
// under other paths is allowed; "~", "$HOME", the home dir itself, and "/" are not.
func isForbiddenMount(loc string) bool {
	clean := filepath.Clean(loc)
	if loc == "" || loc == "~" || loc == "/" || strings.HasPrefix(loc, "~") || strings.HasPrefix(loc, "$HOME") {
		return true
	}
	// A bare home dir (…/Users/<x> or /home/<x>) is forbidden; deeper paths under it are fine.
	parts := strings.Split(strings.Trim(clean, "/"), "/")
	if len(parts) == 2 && (parts[0] == "Users" || parts[0] == "home") {
		return true
	}
	return false
}
