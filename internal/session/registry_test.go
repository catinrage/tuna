package session

import (
	"context"
	"testing"
	"time"
)

type stubSink struct {
	frames []*Frame
	closed bool
	last   time.Time
}

func (s *stubSink) Enqueue(_ context.Context, frame *Frame) error {
	s.frames = append(s.frames, frame)
	s.last = time.Now()
	return nil
}

func (s *stubSink) Close() {
	s.closed = true
	for _, frame := range s.frames {
		frame.Release()
	}
}

func (s *stubSink) LastActive() time.Time {
	if s.last.IsZero() {
		return time.Now()
	}
	return s.last
}

func TestRegistryAddDeliverRemove(t *testing.T) {
	registry := NewRegistry()
	sink := &stubSink{}

	if err := registry.Add(1, sink); err != nil {
		t.Fatalf("add session: %v", err)
	}

	frame := NewFrame([]byte("hello"), nil)
	if err := registry.Deliver(context.Background(), 1, frame); err != nil {
		t.Fatalf("deliver returned error: %v", err)
	}

	if len(sink.frames) != 1 || string(sink.frames[0].Data) != "hello" {
		t.Fatalf("unexpected frames: %#v", sink.frames)
	}

	removed := registry.Remove(1)
	if removed != sink {
		t.Fatalf("remove returned %v, want original sink", removed)
	}

	frame = NewFrame([]byte("again"), nil)
	if err := registry.Deliver(context.Background(), 1, frame); err == nil {
		t.Fatalf("deliver succeeded after remove")
	}
}

func TestRegistryRejectsDuplicateSession(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Add(1, &stubSink{}); err != nil {
		t.Fatalf("first add failed: %v", err)
	}

	if err := registry.Add(1, &stubSink{}); err != ErrSessionExists {
		t.Fatalf("second add error = %v, want %v", err, ErrSessionExists)
	}
}

func TestRegistryCloseAllClosesEachSink(t *testing.T) {
	registry := NewRegistry()
	first := &stubSink{}
	second := &stubSink{}

	if err := registry.Add(1, first); err != nil {
		t.Fatalf("add first: %v", err)
	}
	if err := registry.Add(2, second); err != nil {
		t.Fatalf("add second: %v", err)
	}

	registry.CloseAll()

	if !first.closed || !second.closed {
		t.Fatalf("expected all sinks to be closed: first=%v second=%v", first.closed, second.closed)
	}

	if removed := registry.Remove(1); removed != nil {
		t.Fatalf("expected registry to be empty after close all")
	}
}

func TestRegistryCloseIdle(t *testing.T) {
	registry := NewRegistry()
	idle := &stubSink{last: time.Now().Add(-2 * time.Minute)}
	active := &stubSink{last: time.Now()}

	if err := registry.Add(1, idle); err != nil {
		t.Fatalf("add idle: %v", err)
	}
	if err := registry.Add(2, active); err != nil {
		t.Fatalf("add active: %v", err)
	}

	closed := registry.CloseIdle(time.Now().Add(-time.Minute))
	if closed != 1 {
		t.Fatalf("closed = %d, want 1", closed)
	}
	if !idle.closed || active.closed {
		t.Fatalf("unexpected close state idle=%v active=%v", idle.closed, active.closed)
	}
}
