package container

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/freakhill/safeslop/internal/engine/container/runtime"
	"github.com/freakhill/safeslop/internal/engine/hostexec"
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

// Available reports whether this host can run the container boundary with a detected runtime.
func Available() bool {
	_, err := detectRuntime(runtime.PolicyAllow)
	return err == nil
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

// runBuild runs an image build with BuildKit forced on (DOCKER_BUILDKIT=1). The agent
// Dockerfile uses BuildKit-only features (`--mount=type=cache`, the `# syntax=`
// directive); modern docker defaults to BuildKit but older daemons fall back to the
// legacy builder, which would fail on those, so we set it explicitly (specs/0058 N1).
// nerdctl always builds via BuildKit, so the extra env is harmless there.
func runBuild(ctx context.Context, eng runtime.Engine, args ...string) error {
	c := eng.Command(ctx, args...)
	c.Stdout, c.Stderr = os.Stdout, os.Stderr
	c.Env = hostexec.AppendEnv(c.Env, "DOCKER_BUILDKIT=1")
	return c.Run()
}

// buildImages builds the base then the tools image, each tagged by its content-hash recipe id
// (local/safeslop-{base,tools}:<id>) so imageExists(<id-tag>) is a CORRECT skip — an unchanged
// recipe is reused, a changed Dockerfile/build-arg yields a new tag and rebuilds (specs/0055 W1,
// killing the stale-":latest" Bug B). dir is the build context.
func buildImages(ctx context.Context, eng runtime.Engine, dir string, enabled []string) error {
	if err := ensureDockerfiles(dir); err != nil {
		return err
	}
	baseImg, toolsImg, toolsArgs, err := agentImageTags(enabled)
	if err != nil {
		return err
	}
	if err := ensureImage(baseImg,
		func() bool { return imageExists(ctx, eng, baseImg) },
		func() error {
			return runBuild(ctx, eng, "build", "-f", filepath.Join(dir, "Dockerfile.agent"), "-t", baseImg, dir)
		}); err != nil {
		return fmt.Errorf("build base image: %w", err)
	}
	if err := ensureImage(toolsImg,
		func() bool { return imageExists(ctx, eng, toolsImg) },
		func() error {
			args := []string{"build", "-f", filepath.Join(dir, "Dockerfile.agent.tools"), "-t", toolsImg}
			for _, kv := range toolsArgs {
				args = append(args, "--build-arg", kv)
			}
			return runBuild(ctx, eng, append(args, dir)...)
		}); err != nil {
		return fmt.Errorf("build tools image: %w", err)
	}
	return nil
}

// ensureImage runs build() to create the image unless exists() already reports it present. The build
// is serialized by a per-id flock with a double-check (exists() is re-tested once the lock is held),
// so concurrent safeslop runs build a given recipe at most once.
func ensureImage(id string, exists func() bool, build func() error) error {
	if exists() {
		return nil
	}
	return withBuildLock(id, func() error {
		if exists() { // built by another run while we waited for the lock
			return nil
		}
		return build()
	})
}

var ErrComposeSafetyUnsupported = errors.New("container runtime does not support required safe bind mounts")

const (
	proxyReadyTimeout   = 10 * time.Second
	proxyReadyInterval  = 100 * time.Millisecond
	proxyCleanupTimeout = 5 * time.Second
	proxyReadyCommand   = "squid -k check >/dev/null 2>&1 && exec 3<>/dev/tcp/127.0.0.1/3128"
)

// waitForProxy requires both a valid Squid configuration and a live local listener.
// `compose up -d` only proves that a container start was requested, while `squid -k
// check` alone can pass before the foreground daemon later fails its privilege drop.
func waitForProxy(ctx context.Context, eng runtime.Engine, composeFile string) error {
	readyCtx, cancel := context.WithTimeout(ctx, proxyReadyTimeout)
	defer cancel()
	args, err := composeProjectArgs(composeFile, "exec", "-T", "proxy", "bash", "-ec", proxyReadyCommand)
	if err != nil {
		return err
	}
	for {
		if err := eng.Command(readyCtx, args...).Run(); err == nil {
			return nil
		}
		select {
		case <-readyCtx.Done():
			return readyCtx.Err()
		case <-time.After(proxyReadyInterval):
		}
	}
}

func cleanupUnreadyProxy(eng runtime.Engine, composeFile string) {
	ctx, cancel := context.WithTimeout(context.Background(), proxyCleanupTimeout)
	defer cancel()
	args, err := composeProjectArgs(composeFile, "down", "--remove-orphans")
	if err != nil {
		return
	}
	_ = runEngine(ctx, eng, args...)
}

// Up ensures images are built (for the profile's resolved package set, enabled) and the
// squid proxy is running and ready for the given compose file. A proxy startup failure
// tears the partial stack down and returns only an engine-owned, value-free failure.
func Up(ctx context.Context, eng runtime.Engine, dir, composeFile string, enabled []string) error {
	configArgs, err := composeProjectArgs(composeFile, "config")
	if err != nil {
		return ErrComposeSafetyUnsupported
	}
	if err := eng.Command(ctx, configArgs...).Run(); err != nil {
		return ErrComposeSafetyUnsupported
	}
	if err := buildImages(ctx, eng, dir, enabled); err != nil {
		return err
	}
	args, err := composeProjectArgs(composeFile, "up", "-d", "proxy")
	if err != nil {
		return err
	}
	if err := runEngine(ctx, eng, args...); err != nil {
		cleanupUnreadyProxy(eng, composeFile)
		return newRuntimeFailure(NetworkProxyUnavailable)
	}
	if err := waitForProxy(ctx, eng, composeFile); err != nil {
		cleanupUnreadyProxy(eng, composeFile)
		return newRuntimeFailure(NetworkProxyUnavailable)
	}
	return nil
}
