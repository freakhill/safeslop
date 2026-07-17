package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/freakhill/safeslop/internal/engine/container"
	runtimepkg "github.com/freakhill/safeslop/internal/engine/container/runtime"
	"github.com/freakhill/safeslop/internal/engine/creds"
	engexec "github.com/freakhill/safeslop/internal/engine/exec"
	"github.com/freakhill/safeslop/internal/engine/hostexec"
	"github.com/freakhill/safeslop/internal/engine/policy"
	engsession "github.com/freakhill/safeslop/internal/engine/session"
	"github.com/freakhill/safeslop/internal/engine/trust"
)

type containerLauncher func(context.Context, runtimepkg.Engine, engexec.LaunchSpec, string, string, []string, []string, string, []string, *policy.Projection, ...container.SessionGrant) (int, error)

// dependencies is constructed once per command root. Command closures retain
// that instance, so execution and test seams never rely on mutable package state.
type dependencies struct {
	jsonOut                 bool
	now                     func() time.Time
	store                   engsession.Store
	profileProjectTrust     func(string, []byte) (trust.Status, error)
	profileBuiltinIntegrity func(string, string) (bool, error)
	profileWorkspace        func(string) profilePrerequisiteCheck
	profileHelper           func(string) profilePrerequisiteCheck
	profileRuntime          func(string) profilePrerequisiteCheck
	profileAccountLinks     func(policy.Profile) profileAccountLinkChecks
	catalogFetcher          policy.Fetcher
	forgejoGCBaseForHost    func(string) string
	newForgejoGCClient      func() creds.ForgejoHTTP
	hostDiscoveryEnv        func() map[string]string
	detectRuntime           func(runtimepkg.NetworkPolicy) (runtimepkg.Engine, error)
	backendEngine           func(string) (runtimepkg.Engine, error)
	gcImages                func(context.Context, runtimepkg.Engine, container.GCOptions, container.GCProtection) ([]string, error)
	launchContainer         containerLauncher
	reapDirectInvocation    func(runtimepkg.Engine, string) error
	applyEgressOverlay      func(context.Context, engsession.Session, []container.SessionGrant) error
	inspectEgress           func(context.Context, engsession.Session) (container.EgressGeneration, error)
	teardownEgress          func(engsession.Session) error
	observeEgress           func(context.Context, engsession.Session) ([]container.EgressObservation, error)
	processAlive            func(engsession.Session) bool
	killProcess             func(int) error
	waitProcess             func(int, engsession.Session) error
	revokeCredentials       func(engsession.Session) error
	wipeStageDir            func(engsession.Session) error
	sessionSocket           func(engsession.Session) (string, bool)
	chmodSocket             func(string, os.FileMode) error
	removeSocket            func(string) error
	hasInteractivePTY       func() bool
	launchSupervisor        func(string) (launchedSupervisor, error)
	detachReadyTimeout      time.Duration
	hostLaunchConsent       func(string, policy.Profile, io.Reader, io.Writer) error
	doctorHostExec          func() *hostexec.Resolver
	stageHostExec           func() *hostexec.Resolver
	credsProber             func() creds.Prober
	writePolicy             func(string, []byte) error
	writePolicyAtomically   func(string, []byte) error
	stageRoot               func() (string, error)
	removeStageDir          func(string) error
	retainInvocationMarker  func(string) error
}

func defaultDependencies() *dependencies {
	d := &dependencies{
		now:                     time.Now,
		store:                   sessionStore(),
		profileProjectTrust:     defaultProfileEvaluationProjectTrust,
		profileBuiltinIntegrity: defaultProfileEvaluationBuiltinIntegrity,
		profileWorkspace:        defaultProfileEvaluationWorkspace,
		profileAccountLinks:     inspectProfileEvaluationAccountLinks,
		catalogFetcher:          newHTTPFetcher(),
		forgejoGCBaseForHost:    func(host string) string { return "https://" + host },
		newForgejoGCClient:      creds.NewForgejoHTTP,
		hostDiscoveryEnv:        defaultHostDiscoveryEnv,
		detectRuntime:           runtimepkg.Detect,
		backendEngine: func(name string) (runtimepkg.Engine, error) {
			return runtimepkg.Resolve(name, runtimepkg.PolicyAllow)
		},
		gcImages: func(ctx context.Context, eng runtimepkg.Engine, opts container.GCOptions, protect container.GCProtection) ([]string, error) {
			return container.GCImages(ctx, eng, opts, protect)
		},
		launchContainer:        container.LaunchWithEngine,
		processAlive:           engsession.ProcessAliveSession,
		chmodSocket:            os.Chmod,
		removeSocket:           os.Remove,
		hasInteractivePTY:      defaultSessionHasInteractivePTY,
		launchSupervisor:       defaultLaunchSupervisor,
		detachReadyTimeout:     2 * time.Second,
		hostLaunchConsent:      confirmHostLaunchConsent,
		doctorHostExec:         hostexec.Default,
		stageHostExec:          hostexec.Default,
		credsProber:            creds.DefaultProber,
		writePolicy:            func(path string, content []byte) error { return os.WriteFile(path, content, 0o644) },
		writePolicyAtomically:  writePolicyAtomically,
		stageRoot:              stageRootPath,
		removeStageDir:         os.RemoveAll,
		retainInvocationMarker: container.RetainInvocationMarker,
	}
	d.profileHelper = func(name string) profilePrerequisiteCheck {
		return inspectProfileEvaluationHelperWithResolver(d.stageHostExec(), name)
	}
	d.profileRuntime = func(network string) profilePrerequisiteCheck {
		return inspectProfileEvaluationRuntime(d.detectRuntime, network)
	}
	d.killProcess = defaultSessionKillProcess
	d.waitProcess = defaultSessionWaitProcess
	d.revokeCredentials = defaultSessionRevokeCredentials
	d.wipeStageDir = defaultSessionWipeStageDir
	d.reapDirectInvocation = func(eng runtimepkg.Engine, invocationID string) error {
		if eng == nil {
			return errors.New("direct invocation cleanup runtime is unavailable")
		}
		return container.ReapByInvocation(context.Background(), eng, invocationID)
	}
	d.applyEgressOverlay = func(ctx context.Context, sess engsession.Session, desired []container.SessionGrant) error {
		stageDir, err := sessionStageDir(sess)
		if err != nil {
			return err
		}
		eng, err := d.engineForSession(sess)
		if err != nil {
			return err
		}
		_, err = container.EnsureEgressGeneration(ctx, eng, filepath.Join(stageDir, "compose.yml"), stageDir, desired, sess.GrantRevision)
		return err
	}
	d.inspectEgress = func(ctx context.Context, sess engsession.Session) (container.EgressGeneration, error) {
		stageDir, err := sessionStageDir(sess)
		if err != nil {
			return container.EgressGeneration{}, err
		}
		eng, err := d.engineForSession(sess)
		if err != nil {
			return container.EgressGeneration{}, err
		}
		return container.InspectEgressGeneration(ctx, eng, filepath.Join(stageDir, "compose.yml"))
	}
	d.observeEgress = func(ctx context.Context, sess engsession.Session) ([]container.EgressObservation, error) {
		stageDir, err := sessionStageDir(sess)
		if err != nil {
			return nil, err
		}
		eng, err := d.engineForSession(sess)
		if err != nil {
			return nil, err
		}
		return container.ReadDeniedEgressObservations(ctx, eng, filepath.Join(stageDir, "compose.yml"))
	}
	d.teardownEgress = func(sess engsession.Session) error { return d.defaultEgressTeardown(sess) }
	d.sessionSocket = func(sess engsession.Session) (string, bool) {
		if sess.Status != engsession.StatusRunning {
			return "", false
		}
		for _, path := range d.store.SocketPaths(sess.ID) {
			if safeAttachSocket(path) {
				return path, true
			}
		}
		return "", false
	}
	return d
}

// ErrSessionBackendUnavailable is deliberately value-free: a persisted session
// may be acted on only through its recorded runtime, never a newly detected
// fallback that could leave its original network boundary live.
var ErrSessionBackendUnavailable = errors.New("session backend is unavailable; refusing to use another runtime")

func (d *dependencies) engineForSession(sess engsession.Session) (runtimepkg.Engine, error) {
	if sess.Environment != "container" || sess.Backend == "" {
		return nil, ErrSessionBackendUnavailable
	}
	eng, err := d.backendEngine(sess.Backend)
	if err != nil || eng == nil || eng.Name() != sess.Backend {
		return nil, ErrSessionBackendUnavailable
	}
	return eng, nil
}

func (d *dependencies) defaultEgressTeardown(sess engsession.Session) error {
	var firstErr error
	if sess.PID != 0 && sess.PID != os.Getpid() && d.processAlive(sess) {
		target := sess.PID
		if sess.Detached {
			target = -target
		}
		if err := d.killProcess(target); err != nil {
			firstErr = err
		}
	}
	if err := sessionReapBoundaryWithDeps(d, sess); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := d.wipeStageDir(sess); err != nil && firstErr == nil {
		firstErr = err
	}
	for _, path := range d.store.SocketPaths(sess.ID) {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

const stopGraceTimeout = 5 * time.Second

// defaultSessionKillProcess sends only the initial signal. Callers that need
// bounded group-exit confirmation invoke defaultSessionWaitProcess after
// releasing any session record lock, so the supervisor can persist Finish.
func defaultSessionKillProcess(target int) error {
	if target == 0 {
		return nil
	}
	if err := syscall.Kill(target, syscall.SIGTERM); err != nil {
		// A detached group can finish between the token check and signal. It is
		// already in the desired state; no fallback PID/group may be selected.
		if target < 0 && errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}
	return nil
}

func defaultSessionWaitProcess(target int, identity engsession.Session) error {
	if target >= 0 {
		return nil
	}
	if identity.PID <= 0 || target != -identity.PID {
		return errors.New("session process identity is invalid")
	}
	deadline := time.Now().Add(stopGraceTimeout)
	for time.Now().Before(deadline) {
		if syscall.Kill(target, 0) != nil {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	// The grace period is long enough for PID/group reuse. Revalidate the
	// captured process-start token immediately before escalating authority.
	if identity.ProcessToken == "" || !engsession.ProcessAliveSession(identity) {
		return errors.New("session process identity changed before escalation")
	}
	if err := syscall.Kill(target, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	killDeadline := time.Now().Add(time.Second)
	for time.Now().Before(killDeadline) {
		if syscall.Kill(target, 0) != nil {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return errors.New("session process group did not exit")
}
