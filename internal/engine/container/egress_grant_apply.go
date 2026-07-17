package container

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/freakhill/safeslop/internal/engine/container/runtime"
)

var ErrEgressGenerationUncertain = errors.New("proxy egress generation is uncertain")

type overlayOptions struct {
	beforeInstall func(string) error
}

// OverlayOption remains a hermetic filesystem-fault seam. Production passes no
// options; runtime/network behavior is exercised through runtime.Engine.
type OverlayOption func(*overlayOptions)

func WithOverlayTestHook(hook func(path string) error) OverlayOption {
	return func(options *overlayOptions) { options.beforeInstall = hook }
}

// ApplySessionGrants is the compatibility front for existing callers. New
// transactional callers use ApplyEgressGeneration with the durable revision.
func ApplySessionGrants(ctx context.Context, eng runtime.Engine, composeFile, runtimeDir string, grants []SessionGrant, opts ...OverlayOption) error {
	_, err := ApplyEgressGeneration(ctx, eng, composeFile, runtimeDir, grants, 0, opts...)
	return err
}

// ApplyEgressGeneration atomically installs candidate bytes inside a directory
// bind, force-replaces the sole proxy, and returns only after an inspectable
// generation/hash ACK. It never treats a signal/reconfigure exit as an ACK.
func ApplyEgressGeneration(ctx context.Context, eng runtime.Engine, composeFile, runtimeDir string, grants []SessionGrant, revision int, opts ...OverlayOption) (EgressGeneration, error) {
	generation, body, err := BuildEgressGeneration(grants, revision)
	if err != nil {
		return EgressGeneration{}, err
	}
	options := overlayOptions{}
	for _, option := range opts {
		option(&options)
	}
	overrideFile, err := installEgressGeneration(runtimeDir, generation, body, options)
	if err != nil {
		return EgressGeneration{}, err
	}
	files := []string{overrideFile}
	upArgs, err := composeProjectArgsWithOverrides(composeFile, files, "up", "-d", "--no-deps", "--force-recreate", "proxy")
	if err != nil {
		return EgressGeneration{}, ErrEgressGenerationUncertain
	}
	if err := runEngine(ctx, eng, upArgs...); err != nil {
		return EgressGeneration{}, ErrEgressGenerationUncertain
	}
	if err := waitForProxyGeneration(ctx, eng, composeFile, files, generation); err != nil {
		return EgressGeneration{}, ErrEgressGenerationUncertain
	}
	return generation, nil
}

// EnsureEgressGeneration avoids a connection-dropping replacement when the
// running proxy already positively acknowledges the desired bytes.
func EnsureEgressGeneration(ctx context.Context, eng runtime.Engine, composeFile, runtimeDir string, grants []SessionGrant, revision int, opts ...OverlayOption) (EgressGeneration, error) {
	wanted, _, err := BuildEgressGeneration(grants, revision)
	if err != nil {
		return EgressGeneration{}, err
	}
	if current, err := InspectEgressGeneration(ctx, eng, composeFile); err == nil && current == wanted {
		return current, nil
	}
	return ApplyEgressGeneration(ctx, eng, composeFile, runtimeDir, grants, revision, opts...)
}

func InspectEgressGeneration(ctx context.Context, eng runtime.Engine, composeFile string) (EgressGeneration, error) {
	if err := waitForProxyFiles(ctx, eng, composeFile, nil); err != nil {
		return EgressGeneration{}, ErrEgressGenerationUncertain
	}
	generation, err := inspectProxyGeneration(ctx, eng, composeFile, nil)
	if err != nil {
		return EgressGeneration{}, ErrEgressGenerationUncertain
	}
	return generation, nil
}

func installEgressGeneration(runtimeDir string, generation EgressGeneration, body []byte, options overlayOptions) (string, error) {
	overlayDir := filepath.Join(runtimeDir, "proxy-overlay")
	if err := os.MkdirAll(overlayDir, 0o700); err != nil {
		return "", err
	}
	overlayPath := filepath.Join(overlayDir, "session-grants.conf")
	if options.beforeInstall != nil {
		if err := options.beforeInstall(overlayPath); err != nil {
			return "", fmt.Errorf("write session grants overlay: %w", err)
		}
	}
	if err := replaceSyncedFile(overlayPath, body, 0o600); err != nil {
		return "", fmt.Errorf("write session grants overlay: %w", err)
	}
	if err := installOverlaySquidInclude(runtimeDir); err != nil {
		return "", err
	}
	override, err := renderEgressOverride(overlayDir, generation)
	if err != nil {
		return "", err
	}
	overridePath := filepath.Join(runtimeDir, "egress.override.yml")
	if err := replaceSyncedFile(overridePath, []byte(override), 0o600); err != nil {
		return "", err
	}
	return overridePath, nil
}

func installOverlaySquidInclude(runtimeDir string) error {
	path := filepath.Join(runtimeDir, "squid.conf")
	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	const oldInclude = "include /etc/squid/session-grants.conf"
	const newInclude = "include /etc/squid/safeslop.d/session-grants.conf"
	text := string(body)
	if strings.Contains(text, newInclude) {
		return nil
	}
	if !strings.Contains(text, oldInclude) {
		return fmt.Errorf("proxy configuration has no reviewed session overlay include")
	}
	text = strings.Replace(text, oldInclude, newInclude, 1)
	return replaceSyncedFile(path, []byte(text), 0o600)
}

func renderEgressOverride(overlayDir string, generation EgressGeneration) (string, error) {
	source, err := yamlScalar(overlayDir)
	if err != nil {
		return "", err
	}
	hash, err := yamlScalar(generation.Hash)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`services:
  proxy:
    labels:
      safeslop.egress-revision: %q
      safeslop.egress-hash: %s
    volumes:
      - type: bind
        source: %s
        target: /etc/squid/safeslop.d
        read_only: true
        bind:
          create_host_path: false
`, strconv.Itoa(generation.Revision), hash, source), nil
}

func replaceSyncedFile(path string, body []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".egress-tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := io.Copy(tmp, bytes.NewReader(body)); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	directory, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func waitForProxyGeneration(ctx context.Context, eng runtime.Engine, composeFile string, overrides []string, wanted EgressGeneration) error {
	readyCtx, cancel := context.WithTimeout(ctx, proxyReadyTimeout)
	defer cancel()
	for {
		if err := waitForProxyFiles(readyCtx, eng, composeFile, overrides); err == nil {
			if current, err := inspectProxyGeneration(readyCtx, eng, composeFile, overrides); err == nil && current == wanted {
				return nil
			}
		}
		select {
		case <-readyCtx.Done():
			return readyCtx.Err()
		case <-time.After(proxyReadyInterval):
		}
	}
}

func waitForProxyFiles(ctx context.Context, eng runtime.Engine, composeFile string, overrides []string) error {
	args, err := composeProjectArgsWithOverrides(composeFile, overrides, "exec", "-T", "proxy", "bash", "-ec", proxyReadyCommand)
	if err != nil {
		return err
	}
	return eng.Command(ctx, args...).Run()
}

func sameRuntimeObjectID(a, b string) bool {
	// `docker ps -q` emits the daemon's unique short ID while Compose may emit
	// the full ID. The project-wide count above still proves sole ownership.
	return a == b || strings.HasPrefix(a, b) || strings.HasPrefix(b, a)
}

func inspectProxyGeneration(ctx context.Context, eng runtime.Engine, composeFile string, overrides []string) (EgressGeneration, error) {
	psArgs, err := composeProjectArgsWithOverrides(composeFile, overrides, "ps", "--status", "running", "-q", "proxy")
	if err != nil {
		return EgressGeneration{}, err
	}
	ids, err := eng.Command(ctx, psArgs...).Output()
	if err != nil {
		return EgressGeneration{}, err
	}
	lines := strings.Fields(string(ids))
	if len(lines) != 1 {
		return EgressGeneration{}, fmt.Errorf("proxy instance count is not one")
	}
	file, err := filepath.Abs(composeFile)
	if err != nil {
		return EgressGeneration{}, err
	}
	project := filepath.Base(filepath.Dir(file))
	if !composeProjectPattern.MatchString(project) {
		return EgressGeneration{}, fmt.Errorf("runtime identity is not a valid Compose project name")
	}
	projectIDs, err := eng.Command(ctx, "ps", "-q", "--filter", "label=com.docker.compose.project="+project, "--filter", "label=com.docker.compose.service=proxy").Output()
	if err != nil {
		return EgressGeneration{}, err
	}
	allProxies := strings.Fields(string(projectIDs))
	if len(allProxies) != 1 || !sameRuntimeObjectID(allProxies[0], lines[0]) {
		return EgressGeneration{}, fmt.Errorf("proxy instance count is not one")
	}
	format := `{{ index .Config.Labels "safeslop.egress-revision" }} {{ index .Config.Labels "safeslop.egress-hash" }}`
	labels, err := eng.Command(ctx, "inspect", "-f", format, lines[0]).Output()
	if err != nil {
		return EgressGeneration{}, err
	}
	fields := strings.Fields(string(labels))
	if len(fields) != 2 {
		return EgressGeneration{}, fmt.Errorf("proxy generation labels are missing")
	}
	revision, err := strconv.Atoi(fields[0])
	if err != nil || revision < 0 || len(fields[1]) != 64 {
		return EgressGeneration{}, fmt.Errorf("proxy generation labels are invalid")
	}
	hashArgs, err := composeProjectArgsWithOverrides(composeFile, overrides, "exec", "-T", "proxy", "sha256sum", "/etc/squid/safeslop.d/session-grants.conf")
	if err != nil {
		return EgressGeneration{}, err
	}
	hashOutput, err := eng.Command(ctx, hashArgs...).Output()
	if err != nil {
		return EgressGeneration{}, err
	}
	hashFields := strings.Fields(string(hashOutput))
	if len(hashFields) < 1 || hashFields[0] != fields[1] {
		return EgressGeneration{}, fmt.Errorf("proxy overlay hash does not match its label")
	}
	return EgressGeneration{Revision: revision, Hash: fields[1]}, nil
}
