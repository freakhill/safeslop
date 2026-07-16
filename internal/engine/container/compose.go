package container

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/freakhill/safeslop/internal/engine/container/runtime"
)

// composeParams fills compose.yml.tmpl. RuntimeDir holds the rendered squid.conf +
// allowlist.domains; StageDir (== RuntimeDir in Launch) is bind-mounted ro at /safeslop/runtime.
type composeParams struct {
	RuntimeDir    string
	Workspace     string
	StageDir      string
	SessionID     string // stable label value for record-independent reap (session id, or profile name for direct run)
	AgentImage    string // content-addressed agent image tag (local/safeslop-tools:<id>) -> compose image:
	GitConfig     bool   // true when staged .gitconfig exists (GIT_CONFIG_GLOBAL -> bind-mount path)
	GitConfigPath string // path to the in-boundary gitconfig, usually /safeslop/runtime/.gitconfig
	GitSSHConfig  bool   // true when staged .ssh/config.container exists (GIT_SSH_COMMAND -> bind-mount path)
	NpmConfig     bool   // true when a staged .npmrc exists
	Kubeconfig    bool   // true when a staged kubeconfig exists (KUBECONFIG -> bind-mount path)
	OpenEgress    bool   // true in network:allow -> agent joins egress bridge with real route + DNS; false pins external DNS to loopback
	// InternalNet, when set, is the name of an externally pre-created `--internal` network the compose
	// references instead of declaring `internal: true` inline. The lima/rootless-nerdctl backend MUST set
	// it (compose's inline internal:true does not isolate egress there); the host docker backend leaves it
	// empty (rootful docker honors internal:true). See compose.yml.tmpl.
	InternalNet string
	// Egress is the extra allowlist domains (the agent's built-in providers + the profile's `egress:`,
	// already unioned by the caller) appended to the base allowlist asset when the per-run
	// allowlist.domains is materialized. Empty => base allowlist only (specs/0046).
	Egress []string
	// Projection is the resolved safe-host-projection manifest (specs/0096). When non-nil, present
	// entries are rendered as read-only bind mounts under opaque /safeslop/projected/<id> paths and
	// projection.json/projection.tsv are written into RuntimeDir for the entrypoint's copy step. Nil
	// => no projection (the default for profiles without projection).
	Projection *ProjectionManifest
	// SessionGrants are the operator-invoked session egress grants (specs/0097) rendered into the
	// squid session-grants.conf overlay include. Empty => a comment-only file (the include + bind
	// mount are unconditional, so the file must always exist at compose-up).
	SessionGrants []SessionGrant
}

func renderCompose(p composeParams) (string, error) {
	if err := validateProjectionSnapshotMounts(p.StageDir, p.Projection); err != nil {
		return "", err
	}
	raw, err := readAsset("compose.yml.tmpl")
	if err != nil {
		return "", err
	}
	t, err := template.New("compose").Funcs(template.FuncMap{
		// present yields only the bind-mounted projection entries (skipped-absent/unreadable
		// entries stay in projection.json for legibility but get no volume).
		"present": func(m *ProjectionManifest) []ProjectionMount {
			if m == nil {
				return nil
			}
			return m.PresentMounts()
		},
	}).Parse(string(raw))
	if err != nil {
		return "", err
	}
	var b bytes.Buffer
	if err := t.Execute(&b, p); err != nil {
		return "", err
	}
	return b.String(), nil
}

func validateProjectionSnapshotMounts(stageDir string, manifest *ProjectionManifest) error {
	if manifest == nil {
		return nil
	}
	root := filepath.Clean(filepath.Join(stageDir, "projection-snapshots"))
	for _, mount := range manifest.PresentMounts() {
		rel, err := filepath.Rel(root, filepath.Clean(mount.Host))
		if err != nil || escapesRoot(rel) || filepath.Dir(rel) != "." || rel == "." {
			return projectionBoundaryError()
		}
	}
	return nil
}

// writeSecretsEnv writes shell-escaped KEY='VAL' lines (0600) to stageDir/secrets.env so
// entrypoint.sh can source them. Returns the path ("" when there are no secrets). Single
// quotes in values are escaped POSIX-style ('\”).
func writeSecretsEnv(stageDir string, secretEnv []string) (string, error) {
	if len(secretEnv) == 0 {
		return "", nil
	}
	var b strings.Builder
	for _, kv := range secretEnv {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		b.WriteString(k)
		b.WriteString("='")
		b.WriteString(strings.ReplaceAll(v, "'", `'\''`))
		b.WriteString("'\n")
	}
	path := filepath.Join(stageDir, "secrets.env")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// composeRunArgv builds the engine's `compose -f <file> run --rm agent <argv...>` invocation (docker on
// the host, or `limactl shell <inst> … nerdctl …` for lima). There is NO -e: secrets ride secrets.env
// (sourced by the entrypoint), non-secret env lives in the compose file. The result is driven through a
// PTY (RunInPTY) — the interactive terminal passes through limactl shell (validated 2026-06-22).
func composeRunArgv(eng runtime.Engine, composeFile string, argv []string) []string {
	return eng.Argv(append([]string{"compose", "-f", composeFile, "run", "--rm", "agent"}, argv...)...)
}

// writeEntrypoint copies the embedded entrypoint.sh into dir (mode 0755).
func writeEntrypoint(dir string) error {
	b, err := readAsset("entrypoint.sh")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "entrypoint.sh"), b, 0o755)
}
