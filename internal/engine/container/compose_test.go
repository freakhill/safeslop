package container

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/container/runtime"
)

func TestComposeIsNetworkEnforcedAndLeakFree(t *testing.T) {
	yml, err := renderCompose(composeParams{RuntimeDir: "/rt", Workspace: "/ws", StageDir: "/st", NpmConfig: true})
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

func TestComposeGivesAgentWritableEphemeralHome(t *testing.T) {
	yml, err := renderCompose(composeParams{RuntimeDir: "/rt", Workspace: "/ws", StageDir: "/st"})
	if err != nil {
		t.Fatal(err)
	}
	// specs/0064: the rootfs stays read-only; $HOME is a tmpfs so agents can
	// keep runtime state (pi's ~/.pi session store) with no host exposure.
	if !strings.Contains(yml, "read_only: true") {
		t.Fatal("agent rootfs must stay read-only")
	}
	if !strings.Contains(yml, "- /home/agent") {
		t.Fatalf("agent tmpfs home missing (pi state-dir crash, specs/0064):\n%s", yml)
	}
}

func TestComposeHardSetsAgentUser(t *testing.T) {
	yml, err := renderCompose(composeParams{RuntimeDir: "/rt", Workspace: "/ws", StageDir: "/st"})
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(yml, "    user: \"1000:1000\"\n"); n != 1 {
		t.Fatalf("generated compose must hard-set exactly one agent user, got %d:\n%s", n, yml)
	}

	legacyPath := filepath.Join("..", "..", "..", "library", "layer", "container", "docker-compose.yml")
	legacy, err := os.ReadFile(legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(legacy), "    user: \"1000:1000\"\n"); n != 2 {
		t.Fatalf("legacy compose must hard-set agent and agent-tools users, got %d:\n%s", n, legacy)
	}
}

func TestEntrypointPreCreatesAgentStateDirs(t *testing.T) {
	b, err := readAsset("entrypoint.sh")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	// specs/0064: the tmpfs home starts empty and pi's session-store mkdir is
	// non-recursive, so the entrypoint must pre-create the state trees.
	for _, want := range []string{"mkdir -p", ".pi/agent/sessions", ".claude"} {
		if !strings.Contains(s, want) {
			t.Fatalf("entrypoint.sh missing %q (specs/0064)", want)
		}
	}
}

// TestEntrypointProjectionCopyIsSafe pins specs/0096 T4c: the entrypoint reads projection.tsv and
// COPIES staged files into /home/agent, writes a status file, and must never source/. /eval/execute
// projected content (shell/pi-skill config is readable instruction/code authority).
func TestEntrypointProjectionCopyIsSafe(t *testing.T) {
	b, err := readAsset("entrypoint.sh")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, "/safeslop/runtime/projection.tsv") {
		t.Fatal("entrypoint must read projection.tsv when projection is staged")
	}
	if !strings.Contains(s, "projection-status") {
		t.Fatal("entrypoint must write a projection-status file under the ephemeral home")
	}
	// copy-only: projected content must never be sourced/evaluated/executed — only `cp`'d. Assert
	// none of the execution sinks reference a projected staging path or the TSV directly.
	for _, bad := range []string{
		". /safeslop/projected",
		"source /safeslop/projected",
		"sh /safeslop/projected",
		"bash /safeslop/projected",
		". /safeslop/runtime/projection.tsv",
		"source /safeslop/runtime/projection.tsv",
		"sh /safeslop/runtime/projection.tsv",
	} {
		if strings.Contains(s, bad) {
			t.Errorf("entrypoint must never execute projected content (found %q):\n%s", bad, s)
		}
	}
	// the copy primitive itself must be present.
	if !strings.Contains(s, "cp --") {
		t.Errorf("entrypoint must cp (copy) staged projection files:\n%s", s)
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
// `lima nerdctl …` against the user's own default instance — the tier code is unchanged, only the engine
// differs (specs/0066).
func TestComposeRunArgvLimaWrapsInGuest(t *testing.T) {
	got := strings.Join(composeRunArgv(runtime.LimaEngine{}, "/rt/compose.yml", []string{"fish"}), " ")
	want := "lima nerdctl compose -f /rt/compose.yml run --rm agent fish"
	if got != want {
		t.Fatalf("lima compose argv = %q, want %q", got, want)
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
func TestComposeDenyTierPinsDNSLoopback(t *testing.T) {
	deny, err := renderCompose(composeParams{RuntimeDir: "/r", Workspace: "/w", StageDir: "/r", OpenEgress: false})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(deny, "    dns:\n      - 127.0.0.1\n") {
		t.Fatalf("deny-tier agent must pin external DNS forwarding to container loopback:\n%s", deny)
	}
	if n := strings.Count(deny, "    dns:\n"); n != 1 {
		t.Fatalf("deny-tier compose should render one agent DNS pin, got %d:\n%s", n, deny)
	}

	open, err := renderCompose(composeParams{RuntimeDir: "/r", Workspace: "/w", StageDir: "/r", OpenEgress: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(open, "    dns:\n") {
		t.Fatalf("open-egress compose must keep normal runtime DNS:\n%s", open)
	}
}

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

func TestComposeNoAgentSocketAndGitConfig(t *testing.T) {
	with, err := renderCompose(composeParams{RuntimeDir: "/r", Workspace: "/w", StageDir: "/r", GitConfig: true, GitConfigPath: "/safeslop/runtime/.gitconfig", GitSSHConfig: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(with, "SSH_AUTH_SOCK") || strings.Contains(with, "ssh-agent.sock") {
		t.Fatalf("agent socket must be gone from compose:\n%s", with)
	}
	if !strings.Contains(with, "GIT_CONFIG_GLOBAL: /safeslop/runtime/.gitconfig") {
		t.Fatalf("compose missing GIT_CONFIG_GLOBAL:\n%s", with)
	}
	if !strings.Contains(with, "GIT_SSH_COMMAND: ssh -F /safeslop/runtime/.ssh/config.container") {
		t.Fatalf("compose missing container SSH config:\n%s", with)
	}
	if !strings.Contains(with, "GIT_TERMINAL_PROMPT: 0") {
		t.Fatalf("compose must disable interactive git credential prompts when staged git config exists:\n%s", with)
	}
	without, err := renderCompose(composeParams{RuntimeDir: "/r", Workspace: "/w", StageDir: "/r", GitConfig: false, GitSSHConfig: false})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(without, "GIT_CONFIG_GLOBAL") || strings.Contains(without, "GIT_TERMINAL_PROMPT") || strings.Contains(without, "GIT_SSH_COMMAND") {
		t.Fatalf("git config env must be absent when no staged .gitconfig exists:\n%s", without)
	}
}

func TestComposeUsesAgentImage(t *testing.T) {
	yml, err := renderCompose(composeParams{RuntimeDir: "/r", Workspace: "/w", StageDir: "/r", AgentImage: "local/safeslop-tools:deadbeef1234"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(yml, "image: local/safeslop-tools:deadbeef1234") {
		t.Fatalf("agent image not threaded from composeParams.AgentImage:\n%s", yml)
	}
	if strings.Contains(yml, "agent-sandbox-tools:latest") {
		t.Fatalf("stale hardcoded :latest agent image still present:\n%s", yml)
	}
}

func TestComposeLabelsServicesForRecordIndependentReap(t *testing.T) {
	yml, err := renderCompose(composeParams{RuntimeDir: "/r", Workspace: "/w", StageDir: "/r", SessionID: "sess-deadbeef"})
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(yml, `safeslop.session: "sess-deadbeef"`); n != 4 {
		t.Fatalf("services and host-created networks must carry the session label, got %d labels:\n%s", n, yml)
	}
	if n := strings.Count(yml, `safeslop.managed: "true"`); n != 4 {
		t.Fatalf("services and host-created networks must carry the managed label, got %d labels:\n%s", n, yml)
	}
}

func TestComposeForcesTruecolorTerm(t *testing.T) {
	yml, err := renderCompose(composeParams{RuntimeDir: "/r", Workspace: "/w", StageDir: "/r"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(yml, "TERM: xterm-256color") || !strings.Contains(yml, "COLORTERM: truecolor") {
		t.Fatalf("compose must force a truecolor terminal unconditionally:\n%s", yml)
	}
}

// TestComposeProjectionRendersReadOnlyMounts pins specs/0096 T4b: a resolved projection renders
// one read-only bind mount per present file under opaque /safeslop/projected/<id> staging paths,
// preserving the read-only rootfs + tmpfs home + no host-bridge invariants.
func TestComposeProjectionRendersReadOnlyMounts(t *testing.T) {
	yml, err := renderCompose(composeParams{RuntimeDir: "/r", Workspace: "/w", StageDir: "/r", Projection: &ProjectionManifest{
		Items: []ProjectionMount{
			{Host: "/home/u/.pi/agent/AGENTS.md", Container: "/safeslop/projected/0", Target: ".pi/agent/AGENTS.md", Status: projPresent},
			{Host: "/home/u/.zshrc", Container: "/safeslop/projected/1", Target: ".zshrc", Status: projPresent},
			{Host: "/home/u/.config/fish/config.fish", Target: ".config/fish/config.fish", Status: projSkippedAbsent}, // skipped: no mount
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	// exactly two ro projection mounts (present entries only; skipped absent must NOT mount)
	if n := strings.Count(yml, "/safeslop/projected/"); n != 2 {
		t.Fatalf("want 2 projection mounts (present only), got %d:\n%s", n, yml)
	}
	for _, want := range []string{
		"/home/u/.pi/agent/AGENTS.md:/safeslop/projected/0:ro",
		"/home/u/.zshrc:/safeslop/projected/1:ro",
	} {
		if !strings.Contains(yml, want) {
			t.Errorf("missing projection mount %q:\n%s", want, yml)
		}
	}
	if strings.Contains(yml, "config.fish:/safeslop/projected") {
		t.Errorf("skipped-absent entry must not get a mount:\n%s", yml)
	}
	// projection must not weaken container hardening (specs/0096 FLO law #10).
	if !strings.Contains(yml, "read_only: true") || !strings.Contains(yml, "cap_drop: [ALL]") {
		t.Errorf("projection must preserve read-only rootfs + cap_drop:\n%s", yml)
	}
	if strings.Contains(yml, "host.docker.internal") || strings.Contains(yml, "network_mode: host") {
		t.Errorf("projection must not introduce a host bridge:\n%s", yml)
	}
}

// TestComposeNoProjectionIsUnchanged pins that a profile without projection renders no projection
// mounts and no projection section (regression guard, specs/0096).
func TestComposeNoProjectionIsUnchanged(t *testing.T) {
	yml, err := renderCompose(composeParams{RuntimeDir: "/r", Workspace: "/w", StageDir: "/r"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(yml, "/safeslop/projected/") {
		t.Errorf("no-projection compose must not mention projection mounts:\n%s", yml)
	}
}

// TestMaterializeRunAlwaysWritesSessionGrants pins specs/0097 T2: session-grants.conf is written
// unconditionally (comment-only when there are no grants) so the unconditional proxy bind mount +
// squid include always resolve at compose-up; rendered grants are threaded from composeParams.
func TestMaterializeRunAlwaysWritesSessionGrants(t *testing.T) {
	empty := t.TempDir()
	if _, err := materializeRun(composeParams{RuntimeDir: empty, StageDir: empty, Workspace: "/"}, false); err != nil {
		t.Fatal(err)
	}
	b, err := readAssetFile(empty, "session-grants.conf")
	if err != nil {
		t.Fatalf("session-grants.conf must always be written (empty case): %v", err)
	}
	if !strings.HasPrefix(strings.TrimSpace(string(b)), "#") {
		t.Errorf("empty grants must yield a comment-only file:\n%s", b)
	}

	with := t.TempDir()
	if _, err := materializeRun(composeParams{RuntimeDir: with, StageDir: with, Workspace: "/", SessionGrants: []SessionGrant{{Host: "example.com", Port: 443}}}, false); err != nil {
		t.Fatal(err)
	}
	b2, err := readAssetFile(with, "session-grants.conf")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b2), "grant_0_host dstdom_regex -n ^example\\.com$") {
		t.Errorf("session-grants.conf must render the threaded grant:\n%s", b2)
	}

	// compose.yml mounts session-grants.conf into the proxy.
	yml, err := readAssetFile(with, "compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(yml), "session-grants.conf:/etc/squid/session-grants.conf:ro") {
		t.Errorf("compose must bind-mount session-grants.conf into the proxy:\n%s", yml)
	}
}

func readAssetFile(dir, name string) ([]byte, error) {
	return os.ReadFile(dir + "/" + name)
}

// TestMaterializeRunWritesProjectionManifest pins that materializeRun writes projection.json (the
// provenance manifest) and projection.tsv (the entrypoint's shell-friendly copy input) when a
// projection is present, and writes neither when there is none.
func TestMaterializeRunWritesProjectionManifest(t *testing.T) {
	dir := t.TempDir()
	_, err := materializeRun(composeParams{RuntimeDir: dir, Workspace: "/w", StageDir: dir, Projection: &ProjectionManifest{
		Items: []ProjectionMount{
			{Host: "/h/.zshrc", Container: "/safeslop/projected/0", Target: ".zshrc", Status: projPresent, Label: "zsh"},
			{Host: "/h/.config/fish/config.fish", Target: ".config/fish/config.fish", Status: projSkippedAbsent},
		},
	}}, false)
	if err != nil {
		t.Fatal(err)
	}
	pj, err := os.ReadFile(dir + "/projection.json")
	if err != nil {
		t.Fatalf("projection.json not written: %v", err)
	}
	ps := string(pj)
	for _, want := range []string{"\"host\"", "/h/.zshrc", projPresent, projSkippedAbsent, "\"label\": \"zsh\""} {
		if !strings.Contains(ps, want) {
			t.Errorf("projection.json missing %q:\n%s", want, ps)
		}
	}
	tsv, err := os.ReadFile(dir + "/projection.tsv")
	if err != nil {
		t.Fatalf("projection.tsv not written: %v", err)
	}
	if string(tsv) != "/safeslop/projected/0\t/home/agent/.zshrc\n" {
		t.Errorf("projection.tsv must list only the present mount:\n%q", string(tsv))
	}

	// No projection => neither file is written.
	dir2 := t.TempDir()
	if _, err := materializeRun(composeParams{RuntimeDir: dir2, Workspace: "/w", StageDir: dir2}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir2 + "/projection.json"); !os.IsNotExist(err) {
		t.Errorf("projection.json must not be written without a projection")
	}
	if _, err := os.Stat(dir2 + "/projection.tsv"); !os.IsNotExist(err) {
		t.Errorf("projection.tsv must not be written without a projection")
	}
}
