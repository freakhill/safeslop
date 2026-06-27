package session

import (
	"errors"
	"io"
	"sync"
	"syscall"

	"github.com/freakhill/safeslop/internal/engine/session/wire"
)

// Outcome reports why Bridge (the supervisor side) stopped proxying.
type Outcome int

const (
	// ClientGone means the client connection closed while the agent is still
	// alive — a detach or a dropped link. The supervisor keeps the agent and its
	// PTY and can accept a fresh attach (specs/0051 D8). No exit frame is sent.
	ClientGone Outcome = iota
	// ChildExited means the agent's PTY reached EOF: the agent exited. Bridge has
	// emitted an X frame carrying waitExit()'s code, and the supervisor should run
	// teardown.
	ChildExited
)

// readBuf is the PTY read chunk size. PTY traffic is human-interactive, so a
// modest buffer keeps latency low while staying well under wire.maxPayload.
const readBuf = 32 * 1024

// Bridge is the supervisor side of a detached session: it proxies the agent's
// PTY master (ptmx) and one client connection (conn) using the wire protocol.
//
//   - client Data frames -> raw bytes written to the PTY (agent input)
//   - raw PTY output      -> Data frames written to the client (agent output)
//   - client Resize frames -> onResize (the supervisor wires this to pty.Setsize)
//
// It returns ChildExited once the PTY reaches EOF — after reaping the code via
// waitExit and writing an X frame — or ClientGone if the client disconnects
// first, leaving the agent untouched. The caller owns conn and ptmx and must
// close them to release whichever pump is still blocked on the side that did not
// trigger the return (e.g. close conn after ChildExited).
func Bridge(conn io.ReadWriter, ptmx io.ReadWriter, onResize func(rows, cols uint16) error, waitExit func() int) (Outcome, error) {
	var wmu sync.Mutex // serialise the two writers to conn (PTY data + the exit frame)
	writeFrame := func(f wire.Frame) error {
		wmu.Lock()
		defer wmu.Unlock()
		return wire.Write(conn, f)
	}

	// Buffered so the second pump's eventual result never blocks on send after we
	// have already returned on the first.
	done := make(chan pumpResult, 2)
	go func() { done <- pumpConnToPTY(conn, ptmx, onResize) }()
	go func() { done <- pumpPTYToConn(ptmx, writeFrame) }()

	res := <-done
	if !res.childExited {
		return ClientGone, res.err
	}
	code := waitExit()
	if werr := writeFrame(wire.ExitFrame(code)); werr != nil {
		return ChildExited, werr
	}
	return ChildExited, res.err
}

// pumpResult is which side ended a pump: childExited distinguishes "the agent's
// PTY closed" (emit an exit frame) from "the client link broke" (keep the agent).
type pumpResult struct {
	childExited bool
	err         error
}

// pumpConnToPTY decodes client frames and applies them: Data to the PTY, Resize
// via onResize. A read error on conn is the client leaving; a write error to the
// PTY means the agent is gone.
func pumpConnToPTY(conn io.Reader, ptmx io.Writer, onResize func(rows, cols uint16) error) pumpResult {
	for {
		f, err := wire.Read(conn)
		if err != nil {
			return pumpResult{childExited: false, err: dropExpected(err)}
		}
		switch f.Type {
		case wire.Data:
			if _, werr := ptmx.Write(f.Data); werr != nil {
				return pumpResult{childExited: true, err: dropExpected(werr)}
			}
		case wire.Resize:
			if onResize != nil {
				if rerr := onResize(f.Rows, f.Cols); rerr != nil {
					return pumpResult{childExited: false, err: rerr}
				}
			}
		case wire.Exit:
			// Clients never send Exit; ignore it rather than desync.
		}
	}
}

// pumpPTYToConn frames raw PTY output to the client. A read error (EOF/EIO) is
// the agent exiting; a write error to conn is the client leaving.
func pumpPTYToConn(ptmx io.Reader, writeFrame func(wire.Frame) error) pumpResult {
	buf := make([]byte, readBuf)
	for {
		n, rerr := ptmx.Read(buf)
		if n > 0 {
			if werr := writeFrame(wire.DataFrame(buf[:n])); werr != nil {
				return pumpResult{childExited: false, err: dropExpected(werr)}
			}
		}
		if rerr != nil {
			return pumpResult{childExited: true, err: dropExpected(rerr)}
		}
	}
}

// Attach is the client side: it bridges a local terminal with the session
// socket. Local input (in) is sent as Data frames; Data frames from the
// supervisor are written to out; sizes received on resize are sent as Resize
// frames (nil disables resize forwarding). It returns the agent's exit code when
// an X frame arrives, or a non-nil error if the connection closes without one.
func Attach(conn io.ReadWriter, in io.Reader, out io.Writer, resize <-chan [2]uint16) (int, error) {
	var wmu sync.Mutex // serialise the two writers to conn (input data + resizes)
	writeFrame := func(f wire.Frame) error {
		wmu.Lock()
		defer wmu.Unlock()
		return wire.Write(conn, f)
	}

	go func() {
		buf := make([]byte, readBuf)
		for {
			n, err := in.Read(buf)
			if n > 0 {
				if werr := writeFrame(wire.DataFrame(buf[:n])); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	if resize != nil {
		go func() {
			for sz := range resize {
				if err := writeFrame(wire.ResizeFrame(sz[0], sz[1])); err != nil {
					return
				}
			}
		}()
	}

	for {
		f, err := wire.Read(conn)
		if err != nil {
			return 1, dropExpected(err)
		}
		switch f.Type {
		case wire.Data:
			if _, werr := out.Write(f.Data); werr != nil {
				return 1, werr
			}
		case wire.Exit:
			return f.Code, nil
		case wire.Resize:
			// The supervisor does not resize the client; ignore.
		}
	}
}

// dropExpected normalises the benign end-of-stream signals — io.EOF and the
// PTY-master EIO seen on Linux after the child exits — to nil, so a clean
// teardown is not reported as an error.
func dropExpected(err error) error {
	if err == nil || errors.Is(err, io.EOF) || errors.Is(err, syscall.EIO) {
		return nil
	}
	return err
}
