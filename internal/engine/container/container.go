package container

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/freakhill/safeslop/internal/engine/container/runtime"
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

func imageExists(ctx context.Context, eng runtime.Engine, tag string) bool {
	return eng.Command(ctx, "image", "inspect", tag).Run() == nil
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

// runEngine runs a container subcommand through the selected engine, streaming to the host stdio
// (docker on the host, or nerdctl inside the lima guest). Replaces the former hardcoded `docker`.
func runEngine(ctx context.Context, eng runtime.Engine, args ...string) error {
	c := eng.Command(ctx, args...)
	c.Stdout, c.Stderr = os.Stdout, os.Stderr
	return c.Run()
}

// buildImages builds the base then the tools image (idempotent via image inspect), from dir as build
// context. The tools Dockerfile's FROM local/agent-sandbox:latest is satisfied by the base build.
func buildImages(ctx context.Context, eng runtime.Engine, dir string) error {
	if err := ensureDockerfiles(dir); err != nil {
		return err
	}
	if !imageExists(ctx, eng, baseTag) {
		if err := runEngine(ctx, eng, "build", "-f", filepath.Join(dir, "Dockerfile.agent"), "-t", baseTag, dir); err != nil {
			return fmt.Errorf("build base image: %w", err)
		}
	}
	if !imageExists(ctx, eng, toolsTag) {
		if err := runEngine(ctx, eng, "build", "-f", filepath.Join(dir, "Dockerfile.agent.tools"), "-t", toolsTag,
			"--build-arg", "ENABLE_CLAUDE_CODE=true", "--build-arg", "ENABLE_OPENCODE=true",
			"--build-arg", "ENABLE_PI=true", dir); err != nil {
			return fmt.Errorf("build tools image: %w", err)
		}
	}
	return nil
}

// Up ensures images are built and the squid proxy is running for the given compose file.
func Up(ctx context.Context, eng runtime.Engine, dir, composeFile string) error {
	if err := buildImages(ctx, eng, dir); err != nil {
		return err
	}
	return runEngine(ctx, eng, "compose", "-f", composeFile, "up", "-d", "proxy")
}

// Down stops squid + networks. A "" composeFile is a no-op.
func Down(ctx context.Context, eng runtime.Engine, composeFile string) error {
	if composeFile == "" {
		return nil
	}
	return runEngine(ctx, eng, "compose", "-f", composeFile, "down")
}
