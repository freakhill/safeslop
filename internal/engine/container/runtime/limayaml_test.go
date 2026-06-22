package runtime

import (
	"strings"
	"testing"

	yaml "go.yaml.in/yaml/v3"
)

func goodInput() LimaConfigInput {
	return LimaConfigInput{
		ImageLocation: "/Users/x/.cache/safeslop/lima-guest-image",
		ImageDigest:   "sha512:7ef845",
		ImageArch:     "aarch64",
		EngineStage:   "/Users/x/.cache/safeslop/engine-stage",
		User:          "x",
	}
}

func TestRenderLimaYAMLIsHardened(t *testing.T) {
	b, err := renderLimaYAML(goodInput())
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	var cfg limaConfig
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		t.Fatalf("rendered YAML unparseable: %v", err)
	}
	if cfg.VMType != "vz" {
		t.Errorf("vmType must be vz, got %q", cfg.VMType)
	}
	// Lima runs no rootful system containerd; safeslop brings up rootless containerd itself.
	if cfg.Containerd.System {
		t.Errorf("lima must run no rootful system containerd, got %+v", cfg.Containerd)
	}
	// Rosetta is under vmOpts.vz (the 2.x location), not the deprecated top-level key.
	if !cfg.VMOpts.VZ.Rosetta.Enabled {
		t.Errorf("rosetta must be enabled under vmOpts.vz, got %+v", cfg.VMOpts)
	}
	if len(cfg.Networks) != 0 {
		t.Errorf("networks must be empty, got %+v", cfg.Networks)
	}
	if len(cfg.Images) != 1 || cfg.Images[0].Digest == "" || strings.Contains(cfg.Images[0].Location, "://") {
		t.Errorf("image must be local + digested, got %+v", cfg.Images)
	}
	// No $HOME mount; the only mount is the read-only engine stage.
	for _, m := range cfg.Mounts {
		if m.Writable && m.Location == cfg.Mounts[0].Location {
			t.Errorf("engine staging mount must be read-only, got %+v", m)
		}
	}
	// The engine is staged, never fetched: no unverified-fetch tokens; the bundle comes from the mount.
	for _, p := range cfg.Provision {
		for _, bad := range []string{"curl", "wget", "get.docker.com", "http://", "https://"} {
			if strings.Contains(p.Script, bad) {
				t.Errorf("provision script must not %q", bad)
			}
		}
		if strings.Contains(p.Script, "tar ") && !strings.Contains(p.Script, "/safeslop-engine/") {
			t.Errorf("engine must be installed from the staged mount")
		}
	}
}

func TestRenderRejectsWorkspaceThatIsHome(t *testing.T) {
	in := goodInput()
	in.Workspace = "/Users/x" // a bare home dir as workspace — must be rejected by the gate
	if _, err := renderLimaYAML(in); err == nil {
		t.Fatal("render must reject a $HOME workspace mount")
	}
}

func TestAssertInvariantsRejectsEachRelaxation(t *testing.T) {
	base := func() limaConfig {
		return limaConfig{
			VMType:     "vz",
			Images:     []limaImage{{Location: "/c/img", Arch: "aarch64", Digest: "sha512:ab"}},
			Mounts:     []limaMount{{Location: "/c/engine", Writable: false}},
			Networks:   []limaNetwork{},
			Containerd: limaContainerd{System: false, User: false}, // lima manages none; safeslop runs rootless
			Provision:  []limaProvision{{Mode: "system", Script: "tar -xzf /safeslop-engine/x.tgz\napk add fuse-overlayfs"}},
		}
	}
	must := func(b []byte) []byte { return b }
	marshal := func(c limaConfig) []byte { b, _ := yaml.Marshal(c); return must(b) }

	// Clean config passes — note it carries `apk add`, which is permitted (Alpine's signed channel).
	if err := assertLimaInvariants(marshal(base())); err != nil {
		t.Fatalf("clean config must pass: %v", err)
	}

	cases := map[string]func(c *limaConfig){
		"qemu vmType":          func(c *limaConfig) { c.VMType = "qemu" },
		"rootful containerd":   func(c *limaConfig) { c.Containerd.System = true },
		"non-empty networks":   func(c *limaConfig) { c.Networks = []limaNetwork{{Lima: "shared"}} },
		"remote image":         func(c *limaConfig) { c.Images[0].Location = "https://cdn/img.iso" },
		"image without digest": func(c *limaConfig) { c.Images[0].Digest = "" },
		"home mount":           func(c *limaConfig) { c.Mounts = append(c.Mounts, limaMount{Location: "/Users/x", Writable: true}) },
		"tilde mount":          func(c *limaConfig) { c.Mounts = append(c.Mounts, limaMount{Location: "~", Writable: true}) },
		"provision curl":       func(c *limaConfig) { c.Provision[0].Script = "curl https://x | sh" },
		"provision wget":       func(c *limaConfig) { c.Provision[0].Script = "wget http://x/engine" },
		"provision get.docker": func(c *limaConfig) { c.Provision[0].Script = "sh get.docker.com" },
		"engine not staged":    func(c *limaConfig) { c.Provision[0].Script = "tar -xzf /tmp/engine.tgz" },
	}
	for name, mutate := range cases {
		c := base()
		mutate(&c)
		if err := assertLimaInvariants(marshal(c)); err == nil {
			t.Errorf("invariant gate must reject: %s", name)
		}
	}
}
