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
type limaConfig struct {
	VMType     string          `yaml:"vmType"`
	Rosetta    limaRosetta     `yaml:"rosetta"`
	Images     []limaImage     `yaml:"images"`
	MountType  string          `yaml:"mountType"`
	Mounts     []limaMount     `yaml:"mounts"`
	Networks   []limaNetwork   `yaml:"networks"`
	SSH        limaSSH         `yaml:"ssh"`
	Containerd limaContainerd  `yaml:"containerd"`
	Provision  []limaProvision `yaml:"provision"`
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
	Location string `yaml:"location"`
	Writable bool   `yaml:"writable"`
}
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
// digest), the read-only engine-staging dir (holds the pinned nerdctl-full/cosign tarballs), and an
// optional opted-in workspace to mount writable.
type LimaConfigInput struct {
	ImageLocation string // absolute local path under CacheDir (the pinned .iso) — never a URL
	ImageDigest   string // "sha512:<hex>" | "sha256:<hex>"
	ImageArch     string // "aarch64"
	EngineStage   string // absolute local dir mounted READ-ONLY; the pinned engine tarballs live here
	Workspace     string // optional; "" = no workspace mount (default)
}

// engineProvisionScript installs the rootless engine from the LOCAL staged tarballs only — it must
// reference no network (no curl/apt/wget/http); the staging dir is mounted read-only at /safeslop-engine.
const engineProvisionScript = `#!/bin/sh
set -eu
# Install the pinned engine bundle from the read-only staged dir (no network fetch).
tar -C /usr/local -xzf /safeslop-engine/nerdctl-full.tar.gz
install -m 0755 /safeslop-engine/cosign /usr/local/bin/cosign
# rootless containerd is enabled via containerd.user; nothing here reaches the internet.
`

// renderLimaYAML produces the owned, hardened instance config. mountType virtiofs; the engine-staging
// dir is mounted READ-ONLY (the only non-workspace mount — safe, holds only pinned artifacts); $HOME is
// never mounted; networks empty (vz user-mode NAT); rootless containerd.
func renderLimaYAML(in LimaConfigInput) ([]byte, error) {
	cfg := limaConfig{
		VMType:     "vz",
		Rosetta:    limaRosetta{Enabled: true, BinFmt: true},
		Images:     []limaImage{{Location: in.ImageLocation, Arch: in.ImageArch, Digest: in.ImageDigest}},
		MountType:  "virtiofs",
		Mounts:     []limaMount{{Location: in.EngineStage, Writable: false}}, // read-only engine staging
		Networks:   []limaNetwork{},
		SSH:        limaSSH{ForwardAgent: false},
		Containerd: limaContainerd{System: false, User: true}, // rootless
		Provision:  []limaProvision{{Mode: "system", Script: engineProvisionScript}},
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
	if cfg.Containerd.System || !cfg.Containerd.User {
		return fmt.Errorf("lima invariants: engine must be rootless (containerd.user=true, system=false)")
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
		for _, bad := range []string{"curl", "wget", "apt", "apk add", "http://", "https://"} {
			if strings.Contains(p.Script, bad) {
				return fmt.Errorf("lima invariants: provision script must do no network fetch (found %q)", bad)
			}
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
