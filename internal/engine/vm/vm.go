// Package vm runs a profile's agent inside a disposable Tart macOS VM: a fresh session VM is
// cloned from a cached base per run, the agent runs over ssh, and the VM is destroyed on exit.
package vm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	// sourceImage is the Tart base image, PINNED by digest (not :latest) — slop-pinning only
	// scans *.cue/build configs, so the imageIsPinned() test is SP4's pin guard. Digest is the
	// resolved ghcr.io/cirruslabs/macos-sonoma-base:latest as of 2026-06-17; bump deliberately.
	sourceImage  = "ghcr.io/cirruslabs/macos-sonoma-base@sha256:41a4a6eef68363b23f9cfcd520fce5db5523aa90e10c0db70e51974bcc7f058c"
	baseTemplate = "slop-vm-base"
	sshUser      = "admin"
)

// sessionName is the disposable per-profile VM name.
func sessionName(profile string) string { return "slop-vm-" + profile }

// Available reports whether this host can run the VM boundary: the tart CLI on PATH.
func Available() bool {
	_, err := exec.LookPath("tart")
	return err == nil
}

// imageIsPinned reports whether sourceImage is digest-pinned (no floating :latest tag).
func imageIsPinned() bool {
	return strings.Contains(sourceImage, "@sha256:") && !strings.HasSuffix(sourceImage, ":latest")
}

func tartList(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "tart", "list", "--format", "json").Output()
	return string(out), err
}

func runTart(ctx context.Context, args ...string) error {
	c := exec.CommandContext(ctx, "tart", args...)
	c.Stdout, c.Stderr = os.Stdout, os.Stderr
	return c.Run()
}

// osCommand builds an *exec.Cmd (stdlib) inheriting stdio — used by launch.go's runScp so the
// engine-exec import in launch.go doesn't clash with stdlib os/exec.
func osCommand(ctx context.Context, argv []string) *exec.Cmd {
	c := exec.CommandContext(ctx, argv[0], argv[1:]...)
	c.Stdout, c.Stderr = os.Stdout, os.Stderr
	return c
}

// vmExists reports whether a VM/template of the given name exists.
func vmExists(ctx context.Context, name string) bool {
	out, err := tartList(ctx)
	if err != nil {
		return false
	}
	var entries []struct {
		Name string `json:"Name"`
	}
	if json.Unmarshal([]byte(out), &entries) != nil {
		return false
	}
	for _, e := range entries {
		if e.Name == name {
			return true
		}
	}
	return false
}

// EnsureBase clones the pinned source image into the cached base template if absent (idempotent).
func EnsureBase(ctx context.Context) error {
	if !imageIsPinned() {
		return fmt.Errorf("vm: source image is not digest-pinned (%s)", sourceImage)
	}
	if vmExists(ctx, baseTemplate) {
		return nil
	}
	return runTart(ctx, "clone", sourceImage, baseTemplate)
}

// CloneAndBoot clones a fresh session VM from the base, boots it headless, and returns its IP
// once SSH is reachable. Caller is responsible for Destroy.
func CloneAndBoot(ctx context.Context, profile string) (string, error) {
	name := sessionName(profile)
	if vmExists(ctx, name) {
		_ = Destroy(ctx, profile) // reclaim a stale session before re-cloning
	}
	if err := runTart(ctx, "clone", baseTemplate, name); err != nil {
		return "", fmt.Errorf("clone session vm: %w", err)
	}
	// tart run blocks; start it in the background and poll for IP + SSH.
	cmd := exec.CommandContext(ctx, "tart", "run", "--no-graphics", name)
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("boot session vm: %w", err)
	}
	ip, err := waitIP(ctx, name, 120*time.Second)
	if err != nil {
		return "", err
	}
	if err := waitSSH(ctx, ip, 120*time.Second); err != nil {
		return "", err
	}
	return ip, nil
}

func tartIP(ctx context.Context, name string) string {
	out, err := exec.CommandContext(ctx, "tart", "ip", name).Output()
	if err != nil {
		return ""
	}
	return string(bytesTrim(out))
}

func waitIP(ctx context.Context, name string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ip := tartIP(ctx, name); ip != "" {
			return ip, nil
		}
		time.Sleep(2 * time.Second)
	}
	return "", fmt.Errorf("vm %s did not get an IP within %s", name, timeout)
}

func waitSSH(ctx context.Context, ip string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if exec.CommandContext(ctx, "ssh", append(sshBaseOpts(), sshUser+"@"+ip, "true")...).Run() == nil {
			return nil
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("vm at %s did not accept SSH within %s", ip, timeout)
}

// Destroy stops and deletes the session VM (no-op if absent).
func Destroy(ctx context.Context, profile string) error {
	name := sessionName(profile)
	if !vmExists(ctx, name) {
		return nil
	}
	_ = runTart(ctx, "stop", name)
	return runTart(ctx, "delete", name)
}

// Reconcile destroys a session VM orphaned by a crashed prior run.
func Reconcile(ctx context.Context, profile string) error { return Destroy(ctx, profile) }

// DestroyAll stops+deletes every disposable slop-vm-* session (never the base template).
// Best-effort: a missing/unusable tart yields nil so `slop down` stays graceful.
func DestroyAll(ctx context.Context) error {
	out, err := tartList(ctx)
	if err != nil {
		return nil
	}
	var entries []struct {
		Name string `json:"Name"`
	}
	if json.Unmarshal([]byte(out), &entries) != nil {
		return nil
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name, "slop-vm-") && e.Name != baseTemplate {
			_ = runTart(ctx, "stop", e.Name)
			_ = runTart(ctx, "delete", e.Name)
		}
	}
	return nil
}

// provisionToolchain installs mise/nix into the running VM if the toolchain needs it (idempotent:
// skips when the CLI is already present). The VM is a full writable macOS. Pin the installers
// deliberately before relying on this in anger (SP5 deferred follow-through).
func provisionToolchain(ctx context.Context, ip, kind string) error {
	var script string
	switch kind {
	case "mise":
		script = "command -v mise >/dev/null 2>&1 || curl -fsSL https://mise.run | sh"
	case "nix":
		script = "command -v nix >/dev/null 2>&1 || curl --proto '=https' --tlsv1.2 -sSf -L " +
			"https://install.determinate.systems/nix | sh -s -- install --no-confirm"
	default:
		return nil
	}
	if err := osCommand(ctx, sshArgv(ip, false, "zsh", "-lc", script)).Run(); err != nil {
		return fmt.Errorf("provision %s toolchain in vm: %w", kind, err)
	}
	return nil
}

func bytesTrim(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r' || b[len(b)-1] == ' ') {
		b = b[:len(b)-1]
	}
	return b
}
