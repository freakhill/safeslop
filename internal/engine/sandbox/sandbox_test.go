package sandbox

import (
	"context"
	"os"
	osexec "os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/freakhill/safeslop/internal/engine/exec"
)

func TestProfileContainsExpectedDirectives(t *testing.T) {
	p := Profile("/Users/x/repo", "deny", Scope{})
	for _, want := range []string{
		"(version 1)",
		`(import "system.sb")`,
		`(allow file-read* (subpath "/Users/x/repo"))`,
		`(allow file-write* (subpath "/Users/x/repo"))`,
		`(allow file-write* (subpath "/private/tmp"))`,
		`(allow file-ioctl (regex #"^/dev/ttys"))`,
		"(deny network*)",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("profile missing %q\n---\n%s", want, p)
		}
	}
}

func TestWrapArgvWritesProfileAndWraps(t *testing.T) {
	argv, cleanup, err := WrapArgv([]string{"claude"}, "/ws", "deny", Scope{})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if argv[0] != SandboxExecPath || argv[1] != "-f" {
		t.Fatalf("argv must start with sandbox-exec -f: %v", argv)
	}
	if _, err := os.Stat(argv[2]); err != nil {
		t.Fatalf("profile file %q must exist: %v", argv[2], err)
	}
	last := argv[len(argv)-1]
	if last != "claude" {
		t.Fatalf("agent argv must be appended: %v", argv)
	}
	cleanup()
	if _, err := os.Stat(argv[2]); !os.IsNotExist(err) {
		t.Fatalf("cleanup must remove the profile file")
	}
}

func TestProfileNetworkAllow(t *testing.T) {
	p := Profile("/w", "allow", Scope{})
	if !strings.Contains(p, "(allow network*)") {
		t.Errorf("network=allow profile missing (allow network*)")
	}
	if strings.Contains(p, "(deny network*)") {
		t.Errorf("network=allow profile should not contain (deny network*)")
	}
}

func TestProfileEscapesQuotes(t *testing.T) {
	p := Profile(`/tmp/a"b\c`, "deny", Scope{})
	if !strings.Contains(p, `/tmp/a\"b\\c`) {
		t.Errorf("profile did not escape quotes/backslashes in workspace path:\n%s", p)
	}
}

// --- darwin-only launch behavior (skipped elsewhere; the Go CI runs on macOS) ---

func TestLaunchRunsCommandOnDarwin(t *testing.T) {
	if !Available() {
		t.Skip("sandbox-exec unavailable (not macOS)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	code, err := Launch(ctx, exec.LaunchSpec{
		Argv:   []string{"/usr/bin/true"},
		Stdout: &strings.Builder{},
	}, t.TempDir(), "deny", Scope{})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (profile rejected or command failed)", code)
	}
}

func TestLaunchAllowsWorkspaceWriteOnDarwin(t *testing.T) {
	if !Available() {
		t.Skip("sandbox-exec unavailable (not macOS)")
	}
	ws := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var out strings.Builder
	code, err := Launch(ctx, exec.LaunchSpec{
		Argv:   []string{"/bin/sh", "-c", "echo ok > " + filepath.Join(ws, "probe")},
		Stdout: &out,
		Stderr: &out,
	}, ws, "deny", Scope{})
	if err != nil || code != 0 {
		t.Fatalf("workspace write failed: code=%d err=%v out=%q", code, err, out.String())
	}
	if _, err := os.Stat(filepath.Join(ws, "probe")); err != nil {
		t.Fatalf("expected probe file written inside workspace: %v", err)
	}
}

// TestLaunchAllowsTtyJobControlOnDarwin is the regression guard for the
// "sandboxed shell opens but runs nothing" bug. An interactive shell on a PTY must
// be able to ioctl its controlling terminal — tcsetpgrp (TIOCSPGRP) to claim the
// foreground, and window-size ioctls. Seatbelt gates these as `file-ioctl`, which
// is NOT implied by file-read* on /dev; without the rule the ioctl returns EPERM,
// zsh prints "can't set tty pgrp", and commands suspend on SIGTTIN/SIGTTOU. We
// exercise it with `stty size`, a TIOCGWINSZ ioctl on the tty: denied -> stty errors
// and exits non-zero; allowed -> it prints "rows cols".
func TestLaunchAllowsTtyJobControlOnDarwin(t *testing.T) {
	if !Available() {
		t.Skip("sandbox-exec unavailable (not macOS)")
	}
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Fatalf("pty.Open: %v", err)
	}
	defer func() { _ = ptmx.Close() }()

	argv, cleanup, err := WrapArgv([]string{"/bin/stty", "size"}, t.TempDir(), "deny", Scope{})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := osexec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, tty, tty
	// Make the child a session leader owning the tty, so its stty ioctls the slave.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	if err := cmd.Start(); err != nil {
		_ = tty.Close()
		t.Fatalf("start stty under sandbox: %v", err)
	}
	_ = tty.Close() // the child holds the slave now

	got := make(chan string, 1)
	go func() {
		buf := make([]byte, 256)
		n, _ := ptmx.Read(buf)
		got <- string(buf[:n])
	}()
	werr := cmd.Wait()
	out := <-got
	if werr != nil {
		t.Fatalf("stty under sandbox failed — tty ioctl likely denied (file-ioctl rule missing?): err=%v out=%q", werr, out)
	}
	if !regexp.MustCompile(`\d+\s+\d+`).MatchString(out) {
		t.Fatalf("stty size produced no numeric size — tty ioctl likely denied: %q", out)
	}
}

func TestLaunchDeniesWriteOutsideWorkspaceOnDarwin(t *testing.T) {
	if !Available() {
		t.Skip("sandbox-exec unavailable (not macOS)")
	}
	ws := t.TempDir()
	// A path outside workspace and outside the allowed temp dirs: a sibling of
	// the workspace under the same parent.
	outside := filepath.Join(filepath.Dir(ws), "safeslop_outside_probe")
	defer os.Remove(outside)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	code, _ := Launch(ctx, exec.LaunchSpec{
		Argv:   []string{"/bin/sh", "-c", "echo x > " + outside},
		Stdout: &strings.Builder{},
		Stderr: &strings.Builder{},
	}, ws, "deny", Scope{})
	if code == 0 {
		t.Fatalf("write outside workspace unexpectedly succeeded (confinement broken)")
	}
	if _, err := os.Stat(outside); err == nil {
		t.Fatalf("file was written outside the workspace — confinement failed")
	}
}

func TestProfileHonorsFileScope(t *testing.T) {
	p := Profile("/ws", "deny", Scope{
		Read:  []string{"/extra/ro"},
		Write: []string{"/extra/rw"},
		Deny:  []string{"/ws/secret"},
	})
	for _, want := range []string{
		`(allow file-read* (subpath "/extra/ro"))`,
		`(allow file-write* (subpath "/extra/rw"))`,
		`(deny file-read* (subpath "/ws/secret"))`,
		`(deny file-write* (subpath "/ws/secret"))`,
	} {
		if !strings.Contains(p, want) {
			t.Errorf("profile missing %q\n---\n%s", want, p)
		}
	}
	// deny must be emitted AFTER the workspace allow so it wins (Seatbelt = last match).
	if strings.Index(p, `(deny file-write* (subpath "/ws/secret"))`) <
		strings.Index(p, `(allow file-write* (subpath "/ws"))`) {
		t.Error("deny rules must come after the workspace allow")
	}
}

func TestLaunchFileScopeWritesAndDeniesOnDarwin(t *testing.T) {
	if !Available() {
		t.Skip("sandbox-exec unavailable (not macOS)")
	}
	ws := t.TempDir()
	extra := t.TempDir() // outside ws
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// With a Write scope, the agent may write into `extra` (which is outside the workspace).
	probe := filepath.Join(extra, "probe")
	code, err := Launch(ctx, exec.LaunchSpec{
		Argv: []string{"/bin/sh", "-c", "echo ok > " + probe}, Stdout: &strings.Builder{}, Stderr: &strings.Builder{},
	}, ws, "deny", Scope{Write: []string{extra}})
	if err != nil || code != 0 {
		t.Fatalf("scoped write failed: code=%d err=%v", code, err)
	}
	if _, err := os.Stat(probe); err != nil {
		t.Fatalf("scoped write not present: %v", err)
	}

	// Deny wins: grant write to `extra` but deny a subdir — writing there must fail.
	denied := filepath.Join(extra, "secret")
	if err := os.Mkdir(denied, 0o700); err != nil {
		t.Fatal(err)
	}
	leak := filepath.Join(denied, "leak")
	code, _ = Launch(ctx, exec.LaunchSpec{
		Argv: []string{"/bin/sh", "-c", "echo x > " + leak}, Stdout: &strings.Builder{}, Stderr: &strings.Builder{},
	}, ws, "deny", Scope{Write: []string{extra}, Deny: []string{denied}})
	if code == 0 {
		t.Fatal("deny scope did not override the write allow")
	}
	if _, err := os.Stat(leak); err == nil {
		t.Fatal("file was written into a denied subpath — deny did not win")
	}
}

func TestAutoDenyCredentialsOnlyWithScope(t *testing.T) {
	home, _ := os.UserHomeDir()
	awsCreds := home + "/.aws/credentials"
	sshKey := home + "/.ssh/id_ed25519"

	// default workspace-only sandbox: NO auto-deny (creds already out of scope, keep profile clean)
	plain := Profile("/ws", "deny", Scope{})
	if strings.Contains(plain, awsCreds) {
		t.Errorf("default sandbox should not carry credential auto-deny:\n%s", plain)
	}

	// granting an extra read turns on the auto-deny for the credential set
	scoped := Profile("/ws", "deny", Scope{Read: []string{home}})
	for _, want := range []string{
		`(deny file-read* (literal "` + awsCreds + `"))`,
		`(deny file-read* (literal "` + sshKey + `"))`,
		`(deny file-read* (subpath "` + home + `/.gnupg"))`,
	} {
		if !strings.Contains(scoped, want) {
			t.Errorf("scoped sandbox missing auto-deny %q\n%s", want, scoped)
		}
	}
	// the ambiguous bucket must NOT be auto-denied (child tools need them)
	for _, bad := range []string{home + "/.npmrc", home + "/.kube/config", home + "/.docker/config.json", home + "/.gitconfig", "/.git\""} {
		if strings.Contains(scoped, `(deny file-read* (literal "`+bad+`"`) || strings.Contains(scoped, `(deny file-read* (subpath "`+bad+`"`) {
			t.Errorf("ambiguous path %q must NOT be auto-denied (breaks tools):\n%s", bad, scoped)
		}
	}
	// SSH private key denied but the dir itself is not (config/known_hosts stay readable)
	if strings.Contains(scoped, `(deny file-read* (subpath "`+home+`/.ssh"))`) {
		t.Error("must not deny the whole ~/.ssh — only the private keys")
	}
}

func TestAutoDenyExplicitGrantWins(t *testing.T) {
	home, _ := os.UserHomeDir()
	awsCreds := home + "/.aws/credentials"
	// explicitly granting a credential path opts it out of the auto-deny
	p := Profile("/ws", "deny", Scope{Read: []string{awsCreds}})
	if strings.Contains(p, `(deny file-read* (literal "`+awsCreds+`"))`) {
		t.Errorf("explicit read of %q must override the auto-deny:\n%s", awsCreds, p)
	}
}
