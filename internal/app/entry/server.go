package entry

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go"

	"tuna/internal/config"
	"tuna/internal/session"
	"tuna/internal/tunnel"
)

type Server struct {
	cfg       config.EntryConfig
	logger    *log.Logger
	control   *nats.Conn
	data      *nats.Conn
	registry  *session.Registry
	framePool *tunnel.FramePool
	wg        sync.WaitGroup
	nextID    atomic.Uint64
	bootID    uint32
}

type clientSession struct {
	conn     net.Conn
	inbound  *session.Queue
	closed   atomic.Bool
	closeOne sync.Once
}

func New(cfg config.EntryConfig, logger *log.Logger) *Server {
	bootID, err := randomBootID()
	if err != nil {
		logger.Fatalf("generate entry boot id: %v", err)
	}

	return &Server{
		cfg:       cfg,
		logger:    logger,
		registry:  session.NewRegistry(),
		framePool: tunnel.NewFramePool(tunnel.FrameBufferSize(cfg.Tunnel.ChunkSizeBytes)),
		bootID:    bootID,
	}
}

func (s *Server) Run(ctx context.Context) error {
	control, err := tunnel.ConnectNATS(s.cfg.NATS, s.logger, "entry-control")
	if err != nil {
		return err
	}
	data, err := tunnel.ConnectNATS(s.cfg.NATS, s.logger, "entry-data")
	if err != nil {
		_ = control.Drain()
		return err
	}
	s.control = control
	s.data = data
	defer control.Drain()
	defer data.Drain()

	for shard := 0; shard < s.cfg.Tunnel.DataSubjectShards; shard++ {
		sub, subErr := data.Subscribe(tunnel.DownSubject(s.cfg.Tunnel.SubjectPrefix, shard), s.handleDownstream)
		if subErr != nil {
			return fmt.Errorf("subscribe downstream shard %d: %w", shard, subErr)
		}
		if err := sub.SetPendingLimits(s.cfg.Tunnel.SubscriptionPendingMessages, s.cfg.Tunnel.SubscriptionPendingBytes); err != nil {
			return fmt.Errorf("set downstream pending limits for shard %d: %w", shard, err)
		}
	}

	if err := data.FlushTimeout(s.cfg.NATS.ConnectTimeout.Duration); err != nil {
		return fmt.Errorf("flush downstream subscriptions: %w", err)
	}

	listener, err := net.Listen("tcp", s.cfg.Entry.ListenAddress)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", s.cfg.Entry.ListenAddress, err)
	}
	defer listener.Close()

	s.logger.Printf("entry listening on %s", s.cfg.Entry.ListenAddress)
	go s.cleanupIdleSessions(ctx)
	go func() {
		<-ctx.Done()
		_ = listener.Close()
		s.registry.CloseAll()
	}()

	for {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			if ctx.Err() != nil || errors.Is(acceptErr, net.ErrClosed) {
				break
			}
			if ne, ok := acceptErr.(net.Error); ok && ne.Temporary() {
				s.logger.Printf("temporary accept error: %v", acceptErr)
				time.Sleep(50 * time.Millisecond)
				continue
			}
			return fmt.Errorf("accept connection: %w", acceptErr)
		}

		tunnel.TuneTCP(conn, tunnel.SocketOptions{
			NoDelay:         s.cfg.Tunnel.TCPNoDelay,
			KeepAlive:       s.cfg.Tunnel.TCPKeepAlive.Duration,
			ReadBufferSize:  s.cfg.Tunnel.TCPReadBufferBytes,
			WriteBufferSize: s.cfg.Tunnel.TCPWriteBufferBytes,
		})

		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.serveClient(ctx, conn)
		}()
	}

	s.wg.Wait()
	return nil
}

func (s *Server) serveClient(ctx context.Context, conn net.Conn) {
	addr, err := tunnel.AcceptSOCKS5(conn)
	if err != nil {
		sessionID := s.newSessionID()
		s.logger.Printf("[%016x] SOCKS handshake failed: %v", sessionID, err)
		_ = conn.Close()
		return
	}

	client := &clientSession{
		conn:    conn,
		inbound: session.NewQueue(s.cfg.Tunnel.SessionQueueDepth, s.cfg.Tunnel.QueueBackpressureTimeout.Duration),
	}

	sessionID, err := s.connectExit(addr, client)
	if err != nil {
		s.logger.Printf("exit connect request failed for %s:%d: %v", addr.Host, addr.Port, err)
		_ = tunnel.FailureReply(conn)
		_ = conn.Close()
		return
	}
	defer s.unregister(sessionID)

	if err := tunnel.SuccessReply(conn); err != nil {
		s.logger.Printf("[%016x] send SOCKS success failed: %v", sessionID, err)
		return
	}

	shard := tunnel.DataShard(sessionID, s.cfg.Tunnel.DataSubjectShards)
	upSubject := tunnel.UpSubject(s.cfg.Tunnel.SubjectPrefix, shard)
	s.logger.Printf("[%016x] proxying %s:%d on shard %02d", sessionID, addr.Host, addr.Port, shard)

	bridge := tunnel.Bridge{
		SessionID:             sessionID,
		Conn:                  conn,
		Incoming:              client.inbound,
		Publish:               func(frame []byte) error { return s.data.Publish(upSubject, frame) },
		FramePool:             s.framePool,
		ChunkSize:             s.cfg.Tunnel.ChunkSizeBytes,
		WriteBatchBytes:       s.cfg.Tunnel.WriteBatchBytes,
		ReadCoalesceMinDelay:  s.cfg.Tunnel.ReadCoalesceMinDelay.Duration,
		ReadCoalesceMaxDelay:  s.cfg.Tunnel.ReadCoalesceMaxDelay.Duration,
		WriteCoalesceMinDelay: s.cfg.Tunnel.WriteCoalesceMinDelay.Duration,
		WriteCoalesceMaxDelay: s.cfg.Tunnel.WriteCoalesceMaxDelay.Duration,
		OnActivity:            client.inbound.Touch,
	}

	if err := bridge.Run(ctx); err != nil {
		s.logger.Printf("[%016x] bridge ended with error: %v", sessionID, err)
		return
	}

	s.logger.Printf("[%016x] closed", sessionID)
}

func (s *Server) handleDownstream(msg *nats.Msg) {
	sessionID, err := tunnel.SessionIDFromFrame(msg.Data)
	if err != nil {
		s.logger.Printf("invalid downstream frame: %v", err)
		return
	}

	frame := session.NewFrame(s.framePool.Clone(msg.Data), s.framePool.Put)
	if err := s.registry.Deliver(context.Background(), sessionID, frame); err != nil {
		if !errors.Is(err, session.ErrQueueClosed) {
			s.logger.Printf("[%016x] downstream delivery failed: %v", sessionID, err)
		}
		if sink := s.registry.Remove(sessionID); sink != nil {
			sink.Close()
		}
	}
}

func (s *Server) unregister(id uint64) {
	if sink := s.registry.Remove(id); sink != nil {
		sink.Close()
	}
}

func (s *Server) newSessionID() uint64 {
	return uint64(s.bootID)<<32 | s.nextID.Add(1)
}

func (c *clientSession) Enqueue(ctx context.Context, frame *session.Frame) error {
	if c.closed.Load() {
		frame.Release()
		return session.ErrQueueClosed
	}
	return c.inbound.Push(ctx, frame)
}

func (c *clientSession) Close() {
	c.closeOne.Do(func() {
		c.closed.Store(true)
		_ = c.conn.Close()
		c.inbound.Close()
	})
}

func (c *clientSession) LastActive() time.Time {
	return c.inbound.LastActive()
}

func (s *Server) connectExit(addr tunnel.SocksAddress, client *clientSession) (uint64, error) {
	const maxAttempts = 4

	for attempt := 0; attempt < maxAttempts; attempt++ {
		sessionID := s.newSessionID()
		if err := s.registry.Add(sessionID, client); err != nil {
			continue
		}

		req := tunnel.ConnectRequest{CID: sessionID, Host: addr.Host, Port: addr.Port}
		payload, err := json.Marshal(req)
		if err != nil {
			s.unregister(sessionID)
			return 0, fmt.Errorf("[%016x] encode connect request failed: %w", sessionID, err)
		}

		response, err := s.control.Request(tunnel.ConnectSubject(s.cfg.Tunnel.SubjectPrefix), payload, s.cfg.Entry.RequestTimeout.Duration)
		if err != nil {
			s.unregister(sessionID)
			return 0, fmt.Errorf("[%016x] %w", sessionID, err)
		}
		if string(response.Data) == "OK" {
			return sessionID, nil
		}

		s.unregister(sessionID)
		reply := strings.TrimSpace(string(response.Data))
		if strings.Contains(reply, "duplicate session") {
			s.logger.Printf("[%016x] duplicate session rejected by exit, retrying", sessionID)
			continue
		}
		return 0, fmt.Errorf("[%016x] exit refused connection to %s:%d: %s", sessionID, addr.Host, addr.Port, reply)
	}

	return 0, fmt.Errorf("unable to allocate unique session id after %d attempts", maxAttempts)
}

func (s *Server) cleanupIdleSessions(ctx context.Context) {
	ticker := time.NewTicker(s.cfg.Tunnel.CleanupInterval.Duration)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cutoff := time.Now().Add(-s.cfg.Tunnel.SessionIdleTimeout.Duration)
			if closed := s.registry.CloseIdle(cutoff); closed > 0 {
				s.logger.Printf("closed %d idle entry sessions", closed)
			}
		}
	}
}

func randomBootID() (uint32, error) {
	var buf [4]byte
	if _, err := cryptorand.Read(buf[:]); err != nil {
		return 0, err
	}
	bootID := binary.BigEndian.Uint32(buf[:])
	if bootID == 0 {
		bootID = 1
	}
	return bootID, nil
}
