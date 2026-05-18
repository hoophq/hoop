package wire

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		in   Frame
	}{
		{"stream-open with name", Frame{
			Type: FrameTypeStreamOpen, StreamID: 42, Name: "pg-prod",
		}},
		{"data with payload", Frame{
			Type: FrameTypeStreamData, StreamID: 42, Payload: []byte("hello world"),
		}},
		{"empty-payload close", Frame{
			Type: FrameTypeStreamClose, StreamID: 42,
		}},
		{"error frame", Frame{
			Type: FrameTypeStreamError, StreamID: 42, Payload: []byte("dial timeout"),
		}},
		{"ping on control stream", Frame{
			Type: FrameTypePing, StreamID: 0, Payload: []byte{0x01, 0x02, 0x03},
		}},
		{"binary-safe payload", Frame{
			Type: FrameTypeStreamData, StreamID: 1, Payload: []byte{0x00, 0xff, 0x7e, 0x81},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buf, err := Encode(tc.in)
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}
			got, err := Decode(buf)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if got.Type != tc.in.Type {
				t.Errorf("Type: got %v want %v", got.Type, tc.in.Type)
			}
			if got.StreamID != tc.in.StreamID {
				t.Errorf("StreamID: got %d want %d", got.StreamID, tc.in.StreamID)
			}
			if got.Name != tc.in.Name {
				t.Errorf("Name: got %q want %q", got.Name, tc.in.Name)
			}
			if !bytes.Equal(got.Payload, tc.in.Payload) {
				t.Errorf("Payload: got %x want %x", got.Payload, tc.in.Payload)
			}
		})
	}
}

func TestDecodeShortFrame(t *testing.T) {
	_, err := Decode(make([]byte, headerSize-1))
	if !errors.Is(err, ErrShortFrame) {
		t.Fatalf("expected ErrShortFrame, got %v", err)
	}
}

func TestDecodeVersionMismatch(t *testing.T) {
	f, err := Encode(Frame{Type: FrameTypeStreamClose, StreamID: 1})
	if err != nil {
		t.Fatal(err)
	}
	f[0] = 99
	_, err = Decode(f)
	if !errors.Is(err, ErrVersion) {
		t.Fatalf("expected ErrVersion, got %v", err)
	}
}

func TestDecodeTruncatedName(t *testing.T) {
	// header claims name_len=10, buffer only has 2 trailing bytes
	buf := make([]byte, headerSize+2)
	buf[0] = Version
	buf[1] = byte(FrameTypeStreamOpen)
	// stream_id zeros
	buf[6] = 0
	buf[7] = 10
	_, err := Decode(buf)
	if !errors.Is(err, ErrTruncated) {
		t.Fatalf("expected ErrTruncated, got %v", err)
	}
}

func TestEncodeNameTooLong(t *testing.T) {
	_, err := Encode(Frame{Type: FrameTypeStreamOpen, Name: strings.Repeat("a", MaxNameLen+1)})
	if !errors.Is(err, ErrNameTooLong) {
		t.Fatalf("expected ErrNameTooLong, got %v", err)
	}
}

func TestEncodeFrameTooLarge(t *testing.T) {
	_, err := Encode(Frame{
		Type:    FrameTypeStreamData,
		Payload: make([]byte, MaxFrameSize),
	})
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("expected ErrFrameTooLarge, got %v", err)
	}
}

func TestDecodeFrameTooLarge(t *testing.T) {
	_, err := Decode(make([]byte, MaxFrameSize+1))
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("expected ErrFrameTooLarge, got %v", err)
	}
}

func TestFrameTypeString(t *testing.T) {
	for ft, want := range map[FrameType]string{
		FrameTypeStreamOpen:  "stream-open",
		FrameTypeStreamData:  "stream-data",
		FrameTypeStreamClose: "stream-close",
		FrameTypeStreamError: "stream-error",
		FrameTypePing:        "ping",
		FrameTypePong:        "pong",
	} {
		if got := ft.String(); got != want {
			t.Errorf("FrameType(%d).String() = %q, want %q", ft, got, want)
		}
	}
	if got := FrameType(99).String(); !strings.HasPrefix(got, "unknown(") {
		t.Errorf("unexpected unknown stringification: %q", got)
	}
}
