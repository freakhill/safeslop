package creds

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Shared git-credential staging helpers used by the GitHub (App-token HTTPS, github.go) and Forgejo
// (deploy-key SSH, forgejo.go/multirepo.go) providers: ephemeral ed25519 keygen, github.com
// owner/repo parsing for origin inference, forge deploy-key id parsing, and a small exec wrapper.

// keygenArgv builds an ssh-keygen invocation for a fresh ed25519 keypair (no passphrase).
func keygenArgv(keyPath, comment string) []string {
	return []string{"ssh-keygen", "-t", "ed25519", "-N", "", "-C", comment, "-f", keyPath}
}

// parseOwnerRepo extracts owner/repo from a github.com remote URL (ssh, scp-like, or https). Drives
// StageGithub's single-repo origin inference when no repos are declared.
func parseOwnerRepo(out []byte) (owner, repo string, err error) {
	u := strings.TrimSpace(string(out))
	if !strings.Contains(u, "github.com") {
		return "", "", fmt.Errorf("origin remote is not github.com (%q); github creds support GitHub only", u)
	}
	i := strings.Index(u, "github.com")
	tail := u[i+len("github.com"):]
	tail = strings.TrimLeft(tail, ":/")
	tail = strings.TrimSuffix(tail, ".git")
	parts := strings.Split(tail, "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("could not parse owner/repo from %q", u)
	}
	return parts[0], parts[1], nil
}

// parseKeyID reads the numeric id from a forge deploy-key registration response (Forgejo/Gitea).
func parseKeyID(out []byte) (string, error) {
	var k struct {
		ID json.Number `json:"id"`
	}
	if err := json.Unmarshal(out, &k); err != nil {
		return "", fmt.Errorf("parse deploy-key response: %w", err)
	}
	if k.ID.String() == "" || k.ID.String() == "0" {
		return "", fmt.Errorf("deploy-key response had no id")
	}
	return k.ID.String(), nil
}

// runSSHCmd executes argv and returns stdout, wrapping failures with a hint.
func runSSHCmd(ctx context.Context, argv []string, hint string) ([]byte, error) {
	cmd, err := hostCommand(ctx, argv, hint)
	if err != nil {
		return nil, err
	}
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s (%s): helper failed", helperLabel(argv), hint)
	}
	return out, nil
}
