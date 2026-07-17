package cli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/creack/pty"

	engsession "github.com/freakhill/safeslop/internal/engine/session"
	"github.com/freakhill/safeslop/internal/engine/session/wire"
)

// superviseReadBuf sizes the PTY read chunk. Terminal traffic is human-paced, so
// a modest buffer keeps latency low and stays well under wire's frame cap.
const superviseReadBuf = 32 * 1024

// defaultSessionLogMaxBytes bounds the provisional per-session JSONL event log so a
// chatty detached agent cannot grow <id>.jsonl without limit before teardown wipes
// it (specs/0051 Q3). Override with SAFESLOP_SESSION_LOG_MAX_BYTES; 0 disables the cap.
const defaultSessionLogMaxBytes = 8 << 20 // 8 MiB

// sessionLogMaxBytes is the JSONL byte cap: the env override when it parses to a
// non-negative integer, else the default.
func sessionLogMaxBytes() int64 {
	if v := os.Getenv("SAFESLOP_SESSION_LOG_MAX_BYTES"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 0 {
			return n
		}
	}
	return defaultSessionLogMaxBytes
}

// Supervise is the per-session detached supervisor (specs/0051 D1/D2). It owns
// the agent's single PTY, serves it over <state>/sessions/<id>.sock, tees a
// per-session JSONL event log, and runs the agent under runProfileCtx so its
// deferred teardown (stage wipe, credential revoke, boundary destroy) is
// inherited for free. On agent exit it removes the socket and Finishes the
// session with the real code.
//
// It RETURNS the code (os.Exit only at the cobra boundary, PR3), dodging the
// cmdSessionRun os.Exit-on-success gotcha. The re-exec entry point calls this in
// its own process; tests call it in-process in a goroutine (the D1 test seam).
func Supervise(ctx context.Context, store engsession.Store, id string, now func() time.Time) (int, error) {
	d := defaultDependencies()
	d.store = store
	d.now = now
	return superviseWithDeps(d, ctx, store, id, now)
}

func superviseWithDeps(d *dependencies, ctx context.Context, store engsession.Store, id string, now func() time.Time) (int, error) {
	sess, err := store.Get(id)
	if err != nil {
		return 1, err
	}
	// Re-verify host approval at the supervisor's own start (specs/0072 F1): a detached
	// supervisor is re-exec'd, so it must independently confirm the policy is still trusted
	// rather than rely on the issuing process's earlier check.
	if err := verifySessionTrust(sess); err != nil {
		return 1, err
	}
	sess, selectedEngine, err := prepareSessionBackendWithDeps(d, store, sess)
	if err != nil {
		return 1, err
	}
	prof, err := sessionProfile(sess)
	if err != nil {
		return 1, err
	}
	argv, err := agentArgv(prof)
	if err != nil {
		return 1, err
	}
	stageKey, err := sessionStageKey(sess)
	if err != nil {
		return 1, err
	}

	if err := os.MkdirAll(store.Dir, 0o700); err != nil {
		return 1, err
	}
	// ptmx is ours to serve; pts is the agent's controlling terminal. The agent
	// gets its own dup via exec; we hold pts open and close it only after the agent
	// exits, so the PTY reader then sees EOF (no other writer remains).
	ptmx, pts, err := pty.Open()
	if err != nil {
		return 1, err
	}
	// A freshly opened PTY slave is 0x0; give it a sane default so a detached agent that renders
	// before any client attaches doesn't wrap at phantom 0/80 columns. The attach handler resizes it
	// to the client's real winsize on connect (the pty.Setsize in the serve loop).
	_ = pty.Setsize(ptmx, &pty.Winsize{Rows: 24, Cols: 80})

	sockPath := store.SocketPath(id)               // fits sun_path; relocates to a short runtime dir if the state dir is too long
	_ = os.MkdirAll(filepath.Dir(sockPath), 0o700) // ensure the bind dir exists (no-op when it already does)
	_ = os.Remove(sockPath)                        // clear a stale socket from an unclean prior supervisor (D7)
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		_ = ptmx.Close()
		_ = pts.Close()
		return 1, err
	}
	// Owner-only permissions are part of the attach capability boundary,
	// especially when SocketPath relocates beneath a shared runtime directory.
	if err := d.chmodSocket(sockPath, 0o600); err != nil {
		_ = ln.Close()
		_ = os.Remove(sockPath)
		_ = ptmx.Close()
		_ = pts.Close()
		return 1, err
	}

	if _, err := store.MarkRunningDetached(id, os.Getpid(), now()); err != nil {
		_ = ln.Close()
		_ = os.Remove(sockPath)
		_ = ptmx.Close()
		_ = pts.Close()
		return 1, err
	}

	// Best-effort JSONL event log; format is provisional, byte-capped (specs/0051 Q3).
	jsonl, _ := os.OpenFile(filepath.Join(store.Dir, id+".jsonl"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)

	h := &supervisor{ptmx: ptmx, ln: ln, jsonl: jsonl, jsonlCap: sessionLogMaxBytes()}

	// Launch the agent on the PTY slave. runProfileCtx blocks for the agent's
	// whole life and runs the inherited teardown on return.
	exitCh := make(chan agentExit, 1)
	go func() {
		code, runErr := runProfileCtxWithEngineAndDeps(d, ctx, selectedEngine, "session-"+id, prof, argv, sess.Workspace, stageKey,
			runIO{Stdin: pts, Stdout: pts, Stderr: pts})
		_ = pts.Close() // last writer to the slave -> the PTY reader below now hits EOF
		exitCh <- agentExit{code, runErr}
	}()

	// Continuously drain the agent PTY so it never blocks with no client attached;
	// every chunk is tee'd to the JSONL log and forwarded to the attached client.
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		buf := make([]byte, superviseReadBuf)
		for {
			n, rerr := ptmx.Read(buf)
			if n > 0 {
				chunk := append([]byte(nil), buf[:n]...)
				h.teeJSONL(chunk)
				h.broadcast(chunk)
			}
			if rerr != nil {
				return
			}
		}
	}()

	go h.acceptLoop() // one active attach at a time (D8); reconnect after disconnect

	ax := <-exitCh // agent exited; runProfileCtx defers have already torn down
	_ = ln.Close() // stop accepting new attaches
	<-readerDone   // flush all remaining agent output and reach PTY EOF
	h.writeExit(ax.code)
	h.closeConn()
	_ = os.Remove(sockPath)
	if jsonl != nil {
		_ = jsonl.Close()
	}
	finishErr := finishSessionRun(store, id, ax.code, ax.err, now())
	_ = ptmx.Close()
	if finishErr != nil {
		return ax.code, finishErr
	}
	return ax.code, nil
}

type agentExit struct {
	code int
	err  error
}

// supervisor is the per-session IO hub: it fans the single agent PTY out to the
// JSONL log and the one attached client, and pumps that client's input back into
// the PTY. The current attach is guarded by mu so the reader, the accept loop,
// and teardown agree on who (if anyone) is connected.
type supervisor struct {
	ptmx  *os.File
	ln    net.Listener
	jsonl *os.File

	// JSONL cap state. Only the single PTY-reader goroutine calls teeJSONL, so these
	// need no lock. jsonlCap == 0 means unlimited.
	jsonlCap       int64
	jsonlWritten   int64
	jsonlTruncated bool

	mu   sync.Mutex
	conn net.Conn // the single active client, or nil
}

// broadcast forwards a PTY output chunk to the attached client as a Data frame.
// A write error means the client vanished mid-stream; drop it so a later attach
// can take over (the agent keeps running).
func (h *supervisor) broadcast(chunk []byte) {
	h.mu.Lock()
	c := h.conn
	h.mu.Unlock()
	if c == nil {
		return
	}
	if err := wire.Write(c, wire.DataFrame(chunk)); err != nil {
		h.dropConn(c)
	}
}

// writeExit sends the agent's exit code to the attached client (if any) as an X
// frame, so the client exits with that code.
func (h *supervisor) writeExit(code int) {
	h.mu.Lock()
	c := h.conn
	h.mu.Unlock()
	if c != nil {
		_ = wire.Write(c, wire.ExitFrame(code))
	}
}

func (h *supervisor) teeJSONL(chunk []byte) {
	if h.jsonl == nil || h.jsonlTruncated {
		return
	}
	rec := struct {
		Stream string `json:"stream"`
		Data   string `json:"data"`
	}{Stream: "pty", Data: base64.StdEncoding.EncodeToString(chunk)}
	b, err := json.Marshal(rec)
	if err != nil {
		return
	}
	line := append(b, '\n')
	if h.jsonlCap > 0 && h.jsonlWritten+int64(len(line)) > h.jsonlCap {
		// Stop before overflowing the cap; leave a single marker so a reader knows the
		// log was truncated rather than the agent having gone quiet.
		marker, _ := json.Marshal(struct {
			Stream string `json:"stream"`
			Event  string `json:"event"`
		}{Stream: "meta", Event: "truncated"})
		_, _ = h.jsonl.Write(append(marker, '\n'))
		h.jsonlTruncated = true
		return
	}
	if n, err := h.jsonl.Write(line); err == nil {
		h.jsonlWritten += int64(n)
	}
}

// acceptLoop serves at most one client at a time (D8): while one is attached a
// second connection is closed immediately, and the slot frees when the first
// disconnects (socket EOF). The contract error for the rejected attach is the
// attach client's concern (PR4); here the refusal is just an immediate close.
func (h *supervisor) acceptLoop() {
	for {
		conn, err := h.ln.Accept()
		if err != nil {
			return // listener closed during teardown
		}
		h.mu.Lock()
		if h.conn != nil {
			h.mu.Unlock()
			_ = conn.Close()
			continue
		}
		h.conn = conn
		h.mu.Unlock()
		go h.serveInput(conn)
	}
}

// serveInput pumps one client's frames into the agent: Data to the PTY, Resize to
// pty.Setsize. It returns when the client disconnects, freeing the attach slot.
func (h *supervisor) serveInput(conn net.Conn) {
	for {
		f, err := wire.Read(conn)
		if err != nil {
			h.dropConn(conn)
			return
		}
		switch f.Type {
		case wire.Data:
			if _, werr := h.ptmx.Write(f.Data); werr != nil {
				h.dropConn(conn)
				return
			}
		case wire.Resize:
			_ = pty.Setsize(h.ptmx, &pty.Winsize{Rows: f.Rows, Cols: f.Cols})
		}
	}
}

// dropConn clears conn as the active client if it still is, and closes it.
func (h *supervisor) dropConn(conn net.Conn) {
	h.mu.Lock()
	if h.conn == conn {
		h.conn = nil
	}
	h.mu.Unlock()
	_ = conn.Close()
}

func (h *supervisor) closeConn() {
	h.mu.Lock()
	c := h.conn
	h.conn = nil
	h.mu.Unlock()
	if c != nil {
		_ = c.Close()
	}
}
