package session

import (
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/freakhill/safeslop/internal/engine/session/wire"
)

// readWriter joins a separate reader and writer into one io.ReadWriter, standing
// in for a PTY master (read = agent output, write = agent input) without a real
// terminal so the proxy/resize tests stay fully in-memory.
type readWriter struct {
	r io.Reader
	w io.Writer
}

func (rw readWriter) Read(p []byte) (int, error)  { return rw.r.Read(p) }
func (rw readWriter) Write(p []byte) (int, error) { return rw.w.Write(p) }

type bridgeRet struct {
	outcome Outcome
	err     error
}

func TestBridgeProxiesBytesBothWays(t *testing.T) {
	clientConn, bridgeConn := net.Pipe()
	agentOutR, agentOutW := io.Pipe() // agent stdout: test writes, bridge reads
	agentInR, agentInW := io.Pipe()   // agent stdin: bridge writes, test reads
	ptmx := readWriter{r: agentOutR, w: agentInW}
	t.Cleanup(func() {
		clientConn.Close()
		bridgeConn.Close()
		agentOutW.Close()
		agentInW.Close()
	})

	ret := make(chan bridgeRet, 1)
	go func() {
		o, e := Bridge(bridgeConn, ptmx, nil, func() int { return 0 })
		ret <- bridgeRet{o, e}
	}()

	// client -> agent: a Data frame in becomes raw bytes on the PTY write side.
	go func() { _ = wire.Write(clientConn, wire.DataFrame([]byte("to-agent"))) }()
	got := make([]byte, len("to-agent"))
	if _, err := io.ReadFull(agentInR, got); err != nil {
		t.Fatalf("read agent input: %v", err)
	}
	if string(got) != "to-agent" {
		t.Fatalf("agent input = %q, want %q", got, "to-agent")
	}

	// agent -> client: raw PTY output becomes a Data frame on the conn.
	go func() { _, _ = agentOutW.Write([]byte("from-agent")) }()
	f, err := wire.Read(clientConn)
	if err != nil {
		t.Fatalf("read client frame: %v", err)
	}
	if f.Type != wire.Data || string(f.Data) != "from-agent" {
		t.Fatalf("client frame = %+v, want Data %q", f, "from-agent")
	}

	// Client disconnects while the agent is still alive: ClientGone, no exit frame.
	clientConn.Close()
	select {
	case r := <-ret:
		if r.outcome != ClientGone {
			t.Fatalf("outcome = %v, want ClientGone", r.outcome)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Bridge did not return after client disconnect")
	}
}

func TestBridgeAppliesResizeFrame(t *testing.T) {
	clientConn, bridgeConn := net.Pipe()
	agentOutR, agentOutW := io.Pipe()
	_, agentInW := io.Pipe()
	ptmx := readWriter{r: agentOutR, w: agentInW}
	t.Cleanup(func() {
		clientConn.Close()
		bridgeConn.Close()
		agentOutW.Close()
	})

	type size struct{ rows, cols uint16 }
	applied := make(chan size, 1)
	onResize := func(rows, cols uint16) error {
		applied <- size{rows, cols}
		return nil
	}

	go func() { _, _ = Bridge(bridgeConn, ptmx, onResize, func() int { return 0 }) }()

	go func() { _ = wire.Write(clientConn, wire.ResizeFrame(48, 200)) }()
	select {
	case s := <-applied:
		if s.rows != 48 || s.cols != 200 {
			t.Fatalf("resize = %dx%d, want 48x200", s.rows, s.cols)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("resize frame was not applied via onResize")
	}
}

func TestBridgeEmitsExitFrameWithCode(t *testing.T) {
	clientConn, bridgeConn := net.Pipe()
	cmd := helperCommand("exit42")
	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatalf("pty.Start: %v", err)
	}
	t.Cleanup(func() {
		_ = ptmx.Close()
		clientConn.Close()
		bridgeConn.Close()
	})

	waitExit := func() int { return exitCodeOf(cmd.Wait()) }

	ret := make(chan bridgeRet, 1)
	go func() {
		o, e := Bridge(bridgeConn, ptmx, nil, waitExit)
		ret <- bridgeRet{o, e}
	}()

	// The child writes nothing and exits 42; the bridge must surface that as an X
	// frame. Tolerate any incidental Data frames before it.
	deadline := time.After(5 * time.Second)
	for {
		type rf struct {
			f   wire.Frame
			err error
		}
		got := make(chan rf, 1)
		go func() { f, e := wire.Read(clientConn); got <- rf{f, e} }()
		select {
		case r := <-got:
			if r.err != nil {
				t.Fatalf("reading frames: %v", r.err)
			}
			if r.f.Type == wire.Exit {
				if r.f.Code != 42 {
					t.Fatalf("exit code = %d, want 42", r.f.Code)
				}
				goto exited
			}
		case <-deadline:
			t.Fatal("no exit frame within deadline")
		}
	}
exited:
	select {
	case r := <-ret:
		if r.outcome != ChildExited {
			t.Fatalf("outcome = %v, want ChildExited", r.outcome)
		}
		if r.err != nil {
			t.Fatalf("Bridge err = %v, want nil on clean child exit", r.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Bridge did not return after child exit")
	}
}

func TestAttachProxiesBytesAndReturnsExitCode(t *testing.T) {
	clientConn, serverConn := net.Pipe() // serverConn plays the supervisor
	stdinR, stdinW := io.Pipe()          // test -> local stdin
	stdoutR, stdoutW := io.Pipe()        // local stdout -> test
	resize := make(chan [2]uint16, 1)
	t.Cleanup(func() {
		clientConn.Close()
		serverConn.Close()
		stdinW.Close()
		stdoutW.Close()
	})

	type attachRet struct {
		code int
		err  error
	}
	ret := make(chan attachRet, 1)
	go func() {
		c, e := Attach(clientConn, stdinR, stdoutW, resize)
		ret <- attachRet{c, e}
	}()

	// local stdin -> Data frame on the wire
	go func() { _, _ = stdinW.Write([]byte("keystrokes")) }()
	f, err := wire.Read(serverConn)
	if err != nil {
		t.Fatalf("read server frame: %v", err)
	}
	if f.Type != wire.Data || string(f.Data) != "keystrokes" {
		t.Fatalf("server frame = %+v, want Data %q", f, "keystrokes")
	}

	// Data frame from the supervisor -> local stdout
	go func() { _ = wire.Write(serverConn, wire.DataFrame([]byte("rendered"))) }()
	out := make([]byte, len("rendered"))
	if _, err := io.ReadFull(stdoutR, out); err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	if string(out) != "rendered" {
		t.Fatalf("stdout = %q, want %q", out, "rendered")
	}

	// SIGWINCH-style resize -> R frame on the wire
	resize <- [2]uint16{30, 100}
	rf, err := wire.Read(serverConn)
	if err != nil {
		t.Fatalf("read resize frame: %v", err)
	}
	if rf.Type != wire.Resize || rf.Rows != 30 || rf.Cols != 100 {
		t.Fatalf("resize frame = %+v, want Resize 30x100", rf)
	}

	// X frame -> Attach returns the agent's code.
	go func() { _ = wire.Write(serverConn, wire.ExitFrame(42)) }()
	select {
	case r := <-ret:
		if r.err != nil {
			t.Fatalf("Attach err = %v, want nil", r.err)
		}
		if r.code != 42 {
			t.Fatalf("Attach code = %d, want 42", r.code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Attach did not return after exit frame")
	}
}

func exitCodeOf(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

// helperCommand re-execs the test binary as a controllable child, the standard
// os/exec hermetic-child pattern. It avoids depending on /bin/sh so the exit
// test runs the same everywhere and carries no shell.
func helperCommand(behavior string) *exec.Cmd {
	cmd := exec.Command(os.Args[0], "-test.run=^TestBridgeHelperProcess$")
	cmd.Env = append(os.Environ(), "GO_BRIDGE_HELPER="+behavior)
	return cmd
}

// TestBridgeHelperProcess is not a real test: when GO_BRIDGE_HELPER is set it is
// the child spawned by helperCommand and exits with the requested behavior.
func TestBridgeHelperProcess(t *testing.T) {
	switch os.Getenv("GO_BRIDGE_HELPER") {
	case "":
		return // ordinary test run; do nothing
	case "exit42":
		os.Exit(42)
	default:
		os.Exit(0)
	}
}
