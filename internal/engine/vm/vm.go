// Package vm runs a profile's agent inside a disposable Tart macOS VM: a fresh session VM is
// cloned from a cached base per run, the agent runs over ssh, and the VM is destroyed on exit.
package vm

import (
	"context"
	"os/exec"
	"strings"
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
