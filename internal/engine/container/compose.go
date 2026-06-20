package container

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

// composeParams fills compose.yml.tmpl. RuntimeDir holds the rendered squid.conf +
// allowlist.domains; StageDir (== RuntimeDir in Launch) is bind-mounted ro at /safeslop/runtime.
type composeParams struct {
	RuntimeDir string
	Workspace  string
	StageDir   string
	SshKey     bool // true when a staged ssh deploy key exists (GIT_SSH_COMMAND -> bind-mount path)
	Term       string
	NpmConfig  bool // true when a staged .npmrc exists
	Kubeconfig bool // true when a staged kubeconfig exists (KUBECONFIG -> bind-mount path)
	OpenEgress bool // true in network:allow -> agent also joins the egress bridge (real route + DNS)
}

func renderCompose(p composeParams) (string, error) {
	raw, err := readAsset("compose.yml.tmpl")
	if err != nil {
		return "", err
	}
	t, err := template.New("compose").Parse(string(raw))
	if err != nil {
		return "", err
	}
	var b bytes.Buffer
	if err := t.Execute(&b, p); err != nil {
		return "", err
	}
	return b.String(), nil
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

// composeRunArgv builds `docker compose -f <file> run --rm agent <argv...>`. There is NO -e:
// secrets ride secrets.env (sourced by the entrypoint), non-secret env lives in the compose file.
func composeRunArgv(composeFile string, argv []string) []string {
	out := []string{"docker", "compose", "-f", composeFile, "run", "--rm", "agent"}
	return append(out, argv...)
}

// writeEntrypoint copies the embedded entrypoint.sh into dir (mode 0755).
func writeEntrypoint(dir string) error {
	b, err := readAsset("entrypoint.sh")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "entrypoint.sh"), b, 0o755)
}
