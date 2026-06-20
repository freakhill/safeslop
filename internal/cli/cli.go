// Package cli is the safeslop command tree. Every command drives the engine
// packages and (with --json) emits machine-readable output so a future GUI can
// drive the same engine without re-implementing logic (specs/0001 §6, §A).
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"

	"github.com/spf13/cobra"

	"github.com/freakhill/safeslop/internal/engine/container"
	"github.com/freakhill/safeslop/internal/engine/control"
	"github.com/freakhill/safeslop/internal/engine/control/pb"
	"github.com/freakhill/safeslop/internal/engine/creds"
	engexec "github.com/freakhill/safeslop/internal/engine/exec"
	"github.com/freakhill/safeslop/internal/engine/gitguard"
	"github.com/freakhill/safeslop/internal/engine/install"
	"github.com/freakhill/safeslop/internal/engine/launch"
	"github.com/freakhill/safeslop/internal/engine/policy"
	"github.com/freakhill/safeslop/internal/engine/sandbox"
	"github.com/freakhill/safeslop/internal/engine/secrets"
	"github.com/freakhill/safeslop/internal/engine/toolchain"
	"github.com/freakhill/safeslop/internal/engine/trust"
	"github.com/freakhill/safeslop/internal/engine/userconfig"
	"github.com/freakhill/safeslop/internal/engine/vm"
)

// Version is overridden at build time via -ldflags "-X .../cli.Version=...".
var Version = "dev"

var jsonOut bool

// Execute runs the root command and exits non-zero on error.
func Execute() {
	if err := newRoot().Execute(); err != nil {
		if !jsonOut {
			fmt.Fprintln(os.Stderr, "safeslop:", err)
		} else {
			emitJSON(map[string]any{"ok": false, "error": err.Error()})
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
	root.AddCommand(cmdValidate(), cmdList(), cmdDoctor(), cmdRun(), cmdTrust(), cmdDown(), cmdServe(), cmdLaunch(), cmdInstall())
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
				emitJSON(map[string]any{"ok": true, "path": path, "warnings": warns})
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
	tools := []string{"git", "gh", "docker", "op", "claude", "opencode", "tart", "mise", "nix", "aws", "gcloud", "gke-gcloud-auth-plugin"}
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
				emitJSON(map[string]any{"ok": true, "os": runtime.GOOS, "arch": runtime.GOARCH, "tools": report, "tiers": doctorTiers()})
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
					out["sandbox_profile"] = sandbox.Profile(ws, prof.Network)
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
						fmt.Printf("--- seatbelt profile ---\n%s", sandbox.Profile(ws, prof.Network))
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
			if err != nil && code == 0 {
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
func enforceTrust(policyPath string, allowTrust bool) error {
	abs, err := filepath.Abs(policyPath)
	if err != nil {
		return err
	}
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

// cockpitTrust records host-side approval of the safeslop.cue at configPath — the engine side of
// the GUI's Trust RPC, so a subsequent OpenSession passes the fail-closed trust gate (specs/0024
// S1a). Returns the approved absolute path. The peer is already uid/process-tree-checked at the
// socket accept (control/peerauth.go), so a sandboxed agent can't reach this.
func cockpitTrust(configPath string) (string, error) {
	path, err := findConfig(configPath)
	if err != nil {
		return "", err
	}
	if err := enforceTrust(path, true); err != nil {
		return "", err
	}
	return filepath.Abs(path)
}

// cockpitListProfiles returns the safeslop.cue profiles for the GUI launcher, each tagged with its
// honest isolation tier from policy.EnvTier — one source of truth for the cockpit's tier indicator
// (specs/research/2026-06-20-cockpit-safe-by-design.md). Listing is inspection (ungated, like
// `list`/`validate`); the socket peer check (control/peerauth.go) still applies.
func cockpitListProfiles(configPath string) ([]*pb.Profile, error) {
	path, err := findConfig(configPath)
	if err != nil {
		return nil, err
	}
	cfg, err := policy.Load(path)
	if err != nil {
		return nil, err
	}
	out := make([]*pb.Profile, 0, len(cfg.Profiles))
	for name, prof := range cfg.Profiles {
		env := prof.Environment
		if env == "" {
			env = "sandbox" // schema default
		}
		tier, note := policy.EnvTier(env)
		out = append(out, &pb.Profile{
			Name:        name,
			Agent:       prof.Agent,
			Environment: env,
			Network:     prof.Network,
			Tier:        tier,
			TierNote:    note,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
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
				if err := container.Down(context.Background(), composeFile); err != nil {
					return err
				}
			}
			if vm.Available() {
				_ = vm.DestroyAll(context.Background())
			}
			return nil
		},
	}
}

func cmdServe() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Run the gRPC control plane on ~/.safeslop/s.sock (drives the GUI app)",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return control.Serve(Version,
				func(profile, configPath string, emit func(*pb.LaunchEvent)) error {
					emit(&pb.LaunchEvent{Kind: pb.LaunchEvent_SPAWNED, Message: profile})
					code, err := launchProfile(profile, configPath)
					if err != nil {
						emit(&pb.LaunchEvent{Kind: pb.LaunchEvent_ERROR, Message: err.Error()})
						return nil
					}
					emit(&pb.LaunchEvent{Kind: pb.LaunchEvent_EXITED, ExitCode: int32(code)})
					return nil
				},
				resolveSession,
				cockpitTrust,
				cockpitListProfiles,
			)
		},
	}
}

// resolveSession turns a profile name into a control.SessionSpec: the (optionally toolchain-
// wrapped) agent argv, the workspace, the profile's resolved secrets + staged credentials, and
// the per-environment cleanup as OnClose. Credential parity with `safeslop run` (SP7c-3), minus ssh
// deploy keys, which are deferred in the cockpit (they key off the workspace git origin).
func resolveSession(profile, configPath string) (control.SessionSpec, error) {
	path, err := findConfig(configPath)
	if err != nil {
		return control.SessionSpec{}, err
	}
	// Fail-closed policy trust gate, identical to the CLI `run` path (specs/0022). The cockpit's
	// in-process OpenSession data plane was the one launch chokepoint not gated, so a same-uid
	// in-sandbox peer could rewrite safeslop.cue and OpenSession its way to environment:"host"
	// (specs/0024 S1a). The GUI surfaces approval via `safeslop trust` (a wizard screen is a
	// follow-on); allowTrust stays false here (the engine never auto-approves on the agent's behalf).
	if err := enforceTrust(path, false); err != nil {
		return control.SessionSpec{}, err
	}
	cfg, err := policy.Load(path)
	if err != nil {
		return control.SessionSpec{}, err
	}
	_, prof, err := selectProfile(cfg, profile)
	if err != nil {
		return control.SessionSpec{}, err
	}
	argv, err := agentArgv(prof)
	if err != nil {
		return control.SessionSpec{}, err
	}
	if prof.Toolchain != nil && toolchain.Wraps(prof.Toolchain.Kind) {
		argv = toolchain.Wrap(prof.Toolchain.Kind, prof.Toolchain.Run, argv)
	}
	ws := prof.Workspace
	if ws == "" {
		ws, _ = os.Getwd()
	}

	// Credential gates the cockpit can't satisfy yet (reject before staging anything):
	// ssh mints a per-window deploy key scoped to the *workspace* git origin, but safeslop serve's
	// cwd isn't the workspace — deferred to `safeslop run`. vm can't reach kube (mirrors runProfile).
	if prof.Credentials != nil {
		if prof.Credentials.Ssh != nil {
			return control.SessionSpec{}, fmt.Errorf("ssh credentials aren't supported in cockpit sessions yet (the deploy key is scoped to the workspace git origin); use `safeslop run`")
		}
		if prof.Environment == "vm" && prof.Credentials.Kube != nil {
			return control.SessionSpec{}, fmt.Errorf("kube credentials are not supported with environment:%q; use environment:\"container\" (specs/0010)", prof.Environment)
		}
	}

	// Per-session stage dir (unique → N concurrent sessions don't collide; also the vm clone name).
	base := filepath.Join(ws, ".safeslop", "runtime")
	if err := os.MkdirAll(base, 0o700); err != nil {
		return control.SessionSpec{}, err
	}
	stageDir, err := os.MkdirTemp(base, "cockpit-*")
	if err != nil {
		return control.SessionSpec{}, err
	}
	secretEnv, pathEnv, err := stageProfile(context.Background(), prof, stageDir)
	if err != nil {
		_ = os.RemoveAll(stageDir)
		return control.SessionSpec{}, err
	}
	wipe := func() { _ = os.RemoveAll(stageDir) }

	switch prof.Environment {
	case "host":
		env := childEnv(secretEnv, pathEnv)
		return control.SessionSpec{Argv: argv, Dir: ws, Env: env, OnClose: wipe}, nil
	case "sandbox", "": // sandbox is the default
		wrapped, wrapCleanup, err := sandbox.WrapArgv(argv, ws, prof.Network)
		if err != nil {
			_ = os.RemoveAll(stageDir)
			return control.SessionSpec{}, err
		}
		env := childEnv(secretEnv, pathEnv)
		return control.SessionSpec{Argv: wrapped, Dir: ws, Env: env, OnClose: chainClose(wrapCleanup, wipe)}, nil
	case "container":
		cargv, cleanup, err := container.PrepareSession(context.Background(), argv, ws, prof.Network, secretEnv, stageDir)
		if err != nil {
			_ = os.RemoveAll(stageDir)
			return control.SessionSpec{}, err
		}
		return control.SessionSpec{Argv: cargv, Dir: ws, OnClose: cleanup}, nil // cleanup wipes stageDir
	case "vm":
		tk := ""
		if prof.Toolchain != nil {
			tk = prof.Toolchain.Kind
		}
		// stageDir basename is the per-session VM clone name (concurrency isolation).
		vargv, cleanup, err := vm.PrepareSession(context.Background(), argv, prof.Network, secretEnv, stageDir, filepath.Base(stageDir), tk)
		if err != nil {
			_ = os.RemoveAll(stageDir)
			return control.SessionSpec{}, err
		}
		return control.SessionSpec{Argv: vargv, Dir: ws, OnClose: cleanup}, nil // cleanup wipes stageDir
	default:
		_ = os.RemoveAll(stageDir)
		return control.SessionSpec{}, fmt.Errorf("unknown environment %q", prof.Environment)
	}
}

// chainClose returns a cleanup that runs fns in order, skipping nils — used to compose a
// session's OnClose (e.g. a sandbox temp-profile removal followed by the stage-dir wipe).
func chainClose(fns ...func()) func() {
	return func() {
		for _, f := range fns {
			if f != nil {
				f()
			}
		}
	}
}

func cmdInstall() *cobra.Command {
	c := &cobra.Command{
		Use:   "install",
		Short: "Inventory and (later) provision the safeslop toolchain",
	}
	c.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Report whether safeslop, its app, toolchains, and runtimes are installed",
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
			app := "not installed"
			if st.App.Present {
				app = st.App.Path
			}
			fmt.Printf("  app:    %s\n", app)
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
	b, _ := json.MarshalIndent(res, "", "  ")
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
	return &cobra.Command{
		Use:   "launch <profile>",
		Short: "Open a terminal window running the profile's agent (ctty intact)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			_, err := launchProfile(args[0], "")
			return err
		},
	}
}

// launchProfile opens the user's preferred terminal (from ~/.config/safeslop/config.cue) running
// `safeslop run <profile>`, so the real ctty handoff happens inside that window. Returns once the
// terminal is spawned. configPath is reserved for the gRPC delegation (v1 resolves safeslop.cue
// from the workspace).
// profileNameRe constrains launchable profile names: the name is embedded in the spawned
// terminal's window title and SAFESLOP_SESSION, so it must not carry shell/title metacharacters.
var profileNameRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func launchProfile(name, configPath string) (int, error) {
	_ = configPath
	if !profileNameRe.MatchString(name) {
		return 1, fmt.Errorf("invalid profile name %q (allowed: letters, digits, dot, underscore, hyphen)", name)
	}
	ws, err := os.Getwd()
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
	pathEnv = append(pathEnv, npmrcEnv...)
	pathEnv = append(pathEnv, kubeEnv...)
	pathEnv = append(pathEnv, sshEnv...)
	return secretEnv, pathEnv, nil
}

// runProfile stages secrets + credentials into an ephemeral dir under the
// workspace, launches the agent under its environment, and wipes the stage on
// exit. Returns the child's exit code.
func runProfile(name string, prof policy.Profile, argv []string, ws string) (int, error) {
	ctx := context.Background()

	stageDir := filepath.Join(ws, ".safeslop", "runtime", name)
	defer os.RemoveAll(stageDir) // wipe staged secrets/.npmrc regardless of outcome

	// kube/ssh creds need a file at a boundary-stable path; vm's scp'd stage path
	// (unknown guest $HOME, single-quoted secrets.env) isn't wired yet. Fail fast,
	// before minting any token / registering any deploy key (specs/0010, specs/0011).
	if prof.Environment == "vm" && prof.Credentials != nil {
		if prof.Credentials.Kube != nil {
			return 1, fmt.Errorf("kube credentials are not yet supported with environment:%q — use environment:\"container\" (specs/0010)", prof.Environment)
		}
		if prof.Credentials.Ssh != nil {
			return 1, fmt.Errorf("ssh credentials are not yet supported with environment:%q — use environment:\"container\" (specs/0011)", prof.Environment)
		}
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

	// Detect (and warn about) any change the agent makes to git's executable surface —
	// a planted .git/hooks script or a .git/config hooksPath/fsmonitor/filter that the
	// host would run on its next git command in this repo (specs/0025 S3). Best-effort,
	// never blocks the agent's legitimate git use.
	gitBefore, _ := gitguard.Snapshot(ws)
	defer warnGitExecSurface(ws, gitBefore)

	switch prof.Environment {
	case "sandbox":
		env := childEnv(secretEnv, pathEnv)
		return sandbox.Launch(ctx, engexec.LaunchSpec{Argv: argv, Dir: ws, Env: env}, ws, prof.Network)
	case "host":
		env := childEnv(secretEnv, pathEnv)
		return engexec.RunInTerminal(ctx, engexec.LaunchSpec{Argv: argv, Dir: ws, Env: env})
	case "container":
		// secrets go in secrets.env (sourced by the entrypoint); .npmrc and kubeconfig
		// are staged in stageDir and reached via the /safeslop/runtime bind mount.
		return container.Launch(ctx, engexec.LaunchSpec{Argv: argv}, ws, prof.Network, secretEnv, stageDir)
	case "vm":
		// secrets ride secrets.env scp'd into the VM and sourced over ssh; the VM is destroyed on exit.
		tk := ""
		if prof.Toolchain != nil {
			tk = prof.Toolchain.Kind
		}
		return vm.Launch(ctx, argv, prof.Network, secretEnv, stageDir, name, tk)
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

// agentArgv maps a profile's agent to the command to launch.
func agentArgv(p policy.Profile) ([]string, error) {
	switch p.Agent {
	case "claude":
		return []string{"claude"}, nil
	case "opencode":
		return []string{"opencode"}, nil
	case "shell":
		sh := os.Getenv("SHELL")
		if sh == "" {
			sh = "/bin/sh"
		}
		return []string{sh}, nil
	default:
		return nil, fmt.Errorf("unknown agent %q", p.Agent)
	}
}

func emitJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
