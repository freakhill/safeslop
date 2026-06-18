// Package cli is the slop command tree. Every command drives the engine
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
	"github.com/freakhill/safeslop/internal/engine/launch"
	"github.com/freakhill/safeslop/internal/engine/policy"
	"github.com/freakhill/safeslop/internal/engine/sandbox"
	"github.com/freakhill/safeslop/internal/engine/secrets"
	"github.com/freakhill/safeslop/internal/engine/toolchain"
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
			fmt.Fprintln(os.Stderr, "slop:", err)
		} else {
			emitJSON(map[string]any{"ok": false, "error": err.Error()})
		}
		os.Exit(1)
	}
}

func newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:           "slop",
		Short:         "Launch coding agents under isolation, driven by slop.cue",
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().BoolVar(&jsonOut, "json", false, "emit machine-readable JSON output")
	root.AddCommand(cmdValidate(), cmdList(), cmdDoctor(), cmdRun(), cmdDown(), cmdServe(), cmdLaunch())
	return root
}

// ---- validate ----

func cmdValidate() *cobra.Command {
	return &cobra.Command{
		Use:   "validate [slop.cue]",
		Short: "Validate a slop.cue against the embedded schema",
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
		Use:   "list [slop.cue]",
		Short: "List the profiles defined in slop.cue",
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

// doctorReport probes the external tools and isolation boundaries slop can use.
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

func cmdDoctor() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Report which external tools and boundaries are available",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			report := doctorReport()
			if jsonOut {
				emitJSON(map[string]any{"ok": true, "os": runtime.GOOS, "arch": runtime.GOARCH, "tools": report})
				return nil
			}
			fmt.Printf("slop %s  (%s/%s)\n", Version, runtime.GOOS, runtime.GOARCH)
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
			return nil
		},
	}
}

// ---- run ----

func cmdRun() *cobra.Command {
	var dryRun bool
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

			if dryRun {
				out := map[string]any{"ok": true, "profile": name, "environment": prof.Environment, "workspace": ws, "argv": argv, "network": prof.Network}
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

			code, err := runProfile(name, prof, argv, ws)
			if err != nil && code == 0 {
				return err
			}
			os.Exit(code)
			return nil
		},
	}
	c.Flags().BoolVar(&dryRun, "dry-run", false, "print the resolved launch + sandbox profile without executing")
	return c
}

// ---- down ----

func cmdDown() *cobra.Command {
	return &cobra.Command{
		Use:   "down",
		Short: "Tear down the container stack (squid) and any disposable VM sessions",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if !container.Available() && !vm.Available() {
				return fmt.Errorf("nothing to tear down: neither docker nor tart is available (run: slop doctor)")
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
		Short: "Run the gRPC control plane on ~/.slop/s.sock (drives the GUI app)",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return control.Serve(Version, func(profile, configPath string, emit func(*pb.LaunchEvent)) error {
				emit(&pb.LaunchEvent{Kind: pb.LaunchEvent_SPAWNED, Message: profile})
				code, err := launchProfile(profile, configPath)
				if err != nil {
					emit(&pb.LaunchEvent{Kind: pb.LaunchEvent_ERROR, Message: err.Error()})
					return nil
				}
				emit(&pb.LaunchEvent{Kind: pb.LaunchEvent_EXITED, ExitCode: int32(code)})
				return nil
			})
		},
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

// launchProfile opens the user's preferred terminal (from ~/.config/slop/config.cue) running
// `slop run <profile>`, so the real ctty handoff happens inside that window. Returns once the
// terminal is spawned. configPath is reserved for the gRPC delegation (v1 resolves slop.cue
// from the workspace).
// profileNameRe constrains launchable profile names: the name is embedded in the spawned
// terminal's window title and SLOP_SESSION, so it must not carry shell/title metacharacters.
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
	slopPath, err := os.Executable()
	if err != nil {
		return 1, err
	}
	cmd := launch.Command(slopPath, name, ws, uc.Tag.OSCTitle)
	argv := launch.AdapterArgv(uc.Terminal, cmd, name)
	if err := osexec.Command(argv[0], argv[1:]...).Run(); err != nil {
		return 1, fmt.Errorf("open terminal (%s): %w", uc.Terminal, err)
	}
	return 0, nil
}

// ---- helpers ----

// runProfile stages secrets + credentials into an ephemeral dir under the
// workspace, launches the agent under its environment, and wipes the stage on
// exit. Returns the child's exit code.
func runProfile(name string, prof policy.Profile, argv []string, ws string) (int, error) {
	ctx := context.Background()

	stageDir := filepath.Join(ws, ".slop", "runtime", name)
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

	// secretEnv = the resolved profile secrets (sensitive). The pnpm token rides a staged
	// .npmrc file. Kept separate so the container path can deliver secrets via a sourced
	// file (out of `ps`/`docker inspect`) rather than the whole host environment.
	var secretEnv []string
	if len(prof.Secrets) > 0 {
		resolved, err := secrets.ResolveMap(ctx, prof.Secrets)
		if err != nil {
			return 1, err
		}
		for k, v := range resolved {
			secretEnv = append(secretEnv, k+"="+v)
		}
	}

	npmrcEnv, err := creds.StagePnpm(ctx, prof.Credentials, stageDir)
	if err != nil {
		return 1, err
	}

	// Cloud creds are short-lived (SSO role creds / ADC access token) and delivered
	// as env vars through the secret channel, so they ride secrets.env (container) /
	// the scp'd env (vm) and reach host/sandbox children too. No revoke: decay-first.
	awsEnv, err := creds.StageAWS(ctx, prof.Credentials, stageDir)
	if err != nil {
		return 1, err
	}
	gcpEnv, err := creds.StageGCP(ctx, prof.Credentials, stageDir)
	if err != nil {
		return 1, err
	}
	secretEnv = append(secretEnv, awsEnv...)
	secretEnv = append(secretEnv, gcpEnv...)

	// kubeconfig (with the bearer token inside) is staged 0600; KUBECONFIG is a
	// non-secret path delivered per-environment like .npmrc/NPM_CONFIG_USERCONFIG —
	// host path for host/sandbox; /slop/runtime/kubeconfig via compose for container.
	// Kept out of secretEnv so it never lands in the container's secrets.env.
	kubeEnv, err := creds.StageKube(ctx, prof.Credentials, stageDir)
	if err != nil {
		return 1, err
	}

	// ssh deploy key: the bearer is the staged 0600 private key; GIT_SSH_COMMAND is a
	// non-secret path delivered per-environment like KUBECONFIG (host path for host/sandbox;
	// /slop/runtime/.ssh/id via compose for container). Best-effort revoke runs before the
	// stageDir wipe (deferred after the top-of-func wipe, so LIFO orders it first).
	sshEnv, err := creds.StageSSH(ctx, prof.Credentials, stageDir)
	if err != nil {
		return 1, err
	}
	if prof.Credentials != nil && prof.Credentials.Ssh != nil {
		defer creds.RevokeSSH(context.Background(), stageDir)
	}

	switch prof.Environment {
	case "sandbox":
		env := append(append(append(append(os.Environ(), secretEnv...), npmrcEnv...), kubeEnv...), sshEnv...)
		return sandbox.Launch(ctx, engexec.LaunchSpec{Argv: argv, Dir: ws, Env: env}, ws, prof.Network)
	case "host":
		env := append(append(append(append(os.Environ(), secretEnv...), npmrcEnv...), kubeEnv...), sshEnv...)
		return engexec.RunInTerminal(ctx, engexec.LaunchSpec{Argv: argv, Dir: ws, Env: env})
	case "container":
		// secrets go in secrets.env (sourced by the entrypoint); .npmrc and kubeconfig
		// are staged in stageDir and reached via the /slop/runtime bind mount.
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

func hostOr(h string) string {
	if h == "" {
		return "registry.npmjs.org"
	}
	return h
}

// validateAndLint loads + validates the config (returning any fatal error) and
// returns non-fatal lint warnings. Shared by `slop validate` and `slop run`.
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

// findConfig returns the explicit path if given, else the nearest slop.cue
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
		p := filepath.Join(dir, "slop.cue")
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no slop.cue found in %s or any parent directory", start)
		}
		dir = parent
	}
}

// selectProfile picks the requested profile, or "default", or the sole profile.
func selectProfile(cfg *policy.Config, requested string) (string, policy.Profile, error) {
	if requested != "" {
		p, ok := cfg.Profiles[requested]
		if !ok {
			return "", policy.Profile{}, fmt.Errorf("no profile %q in slop.cue", requested)
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
	return "", policy.Profile{}, fmt.Errorf("multiple profiles; name one of them (run `slop list`)")
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
