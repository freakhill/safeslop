package control

import (
	"errors"
	"fmt"
	"os"
	osexec "os/exec"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/creack/pty"
)

// SessionSpec describes the agent process to run on a PTY.
type SessionSpec struct {
	Argv    []string
	Env     []string // nil => inherit
	Dir     string
	OnClose func() // optional cleanup (e.g. remove a temp sandbox profile), run once on Close/exit
}

// Session is one agent process + its host-side PTY master.
type Session struct {
	id       string
	ptmx     *os.File
	cmd      *osexec.Cmd
	onClose  func()
	exited   chan struct{}
	code     int
	closeOne sync.Once
}

func (s *Session) Read(p []byte) (int, error)        { return s.ptmx.Read(p) }
func (s *Session) Write(p []byte) (int, error)       { return s.ptmx.Write(p) }
func (s *Session) SetReadDeadline(t time.Time) error { return s.ptmx.SetReadDeadline(t) }
func (s *Session) Resize(cols, rows uint16) error {
	return pty.Setsize(s.ptmx, &pty.Winsize{Cols: cols, Rows: rows})
}
func (s *Session) Exited() <-chan struct{} { return s.exited }
func (s *Session) Code() int               { return s.code }

func (s *Session) closeProc() {
	s.closeOne.Do(func() {
		if s.cmd.Process != nil {
			_ = s.cmd.Process.Kill()
		}
		_ = s.ptmx.Close()
		if s.onClose != nil {
			s.onClose()
		}
	})
}

// Manager is a concurrency-safe registry of live sessions.
type Manager struct {
	mu   sync.Mutex
	seq  atomic.Uint64
	sess map[string]*Session
}

func NewManager() *Manager { return &Manager{sess: map[string]*Session{}} }

// Open starts spec.Argv on a fresh PTY and registers the session.
func (m *Manager) Open(spec SessionSpec) (string, error) {
	if len(spec.Argv) == 0 {
		return "", fmt.Errorf("session: empty argv")
	}
	cmd := osexec.Command(spec.Argv[0], spec.Argv[1:]...)
	cmd.Dir = spec.Dir
	if spec.Env != nil {
		cmd.Env = spec.Env
	}
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return "", fmt.Errorf("session: start pty: %w", err)
	}
	id := "s" + strconv.FormatUint(m.seq.Add(1), 10)
	s := &Session{id: id, ptmx: ptmx, cmd: cmd, onClose: spec.OnClose, exited: make(chan struct{})}
	go func() {
		err := cmd.Wait()
		s.code = exitCode(err)
		close(s.exited)
	}()
	m.mu.Lock()
	m.sess[id] = s
	m.mu.Unlock()
	return id, nil
}

func (m *Manager) Get(id string) (*Session, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sess[id]
	return s, ok
}

// Close kills the agent, frees the PTY, runs OnClose, and unregisters.
func (m *Manager) Close(id string) {
	m.mu.Lock()
	s, ok := m.sess[id]
	delete(m.sess, id)
	m.mu.Unlock()
	if ok {
		s.closeProc()
	}
}

// exitCode mirrors exec.exitCode (nil=>0, *ExitError=>code, else 1).
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *osexec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return 1
}
