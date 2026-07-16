package localproto

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestFrameRoundTrip(t *testing.T) {
	cases := []struct {
		name    string
		ft      FrameType
		payload []byte
	}{
		{"empty", FrameCancel, nil},
		{"raw token", FrameToken, []byte("héllo 世界")},
		{"json", FrameDone, []byte(`{"finish_reason":"stop"}`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := WriteFrame(&buf, tc.ft, tc.payload); err != nil {
				t.Fatalf("WriteFrame: %v", err)
			}
			ft, got, err := ReadFrame(&buf)
			if err != nil {
				t.Fatalf("ReadFrame: %v", err)
			}
			if ft != tc.ft {
				t.Errorf("type = %s, want %s", ft, tc.ft)
			}
			if len(got) != 0 || len(tc.payload) != 0 {
				if !bytes.Equal(got, tc.payload) {
					t.Errorf("payload = %q, want %q", got, tc.payload)
				}
			}
		})
	}
}

func TestWireLayoutMatchesHostproto(t *testing.T) {
	// Pin the exact bytes so this stays lockstep with the worker's hostproto:
	// token frame type=17 (0x11), big-endian len, raw payload.
	var buf bytes.Buffer
	if err := WriteFrame(&buf, FrameToken, []byte("hi")); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	want := []byte{0x11, 0x00, 0x00, 0x00, 0x02, 'h', 'i'}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("token frame bytes = %v, want %v", buf.Bytes(), want)
	}
}

func TestReadFrameEOF(t *testing.T) {
	_, _, err := ReadFrame(bytes.NewReader(nil))
	if !errors.Is(err, io.EOF) {
		t.Fatalf("err = %v, want io.EOF", err)
	}
}

func TestReadFrameRejectsOversized(t *testing.T) {
	var hdr [headerLen]byte
	hdr[0] = byte(FrameToken)
	binary.BigEndian.PutUint32(hdr[1:], uint32(MaxPayloadLen+1))
	_, _, err := ReadFrame(bytes.NewReader(hdr[:]))
	if !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("err = %v, want ErrPayloadTooLarge", err)
	}
}

func TestOpenPayloadRoundTrip(t *testing.T) {
	want := OpenPayload{
		SessionID: "s1",
		Model:     "auto",
		System:    "be terse",
		Tools:     []ToolDef{{Name: "read", Description: "read a file", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		Messages:  []Msg{{Role: "user", Content: json.RawMessage(`"hi"`)}},
		Sampling:  Sampling{MaxTokens: 256, Temperature: 0.2},
	}
	var buf bytes.Buffer
	if err := WriteJSON(&buf, FrameOpen, want); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	ft, body, err := ReadFrame(&buf)
	if err != nil || ft != FrameOpen {
		t.Fatalf("ReadFrame: ft=%s err=%v", ft, err)
	}
	var got OpenPayload
	if err := Decode(body, &got); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("round-trip mismatch (-want +got):\n%s", diff)
	}
}
