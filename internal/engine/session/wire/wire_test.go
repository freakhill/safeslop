package wire

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		in   Frame
	}{
		{"data-small", DataFrame([]byte("hello world"))},
		{"data-empty", DataFrame([]byte{})},
		{"data-binary", DataFrame([]byte{0x00, 0x1b, 0x5b, 0xff, 0x00})},
		{"data-large", DataFrame(bytes.Repeat([]byte("x"), 70000))}, // > one 32KiB read + > u16
		{"resize", ResizeFrame(48, 200)},
		{"resize-max", ResizeFrame(0xffff, 0xffff)},
		{"exit-zero", ExitFrame(0)},
		{"exit-code", ExitFrame(42)},
		{"exit-max-byte", ExitFrame(255)},
		{"exit-signal", ExitFrame(-1)}, // Go reports -1 for signal death
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := Write(&buf, tc.in); err != nil {
				t.Fatalf("Write: %v", err)
			}
			got, err := Read(&buf)
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			if !framesEqual(got, tc.in) {
				t.Fatalf("round trip mismatch:\n got  %+v\n want %+v", got, tc.in)
			}
			if buf.Len() != 0 {
				t.Fatalf("decoder left %d trailing bytes; frame length is wrong", buf.Len())
			}
		})
	}
}

func TestFrameRoundTripStream(t *testing.T) {
	// Several frames back to back decode in order off one stream.
	frames := []Frame{
		ResizeFrame(24, 80),
		DataFrame([]byte("abc")),
		DataFrame([]byte("def")),
		ExitFrame(7),
	}
	var buf bytes.Buffer
	for _, f := range frames {
		if err := Write(&buf, f); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	for i, want := range frames {
		got, err := Read(&buf)
		if err != nil {
			t.Fatalf("Read #%d: %v", i, err)
		}
		if !framesEqual(got, want) {
			t.Fatalf("frame #%d: got %+v want %+v", i, got, want)
		}
	}
	if _, err := Read(&buf); !errors.Is(err, io.EOF) {
		t.Fatalf("after draining, Read = %v, want io.EOF", err)
	}
}

func TestFrameDecodeRejectsTruncated(t *testing.T) {
	cases := []struct {
		name string
		raw  []byte
	}{
		{"empty", nil}, // clean EOF before any frame
		{"header-short", []byte{byte(Data), 0x00, 0x00}},            // type + 2 of 4 length bytes
		{"payload-short", []byte{byte(Data), 0, 0, 0, 5, 'a', 'b'}}, // claims 5, gives 2
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Read(bytes.NewReader(tc.raw))
			if err == nil {
				t.Fatalf("expected an error decoding %v", tc.raw)
			}
			if tc.name == "empty" {
				if !errors.Is(err, io.EOF) {
					t.Fatalf("empty stream: err = %v, want io.EOF", err)
				}
				return
			}
			if !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatalf("truncated frame: err = %v, want io.ErrUnexpectedEOF", err)
			}
		})
	}
}

func TestFrameDecodeRejectsUnknownType(t *testing.T) {
	_, err := Read(bytes.NewReader([]byte{'Z', 0, 0, 0, 0}))
	if !errors.Is(err, ErrUnknownType) {
		t.Fatalf("err = %v, want ErrUnknownType", err)
	}
}

func TestFrameDecodeRejectsMalformedControl(t *testing.T) {
	// Resize / Exit carry a fixed 4-byte payload; any other length is malformed.
	for _, tc := range []struct {
		name string
		raw  []byte
	}{
		{"resize-wrong-len", []byte{byte(Resize), 0, 0, 0, 2, 0x00, 0x18}},
		{"exit-wrong-len", []byte{byte(Exit), 0, 0, 0, 1, 0x2a}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Read(bytes.NewReader(tc.raw)); !errors.Is(err, ErrMalformed) {
				t.Fatalf("err = %v, want ErrMalformed", err)
			}
		})
	}
}

func TestFrameDecodeRejectsOversize(t *testing.T) {
	// A corrupt length must not trigger a giant allocation.
	raw := []byte{byte(Data), 0xff, 0xff, 0xff, 0xff} // claims ~4GiB
	if _, err := Read(bytes.NewReader(raw)); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("err = %v, want ErrFrameTooLarge", err)
	}
}

// framesEqual compares the meaningful fields per frame type so a Data frame's
// zero Rows/Cols/Code and a control frame's nil Data don't cause false negatives.
func framesEqual(a, b Frame) bool {
	if a.Type != b.Type {
		return false
	}
	switch a.Type {
	case Data:
		return bytes.Equal(a.Data, b.Data)
	case Resize:
		return a.Rows == b.Rows && a.Cols == b.Cols
	case Exit:
		return a.Code == b.Code
	default:
		return false
	}
}
