package tunnel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"tuna/internal/session"
)

type Publisher func(payload []byte) error

type Bridge struct {
	SessionID             uint64
	Conn                  net.Conn
	Incoming              *session.Queue
	Publish               Publisher
	FramePool             *FramePool
	ChunkSize             int
	WriteBatchBytes       int
	ReadCoalesceMinDelay  time.Duration
	ReadCoalesceMaxDelay  time.Duration
	WriteCoalesceMinDelay time.Duration
	WriteCoalesceMaxDelay time.Duration
	OnActivity            func()
}

func (b *Bridge) Run(ctx context.Context) error {
	if b.Conn == nil {
		return errors.New("bridge connection is nil")
	}
	if b.Publish == nil {
		return errors.New("bridge publisher is nil")
	}
	if b.Incoming == nil {
		return errors.New("bridge incoming queue is nil")
	}
	if b.FramePool == nil {
		return errors.New("bridge frame pool is nil")
	}
	if b.ChunkSize <= 0 {
		return fmt.Errorf("invalid chunk size %d", b.ChunkSize)
	}
	if b.WriteBatchBytes <= 0 {
		return fmt.Errorf("invalid write batch size %d", b.WriteBatchBytes)
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
		errCh <- b.queueToSocket(ctx, &remoteClosed)
	}()

	firstErr := <-errCh
	cancel()
	_ = b.Conn.Close()
	wg.Wait()

	if firstErr != nil && !errors.Is(firstErr, context.Canceled) && !errors.Is(firstErr, net.ErrClosed) && !errors.Is(firstErr, session.ErrQueueClosed) {
		return firstErr
	}
	return nil
}

func (b *Bridge) socketToNATS(ctx context.Context, remoteClosed *atomic.Bool) error {
	readWindow := NewAdaptiveDelay(b.ReadCoalesceMinDelay, b.ReadCoalesceMaxDelay)
	buf := b.FramePool.Get()
	defer b.FramePool.Put(buf)

	for {
		n, err := b.Conn.Read(buf[FrameBufferSize(0):])
		if n > 0 && err == nil && n < b.ChunkSize && readWindow.Current() > 0 {
			var coalesced bool
			n, err, coalesced = b.coalesceSocketRead(buf[FrameBufferSize(0):], n, readWindow.Current())
			if coalesced {
				readWindow.Increase()
			} else {
				readWindow.Decrease()
			}
		}
		if n > 0 {
			if b.OnActivity != nil {
				b.OnActivity()
			}
			if publishErr := b.Publish(DataFrame(buf, b.SessionID, n)); publishErr != nil {
				return publishErr
			}
		}

		if err == nil {
			continue
		}
		if errors.Is(err, io.EOF) {
			if !remoteClosed.Load() {
				_ = b.Publish(EOFFrame(b.SessionID))
			}
			return nil
		}
		if ctx.Err() != nil || remoteClosed.Load() || errors.Is(err, net.ErrClosed) {
			return nil
		}
		_ = b.Publish(EOFFrame(b.SessionID))
		return err
	}
}

func (b *Bridge) queueToSocket(ctx context.Context, remoteClosed *atomic.Bool) error {
	writeWindow := NewAdaptiveDelay(b.WriteCoalesceMinDelay, b.WriteCoalesceMaxDelay)
	var (
		batch      net.Buffers
		held       []*session.Frame
		batchBytes int
	)

	releaseHeld := func() {
		for i, frame := range held {
			if frame != nil {
				frame.Release()
				held[i] = nil
			}
		}
		held = held[:0]
	}
	defer releaseHeld()

	flush := func() error {
		if batchBytes == 0 {
			return nil
		}
		_, err := batch.WriteTo(b.Conn)
		if b.OnActivity != nil {
			b.OnActivity()
		}
		releaseHeld()
		clear(batch)
		batch = batch[:0]
		batchBytes = 0
		return err
	}

	appendFrame := func(frame *session.Frame) (bool, error) {
		frameType, err := FrameType(frame.Data)
		if err != nil {
			frame.Release()
			return false, err
		}
		if frameType == FrameEOF {
			frame.Release()
			remoteClosed.Store(true)
			return true, nil
		}
		if frameType != FrameData {
			frame.Release()
			return false, fmt.Errorf("unknown frame type %q", frameType)
		}

		payload, err := FramePayload(frame.Data)
		if err != nil {
			frame.Release()
			return false, err
		}
		batch = append(batch, payload)
		held = append(held, frame)
		batchBytes += len(payload)
		return false, nil
	}

	for {
		frame, err := b.Incoming.Pop(ctx)
		if err != nil {
			if errors.Is(err, session.ErrQueueClosed) {
				return flush()
			}
			return err
		}
		if frame == nil {
			continue
		}

		eof, err := appendFrame(frame)
		if err != nil {
			return err
		}
		if eof {
			if err := flush(); err != nil {
				return err
			}
			return nil
		}

		delay := writeWindow.Current()
		if delay <= 0 || batchBytes >= b.WriteBatchBytes {
			if err := flush(); err != nil {
				return err
			}
			writeWindow.Increase()
			continue
		}

		timer := time.NewTimer(delay)
		gathered := false
	collect:
		for batchBytes < b.WriteBatchBytes {
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					<-timer.C
				}
				return ctx.Err()
			case <-timer.C:
				break collect
			default:
			}

			frame, ok, popErr := b.Incoming.TryPop()
			if popErr != nil {
				if errors.Is(popErr, session.ErrQueueClosed) {
					break collect
				}
				if !timer.Stop() {
					<-timer.C
				}
				return popErr
			}
			if !ok {
				time.Sleep(10 * time.Microsecond)
				continue
			}

			gathered = true
			eof, err := appendFrame(frame)
			if err != nil {
				if !timer.Stop() {
					<-timer.C
				}
				return err
			}
			if eof {
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				if err := flush(); err != nil {
					return err
				}
				return nil
			}
		}

		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}

		if err := flush(); err != nil {
			return err
		}
		if gathered {
			writeWindow.Increase()
		} else {
			writeWindow.Decrease()
		}
	}
}

func (b *Bridge) coalesceSocketRead(buf []byte, initial int, delay time.Duration) (int, error, bool) {
	if err := b.Conn.SetReadDeadline(time.Now().Add(delay)); err != nil {
		return initial, nil, false
	}
	defer b.Conn.SetReadDeadline(time.Time{})

	total := initial
	coalesced := false
	for total < len(buf) {
		n, err := b.Conn.Read(buf[total:])
		total += n
		if n > 0 {
			coalesced = true
		}
		if err == nil {
			continue
		}
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			return total, nil, coalesced
		}
		return total, err, coalesced
	}
	return total, nil, coalesced
}
