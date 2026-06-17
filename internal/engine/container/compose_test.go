package container

import (
	"os"
	"strings"
	"testing"
)

func TestComposeIsNetworkEnforcedAndLeakFree(t *testing.T) {
	yml, err := renderCompose(composeParams{RuntimeDir: "/rt", Workspace: "/ws", StageDir: "/st", Term: "xterm", NpmConfig: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(yml, "internal: true") {
		t.Fatal("agent network is not internal-only — egress not enforced")
	}
	if !strings.Contains(yml, `entrypoint: ["/bin/sh", "/slop/runtime/entrypoint.sh"]`) {
		t.Fatal("entrypoint (secret loader) missing")
	}
	if !strings.Contains(yml, "/ws:/workspace:rw") || !strings.Contains(yml, "/st:/slop/runtime:ro") {
		t.Fatalf("mounts missing:\n%s", yml)
	}
	// a secret VALUE must never be written into the compose file.
	if strings.Contains(yml, "ANTHROPIC") {
		t.Fatal("a secret leaked into the compose file")
	}
}

func TestWriteSecretsEnvEscapesAndIs0600(t *testing.T) {
	dir := t.TempDir()
	p, err := writeSecretsEnv(dir, []string{`ANTHROPIC_API_KEY=sk-a'b`})
	if err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(p)
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("secrets.env perm = %v want 0600", fi.Mode().Perm())
	}
	b, _ := os.ReadFile(p)
	if string(b) != "ANTHROPIC_API_KEY='sk-a'\\''b'\n" {
		t.Fatalf("escaping wrong: %q", string(b))
	}
	if got, _ := writeSecretsEnv(dir, nil); got != "" {
		t.Fatal("no secrets should yield no file")
	}
}

func TestComposeRunArgvHasNoDashE(t *testing.T) {
	got := composeRunArgv("/rt/compose.yml", []string{"fish"})
	for _, a := range got {
		if a == "-e" {
			t.Fatal("composeRunArgv must not use -e (secrets leak to ps/inspect)")
		}
	}
	want := []string{"docker", "compose", "-f", "/rt/compose.yml", "run", "--rm", "agent", "fish"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("got %v want %v", got, want)
	}
}

// Cloud creds are delivered as short-lived env vars via the secret channel, NEVER
// by mounting the host's long-lived config. Pin that the compose never references it.
func TestComposeNeverMountsHostCloudConfig(t *testing.T) {
	yml, err := renderCompose(composeParams{RuntimeDir: "/rt", Workspace: "/ws", StageDir: "/st"})
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{".aws", "application_default_credentials", ".config/gcloud"} {
		if strings.Contains(yml, bad) {
			t.Fatalf("compose references host cloud config (%q):\n%s", bad, yml)
		}
	}
}

// The agent must never gain a host bridge — OrbStack/Docker Desktop can otherwise
// route an internal container to the host loopback (host.docker.internal), defeating
// the squid-only egress topology. Pin that none of these ever leak into the compose.
func TestComposeHasNoHostBridgeLeak(t *testing.T) {
	yml, err := renderCompose(composeParams{RuntimeDir: "/rt", Workspace: "/ws", StageDir: "/st"})
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"host.docker.internal", "host-gateway", "extra_hosts", "network_mode: host", `network_mode: "host"`} {
		if strings.Contains(yml, bad) {
			t.Fatalf("compose leaks a host bridge (%q):\n%s", bad, yml)
		}
	}
	if !strings.Contains(yml, "internal: true") {
		t.Fatalf("agent net no longer internal-only:\n%s", yml)
	}
}

func TestRenderComposeKubeconfig(t *testing.T) {
	with, err := renderCompose(composeParams{RuntimeDir: "/r", Workspace: "/w", StageDir: "/r", Kubeconfig: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(with, "KUBECONFIG: /slop/runtime/kubeconfig") {
		t.Fatalf("compose missing KUBECONFIG env:\n%s", with)
	}
	without, err := renderCompose(composeParams{RuntimeDir: "/r", Workspace: "/w", StageDir: "/r", Kubeconfig: false})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(without, "KUBECONFIG") {
		t.Fatalf("KUBECONFIG must be absent when no kubeconfig staged:\n%s", without)
	}
}
