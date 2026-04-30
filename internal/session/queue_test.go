package session

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestQueuePushPop(t *testing.T) {
	q := NewQueue(2, 100*time.Millisecond)
	frame := NewFrame([]byte("hello"), nil)
	if err := q.Push(context.Background(), frame); err != nil {
		t.Fatalf("push: %v", err)
	}

	got, err := q.Pop(context.Background())
	if err != nil {
		t.Fatalf("pop: %v", err)
	}
	if string(got.Data) != "hello" {
		t.Fatalf("data = %q, want hello", got.Data)
	}
}

func TestQueueBackpressure(t *testing.T) {
	q := NewQueue(1, 20*time.Millisecond)
	if err := q.Push(context.Background(), NewFrame([]byte("one"), nil)); err != nil {
		t.Fatalf("first push: %v", err)
	}
	start := time.Now()
	err := q.Push(context.Background(), NewFrame([]byte("two"), nil))
	if !errors.Is(err, ErrQueueBackpressure) {
		t.Fatalf("push error = %v, want backpressure", err)
	}
	if time.Since(start) < 15*time.Millisecond {
		t.Fatalf("backpressure returned too quickly")
	}
}

func TestQueueCloseUnblocksPop(t *testing.T) {
	q := NewQueue(1, 20*time.Millisecond)
	errCh := make(chan error, 1)
	go func() {
		_, err := q.Pop(context.Background())
		errCh <- err
	}()
	time.Sleep(10 * time.Millisecond)
	q.Close()
	if err := <-errCh; !errors.Is(err, ErrQueueClosed) {
		t.Fatalf("pop error = %v, want queue closed", err)
	}
}
