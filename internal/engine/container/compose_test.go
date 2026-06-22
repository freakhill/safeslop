package container

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/container/runtime"
)

func TestComposeIsNetworkEnforcedAndLeakFree(t *testing.T) {
	yml, err := renderCompose(composeParams{RuntimeDir: "/rt", Workspace: "/ws", StageDir: "/st", Term: "xterm", NpmConfig: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(yml, "internal: true") {
		t.Fatal("agent network is not internal-only — egress not enforced")
	}
	if !strings.Contains(yml, `entrypoint: ["/bin/sh", "/safeslop/runtime/entrypoint.sh"]`) {
		t.Fatal("entrypoint (secret loader) missing")
	}
	if !strings.Contains(yml, "/ws:/workspace:rw") || !strings.Contains(yml, "/st:/safeslop/runtime:ro") {
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
	got := composeRunArgv(runtime.HostDockerEngine{}, "/rt/compose.yml", []string{"fish"})
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

// TestComposeNetworksByBackend pins the egress-isolation fix: the host backend declares the internal
// network inline (`internal: true`, which rootful docker honors), while the lima backend references a
// pre-created external `--internal` network (rootless nerdctl does NOT honor inline internal:true, so the
// agent would otherwise reach the internet directly — validated 2026-06-22).
func TestComposeNetworksByBackend(t *testing.T) {
	host, err := renderCompose(composeParams{RuntimeDir: "/rt", Workspace: "/ws", StageDir: "/st"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(host, "internal: true") {
		t.Error("host backend must declare internal: true inline")
	}
	if strings.Contains(host, "external: true") {
		t.Error("host backend must NOT use an external network")
	}

	lima, err := renderCompose(composeParams{RuntimeDir: "/rt", Workspace: "/ws", StageDir: "/st", InternalNet: "safeslop-internal"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(lima, "external: true") || !strings.Contains(lima, "name: safeslop-internal") {
		t.Errorf("lima backend must reference the external --internal network, got:\n%s", lima)
	}
	if strings.Contains(lima, "internal: true") {
		t.Error("lima backend must NOT use compose's inline internal:true (it leaks egress under rootless nerdctl)")
	}
}

// TestComposeRunArgvLimaWrapsInGuest pins that the lima engine routes the same compose run through
// `limactl shell <inst> … nerdctl …` — the tier code is unchanged, only the engine differs.
func TestComposeRunArgvLimaWrapsInGuest(t *testing.T) {
	eng := runtime.LimaNerdctlEngine{Limactl: "/b/limactl", Instance: "safeslop", UID: 501, LimaHome: "/h"}
	got := strings.Join(composeRunArgv(eng, "/rt/compose.yml", []string{"fish"}), " ")
	for _, want := range []string{"limactl shell safeslop", "nerdctl compose -f /rt/compose.yml run --rm agent fish"} {
		if !strings.Contains(got, want) {
			t.Fatalf("lima compose argv missing %q: %s", want, got)
		}
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

// In deny/allowlist mode the agent is internal-only (squid is the sole egress, so the
// allowlist is enforced). In open-egress mode the agent ALSO joins the egress bridge so
// it has a real route + working DNS (ping/ssh/direct resolution) — otherwise "network:
// allow" is misleadingly limited to HTTP(S)-via-proxy and DNS fails entirely.
func TestComposeOpenEgressJoinsAgentToBridge(t *testing.T) {
	deny, err := renderCompose(composeParams{RuntimeDir: "/r", Workspace: "/w", StageDir: "/r", OpenEgress: false})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(deny, "networks: [internal]") {
		t.Fatalf("deny: agent must stay internal-only (squid is the sole egress):\n%s", deny)
	}
	if n := strings.Count(deny, "networks: [internal, egress]"); n != 1 {
		t.Fatalf("deny: only the proxy joins egress, got %d such lines:\n%s", n, deny)
	}

	open, err := renderCompose(composeParams{RuntimeDir: "/r", Workspace: "/w", StageDir: "/r", OpenEgress: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(open, "networks: [internal]") {
		t.Fatalf("open: agent must also join the egress bridge, not stay internal-only:\n%s", open)
	}
	if n := strings.Count(open, "networks: [internal, egress]"); n != 2 {
		t.Fatalf("open: proxy + agent must both be on egress, got %d such lines:\n%s", n, open)
	}
}

func TestRenderComposeKubeconfig(t *testing.T) {
	with, err := renderCompose(composeParams{RuntimeDir: "/r", Workspace: "/w", StageDir: "/r", Kubeconfig: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(with, "KUBECONFIG: /safeslop/runtime/kubeconfig") {
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

// secretEnv carries only true secrets; KUBECONFIG (a non-secret path) is delivered via
// the compose env, never written into secrets.env. Pin that invariant.
func TestSecretsEnvExcludesKubeconfig(t *testing.T) {
	dir := t.TempDir()
	if _, err := writeSecretsEnv(dir, []string{"AWS_ACCESS_KEY_ID=AKIA"}); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(filepath.Join(dir, "secrets.env"))
	if strings.Contains(string(body), "KUBECONFIG") {
		t.Fatalf("KUBECONFIG must never ride secrets.env:\n%s", body)
	}
}

func TestComposeNoAgentSocketAndSshKey(t *testing.T) {
	with, err := renderCompose(composeParams{RuntimeDir: "/r", Workspace: "/w", StageDir: "/r", SshKey: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(with, "SSH_AUTH_SOCK") || strings.Contains(with, "ssh-agent.sock") {
		t.Fatalf("agent socket must be gone from compose:\n%s", with)
	}
	if !strings.Contains(with, "GIT_SSH_COMMAND: ssh -i /safeslop/runtime/.ssh/id") {
		t.Fatalf("compose missing GIT_SSH_COMMAND:\n%s", with)
	}
	without, err := renderCompose(composeParams{RuntimeDir: "/r", Workspace: "/w", StageDir: "/r", SshKey: false})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(without, "GIT_SSH_COMMAND") {
		t.Fatalf("GIT_SSH_COMMAND must be absent when no ssh key staged:\n%s", without)
	}
}
