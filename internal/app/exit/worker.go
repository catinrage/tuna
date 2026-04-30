package exit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go"

	"tuna/internal/config"
	"tuna/internal/session"
	"tuna/internal/tunnel"
)

type Worker struct {
	cfg       config.ExitConfig
	logger    *log.Logger
	control   *nats.Conn
	data      *nats.Conn
	registry  *session.Registry
	framePool *tunnel.FramePool
	wg        sync.WaitGroup
}

type remoteSession struct {
	conn     net.Conn
	inbound  *session.Queue
	closed   atomic.Bool
	closeOne sync.Once
}

func New(cfg config.ExitConfig, logger *log.Logger) *Worker {
	return &Worker{
		cfg:       cfg,
		logger:    logger,
		registry:  session.NewRegistry(),
		framePool: tunnel.NewFramePool(tunnel.FrameBufferSize(cfg.Tunnel.ChunkSizeBytes)),
	}
}

func (w *Worker) Run(ctx context.Context) error {
	control, err := tunnel.ConnectNATS(w.cfg.NATS, w.logger, "exit-control")
	if err != nil {
		return err
	}
	data, err := tunnel.ConnectNATS(w.cfg.NATS, w.logger, "exit-data")
	if err != nil {
		_ = control.Drain()
		return err
	}
	w.control = control
	w.data = data
	defer control.Drain()
	defer data.Drain()

	for shard := 0; shard < w.cfg.Tunnel.DataSubjectShards; shard++ {
		sub, subErr := data.Subscribe(tunnel.UpSubject(w.cfg.Tunnel.SubjectPrefix, shard), w.handleUpstream)
		if subErr != nil {
			return fmt.Errorf("subscribe upstream shard %d: %w", shard, subErr)
		}
		if err := sub.SetPendingLimits(w.cfg.Tunnel.SubscriptionPendingMessages, w.cfg.Tunnel.SubscriptionPendingBytes); err != nil {
			return fmt.Errorf("set upstream pending limits for shard %d: %w", shard, err)
		}
	}

	connectSub, err := control.Subscribe(tunnel.ConnectSubject(w.cfg.Tunnel.SubjectPrefix), func(msg *nats.Msg) {
		w.wg.Add(1)
		go func() {
			defer w.wg.Done()
			w.handleConnect(ctx, msg)
		}()
	})
	if err != nil {
		return fmt.Errorf("subscribe connect subject: %w", err)
	}
	if err := connectSub.SetPendingLimits(w.cfg.Tunnel.SubscriptionPendingMessages, w.cfg.Tunnel.SubscriptionPendingBytes); err != nil {
		return fmt.Errorf("set connect pending limits: %w", err)
	}

	if err := data.FlushTimeout(w.cfg.NATS.ConnectTimeout.Duration); err != nil {
		return fmt.Errorf("flush upstream subscriptions: %w", err)
	}
	if err := control.FlushTimeout(w.cfg.NATS.ConnectTimeout.Duration); err != nil {
		return fmt.Errorf("flush control subscriptions: %w", err)
	}

	w.logger.Printf("exit worker ready")
	go w.cleanupIdleSessions(ctx)

	<-ctx.Done()
	w.registry.CloseAll()
	w.wg.Wait()
	return nil
}

func (w *Worker) handleConnect(ctx context.Context, msg *nats.Msg) {
	var req tunnel.ConnectRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		w.reply(msg, "ERR invalid connect request")
		w.logger.Printf("decode connect request failed: %v", err)
		return
	}

	dialer := net.Dialer{Timeout: w.cfg.Exit.DialTimeout.Duration}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(req.Host, strconv.Itoa(req.Port)))
	if err != nil {
		w.reply(msg, "ERR dial failed")
		w.logger.Printf("[%016x] dial %s:%d failed: %v", req.CID, req.Host, req.Port, err)
		return
	}

	tunnel.TuneTCP(conn, tunnel.SocketOptions{
		NoDelay:         w.cfg.Tunnel.TCPNoDelay,
		KeepAlive:       w.cfg.Tunnel.TCPKeepAlive.Duration,
		ReadBufferSize:  w.cfg.Tunnel.TCPReadBufferBytes,
		WriteBufferSize: w.cfg.Tunnel.TCPWriteBufferBytes,
	})

	remote := &remoteSession{
		conn:    conn,
		inbound: session.NewQueue(w.cfg.Tunnel.SessionQueueDepth, w.cfg.Tunnel.QueueBackpressureTimeout.Duration),
	}
	if err := w.registry.Add(req.CID, remote); err != nil {
		_ = conn.Close()
		w.reply(msg, "ERR duplicate session")
		w.logger.Printf("[%016x] duplicate session id", req.CID)
		return
	}

	if err := msg.Respond([]byte("OK")); err != nil {
		_ = conn.Close()
		w.unregister(req.CID)
		w.logger.Printf("[%016x] connect reply failed: %v", req.CID, err)
		return
	}

	shard := tunnel.DataShard(req.CID, w.cfg.Tunnel.DataSubjectShards)
	downSubject := tunnel.DownSubject(w.cfg.Tunnel.SubjectPrefix, shard)
	w.logger.Printf("[%016x] proxying %s:%d on shard %02d", req.CID, req.Host, req.Port, shard)

	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		defer w.unregister(req.CID)

		bridge := tunnel.Bridge{
			SessionID:             req.CID,
			Conn:                  conn,
			Incoming:              remote.inbound,
			Publish:               func(frame []byte) error { return w.data.Publish(downSubject, frame) },
			FramePool:             w.framePool,
			ChunkSize:             w.cfg.Tunnel.ChunkSizeBytes,
			WriteBatchBytes:       w.cfg.Tunnel.WriteBatchBytes,
			ReadCoalesceMinDelay:  w.cfg.Tunnel.ReadCoalesceMinDelay.Duration,
			ReadCoalesceMaxDelay:  w.cfg.Tunnel.ReadCoalesceMaxDelay.Duration,
			WriteCoalesceMinDelay: w.cfg.Tunnel.WriteCoalesceMinDelay.Duration,
			WriteCoalesceMaxDelay: w.cfg.Tunnel.WriteCoalesceMaxDelay.Duration,
			OnActivity:            remote.inbound.Touch,
		}

		if bridgeErr := bridge.Run(ctx); bridgeErr != nil {
			w.logger.Printf("[%016x] bridge ended with error: %v", req.CID, bridgeErr)
			return
		}

		w.logger.Printf("[%016x] closed", req.CID)
	}()
}

func (w *Worker) handleUpstream(msg *nats.Msg) {
	sessionID, err := tunnel.SessionIDFromFrame(msg.Data)
	if err != nil {
		w.logger.Printf("invalid upstream frame: %v", err)
		return
	}

	frame := session.NewFrame(w.framePool.Clone(msg.Data), w.framePool.Put)
	if err := w.registry.Deliver(context.Background(), sessionID, frame); err != nil {
		if !errors.Is(err, session.ErrQueueClosed) {
			w.logger.Printf("[%016x] upstream delivery failed: %v", sessionID, err)
		}
		if sink := w.registry.Remove(sessionID); sink != nil {
			sink.Close()
		}
	}
}

func (w *Worker) unregister(id uint64) {
	if sink := w.registry.Remove(id); sink != nil {
		sink.Close()
	}
}

func (w *Worker) reply(msg *nats.Msg, payload string) {
	if err := msg.Respond([]byte(payload)); err != nil && msg.Reply != "" {
		w.logger.Printf("reply failed: %v", err)
	}
}

func (r *remoteSession) Enqueue(ctx context.Context, frame *session.Frame) error {
	if r.closed.Load() {
		frame.Release()
		return session.ErrQueueClosed
	}
	return r.inbound.Push(ctx, frame)
}

func (r *remoteSession) Close() {
	r.closeOne.Do(func() {
		r.closed.Store(true)
		_ = r.conn.Close()
		r.inbound.Close()
	})
}

func (r *remoteSession) LastActive() time.Time {
	return r.inbound.LastActive()
}

func (w *Worker) cleanupIdleSessions(ctx context.Context) {
	ticker := time.NewTicker(w.cfg.Tunnel.CleanupInterval.Duration)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cutoff := time.Now().Add(-w.cfg.Tunnel.SessionIdleTimeout.Duration)
			if closed := w.registry.CloseIdle(cutoff); closed > 0 {
				w.logger.Printf("closed %d idle exit sessions", closed)
			}
		}
	}
}
