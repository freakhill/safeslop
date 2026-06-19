package container

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	baseTag  = "local/agent-sandbox:latest"
	toolsTag = "local/agent-sandbox-tools:latest"
)

// Reconcile reclaims state a crashed prior run may have left: staged dirs older than maxAge
// carrying the .safeslop-stage marker (wiping any leftover secrets.env). Safe to call on every
// run; idempotent. repo is the workspace root. Orphaned-squid reaping rides the Docker path
// (Up/Down) added in a later task.
func Reconcile(ctx context.Context, repo string, maxAge time.Duration) error {
	_ = ctx
	runtimeRoot := filepath.Join(repo, ".safeslop", "runtime")
	entries, _ := os.ReadDir(runtimeRoot)
	for _, e := range entries {
		dir := filepath.Join(runtimeRoot, e.Name())
		if _, err := os.Stat(filepath.Join(dir, ".safeslop-stage")); err != nil {
			continue // not a safeslop staging dir
		}
		if fi, err := os.Stat(dir); err == nil && time.Since(fi.ModTime()) > maxAge {
			_ = os.RemoveAll(dir) // wipes any leftover secrets.env
		}
	}
	return nil
}

// Available reports whether this host can run the container boundary: docker + Compose v2.
func Available() bool {
	if _, err := exec.LookPath("docker"); err != nil {
		return false
	}
	return exec.CommandContext(context.Background(), "docker", "compose", "version").Run() == nil
}

func imageExists(ctx context.Context, tag string) bool {
	return exec.CommandContext(ctx, "docker", "image", "inspect", tag).Run() == nil
}

func ensureDockerfiles(dir string) error {
	for _, name := range []string{"Dockerfile.agent", "Dockerfile.agent.tools"} {
		b, err := readAsset(name)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func runDocker(ctx context.Context, args ...string) error {
	c := exec.CommandContext(ctx, "docker", args...)
	c.Stdout, c.Stderr = os.Stdout, os.Stderr
	return c.Run()
}

// buildImages builds the base then the tools image (idempotent via docker image inspect),
// from dir as build context. The tools Dockerfile's FROM local/agent-sandbox:latest is
// satisfied by the base build.
func buildImages(ctx context.Context, dir string) error {
	if err := ensureDockerfiles(dir); err != nil {
		return err
	}
	if !imageExists(ctx, baseTag) {
		if err := runDocker(ctx, "build", "-f", filepath.Join(dir, "Dockerfile.agent"), "-t", baseTag, dir); err != nil {
			return fmt.Errorf("build base image: %w", err)
		}
	}
	if !imageExists(ctx, toolsTag) {
		if err := runDocker(ctx, "build", "-f", filepath.Join(dir, "Dockerfile.agent.tools"), "-t", toolsTag,
			"--build-arg", "ENABLE_CLAUDE_CODE=true", "--build-arg", "ENABLE_OPENCODE=true", dir); err != nil {
			return fmt.Errorf("build tools image: %w", err)
		}
	}
	return nil
}

// Up ensures images are built and the squid proxy is running for the given compose file.
func Up(ctx context.Context, dir, composeFile string) error {
	if err := buildImages(ctx, dir); err != nil {
		return err
	}
	return runDocker(ctx, "compose", "-f", composeFile, "up", "-d", "proxy")
}

// Down stops squid + networks. A "" composeFile is a no-op.
func Down(ctx context.Context, composeFile string) error {
	if composeFile == "" {
		return nil
	}
	return runDocker(ctx, "compose", "-f", composeFile, "down")
}

// Teardown fully reaps a cockpit session's stack: it force-removes every container carrying the
// compose project label — including the one-off `docker compose run` agent container, which
// `compose down` deliberately leaves behind — then runs Down to drop the proxy + networks. The
// compose project name defaults to the compose file's parent dir basename; safeslop gives each
// cockpit session a unique cockpit-* stage dir, so this only ever reaps that session's own
// containers. A "" composeFile is a no-op.
func Teardown(ctx context.Context, composeFile string) error {
	if composeFile == "" {
		return nil
	}
	project := filepath.Base(filepath.Dir(composeFile))
	out, err := exec.CommandContext(ctx, "docker", "ps", "-aq",
		"--filter", "label=com.docker.compose.project="+project).Output()
	if err == nil {
		for _, id := range strings.Fields(string(out)) {
			_ = exec.CommandContext(ctx, "docker", "rm", "-f", id).Run()
		}
	}
	return Down(ctx, composeFile)
}
