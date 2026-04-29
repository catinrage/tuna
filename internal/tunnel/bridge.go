package tunnel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
)

type Publisher func(payload []byte) error

type Bridge struct {
	Conn      net.Conn
	Incoming  <-chan []byte
	Publish   Publisher
	ChunkSize int
}

func (b *Bridge) Run(ctx context.Context) error {
	if b.Conn == nil {
		return errors.New("bridge connection is nil")
	}

	if b.Publish == nil {
		return errors.New("bridge publisher is nil")
	}

	if b.ChunkSize <= 0 {
		return fmt.Errorf("invalid chunk size %d", b.ChunkSize)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var remoteClosed atomic.Bool
	errCh := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		errCh <- b.socketToNATS(ctx, &remoteClosed)
	}()

	go func() {
		defer wg.Done()
		errCh <- b.natsToSocket(ctx, &remoteClosed)
	}()

	firstErr := <-errCh
	cancel()
	_ = b.Conn.Close()
	wg.Wait()

	if firstErr != nil && !errors.Is(firstErr, context.Canceled) && !errors.Is(firstErr, net.ErrClosed) {
		return firstErr
	}

	return nil
}

func (b *Bridge) socketToNATS(ctx context.Context, remoteClosed *atomic.Bool) error {
	buf := make([]byte, b.ChunkSize+1)

	for {
		n, err := b.Conn.Read(buf[1:])
		if n > 0 {
			if publishErr := b.Publish(DataFrame(buf, n)); publishErr != nil {
				return publishErr
			}
		}

		if err == nil {
			continue
		}

		if errors.Is(err, io.EOF) {
			if !remoteClosed.Load() {
				_ = b.Publish(EOFFrame())
			}
			return nil
		}

		if ctx.Err() != nil || remoteClosed.Load() || errors.Is(err, net.ErrClosed) {
			return nil
		}

		_ = b.Publish(EOFFrame())
		return err
	}
}

func (b *Bridge) natsToSocket(ctx context.Context, remoteClosed *atomic.Bool) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case payload, ok := <-b.Incoming:
			if !ok {
				return nil
			}

			if len(payload) == 0 {
				continue
			}

			switch payload[0] {
			case FrameData:
				if err := writeFull(b.Conn, payload[1:]); err != nil {
					return err
				}
			case FrameEOF:
				remoteClosed.Store(true)
				return nil
			default:
				return fmt.Errorf("unknown frame type %q", payload[0])
			}
		}
	}
}

func writeFull(conn net.Conn, payload []byte) error {
	for len(payload) > 0 {
		written, err := conn.Write(payload)
		if err != nil {
			return err
		}
		payload = payload[written:]
	}

	return nil
}
