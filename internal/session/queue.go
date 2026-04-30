package session

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

var (
	ErrQueueClosed       = errors.New("queue closed")
	ErrQueueBackpressure = errors.New("queue backpressure timeout")
)

type Frame struct {
	Data    []byte
	release func([]byte)
}

func NewFrame(data []byte, release func([]byte)) *Frame {
	return &Frame{Data: data, release: release}
}

func (f *Frame) Release() {
	if f == nil {
		return
	}
	if f.release != nil && f.Data != nil {
		f.release(f.Data)
	}
	f.Data = nil
	f.release = nil
}

type Queue struct {
	mu          sync.Mutex
	buf         []*Frame
	head        int
	tail        int
	size        int
	closed      bool
	notEmpty    chan struct{}
	notFull     chan struct{}
	lastActive  atomic.Int64
	waitTimeout time.Duration
}

func NewQueue(depth int, waitTimeout time.Duration) *Queue {
	q := &Queue{
		buf:         make([]*Frame, depth),
		notEmpty:    make(chan struct{}, 1),
		notFull:     make(chan struct{}, 1),
		waitTimeout: waitTimeout,
	}
	q.Touch()
	return q
}

func (q *Queue) Push(ctx context.Context, frame *Frame) error {
	if frame == nil {
		return nil
	}

	var timer *time.Timer
	if q.waitTimeout > 0 {
		timer = time.NewTimer(q.waitTimeout)
		defer timer.Stop()
	}

	for {
		q.mu.Lock()
		if q.closed {
			q.mu.Unlock()
			frame.Release()
			return ErrQueueClosed
		}
		if q.size < len(q.buf) {
			wasEmpty := q.size == 0
			q.buf[q.tail] = frame
			q.tail = (q.tail + 1) % len(q.buf)
			q.size++
			q.TouchLocked()
			q.mu.Unlock()
			if wasEmpty {
				signal(q.notEmpty)
			}
			return nil
		}
		notFull := q.notFull
		q.mu.Unlock()

		if timer == nil {
			select {
			case <-ctx.Done():
				frame.Release()
				return ctx.Err()
			case <-notFull:
			}
			continue
		}

		select {
		case <-ctx.Done():
			frame.Release()
			return ctx.Err()
		case <-timer.C:
			frame.Release()
			return ErrQueueBackpressure
		case <-notFull:
		}
	}
}

func (q *Queue) Pop(ctx context.Context) (*Frame, error) {
	for {
		q.mu.Lock()
		if q.size > 0 {
			wasFull := q.size == len(q.buf)
			frame := q.buf[q.head]
			q.buf[q.head] = nil
			q.head = (q.head + 1) % len(q.buf)
			q.size--
			q.TouchLocked()
			q.mu.Unlock()
			if wasFull {
				signal(q.notFull)
			}
			return frame, nil
		}
		if q.closed {
			q.mu.Unlock()
			return nil, ErrQueueClosed
		}
		notEmpty := q.notEmpty
		q.mu.Unlock()

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-notEmpty:
		}
	}
}

func (q *Queue) TryPop() (*Frame, bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.size > 0 {
		wasFull := q.size == len(q.buf)
		frame := q.buf[q.head]
		q.buf[q.head] = nil
		q.head = (q.head + 1) % len(q.buf)
		q.size--
		q.TouchLocked()
		if wasFull {
			signal(q.notFull)
		}
		return frame, true, nil
	}

	if q.closed {
		return nil, false, ErrQueueClosed
	}

	return nil, false, nil
}

func (q *Queue) Close() {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return
	}
	q.closed = true
	frames := make([]*Frame, 0, q.size)
	for q.size > 0 {
		frame := q.buf[q.head]
		q.buf[q.head] = nil
		q.head = (q.head + 1) % len(q.buf)
		q.size--
		frames = append(frames, frame)
	}
	q.mu.Unlock()

	signal(q.notEmpty)
	signal(q.notFull)

	for _, frame := range frames {
		if frame != nil {
			frame.Release()
		}
	}
}

func (q *Queue) Touch() {
	q.lastActive.Store(time.Now().UnixNano())
}

func (q *Queue) LastActive() time.Time {
	unix := q.lastActive.Load()
	if unix == 0 {
		return time.Time{}
	}
	return time.Unix(0, unix)
}

func (q *Queue) TouchLocked() {
	q.lastActive.Store(time.Now().UnixNano())
}

func signal(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}
