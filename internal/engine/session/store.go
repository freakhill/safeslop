package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	workspaceboundary "github.com/freakhill/safeslop/internal/engine/workspace"
)

// Store errors are intentionally fixed and value-free: callers can map them to
// public envelopes without exposing record paths or malformed bytes.
var (
	ErrCorruptRecord   = errors.New("session record is corrupt")
	ErrStaleRecord     = errors.New("session record changed; retry the operation")
	ErrCommitUncertain = errors.New("session record commit is uncertain")
)

type Store struct {
	Dir   string
	hooks *atomicHooks
}

func NewStore(dir string) Store { return Store{Dir: dir} }

type diskRecord struct {
	Session
	RecordRevision uint64 `json:"record_revision,omitempty"`
	RuntimeID      string `json:"runtime_id,omitempty"`
	StageLayout    int    `json:"stage_layout,omitempty"`
}

func encodeRecord(sess Session) ([]byte, error) {
	record := diskRecord{Session: sess, RecordRevision: sess.recordRevision, RuntimeID: sess.runtimeID, StageLayout: sess.stageLayout}
	b, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

func decodeRecord(id string, b []byte) (Session, error) {
	var record diskRecord
	if err := json.Unmarshal(b, &record); err != nil {
		return Session{}, ErrCorruptRecord
	}
	sess := record.Session
	sess.recordRevision, sess.runtimeID, sess.stageLayout = record.RecordRevision, record.RuntimeID, record.StageLayout
	if sess.ID != id || !validID(sess.ID) {
		return Session{}, ErrCorruptRecord
	}
	switch sess.Status {
	case StatusCreated, StatusRunning, StatusStopped:
	default:
		return Session{}, ErrCorruptRecord
	}
	if !validRuntimeIdentity(sess) {
		return Session{}, ErrCorruptRecord
	}
	// A legacy on-disk "backend":"system" predates the ambient
	// multi-runtime pivot and meant the ambient Docker engine.
	if sess.Backend == "system" {
		sess.Backend = "docker"
	}
	return sess, nil
}

func validateRecord(sess Session) error {
	if sess.ID == "" || !validID(sess.ID) || !validRuntimeIdentity(sess) {
		return ErrCorruptRecord
	}
	switch sess.Status {
	case StatusCreated, StatusRunning, StatusStopped:
		return nil
	default:
		return ErrCorruptRecord
	}
}

func validRuntimeIdentity(sess Session) bool {
	switch sess.stageLayout {
	case StageLayoutLegacy:
		return sess.runtimeID == ""
	case StageLayoutSessionID:
		return sess.runtimeID == sess.ID
	default:
		return false
	}
}

func (s Store) ensureDir() error {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	return os.Chmod(s.Dir, 0o700)
}

func (s Store) path(id string) string { return filepath.Join(s.Dir, id+".json") }
func (s Store) lockPath(id string) string {
	return filepath.Join(s.Dir, ".locks", id+".lock")
}

func (s Store) withRecordLock(id string, fn func() error) error {
	if id == "" || !validID(id) {
		return ErrNotFound
	}
	if err := s.ensureDir(); err != nil {
		return err
	}
	lockDir := filepath.Join(s.Dir, ".locks")
	if err := os.MkdirAll(lockDir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(lockDir, 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(s.lockPath(id), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := f.Chmod(0o600); err != nil {
		return err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }()
	return fn()
}

func (s Store) getUnlocked(id string) (Session, error) {
	info, err := os.Lstat(s.path(id))
	if errors.Is(err, os.ErrNotExist) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, err
	}
	if !info.Mode().IsRegular() {
		return Session{}, ErrCorruptRecord
	}
	b, err := os.ReadFile(s.path(id))
	if errors.Is(err, os.ErrNotExist) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, err
	}
	return decodeRecord(id, b)
}

func (s Store) Get(id string) (Session, error) {
	if id == "" || !validID(id) {
		return Session{}, ErrNotFound
	}
	return s.getUnlocked(id)
}

func (s Store) List() ([]Session, error) {
	entries, err := os.ReadDir(s.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return []Session{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]Session, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		id := entry.Name()[:len(entry.Name())-len(".json")]
		if id == "" || !validID(id) {
			return nil, ErrCorruptRecord
		}
		sess, err := s.getUnlocked(id)
		if err != nil {
			if errors.Is(err, ErrNotFound) || errors.Is(err, ErrCorruptRecord) {
				return nil, ErrCorruptRecord
			}
			return nil, err
		}
		out = append(out, sess)
	}
	return out, nil
}

func (s Store) writeLocked(sess Session, create bool) (Session, error) {
	if err := validateRecord(sess); err != nil {
		return Session{}, err
	}
	sess.recordRevision++
	b, err := encodeRecord(sess)
	if err != nil {
		return Session{}, err
	}
	if err := writeRecordAtomic(s.path(sess.ID), b, create, s.hooks); err != nil {
		return Session{}, err
	}
	return sess, nil
}

// Save remains as a guarded compatibility boundary for callers that already
// hold a Session value. New read-modify-write code should use Update or
// WithLocked so it always starts from a fresh record under the lock.
func (s Store) Save(candidate Session) error {
	return s.withRecordLock(candidate.ID, func() error {
		current, err := s.getUnlocked(candidate.ID)
		if errors.Is(err, ErrNotFound) {
			if candidate.recordRevision != 0 {
				return ErrStaleRecord
			}
			_, err = s.writeLocked(candidate, true)
			if errors.Is(err, os.ErrExist) {
				return ErrStaleRecord
			}
			return err
		}
		if err != nil {
			return err
		}
		if candidate.recordRevision != current.recordRevision {
			return ErrStaleRecord
		}
		_, err = s.writeLocked(candidate, false)
		return err
	})
}

// Create installs a complete random-id record with no-replace semantics.
func (s Store) Create(agent, environment, workspacePath string, now time.Time) (Session, error) {
	if err := s.ensureDir(); err != nil {
		return Session{}, err
	}
	invocationDir, err := os.Getwd()
	if err != nil {
		return Session{}, workspaceboundary.ErrUnavailable
	}
	resolvedWorkspace, err := workspaceboundary.Resolve(workspacePath, "", invocationDir)
	if err != nil {
		return Session{}, err
	}
	for attempts := 0; attempts < 8; attempts++ {
		id, err := newID()
		if err != nil {
			return Session{}, err
		}
		sess := Session{
			ID: id, Agent: agent, Workspace: resolvedWorkspace, Environment: environment,
			Network: "deny", Backend: "", Status: StatusCreated,
			CreatedAt: now.UTC(), UpdatedAt: now.UTC(),
			runtimeID: id, stageLayout: StageLayoutSessionID,
		}
		var committed Session
		err = s.withRecordLock(id, func() error {
			if _, readErr := s.getUnlocked(id); readErr == nil {
				return os.ErrExist
			} else if !errors.Is(readErr, ErrNotFound) {
				return readErr
			}
			var writeErr error
			committed, writeErr = s.writeLocked(sess, true)
			return writeErr
		})
		if errors.Is(err, os.ErrExist) {
			continue
		}
		return committed, err
	}
	return Session{}, fmt.Errorf("create session id: collision limit reached")
}

// RecordTx serializes one or more durable record commits with runtime work. It
// is internal engine API: public JSON contracts never expose the revision.
type RecordTx struct {
	store   Store
	current Session
}

func (tx *RecordTx) Session() Session { return tx.current }

func (tx *RecordTx) Commit(candidate Session) error {
	if candidate.ID != tx.current.ID || candidate.recordRevision != tx.current.recordRevision {
		return ErrStaleRecord
	}
	committed, err := tx.store.writeLocked(candidate, false)
	if err != nil {
		return err
	}
	tx.current = committed
	return nil
}

func (s Store) WithLocked(id string, fn func(*RecordTx) error) (Session, error) {
	var result Session
	err := s.withRecordLock(id, func() error {
		current, err := s.getUnlocked(id)
		if err != nil {
			return err
		}
		tx := &RecordTx{store: s, current: current}
		if err := fn(tx); err != nil {
			return err
		}
		result = tx.current
		return nil
	})
	return result, err
}

func (s Store) Update(id string, fn func(Session) (Session, error)) (Session, error) {
	return s.WithLocked(id, func(tx *RecordTx) error {
		next, err := fn(tx.Session())
		if err != nil {
			return err
		}
		return tx.Commit(next)
	})
}
