// Package cli is the safeslop command tree. Every command drives the engine
// packages and (with --json) emits machine-readable output so a future GUI can
// drive the same engine without re-implementing logic (specs/0001 §6, §A).
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	osexec "os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/freakhill/safeslop/internal/engine/container"
	runtimepkg "github.com/freakhill/safeslop/internal/engine/container/runtime"
	"github.com/freakhill/safeslop/internal/engine/creds"
	engexec "github.com/freakhill/safeslop/internal/engine/exec"
	"github.com/freakhill/safeslop/internal/engine/gitguard"
	"github.com/freakhill/safeslop/internal/engine/hostenv"
	"github.com/freakhill/safeslop/internal/engine/install"
	"github.com/freakhill/safeslop/internal/engine/launch"
	"github.com/freakhill/safeslop/internal/engine/policy"
	"github.com/freakhill/safeslop/internal/engine/sandbox"
	"github.com/freakhill/safeslop/internal/engine/secrets"
	engsession "github.com/freakhill/safeslop/internal/engine/session"
	"github.com/freakhill/safeslop/internal/engine/toolchain"
	"github.com/freakhill/safeslop/internal/engine/trust"
	"github.com/freakhill/safeslop/internal/engine/userconfig"
	"github.com/freakhill/safeslop/internal/engine/vm"
	"github.com/freakhill/safeslop/internal/jsoncontract"
)

// Version is overridden at build time via -ldflags "-X .../cli.Version=...".
var Version = "dev"

var jsonOut bool

// Execute runs the root command and exits non-zero on error.
func Execute() {
	if err := newRoot().Execute(); err != nil {
		if !errors.Is(err, errOutputEmitted) {
			if !jsonOut {
				fmt.Fprintln(os.Stderr, "safeslop:", err)
			} else {
				emitJSON(map[string]any{"ok": false, "error": err.Error()})
			}
		}
		os.Exit(1)
	}
}

func newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:           "safeslop",
		Short:         "Launch coding agents under isolation, driven by safeslop.cue",
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().BoolVar(&jsonOut, "json", false, "emit machine-readable JSON output")
	root.AddCommand(cmdValidate(), cmdList(), cmdDoctor(), cmdRun(), cmdSession(), cmdTrust(), cmdDown(), cmdLaunch(), cmdInstall(), cmdUninstall())
	return root
}

// ---- validate ----

func cmdValidate() *cobra.Command {
	return &cobra.Command{
		Use:   "validate [safeslop.cue]",
		Short: "Validate a safeslop.cue against the embedded schema",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			path, err := findConfig(arg0(args))
			if err != nil {
				return err
			}
			warns, err := validateAndLint(path)
			if err != nil {
				return err
			}
			if jsonOut {
				emitContract(jsoncontract.OK(map[string]any{"path": path}, lintWarnings(warns)...))
			} else {
				fmt.Printf("ok: %s is valid\n", path)
				printWarnings(warns)
			}
			return nil
		},
	}
}

// ---- list ----

func cmdList() *cobra.Command {
	return &cobra.Command{
		Use:   "list [safeslop.cue]",
		Short: "List the profiles defined in safeslop.cue",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			path, err := findConfig(arg0(args))
			if err != nil {
				return err
			}
			cfg, err := policy.Load(path)
			if err != nil {
				return err
			}
			names := make([]string, 0, len(cfg.Profiles))
			for n := range cfg.Profiles {
				names = append(names, n)
			}
			sort.Strings(names)
			if jsonOut {
				emitJSON(map[string]any{"ok": true, "path": path, "profiles": cfg.Profiles})
				return nil
			}
			if len(names) == 0 {
				fmt.Println("no profiles defined")
				return nil
			}
			for _, n := range names {
				p := cfg.Profiles[n]
				fmt.Printf("%-16s agent=%-8s environment=%-9s network=%s\n", n, p.Agent, p.Environment, p.Network)
			}
			return nil
		},
	}
}

// ---- doctor ----

// doctorReport probes the external tools and isolation boundaries safeslop can use.
// Extracted so it is testable and reusable (e.g. a future GUI / installer).
func doctorReport() map[string]any {
	tools := []string{"git", "gh", "docker", "op", "claude", "pi", "tart", "mise", "nix", "aws", "gcloud", "gke-gcloud-auth-plugin"}
	report := map[string]any{}
	for _, t := range tools {
		p, err := osexec.LookPath(t)
		report[t] = map[string]any{"present": err == nil, "path": p}
	}
	report["sandbox-exec"] = map[string]any{"present": sandbox.Available(), "path": sandbox.SandboxExecPath}
	report["1password-signedin"] = map[string]any{"present": secrets.OpSignedIn(context.Background()), "path": ""}
	report["container-runtime"] = map[string]any{"present": container.Available(), "path": ""}
	report["vm-runtime"] = map[string]any{"present": vm.Available(), "path": ""}
	return report
}

// doctorTiers renders the per-environment isolation tier legend (shared by doctor's human + JSON),
// so the honest "what each boundary protects" framing is never implicit (ayo §10.5 H1).
func doctorTiers() map[string]map[string]string {
	out := map[string]map[string]string{}
	for _, env := range []string{"host", "sandbox", "container", "vm"} {
		tier, note := policy.EnvTier(env)
		out[env] = map[string]string{"tier": tier, "note": note}
	}
	return out
}

func cmdDoctor() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Report which external tools and boundaries are available",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			report := doctorReport()
			if jsonOut {
				emitContract(jsoncontract.OK(map[string]any{"os": runtime.GOOS, "arch": runtime.GOARCH, "tools": report, "tiers": doctorTiers()}))
				return nil
			}
			fmt.Printf("safeslop %s  (%s/%s)\n", Version, runtime.GOOS, runtime.GOARCH)
			names := make([]string, 0, len(report))
			for n := range report {
				names = append(names, n)
			}
			sort.Strings(names)
			for _, n := range names {
				m := report[n].(map[string]any)
				mark := "no"
				if m["present"].(bool) {
					mark = "yes"
				}
				fmt.Printf("  %-14s %-4s %s\n", n, mark, m["path"])
			}
			fmt.Println("isolation tiers (what each environment actually protects):")
			for _, env := range []string{"host", "sandbox", "container", "vm"} {
				tier, note := policy.EnvTier(env)
				fmt.Printf("  %-10s %-16s %s\n", env, tier, note)
			}
			return nil
		},
	}
}

// ---- run ----

func cmdRun() *cobra.Command {
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
			cfg, err := policy.Load(path)
			if err != nil {
				return err
			}
			name, prof, err := selectProfile(cfg, arg0(args))
			if err != nil {
				return err
			}
			if !jsonOut {
				printWarnings(policy.Lint(&policy.Config{Profiles: map[string]policy.Profile{name: prof}}))
			}
			argv, err := agentArgv(prof)
			if err != nil {
				return err
			}
			if prof.Toolchain != nil && toolchain.Wraps(prof.Toolchain.Kind) {
				argv = toolchain.Wrap(prof.Toolchain.Kind, prof.Toolchain.Run, argv) // wrap before env switch (SP5)
			}
			ws := prof.Workspace
			if ws == "" {
				ws, _ = os.Getwd()
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
				if prof.Environment == "sandbox" {
					out["sandbox_profile"] = sandbox.Profile(ws, prof.Network, "", sandboxScope(prof.Files))
				}
				if jsonOut {
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
					if prof.Environment == "sandbox" {
						fmt.Printf("--- seatbelt profile ---\n%s", sandbox.Profile(ws, prof.Network, "", sandboxScope(prof.Files)))
					}
				}
				return nil
			}

			if !jsonOut {
				fmt.Printf("isolation tier: %s — %s\n", tier, tierNote)
			}

			// Fail-closed: only an explicitly host-approved safeslop.cue may launch an agent
			// (specs/0022). --dry-run above stays ungated — it is inspection, like validate.
			if err := enforceTrust(path, trustFlag); err != nil {
				return err
			}
			code, err := runProfile(name, prof, argv, ws)
			if err != nil {
				// Surface the failure reason. runProfile returns code=1 on setup
				// errors (e.g. a deny-network VM with no SAFESLOP_VM_PROXY_URL), so
				// the old `&& code == 0` guard silently dropped them — a launch that
				// failed with no diagnostic. cobra prints returned errors as "Error: …".
				return err
			}
			os.Exit(code)
			return nil
		},
	}
	c.Flags().BoolVar(&dryRun, "dry-run", false, "print the resolved launch + sandbox profile without executing")
	c.Flags().BoolVar(&trustFlag, "trust", false, "approve this safeslop.cue, then run it")
	return c
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

var errOutputEmitted = errors.New("machine-readable error already emitted")

var sessionRevokeCredentials = func(sess engsession.Session) error {
	stageDir := filepath.Join(sess.Workspace, ".safeslop", "runtime", "session-"+sess.ID)
	creds.RevokeSSH(context.Background(), stageDir)
	creds.RevokeForgejo(context.Background(), stageDir)
	return nil
}

// stopGraceTimeout bounds the graceful SIGTERM wait before a detached supervisor's
// process group is SIGKILLed on stop (specs/0051 D6). Overridable in tests.
var stopGraceTimeout = 5 * time.Second

// sessionKillProcess signals a session's recorded target. A positive target is a
// coupled run's wrapper PID (bare SIGTERM, unchanged from 0050). A negative target
// is a detached supervisor's process group (-pgid): SIGTERM the group, wait bounded
// (stopGraceTimeout), then SIGKILL the group so the whole boundary tree is reached
// (specs/0051 D4/D6).
var sessionKillProcess = func(target int) error {
	switch {
	case target == 0:
		return nil
	case target > 0:
		return syscall.Kill(target, syscall.SIGTERM)
	}
	_ = syscall.Kill(target, syscall.SIGTERM)
	deadline := time.Now().Add(stopGraceTimeout)
	for time.Now().Before(deadline) {
		if syscall.Kill(target, 0) != nil {
			return nil // the group is gone
		}
		time.Sleep(50 * time.Millisecond)
	}
	return syscall.Kill(target, syscall.SIGKILL)
}

// sessionProcessAlive probes whether a recorded session PID is still live, so
// status/list can reconcile a session whose run wrapper died without recording
// an exit. Overridable in tests.
var sessionProcessAlive = engsession.ProcessAlive

func cmdSession() *cobra.Command {
	c := &cobra.Command{Use: "session", Short: "Manage Emacs-visible safeslop sessions"}
	c.AddCommand(cmdSessionCreate(), cmdSessionRun(), cmdSessionStatus(), cmdSessionStop(), cmdSessionList(), cmdSessionSupervise(), cmdSessionAttach())
	return c
}

func cmdSessionCreate() *cobra.Command {
	var agent, workspace, output string
	c := &cobra.Command{
		Use:   "create --agent <pi|claude|claude-code> --workspace <dir> --output json",
		Short: "Create a safeslop session record",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if output != "json" {
				return fmt.Errorf("session create requires --output json")
			}
			canonicalAgent := policy.NormalizeAgent(agent)
			if canonicalAgent != "pi" && canonicalAgent != "claude" {
				return emitContractError(jsoncontract.CodeAgentUnsupported, fmt.Sprintf("unsupported agent %q", agent), map[string]any{"agent": agent})
			}
			if workspace == "" {
				return emitContractError(jsoncontract.CodeInvalidArgument, "--workspace is required", nil)
			}
			if fi, err := os.Stat(workspace); err != nil || !fi.IsDir() {
				return emitContractError(jsoncontract.CodeInvalidArgument, "--workspace must name an existing directory", map[string]any{"workspace": workspace})
			}
			sess, err := sessionStore().Create(canonicalAgent, workspace, time.Now())
			if err != nil {
				return emitContractError(jsoncontract.CodeIOError, "create session", map[string]any{"error": err.Error()})
			}
			emitContract(jsoncontract.OK(sessionData(sess)))
			return nil
		},
	}
	c.Flags().StringVar(&agent, "agent", "", "agent to run: pi, claude, or claude-code")
	c.Flags().StringVar(&workspace, "workspace", "", "workspace directory")
	c.Flags().StringVar(&output, "output", "", "output format: json")
	return c
}

func cmdSessionList() *cobra.Command {
	var output string
	c := &cobra.Command{
		Use:   "list --output json",
		Short: "List safeslop sessions",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if output != "json" {
				return fmt.Errorf("session list requires --output json")
			}
			sessions, err := sessionStore().ListReconciled(time.Now(), sessionProcessAlive)
			if err != nil {
				return emitContractError(jsoncontract.CodeIOError, "list sessions", map[string]any{"error": err.Error()})
			}
			items := make([]map[string]any, 0, len(sessions))
			for _, sess := range sessions {
				items = append(items, sessionData(sess))
			}
			emitContract(jsoncontract.OK(map[string]any{"sessions": items}))
			return nil
		},
	}
	c.Flags().StringVar(&output, "output", "", "output format: json")
	return c
}

func cmdSessionStatus() *cobra.Command {
	var id, output string
	c := &cobra.Command{
		Use:   "status --session-id <id> --output <json|jsonl>",
		Short: "Report safeslop session status",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if output != "json" && output != "jsonl" {
				return fmt.Errorf("session status requires --output json or jsonl")
			}
			sess, err := sessionStore().GetReconciled(id, time.Now(), sessionProcessAlive)
			if err != nil {
				if errors.Is(err, engsession.ErrNotFound) {
					return emitContractError(jsoncontract.CodeSessionNotFound, "session not found", map[string]any{"session_id": id})
				}
				return emitContractError(jsoncontract.CodeIOError, "load session", map[string]any{"error": err.Error()})
			}
			env := jsoncontract.OK(sessionData(sess))
			if output == "jsonl" {
				emitContractLine(env)
			} else {
				emitContract(env)
			}
			return nil
		},
	}
	c.Flags().StringVar(&id, "session-id", "", "session id")
	c.Flags().StringVar(&output, "output", "", "output format: json or jsonl")
	return c
}

func cmdSessionStop() *cobra.Command {
	var id, output string
	var revoke bool
	c := &cobra.Command{
		Use:   "stop --session-id <id> --revoke-credentials --output json",
		Short: "Stop a safeslop session",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if output != "json" {
				return fmt.Errorf("session stop requires --output json")
			}
			sess, err := sessionStore().Stop(id, revoke, time.Now(), sessionRevokeCredentials, sessionKillProcess)
			if err != nil {
				if errors.Is(err, engsession.ErrNotFound) {
					return emitContractError(jsoncontract.CodeSessionNotFound, "session not found", map[string]any{"session_id": id})
				}
				return emitContractError(jsoncontract.CodeCredentialRevokeFailed, "stop session", map[string]any{"error": err.Error()})
			}
			emitContract(jsoncontract.OK(sessionData(sess)))
			return nil
		},
	}
	c.Flags().StringVar(&id, "session-id", "", "session id")
	c.Flags().BoolVar(&revoke, "revoke-credentials", false, "revoke ephemeral credentials before stopping")
	c.Flags().StringVar(&output, "output", "", "output format: json")
	return c
}

func cmdSessionRun() *cobra.Command {
	var id string
	var detach bool
	c := &cobra.Command{
		Use:   "run --session-id <id>",
		Short: "Run a safeslop session's agent",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			store := sessionStore()
			sess, err := store.Get(id)
			if err != nil {
				return err
			}
			if detach {
				// Detached: re-exec a supervisor that owns the agent + its PTY and
				// serves it over the per-session socket, then return so the issuing
				// buffer is freed immediately. No local tty is needed here (the
				// supervisor allocates the PTY; the user attaches later), so the
				// interactive PTY guard below is intentionally skipped (specs/0051
				// D1, PR3).
				return runDetach(store, id)
			}
			prof := policy.Profile{Agent: sess.Agent, Environment: sess.Environment, Network: sess.Network, Workspace: sess.Workspace}
			argv, err := agentArgv(prof)
			if err != nil {
				return err
			}
			// `session run` is an interactive attach: every boundary presents the
			// agent under a controlling terminal (host/sandbox via RunInTerminal,
			// container via the RunInPTY tty bridge, vm via `ssh -t`), so without a
			// usable PTY the session is undriveable on all four. Emacs drives this via
			// make-term, which connects the process to a pty; a no-tty invocation
			// (cron, a pipe, a headless shell) gets the PTY_UNAVAILABLE contract error
			// pointing at the JSONL status fallback, exits non-zero, and is *not*
			// marked running — a session that can never start must not be left as a
			// phantom for liveness/reconcile or `session stop` (specs/0050 PR4).
			if !sessionHasInteractivePTY() {
				emitContract(jsoncontract.PTYUnavailable())
				return errOutputEmitted
			}
			if _, err := store.MarkRunning(id, os.Getpid(), time.Now()); err != nil {
				return err
			}
			code, runErr := runProfile("session-"+id, prof, argv, sess.Workspace)
			lastErr := ""
			if runErr != nil {
				lastErr = runErr.Error()
			}
			_, _ = store.Finish(id, code, lastErr, time.Now())
			if runErr != nil {
				return runErr
			}
			os.Exit(code)
			return nil
		},
	}
	c.Flags().StringVar(&id, "session-id", "", "session id")
	c.Flags().BoolVar(&detach, "detach", false, "run the agent under a detached supervisor and return immediately")
	return c
}

// detachReadyTimeout bounds how long `run --detach` waits for the supervisor's
// socket to appear before declaring the launch failed (specs/0051 Q1). Overridable
// in tests.
var detachReadyTimeout = 2 * time.Second

// launchSupervisor re-execs this binary as a detached per-session supervisor (the
// hidden `session supervise`) and returns the supervisor's PID. This is the
// canonical Go daemonization: a new session via Setsid (no controlling tty), which
// also makes the child its own process-group leader so a later `session stop` can
// signal the whole tree via kill(-pgid) (specs/0051 D4), plus fully detached stdio.
// Overridable in tests so no real setsid or second binary is needed (the specs/0051
// D1 test seam).
var launchSupervisor = func(id string) (int, error) {
	exe, err := os.Executable()
	if err != nil {
		return 0, err
	}
	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return 0, err
	}
	defer null.Close()
	cmd := osexec.Command(exe, "session", "supervise", "--session-id", id)
	// Setsid only: it makes the child a new session AND process-group leader
	// (pgid == pid). Adding Setpgid on top is invalid — a session leader cannot
	// setpgid (EPERM), which fails the fork/exec ("operation not permitted").
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = null, null, null
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release() // detach: the daemon is never Wait()ed on
	return pid, nil
}

// runDetach launches the supervisor, waits (bounded) for its socket so a reported
// success means the agent is actually reachable, records the supervisor PID, and
// emits the session envelope. On readiness timeout it kills the half-born
// supervisor and emits a contract error, leaving the session not-running so no
// phantom is left for liveness/reconcile or `session stop` (specs/0051 Q1).
func runDetach(store engsession.Store, id string) error {
	pid, err := launchSupervisor(id)
	if err != nil {
		return emitContractError(jsoncontract.CodeIOError, "launch session supervisor", map[string]any{"error": err.Error()})
	}
	sockPath := store.SocketPath(id)
	if !waitForFile(sockPath, detachReadyTimeout) {
		_ = sessionKillProcess(pid) // best-effort; the supervisor never became ready
		return emitContractError(jsoncontract.CodeIOError, "session supervisor did not become ready", map[string]any{"session_id": id})
	}
	if _, err := store.MarkRunningDetached(id, pid, time.Now()); err != nil {
		return err
	}
	sess, err := store.Get(id)
	if err != nil {
		return err
	}
	emitContract(jsoncontract.OK(sessionData(sess)))
	return nil
}

// waitForFile polls until path exists or the timeout elapses.
func waitForFile(path string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if _, err := os.Stat(path); err == nil {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// cmdSessionSupervise is the hidden re-exec target launched by `run --detach`. It
// runs the per-session supervisor in this process and exits with the agent's code.
// SIGTERM cancels the run so the supervisor tears the agent + boundary down before
// exiting; os.Exit lives only here at the cobra boundary (specs/0051 PR2/PR3).
func cmdSessionSupervise() *cobra.Command {
	var id string
	c := &cobra.Command{
		Use:    "supervise --session-id <id>",
		Short:  "(internal) run a detached session supervisor",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
			defer stop()
			code, err := Supervise(ctx, sessionStore(), id, time.Now)
			if err != nil {
				return err
			}
			os.Exit(code)
			return nil
		},
	}
	c.Flags().StringVar(&id, "session-id", "", "session id")
	return c
}

// cmdSessionAttach attaches to a detached session's supervisor over its socket,
// bridging the local terminal to the agent and exiting with the agent's code.
// Like `run`, it needs a usable controlling terminal: with none it emits
// PTY_UNAVAILABLE pointing at the JSONL status fallback, before any connect
// attempt. os.Exit lives only here at the cobra boundary, so attachSession stays
// drivable to completion in tests (specs/0051 PR4).
func cmdSessionAttach() *cobra.Command {
	var id string
	c := &cobra.Command{
		Use:   "attach --session-id <id>",
		Short: "Attach to a detached safeslop session",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if !sessionHasInteractivePTY() {
				emitContract(jsoncontract.PTYUnavailable())
				return errOutputEmitted
			}
			code, err := attachSession(sessionStore(), id, os.Stdin, os.Stdout, attachResizeChannel())
			if err != nil {
				contractCode, message := attachFailureContract(err)
				return emitContractError(contractCode, message, map[string]any{"session_id": id, "error": err.Error()})
			}
			os.Exit(code)
			return nil
		},
	}
	c.Flags().StringVar(&id, "session-id", "", "session id")
	return c
}

// errSupervisorUnreachable marks an attach that failed at the dial: there is no
// live supervisor socket to bridge to. It is wrapped around the net.Dial error so
// attachFailureContract can map it to SESSION_NOT_RUNNING, distinct from a bridge
// that died after the connection was already live (SESSION_STOPPED).
var errSupervisorUnreachable = errors.New("no live supervisor socket to attach to")

// attachSession dials the per-session socket and bridges in/out to the agent,
// returning the agent's exit code from the X frame. Returning (rather than
// os.Exit-ing) keeps it drivable to completion in tests. A failed dial is wrapped
// in errSupervisorUnreachable so the caller can report SESSION_NOT_RUNNING.
func attachSession(store engsession.Store, id string, in io.Reader, out io.Writer, resize <-chan [2]uint16) (int, error) {
	conn, err := net.Dial("unix", store.SocketPath(id))
	if err != nil {
		return 1, fmt.Errorf("%w: %v", errSupervisorUnreachable, err)
	}
	defer conn.Close()
	return engsession.Attach(conn, in, out, resize)
}

// attachFailureContract maps an attachSession error to its contract code and
// message. A dial that never reached a live supervisor is SESSION_NOT_RUNNING (the
// session exists in the store, or did, but nothing is serving its socket); any
// other failure happened on an already-live bridge and stays SESSION_STOPPED.
// attach is a pure client that never loads the store, so it does not distinguish a
// never-created id from a stopped one — both honestly read as "not running".
func attachFailureContract(err error) (jsoncontract.ErrorCode, string) {
	if errors.Is(err, errSupervisorUnreachable) {
		return jsoncontract.CodeSessionNotRunning, "session is not running; start it before attaching"
	}
	return jsoncontract.CodeSessionStopped, "attach to session ended"
}

// attachResizeChannel reports the local terminal size on SIGWINCH (and once up
// front) as {rows, cols}, which the attach client forwards as R frames so the
// agent's PTY tracks the window. Lives in the cobra path (a real tty); the
// hermetic tests pass a nil channel.
func attachResizeChannel() <-chan [2]uint16 {
	ch := make(chan [2]uint16, 1)
	send := func() {
		if w, h, err := term.GetSize(int(os.Stdout.Fd())); err == nil {
			select {
			case ch <- [2]uint16{uint16(h), uint16(w)}: // rows = height, cols = width
			default:
			}
		}
	}
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	go func() {
		for range winch {
			send()
		}
	}()
	send() // initial size
	return ch
}

// sessionHasInteractivePTY reports whether `session run` has a usable controlling
// terminal for the agent. Both stdin and stdout must be a tty: the agent needs a
// keyboard (stdin) and a display (stdout) to be interactive, and Emacs make-term
// supplies both. Either one being a pipe (the no-controlling-terminal case) means
// the interactive run path cannot be driven, so the caller must fall back to the
// JSONL status monitor (specs/0050 PR4).
func sessionHasInteractivePTY() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}

func sessionStore() engsession.Store {
	root := os.Getenv("SAFESLOP_STATE_DIR")
	if root == "" {
		if userState, err := os.UserConfigDir(); err == nil {
			root = filepath.Join(userState, "safeslop")
		} else {
			root = filepath.Join(os.TempDir(), "safeslop")
		}
	}
	return engsession.NewStore(filepath.Join(root, "sessions"))
}

func sessionData(sess engsession.Session) map[string]any {
	out := map[string]any{
		"session_id":          sess.ID,
		"agent":               sess.Agent,
		"workspace":           sess.Workspace,
		"environment":         sess.Environment,
		"network":             sess.Network,
		"status":              sess.Status,
		"created_at":          sess.CreatedAt.Format(time.RFC3339Nano),
		"updated_at":          sess.UpdatedAt.Format(time.RFC3339Nano),
		"credentials_revoked": sess.CredentialsRevoked,
	}
	if !sess.StartedAt.IsZero() {
		out["started_at"] = sess.StartedAt.Format(time.RFC3339Nano)
	}
	if !sess.StoppedAt.IsZero() {
		out["stopped_at"] = sess.StoppedAt.Format(time.RFC3339Nano)
	}
	if !sess.RevokedAt.IsZero() {
		out["revoked_at"] = sess.RevokedAt.Format(time.RFC3339Nano)
	}
	if sess.PID != 0 {
		out["pid"] = sess.PID
	}
	if sess.ExitCode != nil {
		out["exit_code"] = *sess.ExitCode
	}
	if sess.LastError != "" {
		out["last_error"] = sess.LastError
	}
	if path, ok := sessionSocket(sess); ok {
		out["socket"] = path
	}
	return out
}

// sessionSocket reports a session's per-session socket path, but only when the
// session is running and the socket actually exists on disk (specs/0051 D5): the
// path is derived from the state root the supervisor binds, never persisted, so we
// only ever advertise a socket that is really there. Overridable in tests.
var sessionSocket = func(sess engsession.Session) (string, bool) {
	if sess.Status != engsession.StatusRunning {
		return "", false
	}
	path := sessionStore().SocketPath(sess.ID)
	if _, err := os.Stat(path); err != nil {
		return "", false
	}
	return path, true
}

func emitContract(env jsoncontract.Envelope) {
	b, err := jsoncontract.Marshal(env)
	if err != nil {
		panic(err)
	}
	_, _ = os.Stdout.Write(b)
}

func emitContractLine(env jsoncontract.Envelope) {
	if err := jsoncontract.Validate(env); err != nil {
		panic(err)
	}
	b, err := json.Marshal(env)
	if err != nil {
		panic(err)
	}
	_, _ = os.Stdout.Write(append(b, '\n'))
}

func emitContractError(code jsoncontract.ErrorCode, message string, details map[string]any) error {
	emitContract(jsoncontract.Error(jsoncontract.NewMessage(code, message, false, details)))
	return errOutputEmitted
}

func enforceTrust(policyPath string, allowTrust bool) error {
	abs := canonicalPolicyPath(policyPath)
	policyBytes, err := os.ReadFile(abs)
	if err != nil {
		return err
	}
	storePath, err := trust.DefaultPath()
	if err != nil {
		return err
	}
	store, err := trust.Load(storePath)
	if err != nil {
		return err
	}
	if allowTrust {
		return store.Approve(abs, policyBytes)
	}
	switch store.Check(abs, policyBytes) {
	case trust.Trusted:
		return nil
	case trust.Changed:
		return fmt.Errorf("safeslop.cue at %s changed since you trusted it (an agent or edit may have modified it).\n  review it, then run:  safeslop trust %s", abs, abs)
	default: // Untrusted
		return fmt.Errorf("safeslop.cue at %s is not trusted (a policy can grant network and secret access).\n  review it, then run:  safeslop trust %s", abs, abs)
	}
}

func cmdTrust() *cobra.Command {
	return &cobra.Command{
		Use:   "trust [safeslop.cue]",
		Short: "Record approval of a repo's safeslop.cue so `safeslop run` will honor it",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			path, err := findConfig(arg0(args))
			if err != nil {
				return err
			}
			if err := enforceTrust(path, true); err != nil {
				return err
			}
			abs, _ := filepath.Abs(path)
			if jsonOut {
				emitJSON(map[string]any{"ok": true, "trusted": abs})
			} else {
				fmt.Printf("trusted: %s\n", abs)
			}
			return nil
		},
	}
}

// ---- down ----

func cmdDown() *cobra.Command {
	return &cobra.Command{
		Use:   "down",
		Short: "Tear down the container stack (squid) and any disposable VM sessions",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if !container.Available() && !vm.Available() {
				return fmt.Errorf("nothing to tear down: neither docker nor tart is available (run: safeslop doctor)")
			}
			if container.Available() {
				dir, composeFile, err := container.ComposeForDown()
				if err != nil {
					return err
				}
				defer os.RemoveAll(dir)
				// `safeslop down` targets the ambient host docker (the lima VM is torn down per-run by its
				// own backend Teardown); the engine seam defaults to the host docker engine here.
				if err := container.Down(context.Background(), runtimepkg.HostDockerEngine{}, composeFile); err != nil {
					return err
				}
			}
			if vm.Available() {
				_ = vm.DestroyAll(context.Background())
			}
			// Also reap the safeslop-managed lima container VM if one was provisioned (idempotent: a no-op
			// when limactl is absent or no instance exists). Keeps `down` a complete teardown for the
			// opt-in lima backend.
			if dirs, derr := install.DefaultDirs(); derr == nil {
				_ = runtimepkg.NewLimaBackend(dirs).Teardown(context.Background())
			}
			return nil
		},
	}
}

func cmdInstall() *cobra.Command {
	c := &cobra.Command{
		Use:   "install",
		Short: "Inventory and (later) provision the safeslop toolchain",
	}
	c.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Report whether safeslop, toolchains, and runtimes are installed",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if jsonOut {
				fmt.Println(renderInstallStatusJSON(Version))
				return nil
			}
			st := install.Status(context.Background(), Version)
			fmt.Printf("safeslop %s  (on PATH: %v)\n", st.Self.Version, st.Self.OnPath)
			if st.Self.Path != "" {
				fmt.Printf("  binary: %s\n", st.Self.Path)
			}
			printTools("toolchains", st.Toolchains)
			printTools("runtimes", st.Runtimes)
			return nil
		},
	})
	c.AddCommand(&cobra.Command{
		Use:   "plan",
		Short: "Show the pinned actions needed to install/upgrade toolchains + runtimes",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if jsonOut {
				out, err := renderInstallPlanJSON(Version)
				if err != nil {
					return err
				}
				fmt.Println(out)
				return nil
			}
			res, err := installPlanResult(Version)
			if err != nil {
				return err // fail closed: a bad manifest is an error, not an empty plan
			}
			fmt.Printf("%d change(s) pending\n", res.Pending())
			for _, a := range res.Actions {
				cur := a.Current
				if cur == "" {
					cur = "-"
				}
				fmt.Printf("  %-10s %-8s %s -> %s\n", a.Name, a.Kind, cur, a.Desired)
			}
			if len(res.Actions) == 0 {
				fmt.Println("  (desired-state manifest is empty)")
			}
			if msg := install.Freshness(time.Now()).Message(); msg != "" {
				fmt.Printf("\nnote: %s\n", msg) // advisory freshness floor — warn, never block
			}
			return nil
		},
	})
	c.AddCommand(func() *cobra.Command {
		var dryRun bool
		ac := &cobra.Command{
			Use:   "apply",
			Short: "Download, verify (fail-closed), and install the pinned toolchains + runtimes",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				res, err := installPlanResult(Version)
				if err != nil {
					return err
				}
				if dryRun {
					if jsonOut {
						out, _ := renderInstallApplyDryRunJSON(Version)
						fmt.Println(out)
						return nil
					}
					fmt.Printf("%d change(s) would be applied\n", res.Pending())
					for _, a := range res.Actions {
						if a.Kind != install.ActionOK {
							fmt.Printf("  %-10s %-8s -> %s\n", a.Name, a.Kind, a.Desired)
						}
					}
					return nil
				}
				dirs, err := install.DefaultDirs()
				if err != nil {
					return err
				}
				emit := func(e install.Event) {
					if jsonOut {
						emitJSON(map[string]any{"kind": e.Kind, "tool": e.Tool, "msg": e.Msg})
					} else {
						fmt.Printf("  [%s] %s %s\n", e.Tool, e.Kind, e.Msg)
					}
				}
				if err := install.Apply(cmd.Context(), res, dirs, install.HTTPFetcher{}, emit); err != nil {
					return err
				}
				warnIfNotOnPath(dirs.BinDir)
				return nil
			},
		}
		ac.Flags().BoolVar(&dryRun, "dry-run", false, "show what would be installed without doing it")
		return ac
	}())
	c.AddCommand(&cobra.Command{
		Use:   "rollback <tool>",
		Short: "Restore a tool's prior version, kept as a backup by the last install",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			dirs, err := install.DefaultDirs()
			if err != nil {
				return err
			}
			name := args[0]
			if err := install.Rollback(name, dirs); err != nil {
				return err // fail clearly when there is no backup to restore
			}
			if jsonOut {
				emitJSON(map[string]any{"ok": true, "rolled_back": name})
			} else {
				fmt.Printf("rolled back %s to its prior version\n", name)
			}
			return nil
		},
	})
	return c
}

func renderInstallApplyDryRunJSON(version string) (string, error) {
	res, err := installPlanResult(version)
	if err != nil {
		return "", err
	}
	b, _ := json.MarshalIndent(map[string]any{"dry_run": true, "actions": res.Actions}, "", "  ")
	return string(b), nil
}

func warnIfNotOnPath(binDir string) {
	for _, p := range filepath.SplitList(os.Getenv("PATH")) {
		if p == binDir {
			return
		}
	}
	fmt.Fprintf(os.Stderr, "note: %s is not on your $PATH — add it so installed tools resolve\n", binDir)
}

func renderInstallStatusJSON(version string) string {
	st := install.Status(context.Background(), version)
	b, _ := json.MarshalIndent(st, "", "  ")
	return string(b)
}

func installPlanResult(version string) (install.Result, error) {
	st := install.Status(context.Background(), version)
	return install.Plan(st, install.DesiredState())
}

func renderInstallPlanJSON(version string) (string, error) {
	res, err := installPlanResult(version)
	if err != nil {
		return "", err
	}
	b, _ := json.MarshalIndent(map[string]any{
		"actions":   res.Actions,
		"freshness": install.Freshness(time.Now()),
	}, "", "  ")
	return string(b), nil
}

func printTools(label string, tools []install.Tool) {
	fmt.Printf("  %s:\n", label)
	for _, t := range tools {
		mark := "no"
		if t.Present {
			mark = "yes"
		}
		v := t.Version
		if v == "" {
			v = "-"
		}
		fmt.Printf("    %-10s %-4s %s\n", t.Name, mark, v)
	}
}

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

// ---- helpers ----

// stageProfile resolves the profile's secrets and stages its credentials into stageDir. It
// returns secretEnv (sensitive KEY=VAL — the resolved secrets plus aws/gcp env creds, destined
// for the secrets.env channel / the process env) and pathEnv (non-secret NPM_CONFIG_USERCONFIG /
// KUBECONFIG / GIT_SSH_COMMAND host paths into stageDir, for the host/sandbox process env). The
// caller owns the stageDir lifecycle (creation, the on-exit wipe, and creds.RevokeSSH if an ssh
// key was staged).
func stageProfile(ctx context.Context, prof policy.Profile, stageDir string) (secretEnv, pathEnv []string, err error) {
	if len(prof.Secrets) > 0 {
		resolved, err := secrets.ResolveMap(ctx, prof.Secrets)
		if err != nil {
			return nil, nil, err
		}
		for k, v := range resolved {
			secretEnv = append(secretEnv, k+"="+v)
		}
	}
	npmrcEnv, err := creds.StagePnpm(ctx, prof.Credentials, stageDir)
	if err != nil {
		return nil, nil, err
	}
	// Cloud creds are short-lived (SSO role creds / ADC access token) and delivered as env vars
	// through the secret channel, so they ride secrets.env (container) / the scp'd env (vm) and
	// reach host/sandbox children too. No revoke: decay-first.
	awsEnv, err := creds.StageAWS(ctx, prof.Credentials, stageDir)
	if err != nil {
		return nil, nil, err
	}
	gcpEnv, err := creds.StageGCP(ctx, prof.Credentials, stageDir)
	if err != nil {
		return nil, nil, err
	}
	secretEnv = append(secretEnv, awsEnv...)
	secretEnv = append(secretEnv, gcpEnv...)
	// kubeconfig / .npmrc / ssh key bearers are staged 0600 in stageDir; KUBECONFIG /
	// NPM_CONFIG_USERCONFIG / GIT_SSH_COMMAND are non-secret host paths delivered via the env for
	// host/sandbox, and via the bind mount (paths set by the compose file) for container.
	kubeEnv, err := creds.StageKube(ctx, prof.Credentials, stageDir)
	if err != nil {
		return nil, nil, err
	}
	sshEnv, err := creds.StageSSH(ctx, prof.Credentials, stageDir)
	if err != nil {
		return nil, nil, err
	}
	// ssh (GitHub) and forgejo both deliver a single GIT_SSH_COMMAND into the same stage; one forge
	// per profile until specs/0047 P2 unifies them via per-repo SSH aliases + insteadOf rewrites.
	if prof.Credentials != nil && prof.Credentials.Ssh != nil && prof.Credentials.Forgejo != nil {
		return nil, nil, fmt.Errorf("credentials: set either ssh (GitHub) or forgejo, not both (multi-forge lands in specs/0047 P2)")
	}
	forgejoEnv, err := creds.StageForgejo(ctx, prof.Credentials, stageDir)
	if err != nil {
		return nil, nil, err
	}
	pathEnv = append(pathEnv, npmrcEnv...)
	pathEnv = append(pathEnv, kubeEnv...)
	pathEnv = append(pathEnv, sshEnv...)
	pathEnv = append(pathEnv, forgejoEnv...)
	return secretEnv, pathEnv, nil
}

// runProfile stages secrets + credentials into an ephemeral dir under the
// workspace, launches the agent under its environment, and wipes the stage on
// exit. Returns the child's exit code.
func runProfile(name string, prof policy.Profile, argv []string, ws string) (int, error) {
	// SIGTERM (what `session stop` sends to this wrapper) and SIGHUP (terminal /
	// Emacs-buffer close) cancel the run so the boundary is torn down and staged
	// secrets are wiped via the deferred teardown in runProfileCtx, instead of
	// the process dying with its defers unrun and leaving a live container/VM
	// holding staged secrets (specs/0050 PR2, gap #2). SIGINT is deliberately
	// NOT caught: interactive Ctrl-C must reach the agent (e.g. interrupt a
	// generation), not tear the session down. runProfile is shared with
	// `safeslop run`, so this also gives that path a graceful teardown without
	// changing its Ctrl-C behavior. SIGKILL is uncatchable; PR1's liveness
	// reconcile is the backstop for a wrapper that dies without cleaning up.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	return runProfileCtx(ctx, name, prof, argv, ws)
}

// runIO optionally rebinds an agent's stdio. The zero value is the coupled path:
// the agent inherits the wrapper's stdio (a tty under Emacs make-term). A detached
// supervisor has no inherited terminal, so it passes a PTY slave it owns: host and
// sandbox run the agent on that slave as its controlling terminal, and container
// binds the `compose run` process's stdio to it so the container's tty bridges
// back (specs/0051 D2). VM (`ssh -t`) is the remaining tier that still ignores it.
type runIO struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// containerLaunch is the container-launch seam: the real container.Launch in
// production, swappable in tests to assert the detached supervisor's PTY is
// forwarded without standing up docker.
var containerLaunch = container.Launch

// vmLaunch is the vm-launch seam: the real vm.Launch in production, swappable in
// tests to assert the detached supervisor's PTY is forwarded without booting a VM.
var vmLaunch = vm.Launch

func runProfileCtx(ctx context.Context, name string, prof policy.Profile, argv []string, ws string, stdio ...runIO) (int, error) {
	var rio runIO
	if len(stdio) > 0 {
		rio = stdio[0]
	}
	stageDir := filepath.Join(ws, ".safeslop", "runtime", name)
	defer os.RemoveAll(stageDir) // wipe staged secrets/.npmrc regardless of outcome

	// kube/ssh creds are staged as files in stageDir and delivered into the VM guest at
	// ~/.safeslop-runtime (scp'd whole), with GIT_SSH_COMMAND/KUBECONFIG exported guest-side by
	// remoteAgentCmd — the guest $HOME is resolved via ~ rather than assumed (specs/0010, 0011,
	// 0039). The container path remains the reference implementation.
	if err := seedAgentDefaults(prof, ws); err != nil {
		return 1, err
	}
	secretEnv, pathEnv, err := stageProfile(ctx, prof, stageDir)
	if err != nil {
		return 1, err
	}
	// Best-effort revoke runs before the stageDir wipe (deferred after the top-of-func wipe, so
	// LIFO orders it first).
	if prof.Credentials != nil && prof.Credentials.Ssh != nil {
		defer creds.RevokeSSH(context.Background(), stageDir)
	}
	if prof.Credentials != nil && prof.Credentials.Forgejo != nil {
		defer creds.RevokeForgejo(context.Background(), stageDir)
	}

	// Detect (and warn about) any change the agent makes to git's executable surface —
	// a planted .git/hooks script or a .git/config hooksPath/fsmonitor/filter that the
	// host would run on its next git command in this repo (specs/0025 S3). Best-effort,
	// never blocks the agent's legitimate git use.
	gitBefore, _ := gitguard.Snapshot(ws)
	defer warnGitExecSurface(ws, gitBefore)

	switch prof.Environment {
	case "sandbox":
		argv = resolveHostBinary(argv) // Finder launch: resolve "claude" off the reconstructed PATH
		env := childEnv(secretEnv, pathEnv)
		// Detached: make the supervisor's PTY the agent's controlling terminal, same
		// as host. sandbox.Launch forwards ControllingTTY through to RunInTerminal,
		// so the sandbox-exec child becomes the session leader that owns the tty
		// (the Seatbelt profile already permits the tty ioctls). Coupled (rio zero)
		// inherits the user's terminal and must not steal it (specs/0051).
		return sandbox.Launch(ctx, engexec.LaunchSpec{Argv: argv, Dir: ws, Env: env, Stdin: rio.Stdin, Stdout: rio.Stdout, Stderr: rio.Stderr, ControllingTTY: rio.Stdin != nil}, ws, prof.Network, sandboxScope(prof.Files))
	case "host":
		argv = resolveHostBinary(argv)
		env := childEnv(secretEnv, pathEnv)
		// A detached supervisor passes a PTY slave it owns (rio set); make it the
		// agent's controlling terminal. The coupled path (rio zero) inherits the
		// user's real terminal and must not steal it (specs/0051).
		return engexec.RunInTerminal(ctx, engexec.LaunchSpec{Argv: argv, Dir: ws, Env: env, Stdin: rio.Stdin, Stdout: rio.Stdout, Stderr: rio.Stderr, ControllingTTY: rio.Stdin != nil})
	case "container":
		// secrets go in secrets.env (sourced by the entrypoint); .npmrc and kubeconfig
		// are staged in stageDir and reached via the /safeslop/runtime bind mount.
		// egress = the agent's built-in providers + the profile's egress: list (specs/0046).
		egress := append(append([]string{}, policy.AgentEgress(prof.Agent)...), prof.Egress...)
		// A detached supervisor passes a PTY slave it owns (rio set); forward it so the
		// container's tty bridges to the supervisor's PTY for attach. Coupled (rio zero)
		// leaves stdio nil and container.Launch runs the user's terminal (specs/0051).
		return containerLaunch(ctx, engexec.LaunchSpec{Argv: argv, Stdin: rio.Stdin, Stdout: rio.Stdout, Stderr: rio.Stderr}, ws, prof.Network, egress, secretEnv, stageDir)
	case "vm":
		// secrets ride secrets.env scp'd into the VM and sourced over ssh; the VM is destroyed on exit.
		tk := ""
		if prof.Toolchain != nil {
			tk = prof.Toolchain.Kind
		}
		// A detached supervisor passes a PTY slave it owns (rio set); forward it so the
		// agent's remote tty (ssh -t) bridges to the supervisor's PTY for attach. Coupled
		// (rio zero) leaves stdio nil and ssh runs on the user's terminal (specs/0051).
		return vmLaunch(ctx, engexec.LaunchSpec{Argv: argv, Stdin: rio.Stdin, Stdout: rio.Stdout, Stderr: rio.Stderr}, prof.Network, secretEnv, stageDir, name, tk)
	default:
		return 1, fmt.Errorf("unknown environment %q", prof.Environment)
	}
}

// warnGitExecSurface prints a prominent warning if the agent changed git's executable surface
// (.git/hooks or .git/config) during the run — a planted hook or config directive runs on your
// next git command in this repo (specs/0025 S3). Best-effort: snapshot errors are ignored.
func warnGitExecSurface(ws string, before gitguard.State) {
	after, err := gitguard.Snapshot(ws)
	if err != nil {
		return
	}
	changes := before.Diff(after)
	if len(changes) == 0 {
		return
	}
	fmt.Fprintln(os.Stderr, "\nwarning: the agent changed git's executable surface during this run:")
	for _, c := range changes {
		fmt.Fprintf(os.Stderr, "  - %s\n", c)
	}
	fmt.Fprintln(os.Stderr, "review these before running git in this repo — a planted hook or config directive runs on your next git command.")
}

func hostOr(h string) string {
	if h == "" {
		return "registry.npmjs.org"
	}
	return h
}

// validateAndLint loads + validates the config (returning any fatal error) and
// returns non-fatal lint warnings. Shared by `safeslop validate` and `safeslop run`.
func validateAndLint(path string) ([]policy.Warning, error) {
	cfg, err := policy.Load(path)
	if err != nil {
		return nil, err
	}
	return policy.Lint(cfg), nil
}

// printWarnings writes lint advisories to stderr (human mode only; JSON callers
// embed them in their payload).
func printWarnings(ws []policy.Warning) {
	for _, w := range ws {
		fmt.Fprintf(os.Stderr, "warning: profile %q %s\n", w.Profile, w.Message)
	}
}

func lintWarnings(ws []policy.Warning) []jsoncontract.Message {
	out := make([]jsoncontract.Message, 0, len(ws))
	for _, w := range ws {
		out = append(out, jsoncontract.NewMessage(jsoncontract.CodePolicyDenied, w.Message, false, map[string]any{
			"profile":   w.Profile,
			"lint_code": w.Code,
		}))
	}
	return out
}

func arg0(args []string) string {
	if len(args) > 0 {
		return args[0]
	}
	return ""
}

// findConfig returns the explicit path if given, else the nearest safeslop.cue
// walking up from the current directory.
func findConfig(explicit string) (string, error) {
	if explicit != "" {
		// A directory => the safeslop.cue inside it (callers may pass a config dir).
		if fi, err := os.Stat(explicit); err == nil && fi.IsDir() {
			return filepath.Join(explicit, "safeslop.cue"), nil
		}
		return explicit, nil
	}
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	start := dir
	for {
		p := filepath.Join(dir, "safeslop.cue")
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no safeslop.cue found in %s or any parent directory", start)
		}
		dir = parent
	}
}

// selectProfile picks the requested profile, or "default", or the sole profile.
func selectProfile(cfg *policy.Config, requested string) (string, policy.Profile, error) {
	if requested != "" {
		p, ok := cfg.Profiles[requested]
		if !ok {
			return "", policy.Profile{}, fmt.Errorf("no profile %q in safeslop.cue", requested)
		}
		return requested, p, nil
	}
	if p, ok := cfg.Profiles["default"]; ok {
		return "default", p, nil
	}
	if len(cfg.Profiles) == 1 {
		for n, p := range cfg.Profiles {
			return n, p, nil
		}
	}
	return "", policy.Profile{}, fmt.Errorf("multiple profiles; name one of them (run `safeslop list`)")
}

// sandboxScope maps a profile's optional file scope to the sandbox boundary's extra paths.
func sandboxScope(f *policy.FileScope) sandbox.Scope {
	if f == nil {
		return sandbox.Scope{}
	}
	return sandbox.Scope{Read: f.Read, Write: f.Write, Deny: f.Deny}
}

// agentArgv maps a profile's agent to the command to launch.
func agentArgv(p policy.Profile) ([]string, error) {
	switch policy.NormalizeAgent(p.Agent) {
	case "claude":
		return []string{"claude"}, nil
	case "pi":
		return []string{"pi"}, nil
	case "shell":
		// The host's $SHELL is an absolute host path (e.g. /bin/zsh, /opt/homebrew/bin/fish).
		// That path is correct for host/sandbox (the agent runs on the host) but does NOT exist
		// inside a container or VM guest, where exec would fail ("/bin/zsh: not found"). For those
		// tiers, name a shell guaranteed to exist in the image instead — resolved via the guest's
		// PATH, not the host path.
		switch p.Environment {
		case "container":
			// The agent image (node:22-bookworm + fish) always has bash.
			return []string{"bash"}, nil
		case "vm":
			return []string{"/bin/sh"}, nil
		default: // host, sandbox
			sh := os.Getenv("SHELL")
			if sh == "" {
				sh = "/bin/sh"
			}
			return []string{sh}, nil
		}
	default:
		return nil, fmt.Errorf("unknown agent %q", p.Agent)
	}
}

// resolveHostBinary makes argv[0] an absolute path via the reconstructed host PATH, for the host and
// sandbox tiers where the agent runs in the host process namespace. Under a Finder/launchd launch the
// process PATH is stripped, so a bare "claude" would fail to exec; resolving it against the
// host_discovery_env recovers the real location. Container/VM tiers resolve inside the guest and must
// NOT be passed through here. Uses the same rich-env-for-discovery path as detection (the sandbox
// firewall is childEnv, not this).
func resolveHostBinary(argv []string) []string {
	return resolveBinaryWith(argv, func(name string) (string, bool) {
		return hostenv.Reconstruct().LookPath(name)
	})
}

// resolveBinaryWith resolves argv[0] to an absolute path using lookPath, leaving an already-absolute
// path or an unresolvable name unchanged (extracted from resolveHostBinary so it is testable).
func resolveBinaryWith(argv []string, lookPath func(string) (string, bool)) []string {
	if len(argv) == 0 || filepath.IsAbs(argv[0]) {
		return argv
	}
	if abs, ok := lookPath(argv[0]); ok {
		return append([]string{abs}, argv[1:]...)
	}
	return argv
}

func emitJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
