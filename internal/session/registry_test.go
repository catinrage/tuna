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

	if err := registry.Add("abc", sink); err != nil {
		t.Fatalf("add session: %v", err)
	}

	if !registry.Deliver("abc", []byte("hello")) {
		t.Fatalf("deliver returned false")
	}

	if len(sink.payloads) != 1 || string(sink.payloads[0]) != "hello" {
		t.Fatalf("unexpected payloads: %#v", sink.payloads)
	}

	removed := registry.Remove("abc")
	if removed != sink {
		t.Fatalf("remove returned %v, want original sink", removed)
	}

	if registry.Deliver("abc", []byte("again")) {
		t.Fatalf("deliver succeeded after remove")
	}
}

func TestRegistryRejectsDuplicateSession(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Add("dup", &stubSink{}); err != nil {
		t.Fatalf("first add failed: %v", err)
	}

	if err := registry.Add("dup", &stubSink{}); err != ErrSessionExists {
		t.Fatalf("second add error = %v, want %v", err, ErrSessionExists)
	}
}

func TestRegistryCloseAllClosesEachSink(t *testing.T) {
	registry := NewRegistry()
	first := &stubSink{}
	second := &stubSink{}

	if err := registry.Add("a", first); err != nil {
		t.Fatalf("add first: %v", err)
	}
	if err := registry.Add("b", second); err != nil {
		t.Fatalf("add second: %v", err)
	}

	registry.CloseAll()

	if !first.closed || !second.closed {
		t.Fatalf("expected all sinks to be closed: first=%v second=%v", first.closed, second.closed)
	}

	if removed := registry.Remove("a"); removed != nil {
		t.Fatalf("expected registry to be empty after close all")
	}
}
