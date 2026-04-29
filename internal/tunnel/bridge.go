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
)

type Publisher func(payload []byte) error

type Bridge struct {
	SessionID          uint64
	Conn               net.Conn
	Incoming           <-chan []byte
	Publish            Publisher
	ChunkSize          int
	ReadCoalesceDelay  time.Duration
	WriteCoalesceDelay time.Duration
	WriteBatchBytes    int
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
	buf := make([]byte, b.ChunkSize+frameHeaderSize)

	for {
		n, err := b.Conn.Read(buf[frameHeaderSize:])
		if n > 0 && err == nil && b.ReadCoalesceDelay > 0 && n < b.ChunkSize {
			n, err = b.coalesceSocketRead(buf[frameHeaderSize:], n)
		}

		if n > 0 {
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

func (b *Bridge) natsToSocket(ctx context.Context, remoteClosed *atomic.Bool) error {
	var (
		batch      net.Buffers
		batchBytes int
	)

	flush := func() error {
		if batchBytes == 0 {
			return nil
		}

		if _, err := batch.WriteTo(b.Conn); err != nil {
			return err
		}

		clear(batch)
		batch = batch[:0]
		batchBytes = 0
		return nil
	}

	for {
		payload, ok, err := b.waitForIncoming(ctx)
		if err != nil {
			return err
		}

		if !ok {
			return flush()
		}

		if len(payload) == 0 {
			continue
		}

		frameType, err := FrameType(payload)
		if err != nil {
			return err
		}

		switch frameType {
		case FrameData:
			framePayload, frameErr := FramePayload(payload)
			if frameErr != nil {
				return frameErr
			}
			batch = append(batch, framePayload)
			batchBytes += len(framePayload)
		case FrameEOF:
			remoteClosed.Store(true)
			if err := flush(); err != nil {
				return err
			}
			return nil
		default:
			return fmt.Errorf("unknown frame type %q", frameType)
		}

		if b.WriteCoalesceDelay <= 0 || batchBytes >= b.WriteBatchBytes {
			if err := flush(); err != nil {
				return err
			}
			continue
		}

		timer := time.NewTimer(b.WriteCoalesceDelay)
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
			case payload, ok := <-b.Incoming:
				if !ok {
					if err := flush(); err != nil {
						return err
					}
					return nil
				}

				if len(payload) == 0 {
					continue
				}

				frameType, err := FrameType(payload)
				if err != nil {
					if !timer.Stop() {
						<-timer.C
					}
					return err
				}

				switch frameType {
				case FrameData:
					framePayload, frameErr := FramePayload(payload)
					if frameErr != nil {
						if !timer.Stop() {
							<-timer.C
						}
						return frameErr
					}
					batch = append(batch, framePayload)
					batchBytes += len(framePayload)
				case FrameEOF:
					remoteClosed.Store(true)
					if !timer.Stop() {
						<-timer.C
					}
					if err := flush(); err != nil {
						return err
					}
					return nil
				default:
					if !timer.Stop() {
						<-timer.C
					}
					return fmt.Errorf("unknown frame type %q", frameType)
				}
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
	}
}

func (b *Bridge) coalesceSocketRead(buf []byte, initial int) (int, error) {
	if err := b.Conn.SetReadDeadline(time.Now().Add(b.ReadCoalesceDelay)); err != nil {
		return initial, nil
	}
	defer b.Conn.SetReadDeadline(time.Time{})

	total := initial
	for total < len(buf) {
		n, err := b.Conn.Read(buf[total:])
		total += n

		if err == nil {
			continue
		}

		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			return total, nil
		}

		return total, err
	}

	return total, nil
}

func (b *Bridge) waitForIncoming(ctx context.Context) ([]byte, bool, error) {
	select {
	case <-ctx.Done():
		return nil, false, ctx.Err()
	case payload, ok := <-b.Incoming:
		return payload, ok, nil
	}
}
