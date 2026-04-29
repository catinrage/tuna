package session

import "testing"

type stubSink struct {
	payloads [][]byte
	closed   bool
}

func (s *stubSink) Enqueue(payload []byte) bool {
	s.payloads = append(s.payloads, append([]byte(nil), payload...))
	return true
}

func (s *stubSink) Close() {
	s.closed = true
}

func TestRegistryAddDeliverRemove(t *testing.T) {
	registry := NewRegistry()
	sink := &stubSink{}

	if err := registry.Add(1, sink); err != nil {
		t.Fatalf("add session: %v", err)
	}

	if !registry.Deliver(1, []byte("hello")) {
		t.Fatalf("deliver returned false")
	}

	if len(sink.payloads) != 1 || string(sink.payloads[0]) != "hello" {
		t.Fatalf("unexpected payloads: %#v", sink.payloads)
	}

	removed := registry.Remove(1)
	if removed != sink {
		t.Fatalf("remove returned %v, want original sink", removed)
	}

	if registry.Deliver(1, []byte("again")) {
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
