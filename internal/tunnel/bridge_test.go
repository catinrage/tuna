package tunnel

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

func TestBridgeRunCopiesSocketToPublisherAndBack(t *testing.T) {
	local, peer := net.Pipe()
	defer local.Close()
	defer peer.Close()

	incoming := make(chan []byte, 2)
	published := make(chan []byte, 2)
	bridge := Bridge{
		Conn:            local,
		Incoming:        incoming,
		ChunkSize:       32,
		WriteBatchBytes: 32,
		Publish: func(payload []byte) error {
			published <- append([]byte(nil), payload...)
			return nil
		},
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- bridge.Run(context.Background())
	}()

	go func() {
		_, _ = peer.Write([]byte("hello"))
	}()

	select {
	case frame := <-published:
		if frame[0] != FrameData || !bytes.Equal(frame[1:], []byte("hello")) {
			t.Fatalf("published frame = %v", frame)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for published frame")
	}

	incoming <- append([]byte{FrameData}, []byte("world")...)

	buf := make([]byte, 5)
	if _, err := io.ReadFull(peer, buf); err != nil {
		t.Fatalf("read bridged payload: %v", err)
	}
	if string(buf) != "world" {
		t.Fatalf("bridged payload = %q, want world", buf)
	}

	incoming <- EOFFrame()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("bridge run error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for bridge shutdown")
	}
}

func TestBridgeRunRejectsInvalidConfiguration(t *testing.T) {
	bridge := Bridge{}
	if err := bridge.Run(context.Background()); err == nil {
		t.Fatalf("expected invalid config error")
	}
}

func TestBridgePublishesEOFOnSocketClose(t *testing.T) {
	local, peer := net.Pipe()
	defer local.Close()
	defer peer.Close()

	incoming := make(chan []byte)
	var mu sync.Mutex
	frames := make([][]byte, 0, 2)

	bridge := Bridge{
		Conn:            local,
		Incoming:        incoming,
		ChunkSize:       32,
		WriteBatchBytes: 32,
		Publish: func(payload []byte) error {
			mu.Lock()
			defer mu.Unlock()
			frames = append(frames, append([]byte(nil), payload...))
			return nil
		},
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- bridge.Run(context.Background())
	}()

	_ = peer.Close()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("bridge run error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for bridge shutdown")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(frames) == 0 || !bytes.Equal(frames[len(frames)-1], EOFFrame()) {
		t.Fatalf("expected EOF frame, got %v", frames)
	}
}

func TestBridgeRunPropagatesUnknownFrameError(t *testing.T) {
	local, peer := net.Pipe()
	defer local.Close()
	defer peer.Close()

	incoming := make(chan []byte, 1)
	bridge := Bridge{
		Conn:            local,
		Incoming:        incoming,
		ChunkSize:       32,
		WriteBatchBytes: 32,
		Publish:         func(payload []byte) error { return nil },
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- bridge.Run(context.Background())
	}()

	incoming <- []byte{'X'}

	select {
	case err := <-errCh:
		if err == nil || !errors.Is(err, net.ErrClosed) && err.Error() != "unknown frame type 'X'" {
			if err == nil || err.Error() != "unknown frame type 'X'" {
				t.Fatalf("bridge error = %v, want unknown frame type", err)
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for bridge error")
	}
}

func TestBridgeCoalescesSocketReads(t *testing.T) {
	local, peer := net.Pipe()
	defer local.Close()
	defer peer.Close()

	incoming := make(chan []byte, 1)
	published := make(chan []byte, 2)
	bridge := Bridge{
		Conn:              local,
		Incoming:          incoming,
		ChunkSize:         32,
		ReadCoalesceDelay: 5 * time.Millisecond,
		WriteBatchBytes:   32,
		Publish: func(payload []byte) error {
			published <- append([]byte(nil), payload...)
			return nil
		},
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- bridge.Run(context.Background())
	}()

	go func() {
		_, _ = peer.Write([]byte("hello"))
		_, _ = peer.Write([]byte("world"))
		_ = peer.Close()
	}()

	select {
	case frame := <-published:
		if got, want := string(frame[1:]), "helloworld"; got != want {
			t.Fatalf("coalesced frame payload = %q, want %q", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for coalesced publish")
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("bridge run error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for bridge shutdown")
	}
}
