package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	osexec "os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/freakhill/safeslop/internal/engine/container"
	"github.com/freakhill/safeslop/internal/engine/creds"
	"github.com/freakhill/safeslop/internal/engine/policy"
	engsession "github.com/freakhill/safeslop/internal/engine/session"
	"github.com/freakhill/safeslop/internal/engine/trust"
	workspaceboundary "github.com/freakhill/safeslop/internal/engine/workspace"
	"github.com/freakhill/safeslop/internal/jsoncontract"
)

var errOutputEmitted = errors.New("machine-readable error already emitted")

// sessionStageDir reconstructs the deterministic host stage dir a session's run staged under,
// so teardown paths (credential revoke, boundary reap) address the exact same tree — and the
// exact safeslop.session label — the launch path used (mirrors runProfileCtx's
// stageDirFor("session-"+id, ws); specs/0074).
func cmdSession() *cobra.Command {
	return cmdSessionWithDeps(defaultDependencies())
}

func cmdSessionWithDeps(d *dependencies) *cobra.Command {
	c := &cobra.Command{Use: "session", Short: "Manage Emacs-visible safeslop sessions"}
	c.AddCommand(cmdSessionCreateWithDeps(d), cmdSessionRunWithDeps(d), cmdSessionStatusWithDeps(d), cmdSessionStopWithDeps(d), cmdSessionListWithDeps(d), cmdSessionRemoveWithDeps(d), cmdSessionRenameWithDeps(d), cmdSessionPruneWithDeps(d), cmdSessionSuperviseWithDeps(d), cmdSessionAttachWithDeps(d), cmdSessionEgressWithDeps(d))
	return c
}

// cmdSessionEgress exposes the operator-only, session-scoped controls for the
// container deny proxy overlay. It deliberately has no path from agent traffic to
// a grant: observations are informational and only `grant` changes authority.
func cmdSessionEgress() *cobra.Command {
	return cmdSessionEgressWithDeps(defaultDependencies())
}

func cmdSessionEgressWithDeps(d *dependencies) *cobra.Command {
	c := &cobra.Command{
		Use:   "egress",
		Short: "Inspect and manage session-scoped container egress grants",
	}
	c.AddCommand(cmdSessionEgressObservationsWithDeps(d), cmdSessionEgressGrantsWithDeps(d), cmdSessionEgressGrantWithDeps(d), cmdSessionEgressRevokeWithDeps(d), cmdSessionEgressDismissWithDeps(d))
	return c
}

func cmdSessionEgressObservations() *cobra.Command {
	return cmdSessionEgressObservationsWithDeps(defaultDependencies())
}

func cmdSessionEgressObservationsWithDeps(d *dependencies) *cobra.Command {
	var id, output string
	c := &cobra.Command{
		Use:   "observations --session-id <id> --output json",
		Short: "List proxy-denied egress observations for a session",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if output != "json" {
				return fmt.Errorf("session egress observations requires --output json")
			}
			sess, err := egressSessionWithDeps(d, d.store, id)
			if err != nil {
				return err
			}
			observations, err := d.observeEgress(context.Background(), sess)
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
	return cmdSessionEgressGrantsWithDeps(defaultDependencies())
}

func cmdSessionEgressGrantsWithDeps(d *dependencies) *cobra.Command {
	var id, output string
	c := &cobra.Command{
		Use:   "grants --session-id <id> --output json",
		Short: "List active session egress grants",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if output != "json" {
				return fmt.Errorf("session egress grants requires --output json")
			}
			sess, err := egressSessionWithDeps(d, d.store, id)
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
	return cmdSessionEgressGrantWithDeps(defaultDependencies())
}

func cmdSessionEgressGrantWithDeps(d *dependencies) *cobra.Command {
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
			sess, _, err := grantSessionEgressWithDeps(d, context.Background(), d.store, id, host, port, d.now())
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
	return cmdSessionEgressDismissWithDeps(defaultDependencies())
}

func cmdSessionEgressDismissWithDeps(d *dependencies) *cobra.Command {
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
			sess, _, err := dismissSessionEgressWithDeps(d, d.store, id, host, port, d.now())
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
	return cmdSessionEgressRevokeWithDeps(defaultDependencies())
}

func cmdSessionEgressRevokeWithDeps(d *dependencies) *cobra.Command {
	var id, grantID, output string
	c := &cobra.Command{
		Use:   "revoke --session-id <id> --grant-id <id> --output json",
		Short: "Revoke one session egress grant",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if output != "json" {
				return fmt.Errorf("session egress revoke requires --output json")
			}
			sess, err := revokeSessionEgressWithDeps(d, context.Background(), d.store, id, grantID, d.now())
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
	return egressSessionWithDeps(defaultDependencies(), store, id)
}

func egressSessionWithDeps(d *dependencies, store engsession.Store, id string) (engsession.Session, error) {
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
	if sess.Status == engsession.StatusRunning {
		sess, err = store.WithLocked(id, func(tx *engsession.RecordTx) error {
			if !engsession.CanGrant(tx.Session()) {
				return engsession.ErrSessionNotGrantable
			}
			return recoverRunningSessionEgressWithDeps(d, context.Background(), tx)
		})
		if err != nil {
			return engsession.Session{}, emitContractError(jsoncontract.CodeIOError, "reconcile session egress authority", map[string]any{"session_id": id})
		}
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
	return cmdSessionCreateWithDeps(defaultDependencies())
}

func cmdSessionCreateWithDeps(d *dependencies) *cobra.Command {
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
			store := d.store
			if profile != "" {
				if agent != "" || workspace != "" || environment != "" || network != "" {
					return emitContractError(jsoncontract.CodeInvalidArgument, "--profile cannot be combined with --agent, --environment, --workspace, or --network", nil)
				}
				sess, err := createSessionFromProfileWithDeps(d, store, profile)
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
				emitContract(jsoncontract.OK(sessionDataWithDeps(d, sess)))
				return nil
			}
			canonicalAgent := policy.NormalizeAgent(agent)
			if !policy.IsLaunchableAgent(canonicalAgent) {
				return emitContractError(jsoncontract.CodeAgentUnsupported, fmt.Sprintf("unsupported agent %q", agent), map[string]any{"agent": agent})
			}
			if workspace == "" {
				return emitContractError(jsoncontract.CodeInvalidArgument, "--workspace is required", nil)
			}
			invocationDir, err := os.Getwd()
			if err != nil {
				return emitContractError(jsoncontract.CodeIOError, "resolve invocation directory", nil)
			}
			rawWorkspace := workspace
			workspace, err = workspaceboundary.Resolve(rawWorkspace, "", invocationDir)
			if err != nil {
				return emitContractError(jsoncontract.CodeInvalidArgument, "--workspace must name a valid existing directory", map[string]any{"workspace": rawWorkspace})
			}
			switch environment {
			case "container":
				if err := validateWorkspaceStageRoot(workspace); err != nil {
					return emitContractError(jsoncontract.CodeInvalidArgument, "workspace overlaps the private runtime stage", nil)
				}
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
			sess, err := store.Create(canonicalAgent, environment, workspace, d.now())
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
			emitContract(jsoncontract.OK(sessionDataWithDeps(d, sess)))
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
	return cmdSessionRenameWithDeps(defaultDependencies())
}

func cmdSessionRenameWithDeps(d *dependencies) *cobra.Command {
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
			sess, err := d.store.Rename(id, name, d.now())
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
			emitContract(jsoncontract.OK(sessionDataWithDeps(d, sess)))
			return nil
		},
	}
	c.Flags().StringVar(&id, "session-id", "", "session id to rename")
	c.Flags().StringVar(&name, "name", "", "new display name (empty clears it)")
	c.Flags().StringVar(&output, "output", "", "output format: json")
	return c
}

func createSessionFromProfile(store engsession.Store, profile string) (engsession.Session, error) {
	return createSessionFromProfileWithDeps(defaultDependencies(), store, profile)
}

func createSessionFromProfileWithDeps(d *dependencies, store engsession.Store, profile string) (engsession.Session, error) {
	path, err := findConfig("")
	if err != nil {
		return createBuiltinSessionWithDeps(d, store, profile)
	}
	loaded, err := loadPolicyForLaunch(path)
	if err != nil {
		return engsession.Session{}, emitContractError(jsoncontract.CodeSchemaViolation, "load safeslop.cue", map[string]any{"path": path, "error": err.Error()})
	}
	prof, ok := loaded.cfg.Profiles[profile]
	if !ok {
		if _, builtin := policy.BuiltinProfileByName(profile); builtin {
			return createBuiltinSessionWithDeps(d, store, profile)
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
	invocationDir, err := os.Getwd()
	if err != nil {
		return engsession.Session{}, emitContractError(jsoncontract.CodeIOError, "resolve invocation directory", nil)
	}
	workspace, err := workspaceboundary.Resolve(prof.Workspace, loaded.trustPath, invocationDir)
	if err != nil {
		return engsession.Session{}, emitContractError(jsoncontract.CodeInvalidArgument, "profile workspace must name a valid existing directory", map[string]any{"profile": profile})
	}
	prof.Workspace = workspace
	if prof.Environment == "container" {
		if err := validateWorkspaceStageRoot(workspace); err != nil {
			return engsession.Session{}, emitContractError(jsoncontract.CodeInvalidArgument, "profile workspace overlaps the private runtime stage", map[string]any{"profile": profile})
		}
	}
	agent := policy.NormalizeAgent(prof.Agent)
	if !policy.IsLaunchableAgent(agent) && agent != "shell" {
		return engsession.Session{}, emitContractError(jsoncontract.CodeAgentUnsupported, fmt.Sprintf("unsupported agent %q", prof.Agent), map[string]any{"agent": prof.Agent, "profile": profile})
	}
	sess, err := store.Create(agent, prof.Environment, workspace, d.now())
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
	return createBuiltinSessionWithDeps(defaultDependencies(), store, name)
}

func createBuiltinSessionWithDeps(d *dependencies, store engsession.Store, name string) (engsession.Session, error) {
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
	invocationDir, err := os.Getwd()
	if err != nil {
		return engsession.Session{}, emitContractError(jsoncontract.CodeIOError, "get workspace", nil)
	}
	workspace, err := workspaceboundary.Resolve("", "", invocationDir)
	if err != nil {
		return engsession.Session{}, emitContractError(jsoncontract.CodeInvalidArgument, "workspace must name a valid existing directory", nil)
	}
	prof.Workspace = workspace
	if prof.Environment == "container" {
		if err := validateWorkspaceStageRoot(workspace); err != nil {
			return engsession.Session{}, emitContractError(jsoncontract.CodeInvalidArgument, "workspace overlaps the private runtime stage", map[string]any{"profile": name})
		}
	}
	sess, err := store.Create(policy.NormalizeAgent(prof.Agent), prof.Environment, workspace, d.now())
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
	return cmdSessionListWithDeps(defaultDependencies())
}

func cmdSessionListWithDeps(d *dependencies) *cobra.Command {
	var output string
	c := &cobra.Command{
		Use:   "list --output json",
		Short: "List safeslop sessions",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if output != "json" {
				return fmt.Errorf("session list requires --output json")
			}
			sessions, err := d.store.ListReconciled(d.now(), d.processAlive, func(sess engsession.Session) error { return sessionReapBoundaryWithDeps(d, sess) }, d.wipeStageDir)
			if err != nil {
				return emitContractError(jsoncontract.CodeIOError, "list sessions", map[string]any{"error": err.Error()})
			}
			items := make([]map[string]any, 0, len(sessions))
			for _, sess := range sessions {
				items = append(items, sessionDataWithDeps(d, sess))
			}
			emitContract(jsoncontract.OK(map[string]any{"sessions": items}))
			return nil
		},
	}
	c.Flags().StringVar(&output, "output", "", "output format: json")
	return c
}

func cmdSessionStatus() *cobra.Command {
	return cmdSessionStatusWithDeps(defaultDependencies())
}

func cmdSessionStatusWithDeps(d *dependencies) *cobra.Command {
	var id, output string
	c := &cobra.Command{
		Use:   "status --session-id <id> --output <json|jsonl>",
		Short: "Report safeslop session status",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if output != "json" && output != "jsonl" {
				return fmt.Errorf("session status requires --output json or jsonl")
			}
			sess, err := d.store.GetReconciled(id, d.now(), d.processAlive, func(sess engsession.Session) error { return sessionReapBoundaryWithDeps(d, sess) }, d.wipeStageDir)
			if err != nil {
				if errors.Is(err, engsession.ErrNotFound) {
					return emitContractError(jsoncontract.CodeSessionNotFound, "session not found", map[string]any{"session_id": id})
				}
				return emitContractError(jsoncontract.CodeIOError, "load session", map[string]any{"error": err.Error()})
			}
			data := sessionDataWithDeps(d, sess)
			// Surface the GitHub App-token TTL ceiling so the operator can see how long a
			// session's ephemeral HTTPS access has left (specs/0069 T8). Additive + best
			// effort: unlinked sessions have no manifest and simply omit the block.
			if stageDir, derr := sessionStageDir(sess); derr == nil {
				if exp, ok, _ := creds.GithubCredsExpiry(stageDir); ok {
					block := map[string]any{"min_expires_at": exp.UTC().Format(time.RFC3339)}
					if remaining := exp.Sub(d.now()); remaining > 0 {
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
	return cmdSessionStopWithDeps(defaultDependencies())
}

func cmdSessionStopWithDeps(d *dependencies) *cobra.Command {
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
			store := d.store
			// Reconcile immediately before signalling: if the recorded wrapper/supervisor
			// died, or its PID was reused, persist it stopped and clean local stage/socket
			// state instead of sending SIGTERM to an unrelated process/group.
			if _, err := store.GetReconciled(id, d.now(), d.processAlive, func(sess engsession.Session) error { return sessionReapBoundaryWithDeps(d, sess) }, d.wipeStageDir); err != nil {
				if errors.Is(err, engsession.ErrNotFound) {
					return emitContractError(jsoncontract.CodeSessionNotFound, "session not found", map[string]any{"session_id": id})
				}
				return emitContractError(jsoncontract.CodeIOError, "reconcile session", map[string]any{"error": err.Error()})
			}
			signaledTarget := 0
			var signaledIdentity engsession.Session
			verifyProcess := func(candidate engsession.Session) bool {
				alive := d.processAlive(candidate)
				if alive {
					signaledIdentity = candidate
				}
				return alive
			}
			signalProcess := func(target int) error {
				if err := d.killProcess(target); err != nil {
					return err
				}
				signaledTarget = target
				return nil
			}
			sess, err := store.Stop(id, revoke, d.now(), d.revokeCredentials, signalProcess, verifyProcess, func(sess engsession.Session) error { return sessionReapBoundaryWithDeps(d, sess) }, d.wipeStageDir)
			if err != nil {
				if errors.Is(err, engsession.ErrNotFound) {
					return emitContractError(jsoncontract.CodeSessionNotFound, "session not found", map[string]any{"session_id": id})
				}
				return emitContractError(jsoncontract.CodeCredentialRevokeFailed, "stop session", map[string]any{"error": err.Error()})
			}
			// Wait only after Store.Stop releases its record lock. A detached
			// supervisor persists Finish while exiting; waiting under the lock would
			// deadlock until the grace timeout and force SIGKILL.
			if signaledTarget < 0 {
				if err := d.waitProcess(signaledTarget, signaledIdentity); err != nil {
					return emitContractError(jsoncontract.CodeIOError, "wait for session process", map[string]any{"error": err.Error()})
				}
				// The supervisor may have committed its terminal exit details while
				// we waited. Emit that fresh record rather than the pre-wait snapshot.
				current, err := store.Get(id)
				if err != nil {
					return emitContractError(jsoncontract.CodeIOError, "reload session after process exit", map[string]any{"error": err.Error()})
				}
				sess = current
			}
			// Store.Stop intentionally does not run reap callbacks for an already-stopped
			// record; still wipe the deterministic local stage dir here so repeated/operator
			// stop can clear a SIGKILL orphan without needing credential revocation.
			if err := d.wipeStageDir(sess); err != nil {
				return emitContractError(jsoncontract.CodeIOError, "wipe session stage dir", map[string]any{"error": err.Error()})
			}
			emitContract(jsoncontract.OK(sessionDataWithDeps(d, sess)))
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
	return cmdSessionRemoveWithDeps(defaultDependencies())
}

func cmdSessionRemoveWithDeps(d *dependencies) *cobra.Command {
	var id, output string
	c := &cobra.Command{
		Use:   "rm --session-id <id> --output json",
		Short: "Remove a stopped safeslop session record",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if output != "json" {
				return fmt.Errorf("session rm requires --output json")
			}
			sess, err := d.store.Remove(id, d.revokeCredentials, func(sess engsession.Session) error { return sessionReapBoundaryWithDeps(d, sess) }, d.wipeStageDir)
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
	return cmdSessionPruneWithDeps(defaultDependencies())
}

func cmdSessionPruneWithDeps(d *dependencies) *cobra.Command {
	var output string
	c := &cobra.Command{
		Use:   "prune --output json",
		Short: "Remove all stopped safeslop session records",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if output != "json" {
				return fmt.Errorf("session prune requires --output json")
			}
			store := d.store
			// Reconcile first so a crashed session (still marked running but whose
			// process is gone) is persisted as stopped and swept in this same pass.
			if _, err := store.ListReconciled(d.now(), d.processAlive, func(sess engsession.Session) error { return sessionReapBoundaryWithDeps(d, sess) }, d.wipeStageDir); err != nil {
				return emitContractError(jsoncontract.CodeIOError, "list sessions", map[string]any{"error": err.Error()})
			}
			removed, err := store.PruneStopped(d.revokeCredentials, func(sess engsession.Session) error { return sessionReapBoundaryWithDeps(d, sess) }, d.wipeStageDir)
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
	return cmdSessionRunWithDeps(defaultDependencies())
}

func cmdSessionRunWithDeps(d *dependencies) *cobra.Command {
	var id string
	var detach bool
	c := &cobra.Command{
		Use:   "run --session-id <id>",
		Short: "Run a safeslop session's agent",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			store := d.store
			sess, err := store.Get(id)
			if err != nil {
				return err
			}
			switch sess.Status {
			case engsession.StatusRunning:
				return emitSessionLaunchStateError(engsession.ErrSessionRunning, id)
			case engsession.StatusStopped:
				return emitSessionLaunchStateError(engsession.ErrSessionStopped, id)
			}
			// Re-verify the profile's policy is still host-approved before launch (specs/0072
			// F1): session run rebuilds the profile from the record, so a create-time approval
			// that was later revoked or edited must not still launch. Fail-closed here in the
			// user's process; Supervise re-checks again at the detached supervisor's own start.
			if err := verifySessionTrust(sess); err != nil {
				return err
			}
			sess, selectedEngine, err := prepareSessionBackendWithDeps(d, store, sess)
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
			if !detach && !d.hasInteractivePTY() {
				emitContract(jsoncontract.PTYUnavailable())
				return errOutputEmitted
			}
			if err := requireHostLaunchConsentWithDeps(d, sessionConsentName(sess), prof, os.Stdin, os.Stderr); err != nil {
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
				return runDetachWithDeps(d, store, id)
			}
			stageKey, err := sessionStageKey(sess)
			if err != nil {
				return err
			}
			if _, err := store.MarkRunning(id, os.Getpid(), d.now()); err != nil {
				return emitSessionLaunchStateError(err, id)
			}
			code, runErr := runProfileWithStageKeyAndEngineAndDeps(d, selectedEngine, "session-"+id, prof, argv, sess.Workspace, stageKey)
			if err := finishSessionRun(store, id, code, runErr, d.now()); err != nil {
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

// launchedSupervisor captures the process identity while the newly-started
// child handle is still owned. A bare PID returned after Release would not be
// sufficient authority to signal a readiness-timeout process safely.
type launchedSupervisor struct {
	PID          int
	ProcessToken string
}

// defaultLaunchSupervisor re-execs this binary as a detached per-session supervisor (the
// hidden `session supervise`) and returns its verified process identity. This is
// the canonical Go daemonization: a new session via Setsid (no controlling tty), which
// also makes the child its own process-group leader so a later `session stop` can
// signal the whole tree via kill(-pgid) (specs/0051 D4), plus fully detached stdio.
// Overridable in tests so no real setsid or second binary is needed (the specs/0051
// D1 test seam).
func defaultLaunchSupervisor(id string) (launchedSupervisor, error) {
	exe, err := os.Executable()
	if err != nil {
		return launchedSupervisor{}, err
	}
	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return launchedSupervisor{}, err
	}
	defer null.Close()
	cmd := osexec.Command(exe, "session", "supervise", "--session-id", id)
	// Setsid only: it makes the child a new session AND process-group leader
	// (pgid == pid). Adding Setpgid on top is invalid — a session leader cannot
	// setpgid (EPERM), which fails the fork/exec ("operation not permitted").
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = null, null, null
	if err := cmd.Start(); err != nil {
		return launchedSupervisor{}, err
	}
	pid := cmd.Process.Pid
	processToken, ok := engsession.ProcessStartToken(pid)
	if !ok {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return launchedSupervisor{}, errors.New("capture session supervisor process identity")
	}
	_ = cmd.Process.Release() // detach: the daemon is never Wait()ed on
	return launchedSupervisor{PID: pid, ProcessToken: processToken}, nil
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
	return runDetachWithDeps(defaultDependencies(), store, id)
}

func emitSessionLaunchStateError(err error, id string) error {
	switch {
	case errors.Is(err, engsession.ErrSessionRunning):
		return emitContractError(jsoncontract.CodeSessionAlreadyRunning, "session is already running", map[string]any{"session_id": id})
	case errors.Is(err, engsession.ErrSessionStopped):
		return emitContractError(jsoncontract.CodeSessionStopped, "session is stopped; create a new session to run again", map[string]any{"session_id": id})
	default:
		return err
	}
}

func runDetachWithDeps(d *dependencies, store engsession.Store, id string) error {
	if sess, err := store.Get(id); err == nil {
		if _, err := recordSessionBackendWithDeps(d, store, sess); err != nil {
			return err
		}
	}
	claimPID := os.Getpid()
	if _, err := store.MarkRunning(id, claimPID, d.now()); err != nil {
		return emitSessionLaunchStateError(err, id)
	}
	launched, err := d.launchSupervisor(id)
	if err != nil {
		_, releaseErr := store.ReleaseRunningClaim(id, claimPID, d.now())
		return emitContractError(jsoncontract.CodeIOError, "launch session supervisor", map[string]any{"error": errors.Join(err, releaseErr).Error()})
	}
	pid := launched.PID
	sockPath := store.SocketPath(id)
	if !waitForSupervisorReady(store, id, launched, sockPath, d.detachReadyTimeout, d.processAlive) {
		identity := engsession.Session{PID: pid, ProcessToken: launched.ProcessToken, Detached: true}
		if d.processAlive(identity) {
			target := -pid // defaultLaunchSupervisor made the child its own group leader
			if err := d.killProcess(target); err != nil {
				return emitContractError(jsoncontract.CodeIOError, "stop unready session supervisor", map[string]any{"error": err.Error()})
			}
			if err := d.waitProcess(target, identity); err != nil {
				return emitContractError(jsoncontract.CodeIOError, "wait for unready session supervisor", map[string]any{"error": err.Error()})
			}
		}
		if err := removeSocketWithDeps(d, sockPath); err != nil {
			return emitContractError(jsoncontract.CodeIOError, "remove unready session socket", map[string]any{"error": err.Error()})
		}
		// The supervisor may have committed running after publishing the socket
		// but before failing its permission/readiness path. Never leave that
		// half-published identity as a live session.
		if _, err := store.Finish(id, 1, "session supervisor did not become ready", d.now()); err != nil {
			return emitContractError(jsoncontract.CodeIOError, "record unready session supervisor", map[string]any{"error": err.Error()})
		}
		return emitContractError(jsoncontract.CodeIOError, "session supervisor did not become ready", map[string]any{"session_id": id})
	}
	sess, err := store.Get(id)
	if err != nil {
		return err
	}
	emitContract(jsoncontract.OK(sessionDataWithDeps(d, sess)))
	return nil
}

// waitForSupervisorReady requires both sides of the detached readiness
// handshake: an owner-only Unix socket and the supervisor's own live running
// record with the exact process-start token captured before Process.Release.
// Socket existence alone races Listen against chmod/MarkRunningDetached.
func waitForSupervisorReady(store engsession.Store, id string, launched launchedSupervisor, path string, timeout time.Duration, processAlive func(engsession.Session) bool) bool {
	deadline := time.Now().Add(timeout)
	for {
		sess, getErr := store.Get(id)
		info, statErr := os.Lstat(path)
		if getErr == nil && statErr == nil && sess.Status == engsession.StatusRunning && sess.Detached && sess.PID == launched.PID && sess.ProcessToken != "" && sess.ProcessToken == launched.ProcessToken && info.Mode()&os.ModeSocket != 0 && info.Mode().Perm() == 0o600 && processAlive != nil && processAlive(sess) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
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
	return cmdSessionSuperviseWithDeps(defaultDependencies())
}

func cmdSessionSuperviseWithDeps(d *dependencies) *cobra.Command {
	var id string
	c := &cobra.Command{
		Use:    "supervise --session-id <id>",
		Short:  "(internal) run a detached session supervisor",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
			defer stop()
			code, err := superviseWithDeps(d, ctx, d.store, id, d.now)
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
	return cmdSessionAttachWithDeps(defaultDependencies())
}

func cmdSessionAttachWithDeps(d *dependencies) *cobra.Command {
	var id string
	c := &cobra.Command{
		Use:   "attach --session-id <id>",
		Short: "Attach to a detached safeslop session",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if !d.hasInteractivePTY() {
				emitContract(jsoncontract.PTYUnavailable())
				return errOutputEmitted
			}
			code, err := attachSession(d.store, id, os.Stdin, os.Stdout, attachResizeChannel())
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
func safeAttachSocket(path string) bool {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0o600 {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(stat.Uid) == os.Getuid()
}

func attachSession(store engsession.Store, id string, in io.Reader, out io.Writer, resize <-chan [2]uint16) (int, error) {
	var conn net.Conn
	var err error
	for _, path := range store.SocketPaths(id) {
		if !safeAttachSocket(path) {
			continue
		}
		conn, err = net.Dial("unix", path)
		if err == nil {
			break
		}
	}
	if conn == nil {
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
func defaultSessionHasInteractivePTY() bool {
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

var ErrEgressAuthorityUncertain = errors.New("session network authority is uncertain")

func sessionGeneration(sess engsession.Session) (container.EgressGeneration, error) {
	generation, _, err := container.BuildEgressGeneration(sessionEgressViews(sess), sess.GrantRevision)
	return generation, err
}

func withAppliedGeneration(sess engsession.Session, generation container.EgressGeneration) engsession.Session {
	state := sess.EgressRuntimeState()
	state.AppliedRevision, state.AppliedHash, state.Transition = generation.Revision, generation.Hash, nil
	sess.SetEgressRuntimeState(state)
	return sess
}

func withTransition(sess engsession.Session, direction string, candidate engsession.Session, generation container.EgressGeneration) engsession.Session {
	state := sess.EgressRuntimeState()
	state.Transition = &engsession.EgressTransition{
		Direction: direction, CandidateRevision: generation.Revision, CandidateHash: generation.Hash,
		CandidateGrants: append([]engsession.EgressGrant(nil), candidate.EgressGrants...),
	}
	sess.SetEgressRuntimeState(state)
	return sess
}

func copySessionAuthority(target, source engsession.Session) engsession.Session {
	target.EgressGrants = append([]engsession.EgressGrant(nil), source.EgressGrants...)
	target.GrantRevision = source.GrantRevision
	return target
}

func withEgressUncertaintyFailure(d *dependencies, sess engsession.Session) engsession.Session {
	sess.UpdatedAt = d.now().UTC()
	sess.SetFailure(engsession.Failure{
		Version: 1, Phase: "network", Code: "network_authority_uncertain", Required: true,
		Summary: "The session network boundary could not be proven.",
		Action:  "Stop the session, then create a fresh run before granting network access.",
	})
	return sess
}

func hasEgressUncertaintyFailure(sess engsession.Session) bool {
	return sess.LastFailure != nil && sess.LastFailure.Phase == "network" && sess.LastFailure.Code == "network_authority_uncertain"
}

func stopForEgressUncertainty(d *dependencies, sess engsession.Session) engsession.Session {
	now := d.now().UTC()
	sess.Status = engsession.StatusStopped
	sess.PID, sess.ProcessToken = 0, ""
	sess.StoppedAt, sess.UpdatedAt = now, now
	sess.SetEgressRuntimeState(engsession.EgressRuntimeState{})
	return withEgressUncertaintyFailure(d, sess)
}

type egressRecordTransaction interface {
	Session() engsession.Session
	Commit(engsession.Session) error
}

const egressFailureCommitAttempts = 2

func commitEgressFailureState(tx egressRecordTransaction, candidate engsession.Session) error {
	var err error
	for range egressFailureCommitAttempts {
		if err = tx.Commit(candidate); err == nil {
			return nil
		}
	}
	return err
}

// failClosedEgress tears down before narrowing durable authority. restore is
// used only after teardown is proven; otherwise the current durable upper bound
// remains so an unreachable runtime can never be broader than its record.
func failClosedEgress(tx egressRecordTransaction, restore *engsession.Session) error {
	return failClosedEgressWithDeps(defaultDependencies(), tx, restore)
}

func failClosedEgressWithDeps(d *dependencies, tx egressRecordTransaction, restore *engsession.Session) error {
	current := tx.Session()
	if err := d.teardownEgress(current); err != nil {
		// Do not claim stopped when teardown itself is unproven. The fixed marker
		// is best-effort under simultaneous runtime and record-store failure; a
		// bounded retry covers transient and directory-commit uncertainty.
		if err := commitEgressFailureState(tx, withEgressUncertaintyFailure(d, tx.Session())); err != nil {
			return ErrEgressAuthorityUncertain
		}
		return ErrEgressAuthorityUncertain
	}
	stopped := tx.Session()
	if restore != nil {
		stopped = copySessionAuthority(stopped, *restore)
	}
	stopped = stopForEgressUncertainty(d, stopped)
	if err := commitEgressFailureState(tx, stopped); err != nil {
		return ErrEgressAuthorityUncertain
	}
	return ErrEgressAuthorityUncertain
}

func commitRecoveredGeneration(d *dependencies, tx *engsession.RecordTx, sess engsession.Session, generation container.EgressGeneration) error {
	if err := tx.Commit(withAppliedGeneration(sess, generation)); err != nil {
		return failClosedEgressWithDeps(d, tx, nil)
	}
	return nil
}

// recoverRunningSessionEgress resolves every persisted transition without
// guessing. Widen re-applies the already-durable upper bound. Narrow commits an
// exactly acknowledged candidate, cancels an untouched transition, or restores
// the still-durable old bound when runtime identity is unknown.
func recoverRunningSessionEgress(ctx context.Context, tx *engsession.RecordTx) error {
	return recoverRunningSessionEgressWithDeps(defaultDependencies(), ctx, tx)
}

func recoverRunningSessionEgressWithDeps(d *dependencies, ctx context.Context, tx *engsession.RecordTx) error {
	sess := tx.Session()
	if sess.Status != engsession.StatusRunning {
		return nil
	}
	if hasEgressUncertaintyFailure(sess) {
		return failClosedEgressWithDeps(d, tx, nil)
	}
	state := sess.EgressRuntimeState()
	durableGeneration, err := sessionGeneration(sess)
	if err != nil {
		return failClosedEgressWithDeps(d, tx, nil)
	}
	if state.Transition == nil {
		if state.AppliedRevision == durableGeneration.Revision && state.AppliedHash == durableGeneration.Hash {
			if current, inspectErr := d.inspectEgress(ctx, sess); inspectErr == nil && current == durableGeneration {
				return nil
			}
		}
		if err := d.applyEgressOverlay(ctx, sess, sessionEgressViews(sess)); err != nil {
			return failClosedEgressWithDeps(d, tx, nil)
		}
		return commitRecoveredGeneration(d, tx, tx.Session(), durableGeneration)
	}

	transition := state.Transition
	switch transition.Direction {
	case engsession.EgressDirectionWiden:
		if transition.CandidateRevision != durableGeneration.Revision || transition.CandidateHash != durableGeneration.Hash {
			return failClosedEgressWithDeps(d, tx, nil)
		}
		if current, inspectErr := d.inspectEgress(ctx, sess); inspectErr != nil || current != durableGeneration {
			if err := d.applyEgressOverlay(ctx, sess, sessionEgressViews(sess)); err != nil {
				return failClosedEgressWithDeps(d, tx, nil)
			}
		}
		return commitRecoveredGeneration(d, tx, tx.Session(), durableGeneration)
	case engsession.EgressDirectionNarrow:
		candidate := sess
		candidate.EgressGrants = append([]engsession.EgressGrant(nil), transition.CandidateGrants...)
		candidate.GrantRevision = transition.CandidateRevision
		candidateGeneration, err := sessionGeneration(candidate)
		if err != nil || candidateGeneration.Hash != transition.CandidateHash {
			return failClosedEgressWithDeps(d, tx, nil)
		}
		current, inspectErr := d.inspectEgress(ctx, sess)
		if inspectErr == nil {
			switch current {
			case candidateGeneration:
				final := copySessionAuthority(tx.Session(), candidate)
				if err := tx.Commit(withAppliedGeneration(final, candidateGeneration)); err != nil {
					return failClosedEgressWithDeps(d, tx, nil)
				}
				return nil
			case durableGeneration:
				return commitRecoveredGeneration(d, tx, tx.Session(), durableGeneration)
			}
		}
		// Unknown runtime identity is restored to the old durable bound; that
		// may cancel the interrupted revoke but never exceeds durable authority.
		if err := d.applyEgressOverlay(ctx, sess, sessionEgressViews(sess)); err != nil {
			return failClosedEgressWithDeps(d, tx, nil)
		}
		return commitRecoveredGeneration(d, tx, tx.Session(), durableGeneration)
	default:
		return failClosedEgressWithDeps(d, tx, nil)
	}
}

func grantSessionEgress(ctx context.Context, store engsession.Store, id, host string, port int, now time.Time) (engsession.Session, engsession.EgressGrant, error) {
	return grantSessionEgressWithDeps(defaultDependencies(), ctx, store, id, host, port, now)
}

func grantSessionEgressWithDeps(d *dependencies, ctx context.Context, store engsession.Store, id, host string, port int, now time.Time) (engsession.Session, engsession.EgressGrant, error) {
	var grant engsession.EgressGrant
	committed, err := store.WithLocked(id, func(tx *engsession.RecordTx) error {
		sess := tx.Session()
		if sess.Status == engsession.StatusStopped {
			return fmt.Errorf("session stopped")
		}
		if err := recoverRunningSessionEgressWithDeps(d, ctx, tx); err != nil {
			return err
		}
		sess = tx.Session()
		next, nextGrant, err := engsession.AppendGrant(sess, host, port, now)
		if err != nil {
			return err
		}
		grant = nextGrant
		if next.GrantRevision == sess.GrantRevision {
			return nil // duplicate destination: no authority or generation change
		}
		if sess.Status != engsession.StatusRunning {
			return tx.Commit(next)
		}
		generation, err := sessionGeneration(next)
		if err != nil {
			return err
		}
		oldAuthority := sess
		pending := withTransition(next, engsession.EgressDirectionWiden, next, generation)
		if err := tx.Commit(pending); err != nil {
			if errors.Is(err, engsession.ErrCommitUncertain) {
				return failClosedEgressWithDeps(d, tx, &oldAuthority)
			}
			return err
		}
		if err := d.applyEgressOverlay(ctx, next, sessionEgressViews(next)); err != nil {
			return failClosedEgressWithDeps(d, tx, &oldAuthority)
		}
		final := withAppliedGeneration(tx.Session(), generation)
		if err := tx.Commit(final); err != nil {
			return failClosedEgressWithDeps(d, tx, &oldAuthority)
		}
		return nil
	})
	if err != nil {
		return engsession.Session{}, engsession.EgressGrant{}, err
	}
	return committed, grant, nil
}

func dismissSessionEgress(store engsession.Store, id, host string, port int, now time.Time) (engsession.Session, engsession.EgressAcknowledgement, error) {
	return dismissSessionEgressWithDeps(defaultDependencies(), store, id, host, port, now)
}

func dismissSessionEgressWithDeps(d *dependencies, store engsession.Store, id, host string, port int, now time.Time) (engsession.Session, engsession.EgressAcknowledgement, error) {
	var acknowledgement engsession.EgressAcknowledgement
	committed, err := store.WithLocked(id, func(tx *engsession.RecordTx) error {
		sess := tx.Session()
		if sess.Status == engsession.StatusStopped {
			return fmt.Errorf("session stopped")
		}
		if err := recoverRunningSessionEgressWithDeps(d, context.Background(), tx); err != nil {
			return err
		}
		next, nextAcknowledgement, err := engsession.DismissEgress(tx.Session(), host, port, now)
		if err != nil {
			return err
		}
		acknowledgement = nextAcknowledgement
		return tx.Commit(next)
	})
	if err != nil {
		return engsession.Session{}, engsession.EgressAcknowledgement{}, err
	}
	return committed, acknowledgement, nil
}

func clearNarrowTransition(tx *engsession.RecordTx, durable engsession.Session, generation container.EgressGeneration) error {
	current := copySessionAuthority(tx.Session(), durable)
	current = withAppliedGeneration(current, generation)
	return tx.Commit(current)
}

func revokeSessionEgress(ctx context.Context, store engsession.Store, id, grantID string, now time.Time) (engsession.Session, error) {
	return revokeSessionEgressWithDeps(defaultDependencies(), ctx, store, id, grantID, now)
}

func revokeSessionEgressWithDeps(d *dependencies, ctx context.Context, store engsession.Store, id, grantID string, now time.Time) (engsession.Session, error) {
	return store.WithLocked(id, func(tx *engsession.RecordTx) error {
		sess := tx.Session()
		if sess.Status == engsession.StatusStopped {
			return fmt.Errorf("session stopped")
		}
		if err := recoverRunningSessionEgressWithDeps(d, ctx, tx); err != nil {
			return err
		}
		sess = tx.Session()
		next, err := engsession.RevokeGrant(sess, grantID, now)
		if err != nil {
			return err
		}
		if sess.Status != engsession.StatusRunning {
			return tx.Commit(next)
		}
		candidateGeneration, err := sessionGeneration(next)
		if err != nil {
			return err
		}
		oldGeneration, err := sessionGeneration(sess)
		if err != nil {
			return err
		}
		pending := withTransition(sess, engsession.EgressDirectionNarrow, next, candidateGeneration)
		if err := tx.Commit(pending); err != nil {
			if errors.Is(err, engsession.ErrCommitUncertain) {
				return failClosedEgressWithDeps(d, tx, &sess)
			}
			return err
		}
		if err := d.applyEgressOverlay(ctx, next, sessionEgressViews(next)); err != nil {
			if restoreErr := d.applyEgressOverlay(ctx, sess, sessionEgressViews(sess)); restoreErr != nil {
				return failClosedEgressWithDeps(d, tx, &sess)
			}
			if clearErr := clearNarrowTransition(tx, sess, oldGeneration); clearErr != nil && errors.Is(clearErr, engsession.ErrCommitUncertain) {
				return failClosedEgressWithDeps(d, tx, &sess)
			}
			return err
		}
		final := copySessionAuthority(tx.Session(), next)
		final = withAppliedGeneration(final, candidateGeneration)
		if err := tx.Commit(final); err != nil {
			if errors.Is(err, engsession.ErrCommitUncertain) {
				// The narrower record may already be durable. Never restore the
				// broader old runtime across that uncertainty.
				return failClosedEgressWithDeps(d, tx, nil)
			}
			if restoreErr := d.applyEgressOverlay(ctx, sess, sessionEgressViews(sess)); restoreErr != nil {
				return failClosedEgressWithDeps(d, tx, &sess)
			}
			_ = clearNarrowTransition(tx, sess, oldGeneration)
			return err
		}
		return nil
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
	return sessionGrantViewsFromRunNameWithDeps(defaultDependencies(), name)
}

func sessionGrantViewsFromRunNameWithDeps(d *dependencies, name string) []container.SessionGrant {
	if !strings.HasPrefix(name, "session-") {
		return nil
	}
	sess, err := d.store.Get(strings.TrimPrefix(name, "session-"))
	if err != nil {
		return nil
	}
	return sessionEgressViews(sess)
}
