// Package cli is the safeslop command tree. Every command drives the engine
// packages and (with --json) emits machine-readable output so a future GUI can
// drive the same engine without re-implementing logic (specs/0001 §6, §A).
package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"math/rand"
	"net"
	"net/url"
	"os"
	osexec "os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
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
	"github.com/freakhill/safeslop/internal/engine/hostexec"
	"github.com/freakhill/safeslop/internal/engine/launch"
	"github.com/freakhill/safeslop/internal/engine/policy"
	"github.com/freakhill/safeslop/internal/engine/secrets"
	engsession "github.com/freakhill/safeslop/internal/engine/session"
	"github.com/freakhill/safeslop/internal/engine/toolchain"
	"github.com/freakhill/safeslop/internal/engine/trust"
	"github.com/freakhill/safeslop/internal/engine/userconfig"
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
	root.AddCommand(cmdValidate(), cmdList(), cmdDoctor(), cmdRun(), cmdSession(), cmdTrust(), cmdUntrust(), cmdDown(), cmdGC(), cmdLaunch(), cmdCatalog(), cmdBundle(), cmdProfile(), cmdCreds(), cmdLock())
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

var doctorHostExecResolver = hostexec.Default

// doctorReport probes the external tools and isolation boundaries safeslop can use.
// Extracted so it is testable and reusable (e.g. a future GUI / installer).
func doctorReport() map[string]any {
	tools := []string{"git", "gh", "docker", "op", "claude", "pi", "mise", "nix", "aws", "gcloud", "gke-gcloud-auth-plugin"}
	report := map[string]any{}
	resolver := doctorHostExecResolver()
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
	report["1password-signedin"] = map[string]any{"present": secrets.OpSignedIn(context.Background()), "path": ""}
	report["container-runtime"] = map[string]any{"present": container.Available(), "path": ""}
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
			for _, env := range []string{"host", "container"} {
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
			loaded, err := loadPolicyForLaunch(path)
			if err != nil {
				return err
			}
			name, prof, err := selectProfile(loaded.cfg, arg0(args))
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
				}
				return nil
			}

			if !jsonOut {
				fmt.Printf("isolation tier: %s — %s\n", tier, tierNote)
			}

			// Fail-closed: only an explicitly host-approved safeslop.cue may launch an agent
			// (specs/0022). --dry-run above stays ungated — it is inspection, like validate.
			if err := enforceLoadedPolicyTrust(loaded, trustFlag); err != nil {
				return err
			}
			if err := requireHostLaunchConsent(name, prof, os.Stdin, os.Stderr); err != nil {
				return err
			}
			if prof.Environment == "container" {
				if err := sweepManagedOrphans(context.Background()); err != nil {
					return err
				}
			}
			code, err := runProfile(name, prof, argv, ws)
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

var hostLaunchConsent = confirmHostLaunchConsent

func requireHostLaunchConsent(name string, prof policy.Profile, in io.Reader, out io.Writer) error {
	if prof.Environment != "host" {
		return nil
	}
	return hostLaunchConsent(name, prof, in, out)
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

var errOutputEmitted = errors.New("machine-readable error already emitted")

// sessionStageDir reconstructs the deterministic host stage dir a session's run staged under,
// so teardown paths (credential revoke, boundary reap) address the exact same tree — and the
// exact safeslop.session label — the launch path used (mirrors runProfileCtx's
// stageDirFor("session-"+id, ws); specs/0074).
func sessionStageDir(sess engsession.Session) (string, error) {
	return stageDirFor("session-"+sess.ID, sess.Workspace)
}

var sessionRevokeCredentials = func(sess engsession.Session) error {
	stageDir, err := sessionStageDir(sess)
	if err != nil {
		return err
	}
	creds.RevokeGithub(context.Background(), stageDir)
	creds.RevokeForgejo(context.Background(), stageDir)
	return nil
}

// sessionWipeStageDir is local, idempotent cleanup for a session's staged bearer
// files. It intentionally does not call forge/cloud APIs, so liveness reconcile
// can use it safely while status/list repair stale records from a SIGKILLed run.
var sessionWipeStageDir = func(sess engsession.Session) error {
	stageDir, err := sessionStageDir(sess)
	if err != nil {
		return err
	}
	return os.RemoveAll(stageDir)
}

// stageDirFor returns the host directory where a run's credentials/squid config are
// staged, OUTSIDE the agent-writable workspace (specs/0072 F2, closing 0070 B2). The
// stage tree used to live at <ws>/.safeslop/runtime/<name>, so the agent saw every
// staged bearer at a predictable /workspace path and could rewrite the ":ro" mount via
// the rw workspace path. It now lives under os.UserCacheDir()/safeslop/runtime, with an
// 8-hex fnv(ws) suffix so concurrent coupled runs of the same profile name in different
// workspaces (previously separated by living under each ws) stay distinct. The base is
// created 0700; a UserCacheDir failure is fail-closed (no /tmp fallback — the predictable
// path under $HOME and its 0700 ancestry are part of the boundary, and match what Lima/
// Colima already share rw). The path is deterministic, so the revoke/wipe paths
// reconstruct it without persisting anything.
func stageDirFor(name, ws string) (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolve user cache dir for credential staging: %w", err)
	}
	base := filepath.Join(cache, "safeslop", "runtime")
	if err := os.MkdirAll(base, 0o700); err != nil {
		return "", fmt.Errorf("create credential staging root: %w", err)
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(ws))
	return filepath.Join(base, fmt.Sprintf("%s-%08x", name, h.Sum32())), nil
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

// sessionProcessAlive verifies whether a recorded session still points at the same
// live wrapper/supervisor process, so status/list/stop can reconcile a dead or
// PID-reused session before reporting or signalling it. Overridable in tests.
var sessionProcessAlive = engsession.ProcessAliveSession

// sessionReapKey is the safeslop.session label value the launch path stamps on a session's
// boundary: SessionIDFromStageDir(stageDir), NOT the bare session id. The stage dir carries an
// fnv(ws) suffix, so the label is "<id>-<hash>"; reaping by the bare id misses it and a detached
// stop leaks its containers (specs/0074 Bug 1).
func sessionReapKey(sess engsession.Session) (string, error) {
	stageDir, err := sessionStageDir(sess)
	if err != nil {
		return "", err
	}
	return container.SessionIDFromStageDir(stageDir), nil
}

func sessionReapBoundary(sess engsession.Session) error {
	if sess.Environment != "container" {
		return nil
	}
	key, err := sessionReapKey(sess)
	if err != nil {
		return err
	}
	return container.ReapBySession(context.Background(), engineForSession(sess), key)
}

// detectEngineName reports the ambient container runtime that would drive a session, for recording
// Session.Backend (specs/0066 D7). It detects with PolicyAllow — recording the name must never be blocked
// by the deny-tier egress gate — and is best-effort: with no runtime present it returns "" and Backend
// stays unknown-until-provisioned. Overridable in tests so backend recording stays hermetic.
var detectEngineName = func() string {
	eng, err := runtimepkg.Detect(runtimepkg.PolicyAllow)
	if err != nil {
		return ""
	}
	return eng.Name()
}

func recordSessionBackend(store engsession.Store, sess engsession.Session) (engsession.Session, error) {
	if sess.Environment != "container" {
		return sess, nil
	}
	return store.WithLocked(sess.ID, func(tx *engsession.RecordTx) error {
		current := tx.Session()
		changed := false
		if backend := detectEngineName(); backend != "" && current.Backend != backend {
			current.Backend = backend
			changed = true
		}
		if current.Image == "" || current.RecipeID == "" {
			prof, err := sessionProfile(current)
			if err != nil {
				return err
			}
			resolved, err := policy.Resolve(prof)
			if err != nil {
				return err
			}
			recipe, err := container.ResolveRecipe(resolved.IdentitySet)
			if err != nil {
				return err
			}
			current.RecipeID = recipe.RecipeID
			current.Image = recipe.AgentImage
			current.Resolved = resolvedMetadata(resolved)
			changed = true
		}
		if !changed {
			return nil
		}
		current.UpdatedAt = time.Now().UTC()
		return tx.Commit(current)
	})
}

// engineForSession returns the container engine to reap a session's boundary through. Teardown/reap must
// work on ANY ambient runtime, verified or not, so it detects with PolicyAllow (never the deny-tier
// gate). With no runtime present it falls back to docker — the reap then simply finds nothing to remove
// (specs/0066 D5).
func engineForSession(_ engsession.Session) runtimepkg.Engine {
	if eng, err := runtimepkg.Detect(runtimepkg.PolicyAllow); err == nil {
		return eng
	}
	return runtimepkg.HostDockerEngine{}
}

// detectRuntimeForSweep is a test seam: runtime discovery deliberately uses the reconstructed host
// environment, so mutating process PATH cannot hermetically model an unavailable runtime.
var detectRuntimeForSweep = runtimepkg.Detect

// sweepManagedOrphans reaps labelled boundaries whose session record is gone, before a new container run.
// It detects the ambient runtime with PolicyAllow (teardown is never gated); with no runtime present
// Detect fails and the sweep is a no-op — nothing safeslop could have started (specs/0066 D5).
func sweepManagedOrphans(ctx context.Context) error {
	eng, err := detectRuntimeForSweep(runtimepkg.PolicyAllow)
	if err != nil {
		return nil
	}
	live, err := container.LiveSessions(sessionStore().Dir)
	if err != nil {
		return err
	}
	return container.SweepManagedOrphans(ctx, eng, live)
}

func cmdSession() *cobra.Command {
	c := &cobra.Command{Use: "session", Short: "Manage Emacs-visible safeslop sessions"}
	c.AddCommand(cmdSessionCreate(), cmdSessionRun(), cmdSessionStatus(), cmdSessionStop(), cmdSessionList(), cmdSessionRemove(), cmdSessionRename(), cmdSessionPrune(), cmdSessionSupervise(), cmdSessionAttach(), cmdSessionEgress())
	return c
}

// cmdSessionEgress exposes the operator-only, session-scoped controls for the
// container deny proxy overlay. It deliberately has no path from agent traffic to
// a grant: observations are informational and only `grant` changes authority.
func cmdSessionEgress() *cobra.Command {
	c := &cobra.Command{
		Use:   "egress",
		Short: "Inspect and manage session-scoped container egress grants",
	}
	c.AddCommand(cmdSessionEgressObservations(), cmdSessionEgressGrants(), cmdSessionEgressGrant(), cmdSessionEgressRevoke(), cmdSessionEgressDismiss())
	return c
}

func cmdSessionEgressObservations() *cobra.Command {
	var id, output string
	c := &cobra.Command{
		Use:   "observations --session-id <id> --output json",
		Short: "List proxy-denied egress observations for a session",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if output != "json" {
				return fmt.Errorf("session egress observations requires --output json")
			}
			sess, err := egressSession(sessionStore(), id)
			if err != nil {
				return err
			}
			observations, err := observeSessionEgress(context.Background(), sess)
			if observations == nil {
				observations = []container.EgressObservation{}
			}
			observations = filterAcknowledgedEgressObservations(sess, observations)
			data := map[string]any{
				"session_id":              id,
				"observations":            observations,
				"pending_count":           len(observations),
				"egress_acknowledgements": egressAcknowledgementsOrEmpty(sess.EgressAcknowledgements),
			}
			if err != nil {
				// Observing is read-only: never turn a proxy/log failure into traffic
				// authority, and do not return backend output that might include request
				// material. The caller can retry after the runtime is healthy.
				emitContract(jsoncontract.OK(data,
					jsoncontract.NewMessage(jsoncontract.CodeIOError, "read proxy denied-request observations", true, nil)))
				return nil
			}
			emitContract(jsoncontract.OK(data))
			return nil
		},
	}
	c.Flags().StringVar(&id, "session-id", "", "session id")
	c.Flags().StringVar(&output, "output", "", "output format: json")
	return c
}

func cmdSessionEgressGrants() *cobra.Command {
	var id, output string
	c := &cobra.Command{
		Use:   "grants --session-id <id> --output json",
		Short: "List active session egress grants",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if output != "json" {
				return fmt.Errorf("session egress grants requires --output json")
			}
			sess, err := egressSession(sessionStore(), id)
			if err != nil {
				return err
			}
			emitContract(jsoncontract.OK(sessionEgressData(sess)))
			return nil
		},
	}
	c.Flags().StringVar(&id, "session-id", "", "session id")
	c.Flags().StringVar(&output, "output", "", "output format: json")
	return c
}

func cmdSessionEgressGrant() *cobra.Command {
	var id, host, output string
	var port int
	c := &cobra.Command{
		Use:   "grant --session-id <id> --host <fqdn> --port <80|443> --output json",
		Short: "Grant one exact FQDN:port to a container deny session",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if output != "json" {
				return fmt.Errorf("session egress grant requires --output json")
			}
			sess, _, err := grantSessionEgress(context.Background(), sessionStore(), id, host, port, time.Now())
			if err != nil {
				return emitEgressError(err, id)
			}
			emitContract(jsoncontract.OK(sessionEgressData(sess)))
			return nil
		},
	}
	c.Flags().StringVar(&id, "session-id", "", "session id")
	c.Flags().StringVar(&host, "host", "", "exact FQDN to grant")
	c.Flags().IntVar(&port, "port", 0, "destination port (80 or 443)")
	c.Flags().StringVar(&output, "output", "", "output format: json")
	return c
}

func cmdSessionEgressDismiss() *cobra.Command {
	var id, host, output string
	var port int
	c := &cobra.Command{
		Use:   "dismiss --session-id <id> --host <fqdn> --port <80|443> --output json",
		Short: "Keep one observed exact destination denied for this session",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if output != "json" {
				return fmt.Errorf("session egress dismiss requires --output json")
			}
			sess, _, err := dismissSessionEgress(sessionStore(), id, host, port, time.Now())
			if err != nil {
				return emitEgressError(err, id)
			}
			emitContract(jsoncontract.OK(sessionEgressData(sess)))
			return nil
		},
	}
	c.Flags().StringVar(&id, "session-id", "", "session id")
	c.Flags().StringVar(&host, "host", "", "exact FQDN to acknowledge as denied")
	c.Flags().IntVar(&port, "port", 0, "destination port (80 or 443)")
	c.Flags().StringVar(&output, "output", "", "output format: json")
	return c
}

func cmdSessionEgressRevoke() *cobra.Command {
	var id, grantID, output string
	c := &cobra.Command{
		Use:   "revoke --session-id <id> --grant-id <id> --output json",
		Short: "Revoke one session egress grant",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if output != "json" {
				return fmt.Errorf("session egress revoke requires --output json")
			}
			sess, err := revokeSessionEgress(context.Background(), sessionStore(), id, grantID, time.Now())
			if err != nil {
				return emitEgressError(err, id)
			}
			emitContract(jsoncontract.OK(sessionEgressData(sess)))
			return nil
		},
	}
	c.Flags().StringVar(&id, "session-id", "", "session id")
	c.Flags().StringVar(&grantID, "grant-id", "", "egress grant id")
	c.Flags().StringVar(&output, "output", "", "output format: json")
	return c
}

// sessionEgressData is the narrow, value-free response shape for the egress
// subcommands. Unlike sessionData it always has a grants array and revision, so
// clients can apply a revoke result without inferring an omitted field as stale.
func sessionEgressData(sess engsession.Session) map[string]any {
	grants := sess.EgressGrants
	if grants == nil {
		grants = []engsession.EgressGrant{}
	}
	return map[string]any{
		"session_id":              sess.ID,
		"egress_grants":           grants,
		"egress_acknowledgements": egressAcknowledgementsOrEmpty(sess.EgressAcknowledgements),
		"egress_grant_revision":   sess.GrantRevision,
	}
}

func egressAcknowledgementsOrEmpty(acknowledgements []engsession.EgressAcknowledgement) []engsession.EgressAcknowledgement {
	if acknowledgements == nil {
		return []engsession.EgressAcknowledgement{}
	}
	return acknowledgements
}

// filterAcknowledgedEgressObservations suppresses only the snapshot already
// acknowledged by the operator. A later denial for the same FQDN:port remains
// pending and cannot be hidden by a historical Keep denied action.
func filterAcknowledgedEgressObservations(sess engsession.Session, observations []container.EgressObservation) []container.EgressObservation {
	if len(observations) == 0 || len(sess.EgressAcknowledgements) == 0 {
		return observations
	}
	acknowledgedAt := make(map[string]time.Time, len(sess.EgressAcknowledgements))
	for _, ack := range sess.EgressAcknowledgements {
		acknowledgedAt[fmt.Sprintf("%s:%d", ack.Host, ack.Port)] = ack.AcknowledgedAt
	}
	out := make([]container.EgressObservation, 0, len(observations))
	for _, observation := range observations {
		if at, ok := acknowledgedAt[fmt.Sprintf("%s:%d", observation.Host, observation.Port)]; ok && !observation.LastSeen.After(at) {
			continue
		}
		out = append(out, observation)
	}
	return out
}

// egressSession loads an enforceable session before a read-only egress command.
// Host and network-allow sessions are rejected instead of looking grantable in a
// UI that cannot actually constrain their traffic.
func egressSession(store engsession.Store, id string) (engsession.Session, error) {
	if id == "" {
		return engsession.Session{}, emitContractError(jsoncontract.CodeInvalidArgument, "--session-id is required", nil)
	}
	sess, err := store.Get(id)
	if err != nil {
		if errors.Is(err, engsession.ErrNotFound) {
			return engsession.Session{}, emitContractError(jsoncontract.CodeSessionNotFound, "session not found", map[string]any{"session_id": id})
		}
		return engsession.Session{}, emitContractError(jsoncontract.CodeIOError, "load session", map[string]any{"error": err.Error()})
	}
	if !engsession.CanGrant(sess) {
		return engsession.Session{}, emitContractError(jsoncontract.CodeInvalidArgument, engsession.ErrSessionNotGrantable.Error(), map[string]any{"session_id": id, "environment": sess.Environment, "network": sess.Network})
	}
	return sess, nil
}

// emitEgressError maps all mutation failures to stable contract errors without
// exposing proxy command output or runtime file paths in the JSON response.
func emitEgressError(err error, id string) error {
	switch {
	case errors.Is(err, engsession.ErrNotFound):
		return emitContractError(jsoncontract.CodeSessionNotFound, "session not found", map[string]any{"session_id": id})
	case errors.Is(err, engsession.ErrSessionNotGrantable):
		return emitContractError(jsoncontract.CodeInvalidArgument, err.Error(), map[string]any{"session_id": id})
	case strings.HasPrefix(err.Error(), "egress grant:"):
		return emitContractError(jsoncontract.CodeInvalidArgument, err.Error(), map[string]any{"session_id": id})
	case err.Error() == "session stopped":
		return emitContractError(jsoncontract.CodeSessionStopped, "session is stopped", map[string]any{"session_id": id})
	default:
		return emitContractError(jsoncontract.CodeIOError, "apply session egress grant", map[string]any{"session_id": id})
	}
}

func cmdSessionCreate() *cobra.Command {
	var agent, workspace, output, environment, network, profile, name string
	var trustHost bool
	c := &cobra.Command{
		Use:   "create (--profile <name> | --agent <claude|pi|fish|zsh> --environment <host|container> --workspace <dir>) [--name <label>] --output json",
		Short: "Create a safeslop session record",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if output != "json" {
				return fmt.Errorf("session create requires --output json")
			}
			// Validate the optional display name once, up front, so the identical rule
			// applies to both the --profile and explicit-flag creation paths (specs/0065
			// S2). An empty/whitespace name is allowed and means "no name".
			validName, err := engsession.ValidateName(name)
			if err != nil {
				return emitContractError(jsoncontract.CodeInvalidArgument, err.Error(), map[string]any{"name": name})
			}
			store := sessionStore()
			if profile != "" {
				if agent != "" || workspace != "" || environment != "" || network != "" {
					return emitContractError(jsoncontract.CodeInvalidArgument, "--profile cannot be combined with --agent, --environment, --workspace, or --network", nil)
				}
				sess, err := createSessionFromProfile(store, profile)
				if err != nil {
					return err
				}
				if validName != "" {
					sess, err = store.Update(sess.ID, func(current engsession.Session) (engsession.Session, error) {
						current.Name = validName
						return current, nil
					})
					if err != nil {
						return emitContractError(jsoncontract.CodeIOError, "save session", map[string]any{"error": err.Error()})
					}
				}
				emitContract(jsoncontract.OK(sessionData(sess)))
				return nil
			}
			canonicalAgent := policy.NormalizeAgent(agent)
			if !policy.IsLaunchableAgent(canonicalAgent) {
				return emitContractError(jsoncontract.CodeAgentUnsupported, fmt.Sprintf("unsupported agent %q", agent), map[string]any{"agent": agent})
			}
			if workspace == "" {
				return emitContractError(jsoncontract.CodeInvalidArgument, "--workspace is required", nil)
			}
			if fi, err := os.Stat(workspace); err != nil || !fi.IsDir() {
				return emitContractError(jsoncontract.CodeInvalidArgument, "--workspace must name an existing directory", map[string]any{"workspace": workspace})
			}
			switch environment {
			case "container":
			case "host":
				// An ad-hoc host launch has no safeslop.cue to approve, yet it runs the agent
				// unconfined with your real host credentials. Require an explicit ack so the
				// session lane can't silently launch a host agent (specs/0072 F1, closing the
				// ad-hoc arm of 0070 B1). Profile host launches are gated by policy-byte trust.
				if !trustHost {
					return emitContractError(jsoncontract.CodeTrustRequired,
						"host sessions run the agent unconfined with your host credentials; pass --trust-host to acknowledge",
						map[string]any{"environment": "host", "hint": "add --trust-host"})
				}
			case "":
				return emitContractError(jsoncontract.CodeInvalidArgument,
					"--environment is required; must be one of: host, container", nil)
			default:
				return emitContractError(jsoncontract.CodeInvalidArgument,
					fmt.Sprintf("--environment %q is not valid; must be one of: host, container", environment),
					map[string]any{"environment": environment})
			}
			if network != "" {
				switch network {
				case "deny", "allow":
				default:
					return emitContractError(jsoncontract.CodeInvalidArgument,
						fmt.Sprintf("--network %q is not valid; must be one of: deny, allow", network),
						map[string]any{"network": network})
				}
			}
			sess, err := store.Create(canonicalAgent, environment, workspace, time.Now())
			if err != nil {
				return emitContractError(jsoncontract.CodeIOError, "create session", map[string]any{"error": err.Error()})
			}
			// Persist post-create mutations (network override and display name) in a
			// single Save so the record is written exactly once (specs/0065 S2).
			if network != "" || validName != "" {
				sess, err = store.Update(sess.ID, func(current engsession.Session) (engsession.Session, error) {
					if network != "" {
						current.Network = network
					}
					if validName != "" {
						current.Name = validName
					}
					return current, nil
				})
				if err != nil {
					return emitContractError(jsoncontract.CodeIOError, "save session", map[string]any{"error": err.Error()})
				}
			}
			emitContract(jsoncontract.OK(sessionData(sess)))
			return nil
		},
	}
	c.Flags().StringVar(&profile, "profile", "", "profile name from safeslop.cue")
	c.Flags().StringVar(&agent, "agent", "", "agent to run: claude, pi, fish, or zsh")
	c.Flags().StringVar(&workspace, "workspace", "", "workspace directory")
	c.Flags().StringVar(&output, "output", "", "output format: json")
	c.Flags().StringVar(&environment, "environment", "", "isolation environment (required): host or container")
	c.Flags().StringVar(&network, "network", "", "network policy: deny or allow (overrides profile default)")
	c.Flags().StringVar(&name, "name", "", "optional human display name for the session (combinable with --profile)")
	c.Flags().BoolVar(&trustHost, "trust-host", false, "acknowledge that an ad-hoc host session runs the agent unconfined with your host credentials")
	return c
}

// cmdSessionRename sets or clears a session's human display name. A label touches
// no boundary, credential, or process state, so the engine allows it in any
// status (specs/0065 D5). This mirrors the flag/output/error shape of the sibling
// session commands: --output json is mandatory, an empty --session-id is rejected
// before the store is touched, and an empty --name is a deliberate clear.
func cmdSessionRename() *cobra.Command {
	var id, name, output string
	c := &cobra.Command{
		Use:   "rename --session-id <id> --name <name> --output json",
		Short: "Set or clear a session's human display name",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if output != "json" {
				return fmt.Errorf("session rename requires --output json")
			}
			if id == "" {
				return emitContractError(jsoncontract.CodeInvalidArgument, "--session-id is required", nil)
			}
			sess, err := sessionStore().Rename(id, name, time.Now())
			if err != nil {
				switch {
				case errors.Is(err, engsession.ErrNotFound):
					return emitContractError(jsoncontract.CodeSessionNotFound, "session not found", map[string]any{"session_id": id})
				default:
					// ValidateName returns a plain (non-sentinel) error, so re-run the pure
					// validator to distinguish a rejected name (INVALID_ARGUMENT) from a
					// Save/IO failure (IO_ERROR) — specs/0065 S2.
					if _, verr := engsession.ValidateName(name); verr != nil {
						return emitContractError(jsoncontract.CodeInvalidArgument, verr.Error(), map[string]any{"name": name})
					}
					return emitContractError(jsoncontract.CodeIOError, "rename session", map[string]any{"error": err.Error()})
				}
			}
			emitContract(jsoncontract.OK(sessionData(sess)))
			return nil
		},
	}
	c.Flags().StringVar(&id, "session-id", "", "session id to rename")
	c.Flags().StringVar(&name, "name", "", "new display name (empty clears it)")
	c.Flags().StringVar(&output, "output", "", "output format: json")
	return c
}

func createSessionFromProfile(store engsession.Store, profile string) (engsession.Session, error) {
	path, err := findConfig("")
	if err != nil {
		return createBuiltinSession(store, profile)
	}
	loaded, err := loadPolicyForLaunch(path)
	if err != nil {
		return engsession.Session{}, emitContractError(jsoncontract.CodeSchemaViolation, "load safeslop.cue", map[string]any{"path": path, "error": err.Error()})
	}
	prof, ok := loaded.cfg.Profiles[profile]
	if !ok {
		if _, builtin := policy.BuiltinProfileByName(profile); builtin {
			return createBuiltinSession(store, profile)
		}
		return engsession.Session{}, emitContractError(jsoncontract.CodeNotFound, fmt.Sprintf("no profile %q in safeslop.cue", profile), map[string]any{"profile": profile, "path": path})
	}
	// Gate the session lane on the same host-approval `safeslop run` requires (specs/0072 F1,
	// closing 0070 B1): the Emacs client launches exclusively through session create, so without
	// this every session launch skipped policy-byte approval. The approved hash is recorded on the
	// session below and re-verified at run time (verifySessionTrust). The status check uses the same
	// bytes parsed above, so the session cannot approve one policy read while recording another.
	status, err := loadedPolicyTrustStatus(loaded)
	if err != nil {
		return engsession.Session{}, emitContractError(jsoncontract.CodeIOError, "verify safeslop.cue trust", map[string]any{"path": path, "error": err.Error()})
	}
	if status != trust.Trusted {
		return engsession.Session{}, emitContractError(jsoncontract.CodeTrustRequired, "safeslop.cue is not host-approved", map[string]any{"path": loaded.trustPath, "status": status.String(), "hint": "safeslop trust " + loaded.trustPath})
	}
	resolved, err := policy.Resolve(prof)
	if err != nil {
		return engsession.Session{}, emitContractError(jsoncontract.CodeInvalidArgument, "resolve profile packages", map[string]any{"profile": profile, "error": err.Error()})
	}
	recipe, err := container.ResolveRecipe(resolved.IdentitySet)
	if err != nil {
		return engsession.Session{}, emitContractError(jsoncontract.CodeInvalidArgument, "resolve profile image recipe", map[string]any{"profile": profile, "error": err.Error()})
	}
	workspace := prof.Workspace
	if workspace == "" {
		workspace, _ = os.Getwd()
	} else if !filepath.IsAbs(workspace) {
		workspace = filepath.Join(filepath.Dir(path), workspace)
	}
	if fi, err := os.Stat(workspace); err != nil || !fi.IsDir() {
		return engsession.Session{}, emitContractError(jsoncontract.CodeInvalidArgument, "profile workspace must name an existing directory", map[string]any{"profile": profile, "workspace": workspace})
	}
	agent := policy.NormalizeAgent(prof.Agent)
	if !policy.IsLaunchableAgent(agent) && agent != "shell" {
		return engsession.Session{}, emitContractError(jsoncontract.CodeAgentUnsupported, fmt.Sprintf("unsupported agent %q", prof.Agent), map[string]any{"agent": prof.Agent, "profile": profile})
	}
	sess, err := store.Create(agent, prof.Environment, workspace, time.Now())
	if err != nil {
		return engsession.Session{}, emitContractError(jsoncontract.CodeIOError, "create session", map[string]any{"error": err.Error()})
	}
	sess.Profile = profile
	sess.ProfileSource = "project"
	sess.Network = prof.Network
	sess.RecipeID = recipe.RecipeID
	sess.Image = recipe.AgentImage
	sess.Resolved = resolvedMetadata(resolved)
	sess.PolicyPath = loaded.trustPath
	sess.PolicyHash = loaded.hash
	// Compute value-free credential scopes from the trusted, in-memory policy.Profile
	// before saving, so create/list/status all surface the same legibility rows and no
	// later re-read of the policy is needed (specs/0086 T1).
	sess.CredentialScopes = credentialScopesFromProfile(prof)
	// Snapshot trusted durable rules now. Session launch later reconstructs the
	// profile only for trust verification, never to widen this session after a
	// policy edit (specs/0103).
	sess.PersistentEgress = append([]policy.PersistentEgressRule(nil), prof.PersistentEgress...)
	committed, err := store.Update(sess.ID, func(current engsession.Session) (engsession.Session, error) {
		current.Profile = sess.Profile
		current.ProfileSource = sess.ProfileSource
		current.Network = sess.Network
		current.RecipeID = sess.RecipeID
		current.Image = sess.Image
		current.Resolved = sess.Resolved
		current.PolicyPath = sess.PolicyPath
		current.PolicyHash = sess.PolicyHash
		current.CredentialScopes = sess.CredentialScopes
		current.PersistentEgress = sess.PersistentEgress
		return current, nil
	})
	if err != nil {
		return engsession.Session{}, emitContractError(jsoncontract.CodeIOError, "save session", map[string]any{"error": err.Error()})
	}
	return committed, nil
}

// credentialScopesFromProfile derives the value-free credential legibility rows for
// a profile-backed session from its trusted policy.Profile, mirroring the access
// semantics of the staging path (creds.StageGithub/StageForgejo/StagePnpm/StageAWS/
// StageGCP/StageKube). Declared repo entries use their own RepoCred.Write; an
// origin-inferred git-forge credential (no repos) uses the provider-level Write and
// is keyed on "origin" because the real owner/repo is resolved from the cwd remote
// only at stage time — keeping this pure and hermetic. Mode/deploy-key/TTL is scope
// text only. Rows carry only non-secret targets (repo, registry host, cloud profile,
// cluster) and NEVER a token value, secret ref (op://, env:), staged file path,
// session policy, or private-key ref (specs/0086 T1). Returns nil for an ad-hoc or
// credential-less profile so the JSON field is omitted.
// createBuiltinSession creates a profile-backed session without a repo policy:
// the signed binary registry itself is the trusted policy authority.
func createBuiltinSession(store engsession.Store, name string) (engsession.Session, error) {
	builtin, ok := policy.BuiltinProfileByName(name)
	if !ok {
		return engsession.Session{}, emitContractError(jsoncontract.CodeNotFound, "profile not found", map[string]any{"profile": name})
	}
	prof := builtin.Profile
	resolved, err := policy.Resolve(prof)
	if err != nil {
		return engsession.Session{}, emitContractError(jsoncontract.CodeInvalidArgument, "resolve profile packages", map[string]any{"profile": name, "error": err.Error()})
	}
	recipe, err := container.ResolveRecipe(resolved.IdentitySet)
	if err != nil {
		return engsession.Session{}, emitContractError(jsoncontract.CodeInvalidArgument, "resolve profile image recipe", map[string]any{"profile": name, "error": err.Error()})
	}
	workspace, err := os.Getwd()
	if err != nil {
		return engsession.Session{}, emitContractError(jsoncontract.CodeIOError, "get workspace", map[string]any{"error": err.Error()})
	}
	sess, err := store.Create(policy.NormalizeAgent(prof.Agent), prof.Environment, workspace, time.Now())
	if err != nil {
		return engsession.Session{}, emitContractError(jsoncontract.CodeIOError, "create session", map[string]any{"error": err.Error()})
	}
	sess.Profile, sess.ProfileSource, sess.Network = name, "builtin", prof.Network
	sess.PolicyPath, sess.PolicyHash = "builtin:"+name, builtin.Hash
	sess.RecipeID, sess.Image, sess.Resolved = recipe.RecipeID, recipe.AgentImage, resolvedMetadata(resolved)
	sess.CredentialScopes = credentialScopesFromProfile(prof)
	sess.PersistentEgress = append([]policy.PersistentEgressRule(nil), prof.PersistentEgress...)
	committed, err := store.Update(sess.ID, func(current engsession.Session) (engsession.Session, error) {
		current.Profile, current.ProfileSource, current.Network = sess.Profile, sess.ProfileSource, sess.Network
		current.PolicyPath, current.PolicyHash = sess.PolicyPath, sess.PolicyHash
		current.RecipeID, current.Image, current.Resolved = sess.RecipeID, sess.Image, sess.Resolved
		current.CredentialScopes = sess.CredentialScopes
		current.PersistentEgress = sess.PersistentEgress
		return current, nil
	})
	if err != nil {
		return engsession.Session{}, emitContractError(jsoncontract.CodeIOError, "save session", map[string]any{"error": err.Error()})
	}
	return committed, nil
}

func credentialScopesFromProfile(prof policy.Profile) []engsession.CredentialScope {
	c := prof.Credentials
	if c == nil {
		return nil
	}
	var out []engsession.CredentialScope
	if c.Github != nil {
		out = append(out, githubCredentialScopes(c.Github)...)
	}
	if c.Forgejo != nil {
		out = append(out, forgejoCredentialScopes(c.Forgejo)...)
	}
	for _, r := range c.Pnpm {
		host := r.Host
		if host == "" {
			host = "registry.npmjs.org"
		}
		out = append(out, engsession.CredentialScope{Kind: "pnpm", Name: host, Scope: r.Scope})
	}
	if c.Aws != nil {
		// roleArn is a non-secret identity ARN (surfaced, per the spec example);
		// sessionPolicy is an inline JSON policy document and is deliberately excluded.
		out = append(out, engsession.CredentialScope{Kind: "aws", Name: c.Aws.Profile, Scope: joinScope(c.Aws.Region, c.Aws.RoleArn)})
	}
	if c.Gcp != nil {
		// ADC is the ambient target; declared OAuth scopes are non-secret scope text
		// and mirror StageGCP's --scopes downscoping without exposing the access token.
		out = append(out, engsession.CredentialScope{Kind: "gcp", Name: "adc", Scope: strings.Join(c.Gcp.Scopes, ",")})
	}
	if c.Kube != nil {
		out = append(out, kubeCredentialScopes(c.Kube)...)
	}
	if c.Pi != nil {
		out = append(out, engsession.CredentialScope{
			Kind: "pi-oauth", Name: c.Pi.Provider + "/" + c.Pi.Model,
			Scope: "access snapshot, short-lived",
		})
	}
	return out
}

// githubCredentialScopes renders the GitHub App/PAT rows. Declared repos become one
// row each keyed on "owner/name" with their own RepoCred.Write; with no repos the
// single row is keyed on "origin" using the provider-level Write. Mode ("app"/"pat")
// and any TTL are scope text only — never the PAT secret ref or the App private key.
func githubCredentialScopes(gc *policy.GithubCreds) []engsession.CredentialScope {
	mode := gc.Mode
	if mode == "" {
		mode = "app" // schema default; be robust to a directly-constructed Profile
	}
	if len(gc.Repos) == 0 {
		return []engsession.CredentialScope{{Kind: "github", Name: "origin", Scope: joinScope(mode, access(gc.Write), gc.Ttl)}}
	}
	out := make([]engsession.CredentialScope, 0, len(gc.Repos))
	for _, r := range gc.Repos {
		out = append(out, engsession.CredentialScope{Kind: "github", Name: r.Repo, Scope: joinScope(mode, access(r.Write), gc.Ttl)})
	}
	return out
}

// forgejoCredentialScopes renders the Forgejo/Gitea deploy-key rows with the same
// repo/origin + access semantics as GitHub; "deploy-key" and any TTL are scope text
// only. The instance URL is intentionally not embedded — it can name a private,
// self-hosted forge host, and the repo/origin target already answers "which".
func forgejoCredentialScopes(fc *policy.ForgejoCreds) []engsession.CredentialScope {
	if len(fc.Repos) == 0 {
		return []engsession.CredentialScope{{Kind: "forgejo", Name: "origin", Scope: joinScope("deploy-key", access(fc.Write), fc.Ttl)}}
	}
	out := make([]engsession.CredentialScope, 0, len(fc.Repos))
	for _, r := range fc.Repos {
		out = append(out, engsession.CredentialScope{Kind: "forgejo", Name: r.Repo, Scope: joinScope("deploy-key", access(r.Write), fc.Ttl)})
	}
	return out
}

// kubeCredentialScopes renders the single Kubernetes cluster row: the cluster name
// is the target and the provider (eks/gke) + region/location/project are scope. All
// are non-secret identifiers; the staged bearer token never appears.
func kubeCredentialScopes(k *policy.KubeCluster) []engsession.CredentialScope {
	switch {
	case k.Eks != nil:
		return []engsession.CredentialScope{{Kind: "kube", Name: k.Eks.Name, Scope: joinScope("eks", k.Eks.Region)}}
	case k.Gke != nil:
		return []engsession.CredentialScope{{Kind: "kube", Name: k.Gke.Name, Scope: joinScope("gke", k.Gke.Location, k.Gke.Project)}}
	}
	return nil
}

// access maps a write flag to the compact rw/ro label used across the credential
// scope rows, matching the staging read/write partition semantics.
func access(write bool) string {
	if write {
		return "rw"
	}
	return "ro"
}

// joinScope joins non-empty scope fields with a single space into the compact,
// value-free Scope string; empty fields are dropped so a missing region/ttl never
// leaves stray whitespace.
func joinScope(parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, " ")
}

func resolvedMetadata(resolved *policy.Resolved) *engsession.ResolvedMetadata {
	if resolved == nil {
		return nil
	}
	return &engsession.ResolvedMetadata{
		Packages:      append([]string(nil), resolved.Packages...),
		IdentitySet:   append([]string(nil), resolved.IdentitySet...),
		RuntimeEgress: append([]string(nil), resolved.RuntimeEgress...),
	}
}

// sessionProfile reconstructs the profile a session launches. A profile-backed
// session re-reads its pinned safeslop.cue (specs/0073): the record stores only
// scalar fields, so launching anything less than the parsed policy silently
// strips credentials/secrets/egress/toolchain from the boundary — the 0072 F1
// gate would verify approval of bytes that then didn't drive the launch. The
// bytes are re-hashed against the create-time approval and parsed from that
// same read (no verify→parse TOCTOU); every failure is fail-closed, never a
// silent fallback to the synthetic profile. Ad-hoc sessions (PolicyPath empty)
// have no policy file to be faithful to and keep the synthetic path.
func sessionProfile(sess engsession.Session) (policy.Profile, error) {
	prof := policy.Profile{Agent: sess.Agent, Environment: sess.Environment, Network: sess.Network, Workspace: sess.Workspace}
	if sess.Profile != "" && strings.HasPrefix(sess.PolicyPath, "builtin:") {
		builtin, ok := policy.BuiltinProfileByName(sess.Profile)
		if !ok || builtin.Hash != sess.PolicyHash {
			return policy.Profile{}, fmt.Errorf("builtin profile %q changed or is unavailable; recreate the session", sess.Profile)
		}
		prof = builtin.Profile
		prof.Workspace = sess.Workspace
	} else if sess.Profile != "" && sess.PolicyPath != "" {
		data, err := os.ReadFile(sess.PolicyPath)
		if err != nil {
			return policy.Profile{}, fmt.Errorf("re-read pinned policy for session %s: %w", sess.ID, err)
		}
		if trust.Hash(data) != sess.PolicyHash {
			return policy.Profile{}, fmt.Errorf("safeslop.cue at %s changed since this session was created; review it, then re-trust and recreate the session", sess.PolicyPath)
		}
		cfg, err := policy.LoadBytes(data)
		if err != nil {
			return policy.Profile{}, fmt.Errorf("parse pinned policy %s: %w", sess.PolicyPath, err)
		}
		p, ok := cfg.Profiles[sess.Profile]
		if !ok {
			return policy.Profile{}, fmt.Errorf("profile %q no longer exists in %s; recreate the session", sess.Profile, sess.PolicyPath)
		}
		prof = p
		prof.Workspace = sess.Workspace
	}
	if sess.Resolved != nil {
		prof.Packages = append([]string(nil), sess.Resolved.IdentitySet...)
		prof.BareAgent = true
	}
	return prof, nil
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
			sessions, err := sessionStore().ListReconciled(time.Now(), sessionProcessAlive, sessionReapBoundary, sessionWipeStageDir)
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
			sess, err := sessionStore().GetReconciled(id, time.Now(), sessionProcessAlive, sessionReapBoundary, sessionWipeStageDir)
			if err != nil {
				if errors.Is(err, engsession.ErrNotFound) {
					return emitContractError(jsoncontract.CodeSessionNotFound, "session not found", map[string]any{"session_id": id})
				}
				return emitContractError(jsoncontract.CodeIOError, "load session", map[string]any{"error": err.Error()})
			}
			data := sessionData(sess)
			// Surface the GitHub App-token TTL ceiling so the operator can see how long a
			// session's ephemeral HTTPS access has left (specs/0069 T8). Additive + best
			// effort: unlinked sessions have no manifest and simply omit the block.
			if stageDir, derr := stageDirFor("session-"+sess.ID, sess.Workspace); derr == nil {
				if exp, ok, _ := creds.GithubCredsExpiry(stageDir); ok {
					block := map[string]any{"min_expires_at": exp.UTC().Format(time.RFC3339)}
					if remaining := time.Until(exp); remaining > 0 {
						block["ttl"] = remaining.Round(time.Second).String()
					} else {
						block["note"] = "github token expired (1h App-token ceiling; renewal lands in P2 \u2014 specs/0068 F4)"
					}
					data["github_creds"] = block
				}
			}
			env := jsoncontract.OK(data)
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
			store := sessionStore()
			// Reconcile immediately before signalling: if the recorded wrapper/supervisor
			// died, or its PID was reused, persist it stopped and clean local stage/socket
			// state instead of sending SIGTERM to an unrelated process/group.
			if _, err := store.GetReconciled(id, time.Now(), sessionProcessAlive, sessionReapBoundary, sessionWipeStageDir); err != nil {
				if errors.Is(err, engsession.ErrNotFound) {
					return emitContractError(jsoncontract.CodeSessionNotFound, "session not found", map[string]any{"session_id": id})
				}
				return emitContractError(jsoncontract.CodeIOError, "reconcile session", map[string]any{"error": err.Error()})
			}
			sess, err := store.Stop(id, revoke, time.Now(), sessionRevokeCredentials, sessionKillProcess, sessionReapBoundary, sessionWipeStageDir)
			if err != nil {
				if errors.Is(err, engsession.ErrNotFound) {
					return emitContractError(jsoncontract.CodeSessionNotFound, "session not found", map[string]any{"session_id": id})
				}
				return emitContractError(jsoncontract.CodeCredentialRevokeFailed, "stop session", map[string]any{"error": err.Error()})
			}
			// Store.Stop intentionally does not run reap callbacks for an already-stopped
			// record; still wipe the deterministic local stage dir here so repeated/operator
			// stop can clear a SIGKILL orphan without needing credential revocation.
			if err := sessionWipeStageDir(sess); err != nil {
				return emitContractError(jsoncontract.CodeIOError, "wipe session stage dir", map[string]any{"error": err.Error()})
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

// cmdSessionRemove deletes a single non-running session record so the operator
// can clear a stopped/created "corpse" out of the portal list. A running session
// is refused with SESSION_ALREADY_RUNNING pointing at `stop` first; still-live
// credentials are revoked before the record is deleted so removal can never
// orphan staged secrets.
func cmdSessionRemove() *cobra.Command {
	var id, output string
	c := &cobra.Command{
		Use:   "rm --session-id <id> --output json",
		Short: "Remove a stopped safeslop session record",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if output != "json" {
				return fmt.Errorf("session rm requires --output json")
			}
			sess, err := sessionStore().Remove(id, sessionRevokeCredentials, sessionReapBoundary, sessionWipeStageDir)
			if err != nil {
				switch {
				case errors.Is(err, engsession.ErrNotFound):
					return emitContractError(jsoncontract.CodeSessionNotFound, "session not found", map[string]any{"session_id": id})
				case errors.Is(err, engsession.ErrSessionRunning):
					return emitContractError(jsoncontract.CodeSessionAlreadyRunning, "session is running; stop it before removing", map[string]any{"session_id": id})
				default:
					return emitContractError(jsoncontract.CodeIOError, "remove session", map[string]any{"error": err.Error()})
				}
			}
			emitContract(jsoncontract.OK(map[string]any{"removed": []string{sess.ID}}))
			return nil
		},
	}
	c.Flags().StringVar(&id, "session-id", "", "session id")
	c.Flags().StringVar(&output, "output", "", "output format: json")
	return c
}

// cmdSessionPrune removes every stopped session record in one call so the
// operator can clear all the "failed corpses" at once. It reconciles liveness
// first, so a session whose run process is gone (crash/kill/host sleep) is
// persisted as stopped and then pruned in the same pass; created and running
// sessions are always left untouched.
func cmdSessionPrune() *cobra.Command {
	var output string
	c := &cobra.Command{
		Use:   "prune --output json",
		Short: "Remove all stopped safeslop session records",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if output != "json" {
				return fmt.Errorf("session prune requires --output json")
			}
			store := sessionStore()
			// Reconcile first so a crashed session (still marked running but whose
			// process is gone) is persisted as stopped and swept in this same pass.
			if _, err := store.ListReconciled(time.Now(), sessionProcessAlive, sessionReapBoundary, sessionWipeStageDir); err != nil {
				return emitContractError(jsoncontract.CodeIOError, "list sessions", map[string]any{"error": err.Error()})
			}
			removed, err := store.PruneStopped(sessionRevokeCredentials, sessionReapBoundary, sessionWipeStageDir)
			if err != nil {
				return emitContractError(jsoncontract.CodeIOError, "prune sessions", map[string]any{"error": err.Error(), "removed": removed})
			}
			emitContract(jsoncontract.OK(map[string]any{"removed": removed}))
			return nil
		},
	}
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
			// Re-verify the profile's policy is still host-approved before launch (specs/0072
			// F1): session run rebuilds the profile from the record, so a create-time approval
			// that was later revoked or edited must not still launch. Fail-closed here in the
			// user's process; Supervise re-checks again at the detached supervisor's own start.
			if err := verifySessionTrust(sess); err != nil {
				return err
			}
			sess, err = recordSessionBackend(store, sess)
			if err != nil {
				return err
			}
			prof, err := sessionProfile(sess)
			if err != nil {
				return err
			}
			argv, err := agentArgv(prof)
			if err != nil {
				return err
			}
			// `session run` is an interactive attach: every boundary presents the
			// agent under a controlling terminal (host via RunInTerminal,
			// container via the RunInPTY tty bridge), so without a usable PTY the
			// session is undriveable. Emacs drives this via
			// make-term, which connects the process to a pty; a no-tty invocation
			// (cron, a pipe, a headless shell) gets the PTY_UNAVAILABLE contract error
			// pointing at the JSONL status fallback, exits non-zero, and is *not*
			// marked running — a session that can never start must not be left as a
			// phantom for liveness/reconcile or `session stop` (specs/0050 PR4).
			if !detach && !sessionHasInteractivePTY() {
				emitContract(jsoncontract.PTYUnavailable())
				return errOutputEmitted
			}
			if err := requireHostLaunchConsent(sessionConsentName(sess), prof, os.Stdin, os.Stderr); err != nil {
				return err
			}
			if detach {
				// Detached: re-exec a supervisor that owns the agent + its PTY and
				// serves it over the per-session socket, then return so the issuing
				// buffer is freed immediately. The host consent gate above runs in
				// the issuing process before the background supervisor is spawned, so
				// a detached host session is never born already blocked on a prompt.
				// Container detach still skips the local PTY guard because the
				// supervisor allocates the PTY; the user attaches later (specs/0051
				// D1, PR3).
				return runDetach(store, id)
			}
			if _, err := store.MarkRunning(id, os.Getpid(), time.Now()); err != nil {
				return err
			}
			code, runErr := runProfile("session-"+id, prof, argv, sess.Workspace)
			if err := finishSessionRun(store, id, code, runErr, time.Now()); err != nil {
				return err
			}
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
// finishSessionRun persists any engine-owned, value-free structured failure in the same atomic
// save that records the terminal state. Unstructured failures retain the legacy error path.
func finishSessionRun(store engsession.Store, id string, code int, runErr error, now time.Time) error {
	var structured interface{ Failure() engsession.Failure }
	if errors.As(runErr, &structured) {
		_, err := store.Finish(id, code, "", now, structured.Failure())
		return err
	}
	lastErr := ""
	if runErr != nil {
		lastErr = runErr.Error()
	}
	_, err := store.Finish(id, code, lastErr, now)
	return err
}

func runDetach(store engsession.Store, id string) error {
	if sess, err := store.Get(id); err == nil {
		if _, err := recordSessionBackend(store, sess); err != nil {
			return err
		}
	}
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
// JSONL status monitor (specs/0050 PR4). It is a var so tests can exercise the
// pre-launch host consent gate without requiring a real PTY.
var sessionHasInteractivePTY = func() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}

func sessionConsentName(sess engsession.Session) string {
	if sess.Profile != "" {
		return sess.Profile
	}
	if sess.Name != "" {
		return sess.Name
	}
	return "session-" + sess.ID
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

func grantSessionEgress(ctx context.Context, store engsession.Store, id, host string, port int, now time.Time) (engsession.Session, engsession.EgressGrant, error) {
	var grant engsession.EgressGrant
	committed, err := store.WithLocked(id, func(tx *engsession.RecordTx) error {
		sess := tx.Session()
		if sess.Status == engsession.StatusStopped {
			return fmt.Errorf("session stopped")
		}
		next, nextGrant, err := engsession.AppendGrant(sess, host, port, now)
		if err != nil {
			return err
		}
		if sess.Status == engsession.StatusRunning {
			if err := applySessionGrantOverlay(ctx, sess, sessionEgressViews(next)); err != nil {
				return err
			}
		}
		if err := tx.Commit(next); err != nil {
			return err
		}
		grant = nextGrant
		return nil
	})
	if err != nil {
		return engsession.Session{}, engsession.EgressGrant{}, err
	}
	return committed, grant, nil
}

func dismissSessionEgress(store engsession.Store, id, host string, port int, now time.Time) (engsession.Session, engsession.EgressAcknowledgement, error) {
	var acknowledgement engsession.EgressAcknowledgement
	committed, err := store.Update(id, func(sess engsession.Session) (engsession.Session, error) {
		if sess.Status == engsession.StatusStopped {
			return engsession.Session{}, fmt.Errorf("session stopped")
		}
		next, nextAcknowledgement, err := engsession.DismissEgress(sess, host, port, now)
		if err != nil {
			return engsession.Session{}, err
		}
		acknowledgement = nextAcknowledgement
		return next, nil
	})
	if err != nil {
		return engsession.Session{}, engsession.EgressAcknowledgement{}, err
	}
	return committed, acknowledgement, nil
}

func revokeSessionEgress(ctx context.Context, store engsession.Store, id, grantID string, now time.Time) (engsession.Session, error) {
	return store.WithLocked(id, func(tx *engsession.RecordTx) error {
		sess := tx.Session()
		if sess.Status == engsession.StatusStopped {
			return fmt.Errorf("session stopped")
		}
		next, err := engsession.RevokeGrant(sess, grantID, now)
		if err != nil {
			return err
		}
		if sess.Status == engsession.StatusRunning {
			if err := applySessionGrantOverlay(ctx, sess, sessionEgressViews(next)); err != nil {
				return err
			}
		}
		return tx.Commit(next)
	})
}

func sessionGrantViews(grants []engsession.EgressGrant) []container.SessionGrant {
	if len(grants) == 0 {
		return nil
	}
	out := make([]container.SessionGrant, 0, len(grants))
	for _, g := range grants {
		out = append(out, container.SessionGrant{Host: g.Host, Port: g.Port})
	}
	return out
}

// sessionEgressViews merges the immutable profile-persistent snapshot with the
// mutable session grants for the shared Squid exact include. Persistent entries
// lead and duplicate pairs are coalesced so an explicit temporary grant never
// changes their source/lifetime or creates redundant ACLs (specs/0103).
func sessionEgressViews(sess engsession.Session) []container.SessionGrant {
	out := make([]container.SessionGrant, 0, len(sess.PersistentEgress)+len(sess.EgressGrants))
	seen := make(map[string]struct{}, cap(out))
	add := func(host string, port int) {
		key := fmt.Sprintf("%s:%d", host, port)
		if _, duplicate := seen[key]; duplicate {
			return
		}
		seen[key] = struct{}{}
		out = append(out, container.SessionGrant{Host: host, Port: port})
	}
	for _, rule := range sess.PersistentEgress {
		add(rule.FQDN, rule.Port)
	}
	for _, grant := range sess.EgressGrants {
		add(grant.Host, grant.Port)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func sessionGrantViewsFromRunName(name string) []container.SessionGrant {
	if !strings.HasPrefix(name, "session-") {
		return nil
	}
	sess, err := sessionStore().Get(strings.TrimPrefix(name, "session-"))
	if err != nil {
		return nil
	}
	return sessionEgressViews(sess)
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
	if sess.Profile != "" {
		out["profile"] = sess.Profile
		out["profile_source"] = sess.ProfileSource
		out["policy_path"] = sess.PolicyPath
		out["policy_hash"] = sess.PolicyHash
	}
	if sess.Name != "" {
		out["name"] = sess.Name
	}
	if sess.RecipeID != "" {
		out["recipeID"] = sess.RecipeID
	}
	if sess.Image != "" {
		out["image"] = sess.Image
	}
	if sess.Resolved != nil {
		out["resolved"] = sess.Resolved
	}
	if len(sess.CredentialScopes) > 0 {
		out["credential_scopes"] = sess.CredentialScopes
	}
	if sess.CredentialLease != nil {
		lease := *sess.CredentialLease
		// A reconciled crash cannot leave a stale healthy lease in status. Until the last
		// known token expiry it is degraded/manager_unavailable; afterwards it is expired.
		if sess.LastError == "run process exited without recording status" && lease.State != string(creds.LeaseExpired) {
			if !lease.CurrentExpiresAt.IsZero() && !time.Now().Before(lease.CurrentExpiresAt) {
				lease.State, lease.Reason = string(creds.LeaseExpired), "token_expired"
			} else {
				lease.State, lease.Reason = string(creds.LeaseDegraded), "manager_unavailable"
			}
		}
		out["credential_lease"] = &lease
	}
	if len(sess.PersistentEgress) > 0 {
		rows := make([]map[string]any, 0, len(sess.PersistentEgress))
		for _, rule := range sess.PersistentEgress {
			rows = append(rows, map[string]any{
				"fqdn": rule.FQDN, "port": rule.Port,
				"source": "profile-persistent", "lifetime": "future-sessions",
			})
		}
		out["persistent_egress"] = rows
	}
	if len(sess.EgressGrants) > 0 {
		out["egress_grants"] = sess.EgressGrants
	}
	if len(sess.EgressAcknowledgements) > 0 {
		out["egress_acknowledgements"] = sess.EgressAcknowledgements
	}
	if sess.GrantRevision > 0 {
		out["egress_grant_revision"] = sess.GrantRevision
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
	if sess.LastFailure != nil {
		out["last_failure"] = sess.LastFailure
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

func cmdUntrust() *cobra.Command {
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
			if jsonOut {
				emitJSON(map[string]any{"ok": true, "untrusted": abs})
			} else {
				fmt.Printf("untrusted: %s\n", abs)
			}
			return nil
		},
	}
}

// ---- down / gc ----

func cmdDown() *cobra.Command {
	return &cobra.Command{
		Use:   "down",
		Short: "Tear down safeslop-managed container stacks",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			ctx := context.Background()
			// Detect with PolicyAllow: `down` must clean up whatever ambient runtime safeslop launched on,
			// verified or not (the deny-tier gate applies only to launching). With no runtime present there
			// is nothing safeslop could have started, so down is a no-op (specs/0066 D5).
			eng, err := runtimepkg.Detect(runtimepkg.PolicyAllow)
			if err != nil {
				return nil
			}
			// The label sweep (ReapManaged) is the real teardown: it removes every
			// safeslop.managed container + network regardless of compose project. The former
			// ComposeForDown+Down step rendered a throwaway compose whose ephemeral project
			// matched no live session (a no-op) and, with no AgentImage, failed compose schema
			// validation before the sweep could run — breaking `down` entirely (specs/0074 Bug 2).
			return container.ReapManaged(ctx, eng)
		},
	}
}

func cmdGC() *cobra.Command {
	var until, keepRaw string
	c := &cobra.Command{
		Use:   "gc [--until <age>] [--keep <N>]",
		Short: "Garbage-collect unreferenced safeslop-managed images",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if !container.Available() {
				return fmt.Errorf("cannot gc: docker is not available (run: safeslop doctor)")
			}
			keep, err := container.ParseKeep(keepRaw)
			if err != nil {
				return err
			}
			policyPath, _ := findConfig("")
			removed, err := container.GCImages(context.Background(), runtimepkg.HostDockerEngine{}, container.GCOptions{Until: until, Keep: keep}, container.DefaultProtection(policyPath, sessionStore().Dir))
			if err != nil {
				return err
			}
			if jsonOut {
				emitContract(jsoncontract.OK(map[string]any{"removed": removed}))
				return nil
			}
			if len(removed) == 0 {
				fmt.Println("removed 0 images")
				return nil
			}
			fmt.Printf("removed %d image(s):\n", len(removed))
			for _, ref := range removed {
				fmt.Printf("  %s\n", ref)
			}
			return nil
		},
	}
	c.Flags().StringVar(&until, "until", "", "only consider managed images older than this engine age filter (for example 24h)")
	c.Flags().StringVar(&keepRaw, "keep", "", "keep the N most-recent unreferenced managed images")
	return c
}

type lockfile struct {
	RecipeID string            `json:"recipeID"`
	Agent    string            `json:"agent"`
	Base     string            `json:"base"`
	Bundles  []string          `json:"bundles,omitempty"`
	Packages []string          `json:"packages"`
	Versions map[string]string `json:"versions"`
}

func cmdLock() *cobra.Command {
	var output string
	c := &cobra.Command{
		Use:   "lock [profile] --output json",
		Short: "Write safeslop.lock.json for a profile's resolved image recipe",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if output != "json" {
				return fmt.Errorf("lock requires --output json")
			}
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
			resolved, err := policy.Resolve(prof)
			if err != nil {
				return err
			}
			recipe, err := container.ResolveRecipe(resolved.IdentitySet)
			if err != nil {
				return err
			}
			lf, err := buildLockfile(prof, resolved, recipe)
			if err != nil {
				return err
			}
			b, err := json.MarshalIndent(lf, "", "  ")
			if err != nil {
				return err
			}
			b = append(b, '\n')
			lockPath := filepath.Join(filepath.Dir(path), "safeslop.lock.json")
			if err := os.WriteFile(lockPath, b, 0o644); err != nil {
				return fmt.Errorf("write %s: %w", lockPath, err)
			}
			emitContract(jsoncontract.OK(map[string]any{
				"path":     lockPath,
				"profile":  name,
				"recipeID": lf.RecipeID,
				"lock":     lf,
			}))
			return nil
		},
	}
	c.Flags().StringVar(&output, "output", "", "output format: json")
	return c
}

func buildLockfile(prof policy.Profile, resolved *policy.Resolved, recipe *container.Recipe) (*lockfile, error) {
	versions := make(map[string]string, len(resolved.IdentitySet))
	cat := policy.DefaultCatalog()
	for _, name := range resolved.IdentitySet {
		p, ok := cat.Lookup(name)
		if !ok {
			return nil, fmt.Errorf("lock: resolved package %q is not in the catalog", name)
		}
		versions[name] = p.Version
	}
	return &lockfile{
		RecipeID: recipe.RecipeID,
		Agent:    policy.NormalizeAgent(prof.Agent),
		Base:     recipe.SourceBaseImage,
		Bundles:  append([]string(nil), prof.Bundles...),
		Packages: append([]string(nil), resolved.IdentitySet...),
		Versions: versions,
	}, nil
}

// cmdProfile groups the enveloped policy surfaces the Emacs profiles view consumes
// (specs/0052 E2/E3, specs/0058 IW3).
// credsProber builds the Prober behind `creds list|show`. It is a seam: tests replace it with a
// hermetic prober so the command never shells out to `op` or reads the real process env.
var credsProber = creds.DefaultProber

// cmdCreds groups read-only credential-posture inspection over safeslop.cue (specs/0067). Authoring
// stays CUE-canonical (edit safeslop.cue itself); this surface only reads and reports value-free
// readiness status — it never handles or reveals a secret value.
func cmdCreds() *cobra.Command {
	c := &cobra.Command{Use: "creds", Short: "Inspect the credential posture of safeslop.cue profiles"}
	c.AddCommand(cmdCredsList(), cmdCredsShow(), cmdCredsLink(), cmdCredsUnlink(), cmdCredsStatus(), cmdCredsGC())
	return c
}

func cmdCredsList() *cobra.Command {
	var output string
	c := &cobra.Command{
		Use:   "list [safeslop.cue] --output json",
		Short: "List declared credentials across profiles with value-free readiness status",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if output != "json" {
				return fmt.Errorf("creds list requires --output json")
			}
			path, err := findConfig(argAt(args, 0))
			if err != nil {
				return emitContractError(jsoncontract.CodeNotFound, "safeslop.cue not found", map[string]any{"error": err.Error()})
			}
			cfg, err := policy.Load(path)
			if err != nil {
				return emitContractError(jsoncontract.CodeSchemaViolation, "load safeslop.cue", map[string]any{"path": path, "error": err.Error()})
			}
			rep := creds.Inspect(context.Background(), cfg, credsProber())
			emitContract(jsoncontract.OK(map[string]any{
				"config":      path,
				"op":          rep.Op,
				"credentials": credRowsOrEmpty(rep.Rows),
			}))
			return nil
		},
	}
	c.Flags().StringVar(&output, "output", "", "output format: json")
	return c
}

func cmdCredsShow() *cobra.Command {
	var output string
	c := &cobra.Command{
		Use:   "show <profile> [safeslop.cue] --output json",
		Short: "Show one profile's declared credentials with value-free readiness status",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(_ *cobra.Command, args []string) error {
			if output != "json" {
				return fmt.Errorf("creds show requires --output json")
			}
			path, err := findConfig(argAt(args, 1))
			if err != nil {
				return emitContractError(jsoncontract.CodeNotFound, "safeslop.cue not found", map[string]any{"error": err.Error()})
			}
			cfg, err := policy.Load(path)
			if err != nil {
				return emitContractError(jsoncontract.CodeSchemaViolation, "load safeslop.cue", map[string]any{"path": path, "error": err.Error()})
			}
			prof, ok := cfg.Profiles[args[0]]
			if !ok {
				return emitContractError(jsoncontract.CodeNotFound, fmt.Sprintf("no profile %q in safeslop.cue", args[0]), map[string]any{"profile": args[0], "path": path})
			}
			// Scope Inspect to just this profile for the detail view.
			one := &policy.Config{Version: cfg.Version, Profiles: map[string]policy.Profile{args[0]: prof}}
			rep := creds.Inspect(context.Background(), one, credsProber())
			emitContract(jsoncontract.OK(map[string]any{
				"config":      path,
				"profile":     args[0],
				"op":          rep.Op,
				"credentials": credRowsOrEmpty(rep.Rows),
			}))
			return nil
		},
	}
	c.Flags().StringVar(&output, "output", "", "output format: json")
	return c
}

// credRowsOrEmpty coalesces a nil row slice to non-nil so the envelope always carries a
// `credentials: []` array (never `null`) for the Emacs client.
func credRowsOrEmpty(rows []creds.CredRow) []creds.CredRow {
	if rows == nil {
		return []creds.CredRow{}
	}
	return rows
}

func cmdProfile() *cobra.Command {
	c := &cobra.Command{Use: "profile", Short: "Inspect and author safeslop.cue profiles"}
	c.AddCommand(cmdProfileList(), cmdProfilePresets(), cmdProfileDefaults(), cmdProfileShow(), cmdProfileCreate(), cmdProfileDelete(), cmdProfileCredentials(), cmdProfileEgress())
	return c
}

func cmdProfileList() *cobra.Command {
	var output string
	c := &cobra.Command{
		Use:   "list [safeslop.cue] --output json",
		Short: "List the profiles defined in a safeslop.cue (enveloped JSON contract)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if output != "json" {
				return fmt.Errorf("profile list requires --output json")
			}
			path, err := findConfig(arg0(args))
			if err != nil {
				if len(args) > 0 {
					return err
				}
				emitContract(jsoncontract.OK(map[string]any{
					"path":     "",
					"profiles": map[string]policy.Profile{},
					"builtins": profileBuiltinRows(),
				}))
				return nil
			}
			cfg, err := policy.Load(path)
			if err != nil {
				return err
			}
			emitContract(jsoncontract.OK(map[string]any{"path": path, "profiles": cfg.Profiles, "builtins": profileBuiltinRows()}))
			return nil
		},
	}
	c.Flags().StringVar(&output, "output", "", "output format: json")
	return c
}

func cmdProfilePresets() *cobra.Command {
	var output string
	c := &cobra.Command{
		Use:   "presets --output json",
		Short: "List the embedded policy presets offered as starting points",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if output != "json" {
				return fmt.Errorf("profile presets requires --output json")
			}
			emitContract(jsoncontract.OK(map[string]any{"presets": policy.Presets()}))
			return nil
		},
	}
	c.Flags().StringVar(&output, "output", "", "output format: json")
	return c
}

func profileBuiltinRows() []map[string]any {
	profiles := make([]map[string]any, 0)
	for _, builtin := range policy.BuiltinProfiles() {
		profiles = append(profiles, map[string]any{
			"name": builtin.Name, "description": builtin.Description, "profile_source": "builtin",
			"policy_path": "builtin:" + builtin.Name, "policy_hash": builtin.Hash, "profile": builtin.Profile,
		})
	}
	return profiles
}

func cmdProfileDefaults() *cobra.Command {
	var output string
	c := &cobra.Command{
		Use:   "defaults --output json",
		Short: "List binary-embedded launchable profile defaults",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if output != "json" {
				return fmt.Errorf("profile defaults requires --output json")
			}
			emitContract(jsoncontract.OK(map[string]any{"profiles": profileBuiltinRows()}))
			return nil
		},
	}
	c.Flags().StringVar(&output, "output", "", "output format: json")
	return c
}

func cmdProfileShow() *cobra.Command {
	var output string
	c := &cobra.Command{
		Use:   "show <name> [safeslop.cue] --output json",
		Short: "Show a resolved project or builtin profile with its image recipe",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(_ *cobra.Command, args []string) error {
			if output != "json" {
				return fmt.Errorf("profile show requires --output json")
			}
			resolved, err := resolveProfile(args[0], argAt(args, 1))
			if err != nil {
				code := jsoncontract.CodeNotFound
				var resolutionErr *profileResolutionError
				if errors.As(err, &resolutionErr) {
					code = resolutionErr.code
				}
				return emitContractError(code, "profile not found", map[string]any{"profile": args[0], "error": err.Error()})
			}
			data, err := profileResolvedData(resolved.policyPath, resolved.name, resolved.profile, profileEvaluationInput{
				Source:      profileEvaluationSource(resolved.source),
				Name:        resolved.name,
				PolicyPath:  resolved.policyPath,
				PolicyHash:  resolved.policyHash,
				PolicyBytes: resolved.policyBytes,
				Profile:     resolved.profile,
			})
			if err != nil {
				return emitContractError(jsoncontract.CodeInvalidArgument, "resolve profile image recipe", map[string]any{"profile": args[0], "error": err.Error()})
			}
			data["profile_source"] = resolved.source
			data["profile_name"] = resolved.name
			data["policy_path"] = resolved.policyPath
			data["policy_hash"] = resolved.policyHash
			emitContract(jsoncontract.OK(data))
			return nil
		},
	}
	c.Flags().StringVar(&output, "output", "", "output format: json")
	return c
}

func cmdProfileCreate() *cobra.Command {
	var name, agent, environment, workspace, network, output string
	var bundles, packages []string
	var dryRun, noDefaultBundle bool
	c := &cobra.Command{
		Use:   "create --name N --agent A --environment E [--bundle B ...] [--package P ...] [--dry-run] --output json",
		Short: "Create or update a safeslop.cue profile",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if output != "json" {
				return fmt.Errorf("profile create requires --output json")
			}
			if name == "" || agent == "" || environment == "" {
				return emitContractError(jsoncontract.CodeInvalidArgument, "profile create requires --name, --agent, and --environment", nil)
			}
			if network == "" {
				network = "deny"
			}
			var cfg *policy.Config
			path, err := profileCreatePathForOutput()
			if err != nil {
				return emitContractError(jsoncontract.CodeIOError, "load safeslop.cue", map[string]any{"error": err.Error()})
			}
			if !dryRun {
				path, cfg, err = loadOrNewConfigForCreate()
				if err != nil {
					return emitContractError(jsoncontract.CodeIOError, "load safeslop.cue", map[string]any{"error": err.Error()})
				}
			}
			prof := policy.Profile{
				Agent:       policy.NormalizeAgent(agent),
				Environment: environment,
				Workspace:   workspace,
				Network:     network,
				Bundles:     append([]string(nil), bundles...),
				Packages:    append([]string(nil), packages...),
				BareAgent:   noDefaultBundle,
			}
			if _, err := policy.Resolve(prof); err != nil {
				return emitContractError(jsoncontract.CodeInvalidArgument, "resolve profile packages", map[string]any{"profile": name, "error": err.Error()})
			}
			data, err := profileResolvedData(path, name, prof)
			if err != nil {
				return emitContractError(jsoncontract.CodeInvalidArgument, "resolve profile image recipe", map[string]any{"profile": name, "error": err.Error()})
			}
			if dryRun {
				data["dryRun"] = true
				emitContract(jsoncontract.OK(data))
				return nil
			}
			cfg.Profiles[name] = prof
			rendered, err := renderConfigCUE(cfg)
			if err != nil {
				return emitContractError(jsoncontract.CodeInternal, "render safeslop.cue", map[string]any{"error": err.Error()})
			}
			if _, err := policy.LoadBytes(rendered); err != nil {
				return emitContractError(jsoncontract.CodeSchemaViolation, "rendered safeslop.cue did not validate; not writing", map[string]any{"error": err.Error()})
			}
			if err := os.WriteFile(path, rendered, 0o644); err != nil {
				return emitContractError(jsoncontract.CodeIOError, "write safeslop.cue", map[string]any{"path": path, "error": err.Error()})
			}
			data["created"] = true
			emitContract(jsoncontract.OK(data))
			return nil
		},
	}
	c.Flags().StringVar(&name, "name", "", "profile name")
	c.Flags().StringVar(&agent, "agent", "", "agent: claude, claude-code, pi, fish, zsh, or shell")
	c.Flags().StringVar(&environment, "environment", "", "isolation environment: host or container")
	c.Flags().StringArrayVar(&bundles, "bundle", nil, "catalog bundle to include (repeatable)")
	c.Flags().StringArrayVar(&packages, "package", nil, "catalog package to include (repeatable)")
	c.Flags().StringVar(&workspace, "workspace", "", "workspace directory")
	c.Flags().StringVar(&network, "network", "deny", "network policy: deny or allow")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "resolve and print the profile without writing safeslop.cue")
	c.Flags().BoolVar(&noDefaultBundle, "no-default-bundle", false, "do not include the agent's default package bundle")
	c.Flags().StringVar(&output, "output", "", "output format: json")
	return c
}

func cmdProfileDelete() *cobra.Command {
	var output string
	c := &cobra.Command{
		Use:   "delete <name> [safeslop.cue] --output json",
		Short: "Delete one profile from safeslop.cue",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(_ *cobra.Command, args []string) error {
			if output != "json" {
				return fmt.Errorf("profile delete requires --output json")
			}
			path, err := findConfig(argAt(args, 1))
			if err != nil {
				return emitContractError(jsoncontract.CodeNotFound, "load safeslop.cue", map[string]any{"error": err.Error()})
			}
			cfg, err := policy.Load(path)
			if err != nil {
				return emitContractError(jsoncontract.CodeSchemaViolation, "load safeslop.cue", map[string]any{"path": path, "error": err.Error()})
			}
			name := args[0]
			if _, ok := cfg.Profiles[name]; !ok {
				return emitContractError(jsoncontract.CodeNotFound, fmt.Sprintf("no profile %q in safeslop.cue", name), map[string]any{"profile": name, "path": path})
			}
			delete(cfg.Profiles, name)
			rendered, err := renderConfigCUE(cfg)
			if err != nil {
				return emitContractError(jsoncontract.CodeInternal, "render safeslop.cue", map[string]any{"error": err.Error()})
			}
			if _, err := policy.LoadBytes(rendered); err != nil {
				return emitContractError(jsoncontract.CodeSchemaViolation, "rendered safeslop.cue did not validate; not writing", map[string]any{"error": err.Error()})
			}
			if err := os.WriteFile(path, rendered, 0o644); err != nil {
				return emitContractError(jsoncontract.CodeIOError, "write safeslop.cue", map[string]any{"path": path, "error": err.Error()})
			}
			emitContract(jsoncontract.OK(map[string]any{"removed": true, "profile": name, "path": path}))
			return nil
		},
	}
	c.Flags().StringVar(&output, "output", "", "output format: json")
	return c
}

func profileCreatePathForOutput() (string, error) {
	path, err := findConfig("")
	if err == nil {
		return path, nil
	}
	wd, werr := os.Getwd()
	if werr != nil {
		return "", werr
	}
	return filepath.Join(wd, "safeslop.cue"), nil
}

func loadOrNewConfigForCreate() (string, *policy.Config, error) {
	path, err := findConfig("")
	if err != nil {
		wd, werr := os.Getwd()
		if werr != nil {
			return "", nil, werr
		}
		path = filepath.Join(wd, "safeslop.cue")
		return path, &policy.Config{Version: 1, Profiles: map[string]policy.Profile{}}, nil
	}
	cfg, err := policy.Load(path)
	if err != nil {
		return "", nil, err
	}
	if cfg.Profiles == nil {
		cfg.Profiles = map[string]policy.Profile{}
	}
	if cfg.Version == 0 {
		cfg.Version = 1
	}
	return path, cfg, nil
}

func renderConfigCUE(cfg *policy.Config) ([]byte, error) {
	b, err := json.MarshalIndent(cfg, "", "\t")
	if err != nil {
		return nil, err
	}
	return append([]byte("package safeslop\n\nsafeslop: "), append(b, '\n')...), nil
}

type resolvedProfile struct {
	name        string
	profile     policy.Profile
	source      string
	policyPath  string
	policyHash  string
	policyBytes []byte
}

type profileResolutionError struct {
	code jsoncontract.ErrorCode
	err  error
}

func (e *profileResolutionError) Error() string { return e.err.Error() }
func (e *profileResolutionError) Unwrap() error { return e.err }

// resolveProfile gives a valid project config precedence over the signed binary's
// builtin registry. A present-but-invalid project config is never ignored: that
// would turn a typo or a damaged policy into an unintended authority change.
func resolveProfile(name, explicit string) (resolvedProfile, error) {
	path, findErr := findConfig(explicit)
	if findErr == nil {
		if _, err := os.Stat(path); err == nil {
			loaded, err := loadPolicyForLaunch(path)
			if err != nil {
				return resolvedProfile{}, &profileResolutionError{code: jsoncontract.CodeSchemaViolation, err: fmt.Errorf("load safeslop.cue: %w", err)}
			}
			if prof, ok := loaded.cfg.Profiles[name]; ok {
				return resolvedProfile{
					name: name, profile: prof, source: "project", policyPath: loaded.trustPath,
					policyHash: loaded.hash, policyBytes: append([]byte(nil), loaded.bytes...),
				}, nil
			}
		} else if !os.IsNotExist(err) {
			return resolvedProfile{}, fmt.Errorf("stat safeslop.cue: %w", err)
		}
	}
	if builtin, ok := policy.BuiltinProfileByName(name); ok {
		return resolvedProfile{name: name, profile: builtin.Profile, source: "builtin", policyPath: "builtin:" + name, policyHash: builtin.Hash}, nil
	}
	if findErr != nil {
		return resolvedProfile{}, findErr
	}
	return resolvedProfile{}, fmt.Errorf("no profile %q", name)
}

func profileResolvedData(path, name string, prof policy.Profile, contexts ...profileEvaluationInput) (map[string]any, error) {
	resolved, err := policy.Resolve(prof)
	if err != nil {
		return nil, err
	}
	recipe, err := container.ResolveRecipe(resolved.IdentitySet)
	if err != nil {
		return nil, err
	}
	evaluationInput := profileEvaluationInput{Source: profileEvaluationSourceUnsaved, Name: name, PolicyPath: path, Profile: prof}
	if len(contexts) > 0 {
		evaluationInput = contexts[0]
		evaluationInput.Name = name
		evaluationInput.PolicyPath = path
		evaluationInput.Profile = prof
	}
	return map[string]any{
		"path":       path,
		"name":       name,
		"profile":    prof,
		"evaluation": evaluateProfile(evaluationInput),
		"risk":       policy.RiskSummary(prof),
		"risk_axes":  policy.RiskAxes(prof),
		"resolved":   resolved,
		"recipeID":   recipe.RecipeID,
		"image":      recipe.AgentImage,
		"base":       recipe.SourceBaseImage,
		"baseImage":  recipe.BaseImage,
		"recipe":     recipe,
	}, nil
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

var stageHostExecResolver = hostexec.Default

func preflightProfileHostHelpers(prof policy.Profile, accounts *userconfig.Accounts) error {
	return stageHostExecResolver().Preflight(requiredProfileHostHelpers(prof, accounts)...)
}

func requiredProfileHostHelpers(prof policy.Profile, accounts *userconfig.Accounts) []hostexec.Spec {
	var specs []hostexec.Spec
	add := func(name, purpose string) {
		specs = append(specs, hostexec.CredentialSpec(name, purpose))
	}
	addOpRef := func(ref, purpose string) {
		if strings.HasPrefix(ref, "op://") {
			add("op", purpose)
		}
	}
	for _, ref := range prof.Secrets {
		addOpRef(ref, "op:// secrets")
	}
	c := prof.Credentials
	if c == nil {
		return specs
	}
	for _, r := range c.Pnpm {
		addOpRef(r.Token, "pnpm registry token")
	}
	if c.Aws != nil {
		add("aws", "AWS credentials")
	}
	if c.Gcp != nil {
		add("gcloud", "GCP credentials")
	}
	if k := c.Kube; k != nil {
		if k.Eks != nil {
			add("aws", "EKS kube credentials")
		}
		if k.Gke != nil {
			add("gke-gcloud-auth-plugin", "GKE kube token")
			add("gcloud", "GKE cluster describe")
		}
	}
	if g := c.Github; g != nil {
		if g.Mode == "pat" {
			addOpRef(g.Pat, "GitHub PAT")
		} else if len(g.Repos) == 0 {
			add("git", "GitHub origin inference")
		} else {
			for _, owner := range ownersFromRepos(g.Repos) {
				if link := accounts.Lookup("github.com", owner); link != nil && link.Github != nil {
					addOpRef(link.Github.PrivateKeyRef, "GitHub App key")
				}
			}
		}
	}
	if f := c.Forgejo; f != nil {
		add("ssh-keygen", "Forgejo deploy-key staging")
		add("ssh-keyscan", "Forgejo host-key pinning")
		if len(f.Repos) == 0 {
			add("git", "Forgejo origin inference")
		} else if host := forgejoPreflightHost(f.URL); host != "" {
			for _, owner := range ownersFromRepos(f.Repos) {
				if link := accounts.Lookup(host, owner); link != nil && link.Forgejo != nil {
					addOpRef(link.Forgejo.TokenRef, "Forgejo account token")
				}
			}
		}
	}
	return specs
}

func ownersFromRepos(repos []policy.RepoCred) []string {
	seen := map[string]bool{}
	var owners []string
	for _, r := range repos {
		owner, _, ok := strings.Cut(strings.TrimSpace(r.Repo), "/")
		if owner == "" || !ok || seen[owner] {
			continue
		}
		seen[owner] = true
		owners = append(owners, owner)
	}
	return owners
}

func forgejoPreflightHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// stageProfile resolves the profile's secrets and stages its credentials into stageDir. It
// returns secretEnv (sensitive KEY=VAL — the resolved secrets plus aws/gcp env creds, destined
// for the secrets.env channel / the process env) and pathEnv (non-secret NPM_CONFIG_USERCONFIG /
// KUBECONFIG / GIT_SSH_COMMAND host paths into stageDir, for the host process env). The
// caller owns the stageDir lifecycle (creation, the on-exit wipe, and creds.RevokeGithub if github
// key was staged).
func stageProfile(ctx context.Context, prof policy.Profile, stageDir string) (secretEnv, pathEnv []string, err error) {
	// GitHub App staging and Forgejo deploy-key staging both read the host account links. Load them
	// before preflight so declared repo owners can contribute their op:// account refs, but only when
	// forge creds are present so malformed accounts.cue never breaks unrelated profiles.
	var accounts *userconfig.Accounts
	if prof.Credentials != nil && (prof.Credentials.Github != nil || prof.Credentials.Forgejo != nil) {
		accPath, err := userconfig.DefaultAccountsPath()
		if err != nil {
			return nil, nil, err
		}
		accounts, err = userconfig.LoadAccounts(accPath)
		if err != nil {
			return nil, nil, err
		}
	}
	if err := preflightProfileHostHelpers(prof, accounts); err != nil {
		return nil, nil, err
	}
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
	// through the secret channel, so they ride secrets.env (container) and reach host children too.
	// No revoke: decay-first.
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
	// host, and via the bind mount (paths set by the compose file) for container.
	kubeEnv, err := creds.StageKube(ctx, prof.Credentials, stageDir)
	if err != nil {
		return nil, nil, err
	}
	// One forge per profile: github (App tokens / PAT over HTTPS) and forgejo (deploy keys) both
	// stage git credentials into the same dir; cross-forge unification is out of specs/0068 scope.
	if prof.Credentials != nil && prof.Credentials.Github != nil && prof.Credentials.Forgejo != nil {
		return nil, nil, fmt.Errorf("credentials: set either github or forgejo, not both")
	}
	githubEnv, err := creds.StageGithub(ctx, prof.Credentials, stageDir, accounts)
	if err != nil {
		return nil, nil, err
	}
	forgejoEnv, err := creds.StageForgejo(ctx, prof.Credentials, stageDir, accounts)
	if err != nil {
		return nil, nil, err
	}
	if prof.Credentials != nil && prof.Credentials.Pi != nil {
		if _, err := creds.StagePiOAuth(prof.Credentials.Pi, stageDir); err != nil {
			return nil, nil, err
		}
	}
	pathEnv = append(pathEnv, npmrcEnv...)
	pathEnv = append(pathEnv, kubeEnv...)
	pathEnv = append(pathEnv, githubEnv...)
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
	// the process dying with its defers unrun and leaving a live container
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
// supervisor has no inherited terminal, so it passes a PTY slave it owns: host
// runs the agent on that slave as its controlling terminal, and container
// binds the `compose run` process's stdio to it so the container's tty bridges
// back (specs/0051 D2).
type runIO struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// containerLaunch is the container-launch seam: the real container.Launch in
// production, swappable in tests to assert the detached supervisor's PTY is
// forwarded without standing up docker.
var containerLaunch = container.Launch

var applySessionGrantOverlay = func(ctx context.Context, sess engsession.Session, desired []container.SessionGrant) error {
	stageDir, err := sessionStageDir(sess)
	if err != nil {
		return err
	}
	return container.ApplySessionGrants(ctx, engineForSession(sess), filepath.Join(stageDir, "compose.yml"), stageDir, desired)
}

// observeSessionEgress is a seam so CLI tests never need a live container runtime.
var observeSessionEgress = func(ctx context.Context, sess engsession.Session) ([]container.EgressObservation, error) {
	stageDir, err := sessionStageDir(sess)
	if err != nil {
		return nil, err
	}
	return container.ReadDeniedEgressObservations(ctx, engineForSession(sess), filepath.Join(stageDir, "compose.yml"))
}

// credentialManager owns host-side renewal and bounded Forgejo cleanup for one run. It has no
// listener or sandbox-facing API: the agent sees only staged files.
type credentialManager struct {
	github       *creds.Lease
	forgejoTimer *time.Timer
}

func startCredentialManager(stagedAt time.Time, runName string, prof policy.Profile, stageDir string) (*credentialManager, error) {
	manager := &credentialManager{}
	var err error
	manager.github, err = creds.StartGithubCredentialLease(stagedAt, prof.Credentials, stageDir, func(snapshot creds.LeaseSnapshot) {
		persistLeaseSnapshot(runName, stageDir, snapshot)
	})
	if err != nil {
		return nil, err
	}
	if prof.Credentials != nil && prof.Credentials.Forgejo != nil && prof.Credentials.Forgejo.Ttl != "" {
		ttl, err := time.ParseDuration(prof.Credentials.Forgejo.Ttl)
		if err != nil || ttl <= 0 {
			manager.github.Stop()
			return nil, fmt.Errorf("forgejo credential lease: invalid ttl")
		}
		manager.forgejoTimer = time.AfterFunc(ttl, func() { creds.ExpireForgejo(context.Background(), stageDir) })
	}
	return manager, nil
}

// persistLeaseSnapshot writes only lease metadata for session-owned run names. The stage path is
// used solely as an internal lookup key and is never serialized.
func persistLeaseSnapshot(runName, stageDir string, snapshot creds.LeaseSnapshot) {
	if !strings.HasPrefix(runName, "session-") {
		return
	}
	id := strings.TrimPrefix(runName, "session-")
	if id == "" {
		return
	}
	store := sessionStore()
	_, _ = store.Update(id, func(sess engsession.Session) (engsession.Session, error) {
		partitions, _ := creds.GithubCredsPartitionCount(stageDir)
		lease := &engsession.CredentialLease{Provider: "github", State: string(snapshot.State), Reason: snapshot.Reason, CurrentExpiresAt: snapshot.CurrentExpiresAt.UTC(), GithubMinExpiresAt: snapshot.CurrentExpiresAt.UTC(), GithubPartitions: partitions}
		if snapshot.Horizon != nil {
			lease.Horizon = snapshot.Horizon.UTC()
		}
		sess.CredentialLease = lease
		return sess, nil
	})
}

func (m *credentialManager) Stop() {
	if m == nil {
		return
	}
	if m.forgejoTimer != nil {
		m.forgejoTimer.Stop()
	}
	m.github.Stop()
}

func runProfileCtx(ctx context.Context, name string, prof policy.Profile, argv []string, ws string, stdio ...runIO) (int, error) {
	var rio runIO
	if len(stdio) > 0 {
		rio = stdio[0]
	}
	stageDir, err := stageDirFor(name, ws)
	if err != nil {
		return 1, err
	}
	// A crashed wrapper can leave a stage directory for this exact run identity. Never reuse it:
	// retired tokens and stale canonical files must be removed before any new mint/stage action.
	if err := os.RemoveAll(stageDir); err != nil {
		return 1, fmt.Errorf("remove abandoned credential stage: %w", err)
	}
	defer os.RemoveAll(stageDir) // wipe staged secrets/.npmrc regardless of outcome

	// kube/ssh creds are staged as files in stageDir and delivered via the /safeslop/runtime bind
	// mount (container). GIT_SSH_COMMAND/KUBECONFIG are exported inside the boundary.
	if err := seedAgentDefaults(prof, ws); err != nil {
		return 1, err
	}
	stagedAt := time.Now()
	secretEnv, pathEnv, err := stageProfile(ctx, prof, stageDir)
	if err != nil {
		return 1, err
	}
	manager, err := startCredentialManager(stagedAt, name, prof, stageDir)
	if err != nil {
		return 1, err
	}
	// Best-effort revoke runs before the stageDir wipe (deferred after the top-of-func wipe, so
	// LIFO orders it first).
	if prof.Credentials != nil && prof.Credentials.Github != nil {
		defer creds.RevokeGithub(context.Background(), stageDir)
	}
	if prof.Credentials != nil && prof.Credentials.Forgejo != nil {
		defer creds.RevokeForgejo(context.Background(), stageDir)
	}
	// Register after teardown callbacks so LIFO stops renewal before any revoke or wipe.
	defer manager.Stop()

	// Detect (and warn about) any change the agent makes to git's executable surface —
	// a planted .git/hooks script or a .git/config hooksPath/fsmonitor/filter that the
	// host would run on its next git command in this repo (specs/0025 S3). Best-effort,
	// never blocks the agent's legitimate git use.
	gitBefore, _ := gitguard.Snapshot(ws)
	defer warnGitExecSurface(ws, gitBefore)

	switch prof.Environment {
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
		// Resolve the profile's catalog package set (specs/0058): its identity set selects
		// the agent image (which tools get baked), and its runtime egress is UNIONed into
		// the squid allowlist (union-only; never relaxes default-deny).
		resolved, err := policy.Resolve(prof)
		if err != nil {
			return 1, fmt.Errorf("resolve packages for profile %q: %w", name, err)
		}
		// egress = the agent's built-in providers + the resolved packages' runtimeEgress +
		// the profile's egress: list (specs/0046 + 0058 N2).
		egress := append(append([]string{}, policy.AgentEgress(prof.Agent)...), resolved.RuntimeEgress...)
		// Union the hosts the staged git credentials need to reach (GitHub HTTPS + CDN, specs/0069 T7).
		egress = append(egress, policy.CredsEgress(&prof)...)
		egress = append(egress, prof.Egress...)
		// A detached supervisor passes a PTY slave it owns (rio set); forward it so the
		// container's tty bridges to the supervisor's PTY for attach. Coupled (rio zero)
		// leaves stdio nil and container.Launch runs the user's terminal (specs/0051).
		return containerLaunch(ctx, engexec.LaunchSpec{Argv: argv, Stdin: rio.Stdin, Stdout: rio.Stdout, Stderr: rio.Stderr}, ws, prof.Network, egress, secretEnv, stageDir, resolved.IdentitySet, prof.Projection, sessionGrantViewsFromRunName(name)...)
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

func emitJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
