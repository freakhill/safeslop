// Package session stores and transitions safeslop session metadata for the
// Emacs-facing session command surface.
package session

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	StatusCreated = "created"
	StatusRunning = "running"
	StatusStopped = "stopped"
)

var ErrNotFound = errors.New("session not found")

// Session is the durable, non-secret state for an Emacs-visible session. Do not
// add staged credential values or resolved secret material here; the JSONL status
// path serializes this object for clients.
type Session struct {
	ID                 string    `json:"session_id"`
	Agent              string    `json:"agent"`
	Workspace          string    `json:"workspace"`
	Environment        string    `json:"environment"`
	Network            string    `json:"network"`
	Status             string    `json:"status"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
	StartedAt          time.Time `json:"started_at,omitempty"`
	StoppedAt          time.Time `json:"stopped_at,omitempty"`
	RevokedAt          time.Time `json:"revoked_at,omitempty"`
	CredentialsRevoked bool      `json:"credentials_revoked"`
	PID                int       `json:"pid,omitempty"`
	ExitCode           *int      `json:"exit_code,omitempty"`
	LastError          string    `json:"last_error,omitempty"`
}

type Store struct{ Dir string }

func NewStore(dir string) Store { return Store{Dir: dir} }

func (s Store) Create(agent, workspace string, now time.Time) (Session, error) {
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
		Environment: "sandbox",
		Network:     "deny",
		Status:      StatusCreated,
		CreatedAt:   now.UTC(),
		UpdatedAt:   now.UTC(),
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
	sess, err := s.Get(id)
	if err != nil {
		return Session{}, err
	}
	if sess.Status == StatusStopped {
		return Session{}, fmt.Errorf("session stopped")
	}
	sess.Status = StatusRunning
	sess.PID = pid
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

func (s Store) Stop(id string, revoke bool, now time.Time, revokeCredentials func(Session) error, killProcess func(int) error) (Session, error) {
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
		if err := killProcess(sess.PID); err != nil {
			return Session{}, err
		}
	}
	sess.Status = StatusStopped
	sess.PID = 0
	sess.StoppedAt = now.UTC()
	sess.UpdatedAt = now.UTC()
	return sess, s.Save(sess)
}

func (s Store) path(id string) string { return filepath.Join(s.Dir, id+".json") }

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
