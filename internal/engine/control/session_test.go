package control

import (
	"bytes"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

func TestSessionEchoesAndCloses(t *testing.T) {
	m := NewManager()
	// `cat` echoes its input back through the PTY.
	id, err := m.Open(SessionSpec{Argv: []string{"cat"}})
	if err != nil {
		t.Fatal(err)
	}
	s, ok := m.Get(id)
	if !ok {
		t.Fatal("session not registered")
	}
	if _, err := s.Write([]byte("hello\n")); err != nil {
		t.Fatal(err)
	}
	got := readWithin(t, s, 2*time.Second, []byte("hello"))
	if !bytes.Contains(got, []byte("hello")) {
		t.Fatalf("echo not seen: %q", got)
	}
	m.Close(id)
	if _, ok := m.Get(id); ok {
		t.Fatal("session must be unregistered after Close")
	}
	select {
	case <-s.Exited():
	case <-time.After(2 * time.Second):
		t.Fatal("agent did not exit after Close")
	}
}

func TestOpenAssignsUniqueIDs(t *testing.T) {
	m := NewManager()
	a, _ := m.Open(SessionSpec{Argv: []string{"cat"}})
	b, _ := m.Open(SessionSpec{Argv: []string{"cat"}})
	defer m.Close(a)
	defer m.Close(b)
	if a == b || a == "" {
		t.Fatalf("ids must be unique and non-empty: %q %q", a, b)
	}
}

func TestOpenEmptyArgvErrors(t *testing.T) {
	if _, err := NewManager().Open(SessionSpec{}); err == nil {
		t.Fatal("empty argv must error")
	}
}

// readWithin reads from s until want is seen or the deadline passes.
func readWithin(t *testing.T, s *Session, d time.Duration, want []byte) []byte {
	t.Helper()
	deadline := time.Now().Add(d)
	var acc []byte
	buf := make([]byte, 1024)
	for time.Now().Before(deadline) {
		s.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		n, err := s.Read(buf)
		acc = append(acc, buf[:n]...)
		if bytes.Contains(acc, want) {
			return acc
		}
		if err != nil && err != io.EOF && !isTimeout(err) {
			return acc
		}
	}
	return acc
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

func TestManagerHandlesConcurrentSessions(t *testing.T) {
	m := NewManager()
	const n = 8
	ids := make([]string, n)
	for i := range ids {
		id, err := m.Open(SessionSpec{Argv: []string{"cat"}})
		if err != nil {
			t.Fatal(err)
		}
		ids[i] = id
	}
	// each session is independent: write a distinct marker, read it back.
	for i, id := range ids {
		s, _ := m.Get(id)
		marker := []byte("sess" + string(rune('A'+i)) + "\n")
		if _, err := s.Write(marker); err != nil {
			t.Fatal(err)
		}
		got := readWithin(t, s, 2*time.Second, marker[:len(marker)-1])
		if !bytes.Contains(got, marker[:len(marker)-1]) {
			t.Fatalf("session %s cross-talk or loss: got %q want %q", id, got, marker)
		}
	}
	for _, id := range ids {
		m.Close(id)
	}
	for _, id := range ids {
		if _, ok := m.Get(id); ok {
			t.Fatalf("session %s leaked after Close", id)
		}
	}
}
