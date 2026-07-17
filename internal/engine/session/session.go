// Package session stores and transitions safeslop session metadata for the
// Emacs-facing session command surface.
package session

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/freakhill/safeslop/internal/engine/policy"
)

const (
	StatusCreated = "created"
	StatusRunning = "running"
	StatusStopped = "stopped"

	StageLayoutLegacy    = 0
	StageLayoutSessionID = 2
)

var ErrNotFound = errors.New("session not found")

// ErrSessionRunning rejects any operation that would replace or remove a live
// session owner. The existing boundary must be stopped first.
var ErrSessionRunning = errors.New("session is running")

// ErrSessionStopped rejects reuse of a terminal one-shot session record. A new
// run needs a new random session identity rather than reviving old ownership.
var ErrSessionStopped = errors.New("session is stopped")

// ResolvedMetadata snapshots the non-secret package resolution that selected a
// profile-backed session's image. It is stored with the session so status/list can
// keep portal Recipe/Image columns stable even after the session leaves the
// creating command's process.
type ResolvedMetadata struct {
	Packages      []string `json:"packages,omitempty"`
	IdentitySet   []string `json:"identitySet,omitempty"`
	RuntimeEgress []string `json:"runtimeEgress,omitempty"`
}

// CredentialScope is one value-free row describing a credential a profile-backed
// session stages: the provider Kind (github/forgejo/pnpm/aws/gcp/kube), the
// non-secret target Name (repo, registry host, cloud profile, cluster), and
// access/mode/ttl Scope text. It deliberately carries no token value, secret ref
// (op://, env:), staged file path, or account private-key ref — only what an
// operator needs to answer "which credentials?" at a glance (specs/0086 T1).
type CredentialScope struct {
	Kind  string `json:"kind"`
	Name  string `json:"name"`
	Scope string `json:"scope"`
}

// CredentialLease is the additive, value-free renewal snapshot. It deliberately excludes token
// values, account-link refs, key IDs, provider responses, and stage paths.
type CredentialLease struct {
	Provider           string    `json:"provider"`
	State              string    `json:"state"`
	Reason             string    `json:"reason,omitempty"`
	CurrentExpiresAt   time.Time `json:"current_expires_at,omitempty"`
	Horizon            time.Time `json:"horizon,omitempty"`
	GithubMinExpiresAt time.Time `json:"github_min_expires_at,omitempty"`
	GithubPartitions   int       `json:"github_partitions,omitempty"`
}

// Session is the durable, non-secret state for an Emacs-visible session. Do not
// add staged credential values or resolved secret material here; the JSONL status
// path serializes this object for clients.
// Failure is a versioned, value-free operator explanation for a terminal session
// failure.  Its summary and action must be selected by engine-owned code: never
// put raw OS errors, resolved paths, command output, or secret material here.
type Failure struct {
	Version    int    `json:"version"`
	Phase      string `json:"phase"`
	Code       string `json:"code"`
	Projection string `json:"projection,omitempty"`
	Source     string `json:"source,omitempty"`
	Required   bool   `json:"required"`
	Summary    string `json:"summary"`
	Action     string `json:"action"`
}

const maxLastErrorBytes = 240

// SetFailure records the structured reason and its bounded compatibility summary.
func (s *Session) SetFailure(f Failure) {
	s.LastFailure = &f
	s.LastError = f.Summary
	if len(s.LastError) > maxLastErrorBytes {
		s.LastError = s.LastError[:maxLastErrorBytes]
	}
}

type Session struct {
	ID            string            `json:"session_id"`
	Profile       string            `json:"profile,omitempty"`
	ProfileSource string            `json:"profile_source,omitempty"`
	Name          string            `json:"name,omitempty"`
	Agent         string            `json:"agent"`
	Workspace     string            `json:"workspace"`
	Environment   string            `json:"environment"`
	Network       string            `json:"network"`
	Backend       string            `json:"backend"`
	RecipeID      string            `json:"recipeID,omitempty"`
	Image         string            `json:"image,omitempty"`
	Resolved      *ResolvedMetadata `json:"resolved,omitempty"`
	// CredentialScopes is the value-free credential legibility array for a
	// profile-backed session, computed from the trusted policy.Profile at create
	// time and surfaced by session create/list/status. Empty (omitted) for ad-hoc
	// sessions and profiles without credentials (specs/0086 T1).
	CredentialScopes   []CredentialScope `json:"credential_scopes,omitempty"`
	CredentialLease    *CredentialLease  `json:"credential_lease,omitempty"`
	Status             string            `json:"status"`
	CreatedAt          time.Time         `json:"created_at"`
	UpdatedAt          time.Time         `json:"updated_at"`
	StartedAt          time.Time         `json:"started_at,omitempty"`
	StoppedAt          time.Time         `json:"stopped_at,omitempty"`
	RevokedAt          time.Time         `json:"revoked_at,omitempty"`
	CredentialsRevoked bool              `json:"credentials_revoked"`
	PID                int               `json:"pid,omitempty"`
	// ProcessToken is a non-secret kernel process-start/generation token paired with PID.
	// New sessions record it when the platform exposes one so liveness and stop can detect
	// PID reuse before signalling a stale detached process group (specs/0077 M3). Legacy
	// records leave it empty and fall back to signal-0 liveness.
	ProcessToken string   `json:"process_token,omitempty"`
	ExitCode     *int     `json:"exit_code,omitempty"`
	LastError    string   `json:"last_error,omitempty"`
	LastFailure  *Failure `json:"last_failure,omitempty"`
	// PolicyPath/PolicyHash pin the safeslop.cue that was host-approved when a profile
	// session was created: the canonical (symlink-free, absolute) path and the sha256 of the
	// approved bytes. run/supervise rebuild the profile from this record and never re-read the
	// cue, so they re-verify this exact approval is still current before launch — a create-time
	// trust can't be defeated by editing or revoking the policy afterward (specs/0072 F1, 0070
	// B1/B3). Both empty for ad-hoc (--agent) sessions, which carry no policy file. Non-secret.
	PolicyPath string `json:"policy_path,omitempty"`
	PolicyHash string `json:"policy_hash,omitempty"`
	// EgressGrants are the operator-invoked, session-scoped, exact FQDN:port network grants for a
	// container deny session (specs/0089/0097). Runtime overlay state only — never mutates profile.
	// egress policy; revoked with the session. Empty (omitted) for host/allow sessions and sessions
	// with no grants.
	// PersistentEgress is the normalized exact-rule snapshot from a trusted
	// profile at session creation. It is deliberately separate from the mutable
	// session overlay, so later grant/revoke operations cannot erase durable
	// authority already captured for this session (specs/0103).
	PersistentEgress []policy.PersistentEgressRule `json:"persistent_egress,omitempty"`
	EgressGrants     []EgressGrant                 `json:"egress_grants,omitempty"`
	// EgressAcknowledgements only suppress already-seen review rows through the
	// recorded time. They are not grants, never affect proxy policy, and later
	// denied traffic becomes visible again (specs/0103).
	EgressAcknowledgements []EgressAcknowledgement `json:"egress_acknowledgements,omitempty"`
	GrantRevision          int                     `json:"egress_grant_revision,omitempty"`
	// Detached marks a session whose recorded PID is a detached supervisor that
	// leads its own process group, so `stop` signals the group, not a bare PID
	// (specs/0051 D4). Internal routing state; not surfaced in the JSON envelope.
	Detached bool `json:"detached,omitempty"`

	// These fields are persisted only by diskRecord. Keeping them unexported
	// ensures direct Session JSON and the v1 client envelope never gain internal
	// concurrency or runtime-routing state.
	recordRevision        uint64
	runtimeID             string
	stageLayout           int
	appliedEgressRevision int
	appliedEgressHash     string
	egressTransition      *EgressTransition
}

// RuntimeIdentity returns the internal exact ownership id and stage layout.
// Legacy records return ("", StageLayoutLegacy) and keep their historical
// workspace-hashed reconstruction.
func (s Session) RuntimeIdentity() (string, int) { return s.runtimeID, s.stageLayout }

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
	if pid <= 0 {
		return Session{}, errors.New("session process identity is invalid")
	}
	return s.Update(id, func(sess Session) (Session, error) {
		switch sess.Status {
		case StatusCreated:
		case StatusRunning:
			return Session{}, ErrSessionRunning
		default:
			return Session{}, ErrSessionStopped
		}
		sess.Status = StatusRunning
		sess.PID = pid
		sess.ProcessToken = ""
		if token, ok := ProcessStartToken(pid); ok {
			sess.ProcessToken = token
		}
		sess.Detached = detached
		sess.StartedAt = now.UTC()
		sess.UpdatedAt = now.UTC()
		return sess, nil
	})
}

// HandoffRunningDetached atomically replaces the issuing wrapper claim with the
// child supervisor identity. It cannot adopt an unrelated/stale running record.
func (s Store) HandoffRunningDetached(id string, parentPID, supervisorPID int, now time.Time) (Session, error) {
	if parentPID <= 0 || supervisorPID <= 0 {
		return Session{}, errors.New("session process identity is invalid")
	}
	return s.Update(id, func(sess Session) (Session, error) {
		if sess.Status != StatusRunning || sess.Detached || sess.PID != parentPID || !ProcessAliveSession(sess) {
			return Session{}, ErrStaleRecord
		}
		sess.PID = supervisorPID
		sess.ProcessToken = ""
		if token, ok := ProcessStartToken(supervisorPID); ok {
			sess.ProcessToken = token
		}
		sess.Detached = true
		sess.UpdatedAt = now.UTC()
		return sess, nil
	})
}

// ReleaseRunningClaim returns a failed pre-supervisor claim to created only
// when it still belongs to this exact live issuing process.
func (s Store) ReleaseRunningClaim(id string, pid int, now time.Time) (Session, error) {
	return s.Update(id, func(sess Session) (Session, error) {
		if sess.Status != StatusRunning || sess.Detached || sess.PID != pid || !ProcessAliveSession(sess) {
			return Session{}, ErrStaleRecord
		}
		sess.Status = StatusCreated
		sess.PID = 0
		sess.ProcessToken = ""
		sess.Detached = false
		sess.StartedAt = time.Time{}
		sess.UpdatedAt = now.UTC()
		return sess, nil
	})
}

func (s Store) Finish(id string, exitCode int, lastErr string, now time.Time, failure ...Failure) (Session, error) {
	return s.Update(id, func(sess Session) (Session, error) {
		sess.Status = StatusStopped
		sess.PID = 0
		sess.ProcessToken = ""
		sess.SetEgressRuntimeState(EgressRuntimeState{})
		sess.ExitCode = &exitCode
		sess.LastFailure = nil
		if len(failure) > 0 {
			sess.SetFailure(failure[0])
		} else {
			sess.LastError = lastErr
		}
		sess.StoppedAt = now.UTC()
		sess.UpdatedAt = now.UTC()
		return sess, nil
	})
}

// maxNameRunes caps a display name post-trim. 64 runes is ample for a human
// label; note a wide (CJK/emoji) rune is ~2 terminal cells, so the portal must
// still truncate for display (specs/0065 N1) — this is a storage cap, not a
// width cap.
const maxNameRunes = 64

// ValidateName cleans and checks an optional human display name. It is a pure
// function (no I/O) so the CLI can reuse the exact same rule at create time and
// at rename. It trims surrounding whitespace, returns ("", nil) for an
// empty/whitespace-only input (meaning "no name" / clear), and otherwise returns
// the trimmed name.
//
// It rejects any rune in Unicode categories Cc (controls), Cf (format), Zl, or
// Zp. The name is echoed into the JSONL status line and rendered in a terminal /
// Emacs buffer, so this closes a line-protocol + display-spoof hazard: Cc covers
// newlines/NUL/DEL that would break the one-envelope-per-line protocol, and Cf
// covers the bidi overrides (U+202A-202E/U+2066-2069) and zero-width chars
// behind Trojan Source (CVE-2021-42574) — an RLO could make a stopped session
// render as running, and zero-width chars make two names visually identical.
func ValidateName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", nil
	}
	for _, r := range name {
		if unicode.In(r, unicode.Cc, unicode.Cf, unicode.Zl, unicode.Zp) {
			return "", fmt.Errorf("name contains a disallowed control, format, or separator character (U+%04X)", r)
		}
	}
	if utf8.RuneCountInString(name) > maxNameRunes {
		return "", fmt.Errorf("name exceeds %d characters", maxNameRunes)
	}
	return name, nil
}

// Rename sets (or, with an empty name, clears) a session's display name. The
// name is validated with ValidateName, whose error is returned unchanged so the
// CLI can map it to INVALID_ARGUMENT. There is no status guard: a label touches
// no boundary, credential, or process state, so a rename is allowed in any
// status — created, running, or stopped (specs/0065 D5). ErrNotFound from Get is
// preserved for an unknown id.
func (s Store) Rename(id, name string, now time.Time) (Session, error) {
	name, err := ValidateName(name)
	if err != nil {
		return Session{}, err
	}
	return s.Update(id, func(sess Session) (Session, error) {
		sess.Name = name
		sess.UpdatedAt = now.UTC()
		return sess, nil
	})
}

// ProcessStartToken returns a non-secret kernel process-start/generation token for pid
// when the platform exposes one. It is paired with PID to distinguish the original
// safeslop wrapper/supervisor from a later process that reused the same number.
func ProcessStartToken(pid int) (string, bool) { return processStartToken(pid) }

// ProcessAlive reports whether pid names a live process. On macOS/unix, signal 0
// succeeds for a live process we own, and EPERM means the process is alive but
// owned by another user.
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

// ProcessAliveSession reports whether sess still points at the same live process
// recorded by MarkRunning/MarkRunningDetached. Records without a process token are
// legacy/unsupported-platform sessions and fall back to signal-0 PID liveness.
func ProcessAliveSession(sess Session) bool {
	if sess.ProcessToken == "" {
		return ProcessAlive(sess.PID)
	}
	token, ok := ProcessStartToken(sess.PID)
	return ok && token == sess.ProcessToken
}

// reconcile corrects sess for liveness. In the coupled lifecycle the run wrapper
// holds the agent for the whole run; in the detached lifecycle the recorded PID is
// the supervisor/process-group leader. If that recorded process is no longer alive
// — or its process-start token no longer matches, meaning the PID was reused — the
// run ended without recording an exit (crash, SIGKILL, host sleep): report it as
// stopped. Pure given isAlive; the bool reports whether sess changed so the caller
// can persist exactly once.
func reconcile(sess Session, now time.Time, isAlive func(Session) bool) (Session, bool) {
	if sess.Status != StatusRunning || sess.PID <= 0 || isAlive(sess) {
		return sess, false
	}
	sess.Status = StatusStopped
	sess.PID = 0
	sess.ProcessToken = ""
	sess.SetEgressRuntimeState(EgressRuntimeState{})
	sess.LastError = "run process exited without recording status"
	sess.StoppedAt = now.UTC()
	sess.UpdatedAt = now.UTC()
	return sess, true
}

// GetReconciled is Get plus a liveness pass: a session marked running whose PID
// is dead/stale is persisted and returned as stopped, so status never lies.
func (s Store) GetReconciled(id string, now time.Time, isAlive func(Session) bool, reap ...func(Session) error) (Session, error) {
	return s.WithLocked(id, func(tx *RecordTx) error {
		sess := tx.Session()
		fixed, changed := reconcile(sess, now, isAlive)
		if !changed {
			return nil
		}
		for _, fn := range reap {
			if fn != nil {
				if err := fn(sess); err != nil {
					return err
				}
			}
		}
		if err := s.removeSocketFiles(id); err != nil {
			return err
		}
		return tx.Commit(fixed)
	})
}

// ListReconciled is List with the same per-session liveness pass as GetReconciled.
func (s Store) ListReconciled(now time.Time, isAlive func(Session) bool, reap ...func(Session) error) ([]Session, error) {
	snapshot, err := s.List()
	if err != nil {
		return nil, err
	}
	sessions := make([]Session, 0, len(snapshot))
	for _, sess := range snapshot {
		current, err := s.GetReconciled(sess.ID, now, isAlive, reap...)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, current)
	}
	return sessions, nil
}

func (s Store) Stop(id string, revoke bool, now time.Time, revokeCredentials func(Session) error, killProcess func(int) error, processAlive func(Session) bool, reap ...func(Session) error) (Session, error) {
	return s.WithLocked(id, func(tx *RecordTx) error {
		sess := tx.Session()
		if sess.Status == StatusStopped {
			if revoke && !sess.CredentialsRevoked {
				if err := revokeCredentials(sess); err != nil {
					return err
				}
				sess.CredentialsRevoked = true
				sess.RevokedAt = now.UTC()
				sess.UpdatedAt = now.UTC()
				return tx.Commit(sess)
			}
			return nil
		}
		if revoke && !sess.CredentialsRevoked {
			if err := revokeCredentials(sess); err != nil {
				return err
			}
			sess.CredentialsRevoked = true
			sess.RevokedAt = now.UTC()
		}
		if sess.PID != 0 && processAlive != nil && processAlive(sess) {
			// Recheck the PID/process-start token while the record lock is held,
			// immediately before signalling. The command's earlier reconcile check
			// cannot authorize a PID that exited and was reused before Stop acquired
			// this lock. A nil verifier fails closed and never signals.
			//
			// A detached supervisor leads its own process group (specs/0051 D4): signal
			// the group (negative PID) so the boundary process tree is reached, not just
			// the supervisor. A coupled run keeps the bare-PID signal.
			target := sess.PID
			if sess.Detached {
				target = -sess.PID
			}
			if err := killProcess(target); err != nil {
				return err
			}
		}
		if err := s.removeSocketFiles(id); err != nil {
			return err
		}
		for _, fn := range reap {
			if fn != nil {
				if err := fn(sess); err != nil {
					return err
				}
			}
		}
		sess.Status = StatusStopped
		sess.PID = 0
		sess.ProcessToken = ""
		sess.SetEgressRuntimeState(EgressRuntimeState{})
		sess.StoppedAt = now.UTC()
		sess.UpdatedAt = now.UTC()
		return tx.Commit(sess)
	})
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
	var removed Session
	err := s.withRecordLock(id, func() error {
		sess, err := s.getUnlocked(id)
		if err != nil {
			return err
		}
		if sess.Status == StatusRunning {
			return ErrSessionRunning
		}
		if !sess.CredentialsRevoked && revokeCredentials != nil {
			if err := revokeCredentials(sess); err != nil {
				return err
			}
		}
		for _, fn := range reap {
			if fn != nil {
				if err := fn(sess); err != nil {
					return err
				}
			}
		}
		if err := s.removeSocketFiles(id); err != nil {
			return err
		}
		if err := removeRecordAtomic(s.path(id), s.hooks); err != nil {
			return err
		}
		removed = sess
		return nil
	})
	return removed, err
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
	return filepath.Join(socketRuntimeBase(), s.socketFileName(id))
}

func (s Store) socketFileName(id string) string {
	sum := sha256.Sum256([]byte(s.Dir + "\x00" + id))
	return "safeslop-" + hex.EncodeToString(sum[:8]) + ".sock"
}

// SocketPaths returns the current socket path followed by the pre-0115 overflow
// location when they differ. Attach and cleanup retain compatibility with an
// already-running supervisor while new supervisors bind only in the private dir.
func (s Store) SocketPaths(id string) []string {
	current := s.SocketPath(id)
	legacy := filepath.Join(legacySocketRuntimeBase(), s.socketFileName(id))
	if current == legacy || len(filepath.Join(s.Dir, id+".sock")) <= maxUnixSocketPathLen {
		return []string{current}
	}
	return []string{current, legacy}
}

// EnsureSocketDir creates and verifies the exact parent used by SocketPath.
// The ownership/mode checks make the relocation directory a capability boundary
// rather than a predictable name directly in a shared sticky directory.
func (s Store) EnsureSocketDir(id string) error {
	dir := filepath.Dir(s.SocketPath(id))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || int(stat.Uid) != os.Getuid() {
		return errors.New("session socket directory is not private")
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	return nil
}

func (s Store) removeSocketFiles(id string) error {
	for index, path := range s.SocketPaths(id) {
		if index > 0 {
			// The compatibility candidate may live directly in a shared temp
			// directory. Never let an unrelated/foreign object at that legacy
			// name block current-session reconcile or become cleanup authority.
			info, err := os.Lstat(path)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return err
			}
			stat, ok := info.Sys().(*syscall.Stat_t)
			if !ok || info.Mode()&os.ModeSocket == 0 || int(stat.Uid) != os.Getuid() {
				continue
			}
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// maxUnixSocketPathLen is the longest socket path we will bind/dial directly. The
// macOS sockaddr_un.sun_path is 104 bytes including the NUL terminator, so 103 is
// the portable strlen ceiling (Linux's 107 is looser; the smaller budget is safe
// everywhere).
const maxUnixSocketPathLen = 103

// socketRuntimeBase is a short private per-user directory. XDG_RUNTIME_DIR is
// already per-user; the fallback adds an owned 0700 child beneath shared /tmp.
func socketRuntimeBase() string {
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		candidate := filepath.Join(d, "safeslop")
		if len(filepath.Join(candidate, "safeslop-0000000000000000.sock")) <= maxUnixSocketPathLen {
			return candidate
		}
	}
	return filepath.Join("/tmp", fmt.Sprintf("ss-%d", os.Getuid()))
}

// legacySocketRuntimeBase reproduces the pre-0115 derived location for attach
// and cleanup of a supervisor already running during upgrade.
func legacySocketRuntimeBase() string {
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
