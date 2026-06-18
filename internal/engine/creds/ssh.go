package creds

import (
	"context"
	"encoding/json"
	"fmt"
	osexec "os/exec"
	"strings"
)

// githubKnownHosts pins github.com's published ed25519 host key (StrictHostKeyChecking=yes,
// no TOFU). Update this constant if GitHub rotates the key.
const githubKnownHosts = "github.com ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl\n"

// ---- argv builders ----

func keygenArgv(keyPath, comment string) []string {
	return []string{"ssh-keygen", "-t", "ed25519", "-N", "", "-C", comment, "-f", keyPath}
}

func ghRegisterArgv(owner, repo, title, pubkey string, write bool) []string {
	ro := "true"
	if write {
		ro = "false"
	}
	return []string{"gh", "api", "repos/" + owner + "/" + repo + "/keys",
		"-f", "title=" + title, "-f", "key=" + pubkey, "-F", "read_only=" + ro}
}

func ghRevokeArgv(owner, repo, id string) []string {
	return []string{"gh", "api", "--method", "DELETE", "repos/" + owner + "/" + repo + "/keys/" + id}
}

// ---- parsers ----

// parseOwnerRepo extracts owner/repo from a github.com remote URL (ssh, scp-like, or https).
func parseOwnerRepo(out []byte) (owner, repo string, err error) {
	u := strings.TrimSpace(string(out))
	if !strings.Contains(u, "github.com") {
		return "", "", fmt.Errorf("origin remote is not github.com (%q); ssh creds support GitHub only", u)
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

func parseKeyID(out []byte) (string, error) {
	var k struct {
		ID json.Number `json:"id"`
	}
	if err := json.Unmarshal(out, &k); err != nil {
		return "", fmt.Errorf("parse gh deploy-key response: %w", err)
	}
	if k.ID.String() == "" || k.ID.String() == "0" {
		return "", fmt.Errorf("gh deploy-key response had no id")
	}
	return k.ID.String(), nil
}

// ---- render ----

func renderGitSSHCommand(keyPath, knownHostsPath string) string {
	return "ssh -i " + keyPath +
		" -o IdentitiesOnly=yes -o IdentityAgent=none" +
		" -o StrictHostKeyChecking=yes -o UserKnownHostsFile=" + knownHostsPath
}

// runSSHCmd executes argv and returns stdout, wrapping failures with a hint.
func runSSHCmd(ctx context.Context, argv []string, hint string) ([]byte, error) {
	out, err := osexec.CommandContext(ctx, argv[0], argv[1:]...).Output()
	if err != nil {
		label := argv[0]
		if len(argv) > 1 {
			label += " " + argv[1]
		}
		return nil, fmt.Errorf("%s (%s): %w", label, hint, err)
	}
	return out, nil
}
