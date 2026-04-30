package session

import (
	"context"
	"errors"
	"sync"
	"time"
)

var ErrSessionExists = errors.New("session already exists")

type Sink interface {
	Enqueue(ctx context.Context, frame *Frame) error
	Close()
	LastActive() time.Time
}

type Registry struct {
	mu       sync.RWMutex
	sessions map[uint64]Sink
}

func NewRegistry() *Registry {
	return &Registry{sessions: make(map[uint64]Sink)}
}

func (r *Registry) Add(id uint64, sink Sink) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.sessions[id]; exists {
		return ErrSessionExists
	}

	r.sessions[id] = sink
	return nil
}

func (r *Registry) Remove(id uint64) Sink {
	r.mu.Lock()
	defer r.mu.Unlock()

	sink := r.sessions[id]
	delete(r.sessions, id)
	return sink
}

func (r *Registry) Deliver(ctx context.Context, id uint64, frame *Frame) error {
	r.mu.RLock()
	sink := r.sessions[id]
	r.mu.RUnlock()

	if sink == nil {
		if frame != nil {
			frame.Release()
		}
		return ErrQueueClosed
	}

	return sink.Enqueue(ctx, frame)
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

func (r *Registry) CloseIdle(cutoff time.Time) int {
	r.mu.Lock()
	ids := make([]uint64, 0)
	sinks := make([]Sink, 0)
	for id, sink := range r.sessions {
		if sink.LastActive().Before(cutoff) {
			ids = append(ids, id)
			sinks = append(sinks, sink)
		}
	}
	for _, id := range ids {
		delete(r.sessions, id)
	}
	r.mu.Unlock()

	for _, sink := range sinks {
		sink.Close()
	}
	return len(sinks)
}
