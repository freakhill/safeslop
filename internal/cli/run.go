package cli

import (
	"bufio"
	"context"
	cryptorand "crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	osexec "os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	runtimepkg "github.com/freakhill/safeslop/internal/engine/container/runtime"
	"github.com/freakhill/safeslop/internal/engine/launch"
	"github.com/freakhill/safeslop/internal/engine/policy"
	"github.com/freakhill/safeslop/internal/engine/toolchain"
	"github.com/freakhill/safeslop/internal/engine/userconfig"
	workspaceboundary "github.com/freakhill/safeslop/internal/engine/workspace"
)

func cmdRun() *cobra.Command {
	return cmdRunWithDeps(defaultDependencies())
}

func cmdRunWithDeps(d *dependencies) *cobra.Command {
	var dryRun bool
	var trustFlag bool
	c := &cobra.Command{
		Use:   "run [profile]",
		Short: "Launch a profile's agent under its isolation boundary",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			path, err := findConfig("")
			if err != nil {
				return err
			}
			loaded, err := loadPolicyForLaunch(path)
			if err != nil {
				return err
			}
			name, prof, err := selectProfile(loaded.cfg, arg0(args))
			if err != nil {
				return err
			}
			invocationDir, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("resolve invocation directory: %w", err)
			}
			ws, err := workspaceboundary.Resolve(prof.Workspace, loaded.trustPath, invocationDir)
			if err != nil {
				return fmt.Errorf("resolve profile workspace: %w", err)
			}
			prof.Workspace = ws
			if prof.Environment == "container" {
				if err := validateWorkspaceStageRoot(ws); err != nil {
					return fmt.Errorf("workspace boundary: %w", err)
				}
			}
			if !d.jsonOut {
				printWarnings(policy.Lint(&policy.Config{Profiles: map[string]policy.Profile{name: prof}}))
			}
			argv, err := agentArgv(prof)
			if err != nil {
				return err
			}
			if prof.Toolchain != nil && toolchain.Wraps(prof.Toolchain.Kind) {
				argv = toolchain.Wrap(prof.Toolchain.Kind, prof.Toolchain.Run, argv) // wrap before env switch (SP5)
			}

			tier, tierNote := policy.EnvTier(prof.Environment)

			if dryRun {
				out := map[string]any{"ok": true, "profile": name, "environment": prof.Environment, "workspace": ws, "argv": argv, "network": prof.Network, "isolation_tier": tier, "isolation_note": tierNote}
				if len(prof.Secrets) > 0 {
					out["secrets"] = prof.Secrets // refs, never resolved here
				}
				if prof.Credentials != nil && len(prof.Credentials.Pnpm) > 0 {
					out["pnpm"] = prof.Credentials.Pnpm // token field is a ref, not a value
				}
				if d.jsonOut {
					emitJSON(out)
				} else {
					fmt.Printf("profile %q: environment=%s workspace=%s network=%s\n  argv: %v\n", name, prof.Environment, ws, prof.Network, argv)
					fmt.Printf("  isolation tier: %s — %s\n", tier, tierNote)
					if prof.Toolchain != nil && toolchain.Wraps(prof.Toolchain.Kind) {
						fmt.Printf("  toolchain: %s", prof.Toolchain.Kind)
						if prof.Toolchain.Run != "" {
							fmt.Printf(" run=%s", prof.Toolchain.Run)
						}
						fmt.Println()
					}
					for k, ref := range prof.Secrets {
						fmt.Printf("  secret env %s <- %s\n", k, ref)
					}
					if prof.Credentials != nil {
						for _, r := range prof.Credentials.Pnpm {
							fmt.Printf("  pnpm %s token <- %s\n", hostOr(r.Host), r.Token)
						}
					}
				}
				return nil
			}

			if !d.jsonOut {
				fmt.Printf("isolation tier: %s — %s\n", tier, tierNote)
			}

			// Fail-closed: only an explicitly host-approved safeslop.cue may launch an agent
			// (specs/0022). --dry-run above stays ungated — it is inspection, like validate.
			if err := enforceLoadedPolicyTrust(loaded, trustFlag); err != nil {
				return err
			}
			if err := requireHostLaunchConsentWithDeps(d, name, prof, os.Stdin, os.Stderr); err != nil {
				return err
			}
			if prof.Environment == "container" {
				if err := sweepManagedOrphansWithDeps(d, context.Background()); err != nil {
					return err
				}
			}
			code, err := runDirectProfileWithDeps(d, name, prof, argv, ws)
			if err != nil {
				// Surface the failure reason. runProfile returns code=1 on setup
				// errors, so the old `&& code == 0` guard silently dropped them — a
				// launch that failed with no diagnostic. cobra prints returned errors as "Error: …".
				return err
			}
			os.Exit(code)
			return nil
		},
	}
	c.Flags().BoolVar(&dryRun, "dry-run", false, "print the resolved launch plan without executing")
	c.Flags().BoolVar(&trustFlag, "trust", false, "approve this safeslop.cue, then run it")
	return c
}

func requireHostLaunchConsent(name string, prof policy.Profile, in io.Reader, out io.Writer) error {
	return requireHostLaunchConsentWithDeps(defaultDependencies(), name, prof, in, out)
}

func requireHostLaunchConsentWithDeps(d *dependencies, name string, prof policy.Profile, in io.Reader, out io.Writer) error {
	if prof.Environment != "host" {
		return nil
	}
	return d.hostLaunchConsent(name, prof, in, out)
}

func confirmHostLaunchConsent(name string, prof policy.Profile, in io.Reader, out io.Writer) error {
	stmts := policy.HostConsentStatements(3, rand.New(rand.NewSource(time.Now().UnixNano())))
	return confirmHostLaunchConsentRows(name, prof, mountedVolumes(), stmts, in, out)
}

func confirmHostLaunchConsentRows(name string, prof policy.Profile, volumes []string, stmts []policy.ConsentStatement, in io.Reader, out io.Writer) error {
	if in == nil {
		return fmt.Errorf("host launch consent requires input; launch aborted")
	}
	if out == nil {
		out = io.Discard
	}
	reader := bufio.NewReader(in)
	fmt.Fprintln(out, "host launch consent required")
	fmt.Fprintln(out, policy.HostHeadlineBody(name))
	fmt.Fprintln(out, policy.HostScopeLine(prof, volumes))
	fmt.Fprintln(out, "Answer yes or no for each statement to confirm you understand this host launch:")
	for i, st := range stmts {
		for {
			fmt.Fprintf(out, "%d. %s [yes/no]: ", i+1, st.Text)
			answer, err := readConsentAnswer(reader)
			if err != nil {
				return fmt.Errorf("host launch consent interrupted; launch aborted: %w", err)
			}
			got, ok := parseConsentBool(answer)
			if !ok {
				fmt.Fprintln(out, "Please answer yes or no.")
				continue
			}
			if got != st.Expected {
				return fmt.Errorf("host launch consent failed on statement %d; launch aborted", i+1)
			}
			break
		}
	}
	fmt.Fprintln(out, "host launch consent passed; launching host agent")
	return nil
}

func readConsentAnswer(r *bufio.Reader) (string, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		if errors.Is(err, io.EOF) && strings.TrimSpace(line) != "" {
			return strings.TrimSpace(line), nil
		}
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func parseConsentBool(s string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "y", "yes":
		return true, true
	case "n", "no":
		return false, true
	default:
		return false, false
	}
}

// enforceTrust gates `run` on a host-recorded approval of the policy's exact bytes. With allowTrust
// it records approval and proceeds; otherwise an untrusted or changed policy is a fail-closed error.
// The store is host-side (~/.config/safeslop/trust.json), outside the agent-writable workspace.
// canonicalPolicyPath resolves a policy path to an absolute, symlink-free key so the trust gate
// can't be fooled by /tmp vs /private/tmp (or any symlinked dir): the GUI approves a path the engine
// reaches one way and `safeslop run` reaches the same file another way. Both must hash to one key.
func canonicalPolicyPath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		return real
	}
	return abs
}

// ---- session ----

func cmdLaunch() *cobra.Command {
	var configDir string
	cmd := &cobra.Command{
		Use:   "launch <profile>",
		Short: "Open a terminal window running the profile's agent (ctty intact)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			_, err := launchProfile(args[0], configDir)
			return err
		},
	}
	// --config makes launch usable from a hotkey/skhd in ANY cwd: it resolves the workspace from
	// here instead of os.Getwd() (specs/0028). Trust is still enforced by the launched `safeslop run`.
	cmd.Flags().StringVar(&configDir, "config", "", "directory holding the safeslop.cue (for hotkeys/launchers from any cwd)")
	return cmd
}

// launchProfile opens the user's preferred terminal (from ~/.config/safeslop/config.cue) running
// `safeslop run <profile>`, so the real ctty handoff happens inside that window. Returns once the
// terminal is spawned.
// profileNameRe constrains launchable profile names: the name is embedded in the spawned
// terminal's window title and SAFESLOP_SESSION, so it must not carry shell/title metacharacters.
var profileNameRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// launchWorkspace resolves the workspace directory for a launch: from --config (usable from any cwd,
// e.g. a skhd hotkey) canonicalized so the spawned `safeslop run` computes the same trust key (the
// /tmp vs /private/tmp fix, specs/0028), or the current directory when --config is empty. Extracted
// so the resolution is unit-testable without spawning a terminal.
func launchWorkspace(configPath string) (string, error) {
	if configPath != "" {
		cuePath, err := findConfig(configPath)
		if err != nil {
			return "", err
		}
		// Fail fast for a hotkey/launcher: a missing safeslop.cue should error here, not spawn a
		// terminal that dies (findConfig constructs the path without checking existence).
		if _, err := os.Stat(cuePath); err != nil {
			return "", fmt.Errorf("no safeslop.cue under %s", configPath)
		}
		return filepath.Dir(canonicalPolicyPath(cuePath)), nil
	}
	return os.Getwd()
}

func launchProfile(name, configPath string) (int, error) {
	if !profileNameRe.MatchString(name) {
		return 1, fmt.Errorf("invalid profile name %q (allowed: letters, digits, dot, underscore, hyphen)", name)
	}
	ws, err := launchWorkspace(configPath)
	if err != nil {
		return 1, err
	}
	ucPath, err := userconfig.DefaultPath()
	if err != nil {
		return 1, err
	}
	uc, err := userconfig.Load(ucPath)
	if err != nil {
		return 1, err
	}
	safeslopPath, err := os.Executable()
	if err != nil {
		return 1, err
	}
	cmd := launch.Command(safeslopPath, name, ws, uc.Tag.OSCTitle)
	argv := launch.AdapterArgv(uc.Terminal, uc.Shell, cmd)
	if err := osexec.Command(argv[0], argv[1:]...).Run(); err != nil {
		return 1, fmt.Errorf("open terminal (%s): %w", uc.Terminal, err)
	}
	return 0, nil
}

// mountedVolumes returns a best-effort snapshot of non-boot macOS volumes for the
// host consent scope line. Missing /Volumes (Linux CI) or read errors degrade to
// an empty list because the fixed headline already states the host blast radius.
func mountedVolumes() []string {
	entries, err := os.ReadDir("/Volumes")
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.Name() == "Macintosh HD" {
			continue
		}
		out = append(out, "/Volumes/"+e.Name())
	}
	return out
}

// ---- helpers ----

func newInvocationID() (string, error) {
	var random [16]byte
	if _, err := cryptorand.Read(random[:]); err != nil {
		return "", fmt.Errorf("create direct invocation identity: %w", err)
	}
	return "run-" + hex.EncodeToString(random[:]), nil
}

// runDirectProfile is the direct-run front. Its random stage key is generated
// after trust/consent in cmdRun, so a reusable profile name is never ownership.
func runDirectProfile(name string, prof policy.Profile, argv []string, ws string) (int, error) {
	return runDirectProfileWithDeps(defaultDependencies(), name, prof, argv, ws)
}

func runDirectProfileWithDeps(d *dependencies, name string, prof policy.Profile, argv []string, ws string) (int, error) {
	invocationID, err := newInvocationID()
	if err != nil {
		return 1, err
	}
	return runProfileWithStageKeyAndDeps(d, name, prof, argv, ws, invocationID)
}

// runProfile stages secrets + credentials into an ephemeral dir under the
// workspace, launches the agent under its environment, and wipes the stage on
// exit. Returns the child's exit code. Legacy callers omit a stage key so their
// deployed workspace-hashed session layout remains reconstructable.
func runProfile(name string, prof policy.Profile, argv []string, ws string) (int, error) {
	return runProfileWithStageKeyAndDeps(defaultDependencies(), name, prof, argv, ws, "")
}

func runProfileWithStageKey(name string, prof policy.Profile, argv []string, ws, stageKey string) (int, error) {
	return runProfileWithStageKeyAndDeps(defaultDependencies(), name, prof, argv, ws, stageKey)
}

func runProfileWithStageKeyAndDeps(d *dependencies, name string, prof policy.Profile, argv []string, ws, stageKey string) (int, error) {
	return runProfileWithStageKeyAndEngineAndDeps(d, nil, name, prof, argv, ws, stageKey)
}

func runProfileWithStageKeyAndEngineAndDeps(d *dependencies, eng runtimepkg.Engine, name string, prof policy.Profile, argv []string, ws, stageKey string) (int, error) {
	// SIGTERM (what `session stop` sends to this wrapper) and SIGHUP (terminal /
	// Emacs-buffer close) cancel the run so the boundary is torn down and staged
	// secrets are wiped via the deferred teardown in runProfileCtx, instead of
	// the process dying with its defers unrun and leaving a live container
	// holding staged secrets (specs/0050 PR2, gap #2). SIGINT is deliberately
	// NOT caught: interactive Ctrl-C must reach the agent (e.g. interrupt a
	// generation), not tear the session down. runProfile is shared with
	// `safeslop run`, so this also gives that path a graceful teardown without
	// changing its Ctrl-C behavior. SIGKILL is uncatchable; PR1's liveness
	// reconcile is the backstop for a wrapper that dies without cleaning up.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	return runProfileCtxWithEngineAndDeps(d, ctx, eng, name, prof, argv, ws, stageKey)
}

// runIO optionally rebinds an agent's stdio. The zero value is the coupled path:
// the agent inherits the wrapper's stdio (a tty under Emacs make-term). A detached
// supervisor has no inherited terminal, so it passes a PTY slave it owns: host
// runs the agent on that slave as its controlling terminal, and container
// binds the `compose run` process's stdio to it so the container's tty bridges
// back (specs/0051 D2).
type runIO struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}
