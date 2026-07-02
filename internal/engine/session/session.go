// Package session stores and transitions safeslop session metadata for the
// Emacs-facing session command surface.
package session

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

const (
	StatusCreated = "created"
	StatusRunning = "running"
	StatusStopped = "stopped"
)

var ErrNotFound = errors.New("session not found")

// ErrSessionRunning is returned by Remove when the target session is still
// running. A running session must be stopped first (which tears down the
// boundary and can revoke credentials); removing its record out from under a
// live boundary would orphan the process and its staged credentials.
var ErrSessionRunning = errors.New("session is running")

// ResolvedMetadata snapshots the non-secret package resolution that selected a
// profile-backed session's image. It is stored with the session so status/list can
// keep portal Recipe/Image columns stable even after the session leaves the
// creating command's process.
type ResolvedMetadata struct {
	Packages      []string `json:"packages,omitempty"`
	IdentitySet   []string `json:"identitySet,omitempty"`
	RuntimeEgress []string `json:"runtimeEgress,omitempty"`
}

// Session is the durable, non-secret state for an Emacs-visible session. Do not
// add staged credential values or resolved secret material here; the JSONL status
// path serializes this object for clients.
type Session struct {
	ID                 string            `json:"session_id"`
	Profile            string            `json:"profile,omitempty"`
	Agent              string            `json:"agent"`
	Workspace          string            `json:"workspace"`
	Environment        string            `json:"environment"`
	Network            string            `json:"network"`
	Backend            string            `json:"backend"`
	RecipeID           string            `json:"recipeID,omitempty"`
	Image              string            `json:"image,omitempty"`
	Resolved           *ResolvedMetadata `json:"resolved,omitempty"`
	Status             string            `json:"status"`
	CreatedAt          time.Time         `json:"created_at"`
	UpdatedAt          time.Time         `json:"updated_at"`
	StartedAt          time.Time         `json:"started_at,omitempty"`
	StoppedAt          time.Time         `json:"stopped_at,omitempty"`
	RevokedAt          time.Time         `json:"revoked_at,omitempty"`
	CredentialsRevoked bool              `json:"credentials_revoked"`
	PID                int               `json:"pid,omitempty"`
	ExitCode           *int              `json:"exit_code,omitempty"`
	LastError          string            `json:"last_error,omitempty"`
	// Detached marks a session whose recorded PID is a detached supervisor that
	// leads its own process group, so `stop` signals the group, not a bare PID
	// (specs/0051 D4). Internal routing state; not surfaced in the JSON envelope.
	Detached bool `json:"detached,omitempty"`
}

type Store struct{ Dir string }

func NewStore(dir string) Store { return Store{Dir: dir} }

// Create records a new session. environment is required (specs/0053 removed the
// default sandbox tier) and must be host or container; the CLI validates it
// before calling. network defaults to deny (honored by container).
func (s Store) Create(agent, environment, workspace string, now time.Time) (Session, error) {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return Session{}, err
	}
	abs, err := filepath.Abs(workspace)
	if err != nil {
		return Session{}, err
	}
	id, err := newID()
	if err != nil {
		return Session{}, err
	}
	sess := Session{
		ID:          id,
		Agent:       agent,
		Workspace:   abs,
		Environment: environment,
		Network:     "deny",
		// Backend is unknown-until-provisioned: session.Create runs BEFORE the container runtime is
		// detected, so recordSessionBackend fills it from the detected engine's Name() at run time
		// (specs/0066 D7). Empty rather than a fabricated default.
		Backend:   "",
		Status:    StatusCreated,
		CreatedAt: now.UTC(),
		UpdatedAt: now.UTC(),
	}
	return sess, s.Save(sess)
}

func (s Store) Get(id string) (Session, error) {
	if id == "" || !validID(id) {
		return Session{}, ErrNotFound
	}
	b, err := os.ReadFile(s.path(id))
	if os.IsNotExist(err) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, err
	}
	var sess Session
	if err := json.Unmarshal(b, &sess); err != nil {
		return Session{}, err
	}
	// A legacy on-disk `"backend":"system"` predates the ambient multi-runtime pivot (specs/0066 D7
	// repurposed Backend from "system"|"lima" to the detected engine name). "system" only ever meant the
	// ambient host docker, so normalize it to "docker" on read.
	if sess.Backend == "system" {
		sess.Backend = "docker"
	}
	return sess, nil
}

func (s Store) List() ([]Session, error) {
	entries, err := os.ReadDir(s.Dir)
	if os.IsNotExist(err) {
		return []Session{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]Session, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		id := e.Name()[:len(e.Name())-len(".json")]
		sess, err := s.Get(id)
		if err != nil {
			continue
		}
		out = append(out, sess)
	}
	return out, nil
}

func (s Store) Save(sess Session) error {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(sess, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path(sess.ID), append(b, '\n'), 0o600)
}

func (s Store) MarkRunning(id string, pid int, now time.Time) (Session, error) {
	return s.markRunning(id, pid, false, now)
}

// MarkRunningDetached records a session as running under a detached supervisor
// (specs/0051): the recorded PID is the supervisor's, and Detached routes `stop`
// to signal the whole process group (D4).
func (s Store) MarkRunningDetached(id string, pid int, now time.Time) (Session, error) {
	return s.markRunning(id, pid, true, now)
}

func (s Store) markRunning(id string, pid int, detached bool, now time.Time) (Session, error) {
	sess, err := s.Get(id)
	if err != nil {
		return Session{}, err
	}
	if sess.Status == StatusStopped {
		return Session{}, fmt.Errorf("session stopped")
	}
	sess.Status = StatusRunning
	sess.PID = pid
	sess.Detached = detached
	sess.StartedAt = now.UTC()
	sess.UpdatedAt = now.UTC()
	return sess, s.Save(sess)
}

func (s Store) Finish(id string, exitCode int, lastErr string, now time.Time) (Session, error) {
	sess, err := s.Get(id)
	if err != nil {
		return Session{}, err
	}
	sess.Status = StatusStopped
	sess.PID = 0
	sess.ExitCode = &exitCode
	sess.LastError = lastErr
	sess.StoppedAt = now.UTC()
	sess.UpdatedAt = now.UTC()
	return sess, s.Save(sess)
}

// ProcessAlive reports whether pid names a live process. It is the default
// liveness probe used to reconcile sessions whose run wrapper died without
// recording an exit. On macOS/unix, signal 0 succeeds for a live process we
// own, and EPERM means the process is alive but owned by another user.
func ProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}

// reconcile corrects sess for liveness. In the coupled lifecycle the run wrapper
// holds the agent for the whole run, so a session still marked running whose
// recorded PID is no longer alive means the run ended without recording an exit
// (crash, SIGKILL, host sleep): report it as stopped. Pure given isAlive; the
// bool reports whether sess changed so the caller can persist exactly once.
//
// The recorded PID is today the run-wrapper PID — an honest liveness anchor for
// the coupled model. Surfacing the boundary process PID and a process-group
// teardown is specs/0050 PR2; PID reuse is a known, accepted limitation here.
func reconcile(sess Session, now time.Time, isAlive func(int) bool) (Session, bool) {
	if sess.Status != StatusRunning || sess.PID <= 0 || isAlive(sess.PID) {
		return sess, false
	}
	sess.Status = StatusStopped
	sess.PID = 0
	sess.LastError = "run process exited without recording status"
	sess.StoppedAt = now.UTC()
	sess.UpdatedAt = now.UTC()
	return sess, true
}

// GetReconciled is Get plus a liveness pass: a session marked running whose PID
// is dead is persisted and returned as stopped, so status never lies.
func (s Store) GetReconciled(id string, now time.Time, isAlive func(int) bool, reap ...func(Session) error) (Session, error) {
	sess, err := s.Get(id)
	if err != nil {
		return Session{}, err
	}
	if fixed, changed := reconcile(sess, now, isAlive); changed {
		for _, fn := range reap {
			if fn != nil {
				if err := fn(sess); err != nil {
					return Session{}, err
				}
			}
		}
		if err := s.Save(fixed); err != nil {
			return Session{}, err
		}
		_ = os.Remove(s.SocketPath(id)) // sweep the orphaned socket of a dead supervisor (specs/0051 D7)
		return fixed, nil
	}
	return sess, nil
}

// ListReconciled is List with the same per-session liveness pass as GetReconciled.
func (s Store) ListReconciled(now time.Time, isAlive func(int) bool, reap ...func(Session) error) ([]Session, error) {
	sessions, err := s.List()
	if err != nil {
		return nil, err
	}
	for i, sess := range sessions {
		if fixed, changed := reconcile(sess, now, isAlive); changed {
			for _, fn := range reap {
				if fn != nil {
					if err := fn(sess); err != nil {
						return nil, err
					}
				}
			}
			if err := s.Save(fixed); err != nil {
				return nil, err
			}
			_ = os.Remove(s.SocketPath(sess.ID)) // sweep the orphaned socket (specs/0051 D7)
			sessions[i] = fixed
		}
	}
	return sessions, nil
}

func (s Store) Stop(id string, revoke bool, now time.Time, revokeCredentials func(Session) error, killProcess func(int) error, reap ...func(Session) error) (Session, error) {
	sess, err := s.Get(id)
	if err != nil {
		return Session{}, err
	}
	if sess.Status == StatusStopped {
		if revoke && !sess.CredentialsRevoked {
			if err := revokeCredentials(sess); err != nil {
				return Session{}, err
			}
			sess.CredentialsRevoked = true
			sess.RevokedAt = now.UTC()
			sess.UpdatedAt = now.UTC()
			return sess, s.Save(sess)
		}
		return sess, nil
	}
	if revoke && !sess.CredentialsRevoked {
		if err := revokeCredentials(sess); err != nil {
			return Session{}, err
		}
		sess.CredentialsRevoked = true
		sess.RevokedAt = now.UTC()
	}
	if sess.PID != 0 {
		// A detached supervisor leads its own process group (specs/0051 D4): signal
		// the group (negative PID) so the boundary process tree is reached, not just
		// the supervisor. A coupled run keeps the bare-PID signal.
		target := sess.PID
		if sess.Detached {
			target = -sess.PID
		}
		if err := killProcess(target); err != nil {
			return Session{}, err
		}
	}
	_ = os.Remove(s.SocketPath(id)) // remove the per-session socket regardless (D4); no-op when coupled
	for _, fn := range reap {
		if fn != nil {
			if err := fn(sess); err != nil {
				return Session{}, err
			}
		}
	}
	sess.Status = StatusStopped
	sess.PID = 0
	sess.StoppedAt = now.UTC()
	sess.UpdatedAt = now.UTC()
	return sess, s.Save(sess)
}

// Remove permanently deletes a non-running session record so the operator can
// clear stopped/created "corpses" out of the list (the Emacs portal exposes this
// as `x`). It refuses a running session (ErrSessionRunning): stop it first. For
// any session whose credentials were never revoked, revokeCredentials is invoked
// before the record is deleted, so a removal can never orphan staged credentials
// on disk (AGENTS.md: staged credentials are wiped on exit) — once the record is
// gone there is no later handle to revoke them. reap tears down any residual
// boundary (idempotent for an already-stopped session). The per-session socket
// is swept too. Returns the removed session so callers can report what went.
func (s Store) Remove(id string, revokeCredentials func(Session) error, reap ...func(Session) error) (Session, error) {
	sess, err := s.Get(id)
	if err != nil {
		return Session{}, err
	}
	if sess.Status == StatusRunning {
		return Session{}, ErrSessionRunning
	}
	if !sess.CredentialsRevoked && revokeCredentials != nil {
		if err := revokeCredentials(sess); err != nil {
			return Session{}, err
		}
	}
	for _, fn := range reap {
		if fn != nil {
			if err := fn(sess); err != nil {
				return Session{}, err
			}
		}
	}
	_ = os.Remove(s.SocketPath(id)) // sweep any residual per-session socket
	if err := os.Remove(s.path(id)); err != nil && !os.IsNotExist(err) {
		return Session{}, err
	}
	return sess, nil
}

// PruneStopped removes every stopped session record (the "failed corpses"),
// leaving created and running sessions untouched, and returns the ids removed in
// listing order. Each removal goes through Remove, so still-live credentials are
// revoked before deletion. Callers that want a crashed session (marked running
// but whose process is gone) pruned too should ListReconciled first: that
// persists the reconciled `stopped` status this scan then matches.
func (s Store) PruneStopped(revokeCredentials func(Session) error, reap ...func(Session) error) ([]string, error) {
	sessions, err := s.List()
	if err != nil {
		return nil, err
	}
	removed := make([]string, 0, len(sessions))
	for _, sess := range sessions {
		if sess.Status != StatusStopped {
			continue
		}
		if _, err := s.Remove(sess.ID, revokeCredentials, reap...); err != nil {
			return removed, err
		}
		removed = append(removed, sess.ID)
	}
	return removed, nil
}

func (s Store) path(id string) string { return filepath.Join(s.Dir, id+".json") }

// SocketPath is where a detached session's supervisor binds its per-session unix
// socket (specs/0051 D5). Derived, never persisted, so it cannot go stale — the
// supervisor (bind), the attach client (dial), and the reconcile sweep (remove)
// all call this single function and therefore always agree.
//
// A unix socket path must fit the platform sun_path cap (104 bytes on macOS, 108
// on Linux; we use the smaller, portable budget). The natural <Dir>/<id>.sock is
// kept whenever it fits — the default state dir is ~92 bytes — so the common case
// is unchanged. When a long $SAFESLOP_STATE_DIR (or a deep test temp dir) would
// overflow, the socket is relocated to a short private runtime dir under a name
// hashed from (Dir, id), keeping it deterministic and per-id distinct.
func (s Store) SocketPath(id string) string {
	natural := filepath.Join(s.Dir, id+".sock")
	if len(natural) <= maxUnixSocketPathLen {
		return natural
	}
	sum := sha256.Sum256([]byte(s.Dir + "\x00" + id))
	return filepath.Join(socketRuntimeBase(), "safeslop-"+hex.EncodeToString(sum[:8])+".sock")
}

// maxUnixSocketPathLen is the longest socket path we will bind/dial directly. The
// macOS sockaddr_un.sun_path is 104 bytes including the NUL terminator, so 103 is
// the portable strlen ceiling (Linux's 107 is looser; the smaller budget is safe
// everywhere).
const maxUnixSocketPathLen = 103

// socketRuntimeBase is a short, per-user, private directory to relocate an
// otherwise-overflowing socket into: XDG_RUNTIME_DIR when set (Linux, 0700), else
// the OS temp dir (the per-user 0700 confinement dir on macOS). Both already exist,
// so binding never has to create them.
func socketRuntimeBase() string {
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		return d
	}
	return os.TempDir()
}

func newID() (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "sess-" + hex.EncodeToString(b[:]), nil
}

func validID(id string) bool {
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			continue
		}
		return false
	}
	return true
}
