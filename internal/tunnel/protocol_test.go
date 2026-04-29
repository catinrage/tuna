package tunnel

import (
	"bytes"
	"testing"
)

func TestSubjectHelpers(t *testing.T) {
	if got, want := ConnectSubject("tuna"), "tuna.connect"; got != want {
		t.Fatalf("connect subject = %q, want %q", got, want)
	}
	if got, want := UpSubject("tuna", "123"), "tuna.up.123"; got != want {
		t.Fatalf("up subject = %q, want %q", got, want)
	}
	if got, want := DownSubject("tuna", "123"), "tuna.down.123"; got != want {
		t.Fatalf("down subject = %q, want %q", got, want)
	}
}

func TestSessionIDFromSubject(t *testing.T) {
	got, err := SessionIDFromSubject("tuna.down.0001")
	if err != nil {
		t.Fatalf("session id parse failed: %v", err)
	}
	if got != "0001" {
		t.Fatalf("session id = %q, want 0001", got)
	}
}

func TestSessionIDFromSubjectRejectsInvalid(t *testing.T) {
	if _, err := SessionIDFromSubject("tuna"); err == nil {
		t.Fatalf("expected invalid subject error")
	}
	if _, err := SessionIDFromSubject("tuna.down."); err == nil {
		t.Fatalf("expected empty session id error")
	}
}

func TestDataAndEOFFrames(t *testing.T) {
	buf := make([]byte, 5)
	copy(buf[1:], []byte("test"))
	frame := DataFrame(buf, 4)
	if frame[0] != FrameData {
		t.Fatalf("frame type = %q, want %q", frame[0], FrameData)
	}
	if !bytes.Equal(frame[1:], []byte("test")) {
		t.Fatalf("frame payload = %q, want %q", frame[1:], []byte("test"))
	}

	eof := EOFFrame()
	if len(eof) != 1 || eof[0] != FrameEOF {
		t.Fatalf("eof frame = %v, want [%q]", eof, FrameEOF)
	}
}
