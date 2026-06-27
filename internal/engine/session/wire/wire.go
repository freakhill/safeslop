// Package wire is the length-prefixed frame protocol carried over a detached
// session's per-session unix socket (specs/0051 D3). Three frame types cross the
// wire so PTY data, terminal resizes, and the agent's exit are unambiguous and
// the supervisor can tee output to a JSONL log:
//
//	D  len  bytes      PTY data, either direction
//	R  4    rows cols  resize, client -> supervisor (uint16 each, big-endian)
//	X  4    code       agent exit, supervisor -> client (int32, big-endian)
//
// Every frame is [type:1][payload-len:uint32 big-endian][payload], so an unknown
// or truncated frame is detected without desyncing the stream.
package wire

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// Type is a one-byte frame discriminator.
type Type byte

const (
	Data   Type = 'D' // PTY bytes, either direction
	Resize Type = 'R' // terminal size change, client -> supervisor
	Exit   Type = 'X' // agent exit code, supervisor -> client
)

// maxPayload caps a single frame's payload so a corrupt length cannot trigger a
// huge allocation. PTY data is written in small chunks; 1 MiB is far above any
// legitimate frame.
const maxPayload = 1 << 20

var (
	// ErrUnknownType is returned when a frame carries an unrecognised type byte.
	ErrUnknownType = errors.New("wire: unknown frame type")
	// ErrMalformed is returned when a control frame's payload is the wrong size.
	ErrMalformed = errors.New("wire: malformed frame")
	// ErrFrameTooLarge is returned when a frame's declared length exceeds maxPayload.
	ErrFrameTooLarge = errors.New("wire: frame payload too large")
)

// Frame is a decoded protocol message. Only the fields relevant to Type are
// meaningful: Data for Data frames, Rows/Cols for Resize, Code for Exit.
type Frame struct {
	Type Type
	Data []byte
	Rows uint16
	Cols uint16
	Code int
}

// DataFrame builds a Data frame carrying p (either direction).
func DataFrame(p []byte) Frame { return Frame{Type: Data, Data: p} }

// ResizeFrame builds a Resize frame for a rows x cols terminal.
func ResizeFrame(rows, cols uint16) Frame { return Frame{Type: Resize, Rows: rows, Cols: cols} }

// ExitFrame builds an Exit frame carrying the agent's exit code (-1 for signal death).
func ExitFrame(code int) Frame { return Frame{Type: Exit, Code: code} }

// Write encodes f to w as a single length-prefixed frame. It assembles the whole
// frame in one buffer and issues one Write, so concurrent senders (which the
// caller must still serialise) never split a header from its payload.
func Write(w io.Writer, f Frame) error {
	var payload []byte
	switch f.Type {
	case Data:
		payload = f.Data
	case Resize:
		payload = make([]byte, 4)
		binary.BigEndian.PutUint16(payload[0:2], f.Rows)
		binary.BigEndian.PutUint16(payload[2:4], f.Cols)
	case Exit:
		payload = make([]byte, 4)
		binary.BigEndian.PutUint32(payload, uint32(int32(f.Code)))
	default:
		return fmt.Errorf("%w: %q", ErrUnknownType, byte(f.Type))
	}
	if len(payload) > maxPayload {
		return ErrFrameTooLarge
	}
	buf := make([]byte, 5+len(payload))
	buf[0] = byte(f.Type)
	binary.BigEndian.PutUint32(buf[1:5], uint32(len(payload)))
	copy(buf[5:], payload)
	_, err := w.Write(buf)
	return err
}

// Read decodes the next frame from r. A clean end-of-stream before any byte is
// reported as io.EOF; a frame cut short mid-header or mid-payload is
// io.ErrUnexpectedEOF. Unknown types, wrong-sized control payloads, and
// over-long frames are rejected without allocating the bogus payload.
func Read(r io.Reader) (Frame, error) {
	var head [5]byte
	// Read the type byte alone first, so a clean EOF at a frame boundary is
	// reported verbatim rather than as a truncation.
	if _, err := io.ReadFull(r, head[:1]); err != nil {
		return Frame{}, err
	}
	t := Type(head[0])
	switch t {
	case Data, Resize, Exit:
	default:
		return Frame{}, fmt.Errorf("%w: %q", ErrUnknownType, head[0])
	}
	if _, err := io.ReadFull(r, head[1:5]); err != nil {
		return Frame{}, unexpected(err)
	}
	n := binary.BigEndian.Uint32(head[1:5])
	if n > maxPayload {
		return Frame{}, ErrFrameTooLarge
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return Frame{}, unexpected(err)
	}
	switch t {
	case Data:
		return Frame{Type: Data, Data: payload}, nil
	case Resize:
		if n != 4 {
			return Frame{}, ErrMalformed
		}
		return Frame{
			Type: Resize,
			Rows: binary.BigEndian.Uint16(payload[0:2]),
			Cols: binary.BigEndian.Uint16(payload[2:4]),
		}, nil
	default: // Exit
		if n != 4 {
			return Frame{}, ErrMalformed
		}
		return Frame{Type: Exit, Code: int(int32(binary.BigEndian.Uint32(payload)))}, nil
	}
}

// unexpected maps a mid-frame io.EOF to io.ErrUnexpectedEOF. io.ReadFull already
// does this for short reads of a multi-byte target, but a 0-byte read returns
// io.EOF, which we normalise so callers see one "truncated" signal mid-frame.
func unexpected(err error) error {
	if errors.Is(err, io.EOF) {
		return io.ErrUnexpectedEOF
	}
	return err
}
