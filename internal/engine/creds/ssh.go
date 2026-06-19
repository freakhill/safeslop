package creds

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"

	"github.com/freakhill/safeslop/internal/engine/policy"
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

// StageSSH mints a fresh ed25519 keypair into stageDir/.ssh, registers the public key as
// a repo-scoped GitHub deploy key (read-only unless creds.Write), stages ONLY the 0600
// private key + a pinned known_hosts + a revoke-info file, and returns GIT_SSH_COMMAND as
// a non-secret path env (host path; the container path is set in the compose env). The
// owner/repo come from the process cwd's `origin` remote. No revoke is relied upon (best
// effort via RevokeSSH); the stageDir wipe destroys the private key (decay-first).
func StageSSH(ctx context.Context, creds *policy.Credentials, stageDir string) ([]string, error) {
	if creds == nil || creds.Ssh == nil {
		return nil, nil
	}
	rOut, err := runSSHCmd(ctx, []string{"git", "remote", "get-url", "origin"}, "run safeslop from a repo with a github.com origin")
	if err != nil {
		return nil, err
	}
	owner, repo, err := parseOwnerRepo(rOut)
	if err != nil {
		return nil, err
	}

	sshDir := filepath.Join(stageDir, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		return nil, err
	}
	keyPath := filepath.Join(sshDir, "id")
	khPath := filepath.Join(sshDir, "known_hosts")

	title := "safeslop-" + owner + "-" + repo
	if _, err := runSSHCmd(ctx, keygenArgv(keyPath, title), "is ssh-keygen on PATH?"); err != nil {
		return nil, err
	}
	pub, err := os.ReadFile(keyPath + ".pub")
	if err != nil {
		return nil, fmt.Errorf("read generated public key: %w", err)
	}
	regOut, err := runSSHCmd(ctx, ghRegisterArgv(owner, repo, title, strings.TrimSpace(string(pub)), creds.Ssh.Write), "is `gh auth login` current with repo admin?")
	if err != nil {
		return nil, err
	}
	keyID, err := parseKeyID(regOut)
	if err != nil {
		return nil, err
	}
	_ = os.Remove(keyPath + ".pub") // only the private key crosses the boundary
	if err := os.Chmod(keyPath, 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(khPath, []byte(githubKnownHosts), 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(sshDir, "revoke-info"), []byte(owner+"/"+repo+" "+keyID+"\n"), 0o600); err != nil {
		return nil, err
	}
	return []string{"GIT_SSH_COMMAND=" + renderGitSSHCommand(keyPath, khPath)}, nil
}

// RevokeSSH best-effort revokes the staged deploy key (reads stageDir/.ssh/revoke-info).
// Never relied upon for security; errors are swallowed (decay-first cleanup is the wipe).
func RevokeSSH(ctx context.Context, stageDir string) {
	b, err := os.ReadFile(filepath.Join(stageDir, ".ssh", "revoke-info"))
	if err != nil {
		return
	}
	f := strings.Fields(strings.TrimSpace(string(b)))
	if len(f) != 2 {
		return
	}
	or := strings.SplitN(f[0], "/", 2)
	if len(or) != 2 {
		return
	}
	_, _ = runSSHCmd(ctx, ghRevokeArgv(or[0], or[1], f[1]), "best-effort revoke")
}
