package session

import (
	"errors"
	"sync"
)

var ErrSessionExists = errors.New("session already exists")

type Sink interface {
	Enqueue(payload []byte) bool
	Close()
}

type Registry struct {
	mu       sync.RWMutex
	sessions map[string]Sink
}

func NewRegistry() *Registry {
	return &Registry{sessions: make(map[string]Sink)}
}

func (r *Registry) Add(id string, sink Sink) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.sessions[id]; exists {
		return ErrSessionExists
	}

	r.sessions[id] = sink
	return nil
}

func (r *Registry) Remove(id string) Sink {
	r.mu.Lock()
	defer r.mu.Unlock()

	sink := r.sessions[id]
	delete(r.sessions, id)
	return sink
}

func (r *Registry) Deliver(id string, payload []byte) bool {
	r.mu.RLock()
	sink := r.sessions[id]
	r.mu.RUnlock()

	if sink == nil {
		return false
	}

	return sink.Enqueue(payload)
}

func (r *Registry) CloseAll() {
	r.mu.Lock()
	sinks := make([]Sink, 0, len(r.sessions))
	for id, sink := range r.sessions {
		sinks = append(sinks, sink)
		delete(r.sessions, id)
	}
	r.mu.Unlock()

	for _, sink := range sinks {
		sink.Close()
	}
}
