# SP7c-1 — session control plane + PTY + Attach (sandbox/host) Implementation Plan

**Goal:** Build the engine core of the embedded cockpit (`specs/0014`): a session-oriented control plane where the engine owns each agent's PTY and streams terminal I/O to the app over a gRPC `Attach` stream, for the **sandbox** and **host** environments.

**Architecture:** Extend the SP7a `Control` gRPC service with `OpenSession`/`Attach`/`CloseSession`. A `control.Manager` holds a concurrency-safe registry of `*Session` (each = one agent + its host-side PTY master, via `creack/pty`). `OpenSession` resolves a profile to a launch spec (argv/env/dir + cleanup) through a resolver injected by `cli` (the same pattern as SP7a's `launchFn`, since `control` can't import `cli`), starts the agent on a PTY, and registers it. `Attach` is a bidi stream: a per-session output goroutine copies the PTY master → `ServerFrame.output`; the recv loop writes `ClientFrame.input` to the master and applies `resize` via `pty.Setsize`. One `slop serve` multiplexes N sessions, so multiple app windows are N concurrent sessions.

**Tech stack:** Go, `google.golang.org/grpc`, `github.com/creack/pty` (already a dep), the existing `sandbox`/`policy` packages. protoc toolchain at dev time (generated code committed).

**Scope:** sandbox + host only. container (`docker -it`) and vm (`ssh -t`) bridging are **SP7c-2** (separate plan). Full secrets/creds staging parity with `slop run` is deferred (noted in Task 4) — SP7c-1 launches the agent with the inherited host env so the machinery is provable; cred staging layers on later.

**Base branch:** `sp7c-embedded-cockpit-design` (already holds `specs/0014`); this plan + the SP7c-1 code extend it. Builds on the SP7a control plane (merged, `main`).

**File structure:**
- `internal/engine/control/control.proto` (modify) — add `OpenSession`/`Attach`/`CloseSession` + frames.
- `internal/engine/control/pb/*.pb.go` (regenerated, committed) — new stubs.
- `internal/engine/control/session.go` (create) — `Manager` + `Session` (PTY lifecycle, Read/Write/Resize/Exited/Close).
- `internal/engine/control/session_test.go` (create) — Manager/Session unit tests with `cat`.
- `internal/engine/control/server.go` (modify) — `OpenSession`/`Attach`/`CloseSession` methods + `mgr`/`resolveFn` fields.
- `internal/engine/control/serve.go` (modify) — thread the manager + resolver into `Serve`.
- `internal/engine/control/attach_test.go` (create) — end-to-end gRPC `OpenSession→Attach` over an in-process socket.
- `internal/cli/cli.go` (modify) — `cmdServe` injects the profile→spec resolver (host + sandbox).
- `internal/engine/sandbox/sandbox.go` (modify) — expose `WrapArgv` so the resolver can build the sandboxed argv without launching.

---

### Task 1: `.proto` — session RPCs + frames

**Files:**
- Modify: `internal/engine/control/control.proto`
- Regenerated: `internal/engine/control/pb/control.pb.go`, `control_grpc.pb.go`
- Test: `internal/engine/control/pb/pb_smoke_test.go` (modify)

- [ ] **Step 1: Add the RPCs + messages.** In `internal/engine/control/control.proto`, add to `service Control`:

```proto
  rpc OpenSession(OpenSessionRequest) returns (OpenSessionResponse);
  rpc Attach(stream ClientFrame) returns (stream ServerFrame);
  rpc CloseSession(CloseSessionRequest) returns (CloseSessionResponse);
```

and append these messages:

```proto
message OpenSessionRequest {
  string profile = 1;
  string config_path = 2;
  uint32 cols = 3;
  uint32 rows = 4;
}
message OpenSessionResponse { string session_id = 1; }

message Resize { uint32 cols = 1; uint32 rows = 2; }
message ClientFrame {
  oneof msg {
    string attach_session_id = 1; // MUST be the first frame
    bytes  input = 2;
    Resize resize = 3;
  }
}
message Exited { int32 exit_code = 1; }
message ServerFrame {
  oneof msg {
    bytes  output = 1;
    Exited exited = 2;
  }
}

message CloseSessionRequest { string session_id = 1; }
message CloseSessionResponse {}
```

- [ ] **Step 2: Regenerate + smoke-test.**

```bash
export PATH="$(go env GOPATH)/bin:$PATH"
make proto
```
Append to `internal/engine/control/pb/pb_smoke_test.go`:

```go
func TestSessionTypesExist(t *testing.T) {
	_ = &OpenSessionRequest{Profile: "p", Cols: 80, Rows: 24}
	_ = &OpenSessionResponse{SessionId: "s1"}
	_ = &ClientFrame{Msg: &ClientFrame_AttachSessionId{AttachSessionId: "s1"}}
	_ = &ClientFrame{Msg: &ClientFrame_Input{Input: []byte("x")}}
	_ = &ClientFrame{Msg: &ClientFrame_Resize{Resize: &Resize{Cols: 100, Rows: 40}}}
	_ = &ServerFrame{Msg: &ServerFrame_Output{Output: []byte("y")}}
	_ = &ServerFrame{Msg: &ServerFrame_Exited{Exited: &Exited{ExitCode: 0}}}
}
```

- [ ] **Step 3: Run + build.**

```bash
go test ./internal/engine/control/pb/ -run TestSessionTypesExist -v
go build ./...
```
Expected: PASS; build green.

- [ ] **Step 4: Commit** (generated code included)

```bash
git add internal/engine/control/control.proto internal/engine/control/pb/ internal/engine/control/pb/pb_smoke_test.go
git commit -m "feat(control): proto OpenSession/Attach/CloseSession + session frames"
```

---

### Task 2: `Manager` + `Session` (PTY lifecycle)

**Files:**
- Create: `internal/engine/control/session.go`
- Test: `internal/engine/control/session_test.go`

- [ ] **Step 1: Write the failing test** — create `internal/engine/control/session_test.go`:

```go
package control

import (
	"bytes"
	"io"
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
```

- [ ] **Step 2: Run it, verify it fails**

```bash
go test ./internal/engine/control/ -run 'TestSessionEchoes|TestOpenAssigns|TestOpenEmpty' -v
```
Expected: FAIL — `NewManager`/`SessionSpec`/`Session` undefined.

- [ ] **Step 3: Write the manager** — create `internal/engine/control/session.go`:

```go
package control

import (
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

func (s *Session) Read(p []byte) (int, error)  { return s.ptmx.Read(p) }
func (s *Session) Write(p []byte) (int, error) { return s.ptmx.Write(p) }
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
	if errorsAs(err, &ee) {
		return ee.ExitCode()
	}
	return 1
}
```

Add the tiny `errorsAs`/`isTimeout` helpers (kept local to avoid widening imports) at the bottom of `session.go`:

```go
func errorsAs(err error, target any) bool { return stderrors.As(err, target) }
```

and import `stderrors "errors"` plus `"net"` for `isTimeout` in the test file:

```go
// in session_test.go
func isTimeout(err error) bool {
	var ne net.Error
	return stderrorsAs(err, &ne) && ne.Timeout()
}
```

> Implementation note: simpler than the wrappers above — just `import "errors"` in `session.go` and call `errors.As` directly; in `session_test.go` `import "errors"` and `var ne net.Error; errors.As(err,&ne)`. The wrapper names are only to avoid a name clash if the file already imports something as `errors`; it does not, so use `errors.As` directly and delete the wrapper lines.

- [ ] **Step 4: Run it, verify it passes**

```bash
go test ./internal/engine/control/ -run 'TestSessionEchoes|TestOpenAssigns|TestOpenEmpty' -v
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/engine/control/session.go internal/engine/control/session_test.go
git add internal/engine/control/session.go internal/engine/control/session_test.go
git commit -m "feat(control): session Manager + PTY-backed Session (open/read/write/resize/close)"
```

---

### Task 3: `OpenSession`/`Attach`/`CloseSession` RPC wiring

**Files:**
- Modify: `internal/engine/control/server.go`
- Modify: `internal/engine/control/serve.go`
- Test: `internal/engine/control/attach_test.go`

- [ ] **Step 1: Write the failing end-to-end test** — create `internal/engine/control/attach_test.go`:

```go
package control

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/freakhill/safeslop/internal/engine/control/pb"
)

// startTestServer serves Control on an ephemeral unix socket with a resolver that
// maps any profile to a `cat` session. Returns a connected client + cleanup.
func startTestServer(t *testing.T) (pb.ControlClient, func()) {
	t.Helper()
	dir := t.TempDir()
	addr := dir + "/s.sock"
	ln, err := net.Listen("unix", addr)
	if err != nil {
		t.Fatal(err)
	}
	mgr := NewManager()
	resolve := func(profile, configPath string) (SessionSpec, error) {
		return SessionSpec{Argv: []string{"cat"}}, nil
	}
	gs := grpc.NewServer()
	pb.RegisterControlServer(gs, &server{version: "test", mgr: mgr, resolveFn: resolve})
	go func() { _ = gs.Serve(ln) }()
	conn, err := grpc.NewClient("unix:"+addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	return pb.NewControlClient(conn), func() { conn.Close(); gs.Stop() }
}

func TestOpenAttachRoundTrip(t *testing.T) {
	c, done := startTestServer(t)
	defer done()
	ctx := context.Background()

	open, err := c.OpenSession(ctx, &pb.OpenSessionRequest{Profile: "any", Cols: 80, Rows: 24})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	st, err := c.Attach(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Send(&pb.ClientFrame{Msg: &pb.ClientFrame_AttachSessionId{AttachSessionId: open.SessionId}}); err != nil {
		t.Fatal(err)
	}
	if err := st.Send(&pb.ClientFrame{Msg: &pb.ClientFrame_Input{Input: []byte("ping\n")}}); err != nil {
		t.Fatal(err)
	}
	// read until we see the echo or time out
	deadline := time.Now().Add(3 * time.Second)
	var seen bool
	for time.Now().Before(deadline) && !seen {
		f, err := st.Recv()
		if err != nil {
			t.Fatalf("Recv: %v", err)
		}
		if o := f.GetOutput(); len(o) > 0 && contains(o, []byte("ping")) {
			seen = true
		}
	}
	if !seen {
		t.Fatal("did not see echoed input over Attach")
	}
	_, _ = c.CloseSession(ctx, &pb.CloseSessionRequest{SessionId: open.SessionId})
}

func contains(h, n []byte) bool {
	for i := 0; i+len(n) <= len(h); i++ {
		if string(h[i:i+len(n)]) == string(n) {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Run it, verify it fails**

```bash
go test ./internal/engine/control/ -run TestOpenAttachRoundTrip -v
```
Expected: FAIL — `server` has no `mgr`/`resolveFn` fields; `OpenSession`/`Attach`/`CloseSession` unimplemented.

- [ ] **Step 3: Implement the methods.** In `internal/engine/control/server.go`, extend the `server` struct and add methods:

```go
type server struct {
	pb.UnimplementedControlServer
	version   string
	launchFn  func(profile, configPath string, emit func(*pb.LaunchEvent)) error
	mgr       *Manager
	resolveFn func(profile, configPath string) (SessionSpec, error)
}

func (s *server) OpenSession(_ context.Context, req *pb.OpenSessionRequest) (*pb.OpenSessionResponse, error) {
	spec, err := s.resolveFn(req.Profile, req.ConfigPath)
	if err != nil {
		return nil, err
	}
	id, err := s.mgr.Open(spec)
	if err != nil {
		return nil, err
	}
	if req.Cols > 0 && req.Rows > 0 {
		if sess, ok := s.mgr.Get(id); ok {
			_ = sess.Resize(uint16(req.Cols), uint16(req.Rows))
		}
	}
	return &pb.OpenSessionResponse{SessionId: id}, nil
}

func (s *server) Attach(stream pb.Control_AttachServer) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	id := first.GetAttachSessionId()
	sess, ok := s.mgr.Get(id)
	if !ok {
		return status.Errorf(codes.NotFound, "unknown session %q", id)
	}
	// output pump: PTY -> client
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, er := sess.Read(buf)
			if n > 0 {
				_ = stream.Send(&pb.ServerFrame{Msg: &pb.ServerFrame_Output{Output: append([]byte(nil), buf[:n]...)}})
			}
			if er != nil {
				return
			}
		}
	}()
	// notify on exit
	go func() {
		<-sess.Exited()
		_ = stream.Send(&pb.ServerFrame{Msg: &pb.ServerFrame_Exited{Exited: &pb.Exited{ExitCode: int32(sess.Code())}}})
	}()
	// input loop: client -> PTY / resize
	for {
		f, err := stream.Recv()
		if err != nil {
			return nil // client hung up
		}
		switch m := f.Msg.(type) {
		case *pb.ClientFrame_Input:
			_, _ = sess.Write(m.Input)
		case *pb.ClientFrame_Resize:
			_ = sess.Resize(uint16(m.Resize.Cols), uint16(m.Resize.Rows))
		}
	}
}

func (s *server) CloseSession(_ context.Context, req *pb.CloseSessionRequest) (*pb.CloseSessionResponse, error) {
	s.mgr.Close(req.SessionId)
	return &pb.CloseSessionResponse{}, nil
}
```

Add imports to `server.go`: `"google.golang.org/grpc/codes"` and `"google.golang.org/grpc/status"`.

In `internal/engine/control/serve.go`, thread the manager + resolver into `Serve`:

```go
func Serve(version string,
	launchFn func(profile, configPath string, emit func(*pb.LaunchEvent)) error,
	resolveFn func(profile, configPath string) (SessionSpec, error),
) error {
	// ... unchanged socket setup ...
	gs := grpc.NewServer()
	pb.RegisterControlServer(gs, &server{version: version, launchFn: launchFn, mgr: NewManager(), resolveFn: resolveFn})
	return gs.Serve(peerAuthListener{ln})
}
```

- [ ] **Step 4: Run it, verify it passes**

```bash
go test ./internal/engine/control/ -run TestOpenAttachRoundTrip -v && go build ./...
```
Expected: PASS; build green (the `cli` caller breaks until Task 4 — that's next).

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/engine/control/server.go internal/engine/control/serve.go internal/engine/control/attach_test.go
git add internal/engine/control/server.go internal/engine/control/serve.go internal/engine/control/attach_test.go
git commit -m "feat(control): OpenSession/Attach/CloseSession over the session Manager"
```

---

### Task 4: wire the resolver into `slop serve` (host + sandbox)

**Files:**
- Modify: `internal/engine/sandbox/sandbox.go` (expose `WrapArgv`)
- Modify: `internal/cli/cli.go` (`cmdServe` injects the resolver)
- Test: `internal/engine/sandbox/sandbox_test.go` (modify)

- [ ] **Step 1: Write the failing test** — add to `internal/engine/sandbox/sandbox_test.go`:

```go
func TestWrapArgvWritesProfileAndWraps(t *testing.T) {
	argv, cleanup, err := WrapArgv([]string{"claude"}, "/ws", "deny")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if argv[0] != SandboxExecPath || argv[1] != "-f" {
		t.Fatalf("argv must start with sandbox-exec -f: %v", argv)
	}
	if _, err := os.Stat(argv[2]); err != nil {
		t.Fatalf("profile file %q must exist: %v", argv[2], err)
	}
	last := argv[len(argv)-1]
	if last != "claude" {
		t.Fatalf("agent argv must be appended: %v", argv)
	}
	cleanup()
	if _, err := os.Stat(argv[2]); !os.IsNotExist(err) {
		t.Fatalf("cleanup must remove the profile file")
	}
}
```

> Add `"os"` to the sandbox test imports if missing.

- [ ] **Step 2: Run it, verify it fails**

```bash
go test ./internal/engine/sandbox/ -run TestWrapArgv -v
```
Expected: FAIL — `WrapArgv` undefined.

- [ ] **Step 3: Add `WrapArgv`.** In `internal/engine/sandbox/sandbox.go`, factor the profile-file writing out of `Launch` into a reusable `WrapArgv` (and have `Launch` call it). Add:

```go
// WrapArgv writes a Seatbelt profile for (workspace, network) to a temp file and returns
// the argv that runs agentArgv under it, plus a cleanup that removes the file. The caller
// runs the argv (e.g. on a PTY) and calls cleanup when the process exits.
func WrapArgv(agentArgv []string, workspace, network string) (argv []string, cleanup func(), err error) {
	if _, statErr := os.Stat(SandboxExecPath); statErr != nil {
		return nil, func() {}, fmt.Errorf("sandbox environment requires macOS sandbox-exec at %s", SandboxExecPath)
	}
	f, err := os.CreateTemp("", "slop-sb-*.sb")
	if err != nil {
		return nil, func() {}, err
	}
	if _, err := f.WriteString(Profile(workspace, network)); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return nil, func() {}, err
	}
	_ = f.Close()
	argv = append([]string{SandboxExecPath, "-f", f.Name(), "--"}, agentArgv...)
	return argv, func() { _ = os.Remove(f.Name()) }, nil
}
```

> Then refactor `Launch` (around line 106-134) to call `WrapArgv` instead of inlining the temp-file logic: `argv, cleanup, err := WrapArgv(spec.Argv, workspace, network); if err != nil {...}; defer cleanup(); inner.Argv = argv; return exec.RunInTerminal(ctx, inner)`. Keep `Launch`'s behavior identical (its existing tests must still pass).

- [ ] **Step 4: Inject the resolver in `cmdServe`.** In `internal/cli/cli.go`, change the `control.Serve` call to pass a resolver, and define it. Replace the `cmdServe` `RunE` body's `control.Serve(Version, func(...)...)` with the 3-arg form, adding the resolver:

```go
		RunE: func(_ *cobra.Command, _ []string) error {
			return control.Serve(Version,
				func(profile, configPath string, emit func(*pb.LaunchEvent)) error { /* unchanged Launch body */ },
				resolveSession,
			)
		},
```

and add the resolver helper near `launchProfile`:

```go
// resolveSession turns a profile name into a control.SessionSpec: the agent argv (optionally
// toolchain-wrapped), the workspace, and — for environment:sandbox — the sandbox-exec wrap +
// its temp-profile cleanup as OnClose. host/sandbox only (SP7c-1); container/vm follow (SP7c-2).
func resolveSession(profile, configPath string) (control.SessionSpec, error) {
	path, err := findConfig(configPath)
	if err != nil {
		return control.SessionSpec{}, err
	}
	cfg, err := policy.Load(path)
	if err != nil {
		return control.SessionSpec{}, err
	}
	name, prof, err := selectProfile(cfg, profile)
	if err != nil {
		return control.SessionSpec{}, err
	}
	_ = name
	argv, err := agentArgv(prof)
	if err != nil {
		return control.SessionSpec{}, err
	}
	if prof.Toolchain != nil && toolchain.Wraps(prof.Toolchain.Kind) {
		argv = toolchain.Wrap(prof.Toolchain.Kind, prof.Toolchain.Run, argv)
	}
	ws := prof.Workspace
	if ws == "" {
		ws, _ = os.Getwd()
	}
	switch prof.Environment {
	case "host":
		return control.SessionSpec{Argv: argv, Dir: ws}, nil
	case "sandbox", "": // sandbox is the default
		wrapped, cleanup, err := sandbox.WrapArgv(argv, ws, prof.Network)
		if err != nil {
			return control.SessionSpec{}, err
		}
		return control.SessionSpec{Argv: wrapped, Dir: ws, OnClose: cleanup}, nil
	default:
		return control.SessionSpec{}, fmt.Errorf("embedded cockpit supports environment host/sandbox in SP7c-1; %q is SP7c-2", prof.Environment)
	}
}
```

> `findConfig`/`selectProfile`/`agentArgv` already exist in `cli`. `configPath==""` → `findConfig` searches from cwd (its existing behavior). Secrets/creds staging is **not** wired here yet (SP7c-1 launches with the inherited host env) — tracked as a follow-on so the cockpit reaches parity with `slop run`.

- [ ] **Step 5: Run + build + the sandbox test.**

```bash
go test ./internal/engine/sandbox/ -run TestWrapArgv -v
go test ./internal/engine/sandbox/ ./internal/cli/ ./internal/engine/control/...
go build ./...
```
Expected: all PASS; build green (the `control.Serve` 3-arg signature now matches its caller).

- [ ] **Step 6: Commit**

```bash
gofmt -w internal/engine/sandbox/sandbox.go internal/engine/sandbox/sandbox_test.go internal/cli/cli.go
git add internal/engine/sandbox/sandbox.go internal/engine/sandbox/sandbox_test.go internal/cli/cli.go
git commit -m "feat(cli): resolve profiles to cockpit sessions (host + sandbox WrapArgv)"
```

---

### Task 5: multi-session concurrency test

**Files:**
- Test: `internal/engine/control/session_test.go` (modify)

- [ ] **Step 1: Write the test** — append to `internal/engine/control/session_test.go`:

```go
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
```

- [ ] **Step 2: Run it, verify it passes** (the Manager from Task 2 already supports this)

```bash
go test ./internal/engine/control/ -run TestManagerHandlesConcurrentSessions -v -race
```
Expected: PASS, no race.

- [ ] **Step 3: Commit**

```bash
git add internal/engine/control/session_test.go
git commit -m "test(control): N concurrent independent sessions, no cross-talk or leak"
```

---

### Task 6: full verification + PR

- [ ] **Step 1: Full suite + gates.**

```bash
make check && make build
go test ./internal/engine/control/... -race
fish -n scripts/*.fish
fish tests/run.fish
fish scripts/slop-sync-help.fish check
fish scripts/slop-pinning.fish
```
Expected: all green (`-race` clean on the control package).

- [ ] **Step 2: Push + PR.**

```bash
git push -u origin sp7c-embedded-cockpit-design
gh pr create --title "SP7c-1: embedded-cockpit session control plane (PTY + Attach, sandbox/host)" --body "$(cat <<'EOF'
## Summary
Engine core of the SafeSlop cockpit (design specs/0014): a session-oriented control plane where the engine owns each agent's PTY and streams terminal I/O to the app over a gRPC `Attach` bidi stream. sandbox + host environments (container/vm = SP7c-2). Includes the approved design doc (specs/0014).

- `control.proto`: `OpenSession` / `Attach(stream)` / `CloseSession` + frames (committed generated stubs).
- `control.Manager` + `Session`: PTY-backed sessions (`creack/pty`), concurrency-safe registry, read/write/resize/close.
- `Attach`: per-session output pump (PTY→client) + input/resize recv loop; `Exited` on agent exit.
- cli: `slop serve` resolves profiles to sessions — host (direct argv) + sandbox (`sandbox.WrapArgv`, temp-profile cleanup via `OnClose`).
- N concurrent sessions over one `slop serve` (multi-window). Same-uid peer-auth (SP7a) covers `Attach`.

## Deferred
container (`docker -it`) + vm (`ssh -t`) bridging = SP7c-2; full secrets/creds staging parity with `slop run`; scrollback/reconnect; the SwiftUI app.

## Test
`make check` + `make build` green; `-race` clean; `cat`-backed unit tests (echo/resize/close), end-to-end gRPC OpenSession→Attach round-trip, N-session concurrency; four fish gates green.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

---

## Verification (what "done" means)

- `make check` + `make build` green; `go test ./internal/engine/control/... -race` clean; four fish gates green; `make proto` regenerates cleanly.
- `OpenSession` starts an agent on a PTY and returns a session id; `Attach` round-trips bytes + resize; `CloseSession` kills the agent and unregisters.
- N concurrent sessions are independent (no cross-talk, no leak).
- sandbox sessions run under `sandbox-exec` with a temp profile that is removed on close; host sessions run the agent directly.

## Deliberately deferred (not here)

- **container (`docker -it`) + vm (`ssh -t`)** PTY bridging — **SP7c-2**.
- **Secrets/creds staging** parity with `slop run` (SP7c-1 uses the inherited host env).
- **Scrollback / reconnect-after-drop** (engine streams live bytes only, per specs/0014 §10).
- **The SwiftUI app** (SwiftTerm + WindowGroup + chrome) — jojo's Xcode track, against the committed `.proto`.
