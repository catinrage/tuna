package exit

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/nats-io/nats.go"

	"tuna/internal/config"
	"tuna/internal/session"
	"tuna/internal/tunnel"
)

type Worker struct {
	cfg      config.ExitConfig
	logger   *log.Logger
	nc       *nats.Conn
	registry *session.Registry
	wg       sync.WaitGroup
}

type remoteSession struct {
	conn     net.Conn
	inbound  chan []byte
	closed   atomic.Bool
	closeOne sync.Once
}

func New(cfg config.ExitConfig, logger *log.Logger) *Worker {
	return &Worker{
		cfg:      cfg,
		logger:   logger,
		registry: session.NewRegistry(),
	}
}

func (w *Worker) Run(ctx context.Context) error {
	nc, err := tunnel.ConnectNATS(w.cfg.NATS, w.logger)
	if err != nil {
		return err
	}
	w.nc = nc
	defer nc.Drain()

	upstreamSub, err := nc.Subscribe(tunnel.UpSubject(w.cfg.Tunnel.SubjectPrefix), w.handleUpstream)
	if err != nil {
		return fmt.Errorf("subscribe upstream: %w", err)
	}

	if err := upstreamSub.SetPendingLimits(w.cfg.Tunnel.SubscriptionPendingMessages, w.cfg.Tunnel.SubscriptionPendingBytes); err != nil {
		return fmt.Errorf("set upstream pending limits: %w", err)
	}

	connectSub, err := nc.Subscribe(tunnel.ConnectSubject(w.cfg.Tunnel.SubjectPrefix), func(msg *nats.Msg) {
		w.handleConnect(ctx, msg)
	})
	if err != nil {
		return fmt.Errorf("subscribe connect subject: %w", err)
	}

	if err := connectSub.SetPendingLimits(w.cfg.Tunnel.SubscriptionPendingMessages, w.cfg.Tunnel.SubscriptionPendingBytes); err != nil {
		return fmt.Errorf("set connect pending limits: %w", err)
	}

	if err := nc.FlushTimeout(w.cfg.NATS.ConnectTimeout.Duration); err != nil {
		return fmt.Errorf("flush subscriptions: %w", err)
	}

	w.logger.Printf("exit worker ready")

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
		inbound: make(chan []byte, w.cfg.Tunnel.SessionQueueDepth),
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
	w.logger.Printf("[%016x] proxying %s:%d", req.CID, req.Host, req.Port)
	downSubject := tunnel.DownSubject(w.cfg.Tunnel.SubjectPrefix)

	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		defer w.unregister(req.CID)

		bridge := tunnel.Bridge{
			SessionID:          req.CID,
			Conn:               conn,
			Incoming:           remote.inbound,
			ChunkSize:          w.cfg.Tunnel.ChunkSizeBytes,
			ReadCoalesceDelay:  w.cfg.Tunnel.ReadCoalesceDelay.Duration,
			WriteCoalesceDelay: w.cfg.Tunnel.WriteCoalesceDelay.Duration,
			WriteBatchBytes:    w.cfg.Tunnel.WriteBatchBytes,
			Publish: func(frame []byte) error {
				return w.nc.Publish(downSubject, frame)
			},
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

	if delivered := w.registry.Deliver(sessionID, msg.Data); !delivered {
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

func (r *remoteSession) Enqueue(payload []byte) bool {
	if r.closed.Load() {
		return false
	}

	select {
	case r.inbound <- payload:
		return true
	default:
		return false
	}
}

func (r *remoteSession) Close() {
	r.closeOne.Do(func() {
		r.closed.Store(true)
		_ = r.conn.Close()
		close(r.inbound)
	})
}
