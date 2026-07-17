package cli

import (
	"context"
	"fmt"
	"hash/fnv"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/freakhill/safeslop/internal/engine/container"
	runtimepkg "github.com/freakhill/safeslop/internal/engine/container/runtime"
	"github.com/freakhill/safeslop/internal/engine/creds"
	engexec "github.com/freakhill/safeslop/internal/engine/exec"
	"github.com/freakhill/safeslop/internal/engine/gitguard"
	"github.com/freakhill/safeslop/internal/engine/hostexec"
	"github.com/freakhill/safeslop/internal/engine/policy"
	"github.com/freakhill/safeslop/internal/engine/secrets"
	engsession "github.com/freakhill/safeslop/internal/engine/session"
	"github.com/freakhill/safeslop/internal/engine/userconfig"
	workspaceboundary "github.com/freakhill/safeslop/internal/engine/workspace"
	"github.com/freakhill/safeslop/internal/jsoncontract"
)

func networkPolicyForProfile(network string) runtimepkg.NetworkPolicy {
	if network == "allow" {
		return runtimepkg.PolicyAllow
	}
	return runtimepkg.PolicyDeny
}

func sessionStageKey(sess engsession.Session) (string, error) {
	runtimeID, layout := sess.RuntimeIdentity()
	switch layout {
	case engsession.StageLayoutLegacy:
		return "", nil
	case engsession.StageLayoutSessionID:
		if runtimeID != sess.ID {
			return "", fmt.Errorf("session runtime identity is invalid")
		}
		return "session-" + runtimeID, nil
	default:
		return "", fmt.Errorf("session stage layout is unsupported")
	}
}

func sessionStageDir(sess engsession.Session) (string, error) {
	key, err := sessionStageKey(sess)
	if err != nil {
		return "", err
	}
	if key != "" {
		return stageDirForRuntime(key)
	}
	return stageDirFor("session-"+sess.ID, sess.Workspace)
}

func defaultSessionRevokeCredentials(sess engsession.Session) error {
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
func defaultSessionWipeStageDir(sess engsession.Session) error {
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
func stageRootPath() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolve user cache dir for credential staging: %w", err)
	}
	return filepath.Join(cache, "safeslop", "runtime"), nil
}

func validateWorkspaceStageRoot(ws string) error {
	root, err := stageRootPath()
	if err != nil {
		return err
	}
	return workspaceboundary.RequireDisjointPaths(ws, root)
}

var runtimeStageKeyPattern = regexp.MustCompile(`^(?:run-[0-9a-f]{32}|session-sess-[a-zA-Z0-9_-]+)$`)

func stageDirForRuntime(runtimeID string) (string, error) {
	if !runtimeStageKeyPattern.MatchString(runtimeID) {
		return "", fmt.Errorf("runtime identity is invalid")
	}
	base, err := stageRootPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(base, 0o700); err != nil {
		return "", fmt.Errorf("create credential staging root: %w", err)
	}
	return filepath.Join(base, runtimeID), nil
}

func stageDirFor(name, ws string) (string, error) {
	base, err := stageRootPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(base, 0o700); err != nil {
		return "", fmt.Errorf("create credential staging root: %w", err)
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(ws))
	return filepath.Join(base, fmt.Sprintf("%s-%08x", name, h.Sum32())), nil
}

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
	return sessionReapBoundaryWithDeps(defaultDependencies(), sess)
}

func sessionReapBoundaryWithDeps(d *dependencies, sess engsession.Session) error {
	if sess.Environment != "container" {
		return nil
	}
	key, err := sessionReapKey(sess)
	if err != nil {
		return err
	}
	eng, err := d.engineForSession(sess)
	if err != nil {
		return err
	}
	return container.ReapBySession(context.Background(), eng, key)
}

func recordSessionBackend(store engsession.Store, sess engsession.Session) (engsession.Session, error) {
	return recordSessionBackendWithDeps(defaultDependencies(), store, sess)
}

func recordSessionBackendWithDeps(d *dependencies, store engsession.Store, sess engsession.Session) (engsession.Session, error) {
	updated, _, err := prepareSessionBackendWithDeps(d, store, sess)
	return updated, err
}

// prepareSessionBackendWithDeps persists a new session's policy-gated runtime
// before launch and returns that exact engine to its caller. Existing sessions
// resolve only their recorded backend, never ambient precedence.
func prepareSessionBackendWithDeps(d *dependencies, store engsession.Store, sess engsession.Session) (engsession.Session, runtimepkg.Engine, error) {
	if sess.Environment != "container" {
		return sess, nil, nil
	}
	var selected runtimepkg.Engine
	updated, err := store.WithLocked(sess.ID, func(tx *engsession.RecordTx) error {
		current := tx.Session()
		changed := false
		var prof policy.Profile
		var haveProfile bool
		loadProfile := func() (policy.Profile, error) {
			if haveProfile {
				return prof, nil
			}
			var err error
			prof, err = sessionProfile(current)
			if err != nil {
				return policy.Profile{}, err
			}
			haveProfile = true
			return prof, nil
		}
		if current.Backend == "" {
			p, err := loadProfile()
			if err != nil {
				return err
			}
			selected, err = d.detectRuntime(networkPolicyForProfile(p.Network))
			if err != nil || selected == nil || selected.Name() == "" {
				return ErrSessionBackendUnavailable
			}
			current.Backend = selected.Name()
			changed = true
		} else {
			var err error
			selected, err = d.engineForSession(current)
			if err != nil {
				return ErrSessionBackendUnavailable
			}
		}
		if current.Image == "" || current.RecipeID == "" {
			p, err := loadProfile()
			if err != nil {
				return err
			}
			resolved, err := policy.Resolve(p)
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
		current.UpdatedAt = d.now().UTC()
		return tx.Commit(current)
	})
	if err != nil {
		return engsession.Session{}, nil, err
	}
	return updated, selected, nil
}

// sweepManagedOrphans reaps labelled boundaries whose session record is gone, before a new container run.
// It detects the ambient runtime with PolicyAllow (teardown is never gated); with no runtime present
// Detect fails and the sweep is a no-op — nothing safeslop could have started (specs/0066 D5).
func sweepManagedOrphans(ctx context.Context) error {
	return sweepManagedOrphansWithDeps(defaultDependencies(), ctx)
}

func sweepManagedOrphansWithDeps(d *dependencies, ctx context.Context) error {
	eng, err := d.detectRuntime(runtimepkg.PolicyAllow)
	if err != nil {
		return nil
	}
	live, err := container.LiveSessions(d.store.Dir)
	if err != nil {
		return err
	}
	if err := container.SweepManagedOrphans(ctx, eng, live); err != nil {
		return err
	}
	stageRoot, err := d.stageRoot()
	if err != nil {
		return err
	}
	return container.SweepDeadInvocations(ctx, eng, stageRoot)
}

func cmdDown() *cobra.Command {
	return cmdDownWithDeps(defaultDependencies())
}

func cmdDownWithDeps(d *dependencies) *cobra.Command {
	return &cobra.Command{
		Use:   "down",
		Short: "Tear down safeslop-managed container stacks",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			ctx := context.Background()
			// Detect with PolicyAllow: `down` must clean up whatever ambient runtime safeslop launched on,
			// verified or not (the deny-tier gate applies only to launching). With no runtime present there
			// is nothing safeslop could have started, so down is a no-op (specs/0066 D5).
			eng, err := d.detectRuntime(runtimepkg.PolicyAllow)
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
	return cmdGCWithDeps(defaultDependencies())
}

func cmdGCWithDeps(d *dependencies) *cobra.Command {
	var until, keepRaw string
	c := &cobra.Command{
		Use:   "gc [--until <age>] [--keep <N>]",
		Short: "Garbage-collect unreferenced safeslop-managed images",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			eng, err := d.detectRuntime(runtimepkg.PolicyAllow)
			if err != nil {
				return fmt.Errorf("cannot gc: container runtime is unavailable (run: safeslop doctor)")
			}
			keep, err := container.ParseKeep(keepRaw)
			if err != nil {
				return err
			}
			policyPath, _ := findConfig("")
			removed, err := d.gcImages(context.Background(), eng, container.GCOptions{Until: until, Keep: keep}, container.DefaultProtection(policyPath, d.store.Dir))
			if err != nil {
				return err
			}
			if d.jsonOut {
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

func preflightProfileHostHelpers(prof policy.Profile, accounts *userconfig.Accounts) error {
	return preflightProfileHostHelpersWithDeps(defaultDependencies(), prof, accounts)
}

func preflightProfileHostHelpersWithDeps(d *dependencies, prof policy.Profile, accounts *userconfig.Accounts) error {
	return d.stageHostExec().Preflight(requiredProfileHostHelpers(prof, accounts)...)
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
	return stageProfileWithDeps(defaultDependencies(), ctx, prof, stageDir)
}

func stageProfileWithDeps(d *dependencies, ctx context.Context, prof policy.Profile, stageDir string) (secretEnv, pathEnv []string, err error) {
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
	if err := preflightProfileHostHelpersWithDeps(d, prof, accounts); err != nil {
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

// credentialManager owns host-side renewal and bounded Forgejo cleanup for one run. It has no
// listener or sandbox-facing API: the agent sees only staged files.
type credentialManager struct {
	github       *creds.Lease
	forgejoTimer *time.Timer
}

func startCredentialManager(d *dependencies, stagedAt time.Time, runName string, prof policy.Profile, stageDir string) (*credentialManager, error) {
	manager := &credentialManager{}
	var err error
	manager.github, err = creds.StartGithubCredentialLease(stagedAt, prof.Credentials, stageDir, func(snapshot creds.LeaseSnapshot) {
		persistLeaseSnapshot(d, runName, stageDir, snapshot)
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
func persistLeaseSnapshot(d *dependencies, runName, stageDir string, snapshot creds.LeaseSnapshot) {
	if !strings.HasPrefix(runName, "session-") {
		return
	}
	id := strings.TrimPrefix(runName, "session-")
	if id == "" {
		return
	}
	_, _ = d.store.Update(id, func(sess engsession.Session) (engsession.Session, error) {
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
	return runProfileCtxWithDeps(defaultDependencies(), ctx, name, prof, argv, ws, "", stdio...)
}

func runProfileCtxWithStageKey(ctx context.Context, name string, prof policy.Profile, argv []string, ws, stageKey string, stdio ...runIO) (int, error) {
	return runProfileCtxWithDeps(defaultDependencies(), ctx, name, prof, argv, ws, stageKey, stdio...)
}

func runProfileCtxWithDeps(d *dependencies, ctx context.Context, name string, prof policy.Profile, argv []string, ws, stageKey string, stdio ...runIO) (int, error) {
	return runProfileCtxWithEngineAndDeps(d, ctx, nil, name, prof, argv, ws, stageKey, stdio...)
}

// runProfileCtxWithEngineAndDeps retains a runtime selection made before session
// persistence. A nil engine is the direct-run path, which detects exactly once.
func runProfileCtxWithEngineAndDeps(d *dependencies, ctx context.Context, selectedEngine runtimepkg.Engine, name string, prof policy.Profile, argv []string, ws, stageKey string, stdio ...runIO) (int, error) {
	var rio runIO
	if len(stdio) > 0 {
		rio = stdio[0]
	}
	invocationDir, err := os.Getwd()
	if err != nil {
		return 1, fmt.Errorf("resolve invocation directory: %w", err)
	}
	ws, err = workspaceboundary.Resolve(ws, "", invocationDir)
	if err != nil {
		return 1, fmt.Errorf("resolve workspace boundary: %w", err)
	}
	prof.Workspace = ws
	var stageDir string
	if stageKey != "" {
		stageDir, err = stageDirForRuntime(stageKey)
	} else {
		stageDir, err = stageDirFor(name, ws)
	}
	if err != nil {
		return 1, err
	}
	if prof.Environment == "container" {
		if err := workspaceboundary.RequireDisjointPaths(ws, stageDir); err != nil {
			return 1, err
		}
	}
	// A crashed wrapper can leave a stage directory for this exact run identity. Never reuse it:
	// retired tokens and stale canonical files must be removed before any new mint/stage action.
	if err := os.RemoveAll(stageDir); err != nil {
		return 1, fmt.Errorf("remove abandoned credential stage: %w", err)
	}
	retainInvocationMarker := false
	defer func() {
		if retainInvocationMarker {
			_ = container.RetainInvocationMarker(stageDir)
			return
		}
		_ = os.RemoveAll(stageDir)
	}()
	if strings.HasPrefix(stageKey, "run-") {
		processToken, _ := engsession.ProcessStartToken(os.Getpid())
		if err := container.WriteInvocationMarker(stageDir, stageKey, os.Getpid(), processToken); err != nil {
			return 1, err
		}
	}

	// kube/ssh creds are staged as files in stageDir and delivered via the /safeslop/runtime bind
	// mount (container). GIT_SSH_COMMAND/KUBECONFIG are exported inside the boundary.
	if err := seedAgentDefaults(prof, ws); err != nil {
		return 1, err
	}
	stagedAt := d.now()
	secretEnv, pathEnv, err := stageProfileWithDeps(d, ctx, prof, stageDir)
	if err != nil {
		return 1, err
	}
	manager, err := startCredentialManager(d, stagedAt, name, prof, stageDir)
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
		env := childEnvWithDeps(d, secretEnv, pathEnv)
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
		containerEngine := selectedEngine
		if containerEngine == nil {
			containerEngine, err = d.detectRuntime(networkPolicyForProfile(prof.Network))
			if err != nil || containerEngine == nil {
				return 1, fmt.Errorf("container runtime is unavailable")
			}
		}
		// A detached supervisor passes a PTY slave it owns (rio set); forward it so the
		// container's tty bridges to the supervisor's PTY for attach. Coupled (rio zero)
		// leaves stdio nil and container.Launch runs the user's terminal (specs/0051).
		code, launchErr := d.launchContainer(ctx, containerEngine, engexec.LaunchSpec{Argv: argv, Stdin: rio.Stdin, Stdout: rio.Stdout, Stderr: rio.Stderr}, ws, prof.Network, egress, secretEnv, stageDir, resolved.IdentitySet, prof.Projection, sessionGrantViewsFromRunNameWithDeps(d, name)...)
		if strings.HasPrefix(stageKey, "run-") {
			if reapErr := d.reapDirectInvocation(containerEngine, stageKey); reapErr != nil {
				retainInvocationMarker = true
				if launchErr == nil {
					return code, reapErr
				}
			}
		}
		return code, launchErr
	default:
		return 1, fmt.Errorf("unknown environment %q", prof.Environment)
	}
}

// warnGitExecSurface prints a prominent warning if the agent changed git's executable surface
// (.git/hooks or .git/config) during the run — a planted hook or config directive runs on your
// next git command in this repo (specs/0025 S3). Best-effort: snapshot errors are ignored.
