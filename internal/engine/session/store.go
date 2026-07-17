package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/freakhill/safeslop/internal/engine/egress"
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
	RecordRevision        uint64            `json:"record_revision,omitempty"`
	RuntimeID             string            `json:"runtime_id,omitempty"`
	StageLayout           int               `json:"stage_layout,omitempty"`
	AppliedEgressRevision int               `json:"applied_egress_revision,omitempty"`
	AppliedEgressHash     string            `json:"applied_egress_hash,omitempty"`
	EgressTransition      *EgressTransition `json:"egress_transition,omitempty"`
}

func encodeRecord(sess Session) ([]byte, error) {
	record := diskRecord{
		Session: sess, RecordRevision: sess.recordRevision, RuntimeID: sess.runtimeID, StageLayout: sess.stageLayout,
		AppliedEgressRevision: sess.appliedEgressRevision, AppliedEgressHash: sess.appliedEgressHash,
		EgressTransition: sess.egressTransition,
	}
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
	sess.appliedEgressRevision, sess.appliedEgressHash, sess.egressTransition = record.AppliedEgressRevision, record.AppliedEgressHash, record.EgressTransition
	if sess.ID != id || !validID(sess.ID) {
		return Session{}, ErrCorruptRecord
	}
	switch sess.Status {
	case StatusCreated, StatusRunning, StatusStopped:
	default:
		return Session{}, ErrCorruptRecord
	}
	if !validRuntimeIdentity(sess) || !validEgressAuthority(sess) || !validEgressRuntimeState(sess) {
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
	if sess.ID == "" || !validID(sess.ID) || !validRuntimeIdentity(sess) || !validEgressAuthority(sess) || !validEgressRuntimeState(sess) {
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

func validGrantID(id string) bool {
	if len(id) != 8 || id[:2] != "g-" {
		return false
	}
	for _, r := range id[2:] {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func sameGrant(a, b EgressGrant) bool {
	return a.ID == b.ID && a.Host == b.Host && a.Port == b.Port && a.Source == b.Source && a.CreatedAt.Equal(b.CreatedAt)
}

func validGrantList(grants []EgressGrant) bool {
	ids, destinations := map[string]bool{}, map[string]bool{}
	for _, grant := range grants {
		host, port, err := ValidateEgressGrant(grant.Host, grant.Port)
		destination := fmt.Sprintf("%s:%d", grant.Host, grant.Port)
		if err != nil || host != grant.Host || port != grant.Port || !validGrantID(grant.ID) || grant.Source != "operator" || grant.CreatedAt.IsZero() || ids[grant.ID] || destinations[destination] {
			return false
		}
		ids[grant.ID], destinations[destination] = true, true
	}
	return true
}

func validEgressAuthority(sess Session) bool {
	if sess.GrantRevision < 0 || len(sess.EgressGrants) > sess.GrantRevision || !validGrantList(sess.EgressGrants) {
		return false
	}
	if (sess.GrantRevision != 0 || len(sess.PersistentEgress) != 0 || len(sess.EgressAcknowledgements) != 0) && !CanGrant(sess) {
		return false
	}
	destinations := map[string]bool{}
	for _, rule := range sess.PersistentEgress {
		host, port, err := ValidateEgressGrant(rule.FQDN, rule.Port)
		key := fmt.Sprintf("%s:%d", rule.FQDN, rule.Port)
		if err != nil || host != rule.FQDN || port != rule.Port || destinations[key] {
			return false
		}
		destinations[key] = true
	}
	for _, acknowledgement := range sess.EgressAcknowledgements {
		host, port, err := ValidateEgressGrant(acknowledgement.Host, acknowledgement.Port)
		key := fmt.Sprintf("%s:%d", acknowledgement.Host, acknowledgement.Port)
		if err != nil || host != acknowledgement.Host || port != acknowledgement.Port || acknowledgement.AcknowledgedAt.IsZero() || destinations["ack:"+key] {
			return false
		}
		destinations["ack:"+key] = true
	}
	return true
}

func sameGrantList(a, b []EgressGrant) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !sameGrant(a[i], b[i]) {
			return false
		}
	}
	return true
}

func isOneGrantNarrower(active, candidate []EgressGrant) bool {
	if len(candidate)+1 != len(active) {
		return false
	}
	candidateAt := 0
	removed := false
	for _, grant := range active {
		if candidateAt < len(candidate) && sameGrant(grant, candidate[candidateAt]) {
			candidateAt++
			continue
		}
		if removed {
			return false
		}
		removed = true
	}
	return removed && candidateAt == len(candidate)
}

func canonicalEgressGeneration(sess Session, grants []EgressGrant, revision int) (egress.Generation, bool) {
	destinations := make([]egress.Destination, 0, len(sess.PersistentEgress)+len(grants))
	seen := make(map[struct {
		host string
		port int
	}]struct{}, cap(destinations))
	add := func(host string, port int) {
		key := struct {
			host string
			port int
		}{host: host, port: port}
		if _, duplicate := seen[key]; duplicate {
			return
		}
		seen[key] = struct{}{}
		destinations = append(destinations, egress.Destination{Host: host, Port: port})
	}
	for _, rule := range sess.PersistentEgress {
		add(rule.FQDN, rule.Port)
	}
	for _, grant := range grants {
		add(grant.Host, grant.Port)
	}
	generation, _, err := egress.Build(destinations, revision)
	return generation, err == nil
}

func validEgressRuntimeState(sess Session) bool {
	if sess.appliedEgressRevision < 0 || (sess.appliedEgressHash != "" && !validSHA256(sess.appliedEgressHash)) {
		return false
	}
	if sess.appliedEgressHash == "" && sess.appliedEgressRevision != 0 {
		return false
	}
	durableGeneration, valid := canonicalEgressGeneration(sess, sess.EgressGrants, sess.GrantRevision)
	if !valid {
		return false
	}
	transition := sess.egressTransition
	if transition == nil {
		return sess.appliedEgressHash == "" || (sess.appliedEgressRevision == sess.GrantRevision && sess.appliedEgressHash == durableGeneration.Hash)
	}
	if sess.appliedEgressHash == "" || transition.CandidateRevision < 0 || !validSHA256(transition.CandidateHash) || !validGrantList(transition.CandidateGrants) {
		return false
	}
	candidateGeneration, valid := canonicalEgressGeneration(sess, transition.CandidateGrants, transition.CandidateRevision)
	if !valid || transition.CandidateHash != candidateGeneration.Hash {
		return false
	}
	switch transition.Direction {
	case EgressDirectionWiden:
		if transition.CandidateRevision != sess.GrantRevision || sess.appliedEgressRevision+1 != sess.GrantRevision || !sameGrantList(transition.CandidateGrants, sess.EgressGrants) || len(sess.EgressGrants) == 0 {
			return false
		}
		oldGeneration, valid := canonicalEgressGeneration(sess, sess.EgressGrants[:len(sess.EgressGrants)-1], sess.appliedEgressRevision)
		return valid && candidateGeneration == durableGeneration && sess.appliedEgressHash == oldGeneration.Hash
	case EgressDirectionNarrow:
		return sess.appliedEgressRevision == sess.GrantRevision && transition.CandidateRevision == sess.GrantRevision+1 && isOneGrantNarrower(sess.EgressGrants, transition.CandidateGrants) && sess.appliedEgressHash == durableGeneration.Hash
	default:
		return false
	}
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
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
