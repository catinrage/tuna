package tunnel

import (
	"bytes"
	"testing"
)

func TestSubjectHelpers(t *testing.T) {
	if got, want := ConnectSubject("tuna"), "tuna.connect"; got != want {
		t.Fatalf("connect subject = %q, want %q", got, want)
	}
	if got, want := UpSubject("tuna", 3), "tuna.up.03"; got != want {
		t.Fatalf("up subject = %q, want %q", got, want)
	}
	if got, want := DownSubject("tuna", 12), "tuna.down.12"; got != want {
		t.Fatalf("down subject = %q, want %q", got, want)
	}
	if got, want := DataShard(42, 16), 10; got != want {
		t.Fatalf("data shard = %d, want %d", got, want)
	}
}

func TestDataAndEOFFrames(t *testing.T) {
	buf := make([]byte, FrameBufferSize(4))
	copy(buf[FrameBufferSize(0):], []byte("test"))
	frame := DataFrame(buf, 42, 4)

	kind, err := FrameType(frame)
	if err != nil {
		t.Fatalf("frame type failed: %v", err)
	}
	if kind != FrameData {
		t.Fatalf("frame type = %q, want %q", kind, FrameData)
	}

	sessionID, err := SessionIDFromFrame(frame)
	if err != nil {
		t.Fatalf("session id parse failed: %v", err)
	}
	if sessionID != 42 {
		t.Fatalf("session id = %d, want 42", sessionID)
	}

	payload, err := FramePayload(frame)
	if err != nil {
		t.Fatalf("frame payload failed: %v", err)
	}
	if !bytes.Equal(payload, []byte("test")) {
		t.Fatalf("frame payload = %q, want %q", payload, []byte("test"))
	}

	eof := EOFFrame(99)
	kind, err = FrameType(eof)
	if err != nil {
		t.Fatalf("eof frame type failed: %v", err)
	}
	if kind != FrameEOF {
		t.Fatalf("eof frame type = %q, want %q", kind, FrameEOF)
	}

	sessionID, err = SessionIDFromFrame(eof)
	if err != nil {
		t.Fatalf("eof session id parse failed: %v", err)
	}
	if sessionID != 99 {
		t.Fatalf("eof session id = %d, want 99", sessionID)
	}
}

func TestFrameHelpersRejectShortFrames(t *testing.T) {
	short := []byte{FrameData, 1, 2}
	if _, err := FrameType(short); err == nil {
		t.Fatalf("expected short frame type error")
	}
	if _, err := SessionIDFromFrame(short); err == nil {
		t.Fatalf("expected short frame session id error")
	}
	if _, err := FramePayload(short); err == nil {
		t.Fatalf("expected short frame payload error")
	}
}
