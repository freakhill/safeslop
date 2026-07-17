package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	runtimepkg "github.com/freakhill/safeslop/internal/engine/container/runtime"
	"github.com/freakhill/safeslop/internal/engine/gitguard"
	"github.com/freakhill/safeslop/internal/engine/hostenv"
	"github.com/freakhill/safeslop/internal/engine/hostexec"
	"github.com/freakhill/safeslop/internal/engine/policy"
	engsession "github.com/freakhill/safeslop/internal/engine/session"
	"github.com/freakhill/safeslop/internal/engine/trust"
	"github.com/freakhill/safeslop/internal/jsoncontract"
)

func newRoot() *cobra.Command {
	return newRootWithDeps(defaultDependencies())
}

func newRootWithDeps(d *dependencies) *cobra.Command {
	root := &cobra.Command{
		Use:           "safeslop",
		Short:         "Launch coding agents under isolation, driven by safeslop.cue",
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().BoolVar(&d.jsonOut, "json", false, "emit machine-readable JSON output")
	root.AddCommand(cmdValidateWithDeps(d), cmdListWithDeps(d), cmdDoctorWithDeps(d), cmdRunWithDeps(d), cmdSessionWithDeps(d), cmdTrustWithDeps(d), cmdUntrustWithDeps(d), cmdDownWithDeps(d), cmdGCWithDeps(d), cmdLaunch(), cmdCatalogWithDeps(d), cmdBundleWithDeps(d), cmdProfileWithDeps(d), cmdCredsWithDeps(d), cmdLock())
	return root
}

// ---- validate ----

func cmdValidate() *cobra.Command {
	return cmdValidateWithDeps(defaultDependencies())
}

func cmdValidateWithDeps(d *dependencies) *cobra.Command {
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
			if d.jsonOut {
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
	return cmdListWithDeps(defaultDependencies())
}

func cmdListWithDeps(d *dependencies) *cobra.Command {
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
			if d.jsonOut {
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
	return doctorReportWithDeps(defaultDependencies())
}

func doctorReportWithDeps(d *dependencies) map[string]any {
	tools := []string{"git", "gh", "docker", "op", "claude", "pi", "mise", "nix", "aws", "gcloud", "gke-gcloud-auth-plugin"}
	report := map[string]any{}
	resolver := d.doctorHostExec()
	for _, t := range tools {
		insp := resolver.Inspect(t)
		row := map[string]any{"present": insp.Present && !insp.Shadowed && insp.Err == nil, "path": insp.Path}
		if len(insp.All) > 1 {
			row["all_paths"] = append([]string(nil), insp.All...)
		}
		if len(insp.AliasPaths) > 0 {
			row["alias_paths"] = append([]string(nil), insp.AliasPaths...)
		}
		if len(insp.ShadowedPaths) > 0 {
			row["shadowed_paths"] = append([]string(nil), insp.ShadowedPaths...)
		}
		if errors.Is(insp.Err, hostexec.ErrIdentity) {
			row["identity_unverified"] = true
		}
		report[t] = row
	}
	prober := d.credsProber()
	report["1password-signedin"] = map[string]any{"present": prober.OpSignedIn(context.Background()), "path": ""}
	_, err := d.detectRuntime(runtimepkg.PolicyAllow)
	report["container-runtime"] = map[string]any{"present": err == nil, "path": ""}
	return report
}

// doctorTiers renders the per-environment isolation tier legend (shared by doctor's human + JSON),
// so the honest "what each boundary protects" framing is never implicit (ayo §10.5 H1).
func doctorTiers() map[string]map[string]string {
	out := map[string]map[string]string{}
	for _, env := range []string{"host", "container"} {
		tier, note := policy.EnvTier(env)
		out[env] = map[string]string{"tier": tier, "note": note}
	}
	return out
}

func cmdDoctor() *cobra.Command {
	return cmdDoctorWithDeps(defaultDependencies())
}

func cmdDoctorWithDeps(d *dependencies) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Report which external tools and boundaries are available",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			report := doctorReportWithDeps(d)
			if d.jsonOut {
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
			for _, env := range []string{"host", "container"} {
				tier, note := policy.EnvTier(env)
				fmt.Printf("  %-10s %-16s %s\n", env, tier, note)
			}
			return nil
		},
	}
}

// ---- run ----

type launchPolicy struct {
	trustPath string
	bytes     []byte
	hash      string
	cfg       *policy.Config
}

func loadPolicyForLaunch(policyPath string) (launchPolicy, error) {
	lp := launchPolicy{trustPath: canonicalPolicyPath(policyPath)}
	policyBytes, err := os.ReadFile(lp.trustPath)
	if err != nil {
		return launchPolicy{}, fmt.Errorf("read %s: %w", lp.trustPath, err)
	}
	cfg, err := policy.LoadBytes(policyBytes)
	if err != nil {
		return launchPolicy{}, fmt.Errorf("%s:\n%w", policyPath, err)
	}
	lp.bytes = policyBytes
	lp.hash = trust.Hash(policyBytes)
	lp.cfg = cfg
	return lp, nil
}

func enforceTrust(policyPath string, allowTrust bool) error {
	abs := canonicalPolicyPath(policyPath)
	policyBytes, err := os.ReadFile(abs)
	if err != nil {
		return err
	}
	if allowTrust {
		return approvePolicyBytes(abs, policyBytes)
	}
	_, status, err := policyBytesTrustStatus(abs, policyBytes)
	if err != nil {
		return err
	}
	return trustStatusError(abs, status)
}

func enforceLoadedPolicyTrust(lp launchPolicy, allowTrust bool) error {
	if allowTrust {
		return approvePolicyBytes(lp.trustPath, lp.bytes)
	}
	status, err := loadedPolicyTrustStatus(lp)
	if err != nil {
		return err
	}
	return trustStatusError(lp.trustPath, status)
}

func loadedPolicyTrustStatus(lp launchPolicy) (trust.Status, error) {
	_, status, err := policyBytesTrustStatus(lp.trustPath, lp.bytes)
	return status, err
}

func approvePolicyBytes(abs string, policyBytes []byte) error {
	store, err := loadTrustStore()
	if err != nil {
		return err
	}
	return store.Approve(abs, policyBytes)
}

func revokePolicyTrust(policyPath string) (string, error) {
	abs := canonicalPolicyPath(policyPath)
	store, err := loadTrustStore()
	if err != nil {
		return "", err
	}
	if err := store.Revoke(abs); err != nil {
		return "", err
	}
	return abs, nil
}

func policyBytesTrustStatus(abs string, policyBytes []byte) (hash string, status trust.Status, err error) {
	store, err := loadTrustStore()
	if err != nil {
		return "", trust.Untrusted, err
	}
	return trust.Hash(policyBytes), store.Check(abs, policyBytes), nil
}

func loadTrustStore() (*trust.Store, error) {
	storePath, err := trust.DefaultPath()
	if err != nil {
		return nil, err
	}
	return trust.Load(storePath)
}

// checkTrust resolves the canonical policy path, hashes its current bytes, and reports the trust
// status against the host store. Shared by the standalone trust gate and the run/supervise re-verify
// (specs/0072 F1). Launch/create paths use loadPolicyForLaunch so parse + trust observe one read.
func checkTrust(policyPath string) (abs, hash string, status trust.Status, err error) {
	abs = canonicalPolicyPath(policyPath)
	policyBytes, err := os.ReadFile(abs)
	if err != nil {
		return abs, "", trust.Untrusted, err
	}
	hash, status, err = policyBytesTrustStatus(abs, policyBytes)
	if err != nil {
		return abs, "", trust.Untrusted, err
	}
	return abs, hash, status, nil
}

// trustStatusError maps a trust status to the fail-closed CLI error `safeslop run` surfaces; a
// Trusted policy yields nil. Kept as plain errors (not the JSON contract) because `run` is the
// human-facing verb; the session lane wraps CodeTrustRequired around checkTrust itself.
func trustStatusError(abs string, status trust.Status) error {
	switch status {
	case trust.Trusted:
		return nil
	case trust.Changed:
		return fmt.Errorf("safeslop.cue at %s changed since you trusted it (an agent or edit may have modified it).\n  review it, then run:  safeslop trust %s", abs, abs)
	default: // Untrusted
		return fmt.Errorf("safeslop.cue at %s is not trusted (a policy can grant network and secret access).\n  review it, then run:  safeslop trust %s", abs, abs)
	}
}

// verifySessionTrust re-checks, at run/supervise time, that a profile session's policy is STILL
// host-approved for the exact bytes recorded when the session was created (specs/0072 F1, closing
// 0070 B1/B3). session run/supervise rebuild the profile from the session record and never re-read
// the cue, so without this a launch could ride a create-time approval that was later revoked, or a
// policy edited-and-retrusted to different bytes. Ad-hoc (--agent) sessions carry no policy file
// (PolicyPath empty) and are gated at create time instead.
func verifySessionTrust(sess engsession.Session) error {
	if sess.PolicyPath == "" {
		return nil
	}
	if strings.HasPrefix(sess.PolicyPath, "builtin:") {
		builtin, ok := policy.BuiltinProfileByName(sess.Profile)
		if !ok || builtin.Hash != sess.PolicyHash {
			return emitContractError(jsoncontract.CodeTrustRequired, "builtin profile changed or is unavailable; recreate the session", map[string]any{"profile": sess.Profile, "path": sess.PolicyPath})
		}
		return nil
	}
	abs, hash, status, err := checkTrust(sess.PolicyPath)
	if err != nil {
		return emitContractError(jsoncontract.CodeTrustRequired, "cannot verify safeslop.cue trust", map[string]any{"path": sess.PolicyPath, "error": err.Error()})
	}
	if status != trust.Trusted || hash != sess.PolicyHash {
		return emitContractError(jsoncontract.CodeTrustRequired, "safeslop.cue is no longer trusted for this session (approval revoked or the file changed since create)", map[string]any{"path": abs, "status": status.String(), "hint": "safeslop trust " + abs})
	}
	return nil
}

func cmdTrust() *cobra.Command {
	return cmdTrustWithDeps(defaultDependencies())
}

func cmdTrustWithDeps(d *dependencies) *cobra.Command {
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
			if d.jsonOut {
				emitJSON(map[string]any{"ok": true, "trusted": abs})
			} else {
				fmt.Printf("trusted: %s\n", abs)
			}
			return nil
		},
	}
}

func cmdUntrust() *cobra.Command {
	return cmdUntrustWithDeps(defaultDependencies())
}

func cmdUntrustWithDeps(d *dependencies) *cobra.Command {
	return &cobra.Command{
		Use:   "untrust [safeslop.cue]",
		Short: "Remove approval of a repo's safeslop.cue so launches must be re-trusted",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			path, err := findConfig(arg0(args))
			if err != nil {
				return err
			}
			abs, err := revokePolicyTrust(path)
			if err != nil {
				return err
			}
			if d.jsonOut {
				emitJSON(map[string]any{"ok": true, "untrusted": abs})
			} else {
				fmt.Printf("untrusted: %s\n", abs)
			}
			return nil
		},
	}
}

// ---- down / gc ----

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

func argAt(args []string, i int) string {
	if len(args) > i {
		return args[i]
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

// agentArgv maps a profile's agent to the command to launch.
func agentArgv(p policy.Profile) ([]string, error) {
	switch policy.NormalizeAgent(p.Agent) {
	case "claude":
		return []string{"claude"}, nil
	case "pi":
		if p.Credentials != nil && p.Credentials.Pi != nil {
			return []string{"pi", "--provider", p.Credentials.Pi.Provider, "--model", p.Credentials.Pi.Model}, nil
		}
		return []string{"pi"}, nil
	case "fish":
		return []string{"fish"}, nil
	case "zsh":
		return []string{"zsh"}, nil
	case "shell":
		// The host's $SHELL is an absolute host path (e.g. /bin/zsh, /opt/homebrew/bin/fish).
		// That path is correct for host (the agent runs on the host) but does NOT exist
		// inside a container, where exec would fail ("/bin/zsh: not found"). For container,
		// name a shell guaranteed to exist in the image instead — resolved via the guest's
		// PATH, not the host path.
		switch p.Environment {
		case "container":
			// The agent image (node:22-bookworm + fish) always has bash.
			return []string{"bash"}, nil
		default: // host
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

// resolveHostBinary makes argv[0] an absolute path via the reconstructed host PATH, for the host
// tier where the agent runs in the host process namespace. Under a Finder/launchd launch the
// process PATH is stripped, so a bare "claude" would fail to exec; resolving it against the
// host_discovery_env recovers the real location. Container resolves inside the guest and must
// NOT be passed through here. Uses the same rich-env-for-discovery path as detection (the host child
// env firewall is childEnv, not this).
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
