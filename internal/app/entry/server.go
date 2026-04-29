package entry

import (
	"context"
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
	cfg      config.EntryConfig
	logger   *log.Logger
	nc       *nats.Conn
	registry *session.Registry
	wg       sync.WaitGroup
	nextID   atomic.Uint64
}

type clientSession struct {
	id       string
	conn     net.Conn
	inbound  chan []byte
	closed   atomic.Bool
	closeOne sync.Once
}

func New(cfg config.EntryConfig, logger *log.Logger) *Server {
	return &Server{
		cfg:      cfg,
		logger:   logger,
		registry: session.NewRegistry(),
	}
}

func (s *Server) Run(ctx context.Context) error {
	nc, err := tunnel.ConnectNATS(s.cfg.NATS, s.logger)
	if err != nil {
		return err
	}
	s.nc = nc
	defer nc.Drain()

	sub, err := nc.Subscribe(tunnel.DownWildcard(s.cfg.Tunnel.SubjectPrefix), s.handleDownstream)
	if err != nil {
		return fmt.Errorf("subscribe downstream: %w", err)
	}

	if err := sub.SetPendingLimits(s.cfg.Tunnel.SubscriptionPendingMessages, s.cfg.Tunnel.SubscriptionPendingBytes); err != nil {
		return fmt.Errorf("set downstream pending limits: %w", err)
	}

	if err := nc.FlushTimeout(s.cfg.NATS.ConnectTimeout.Duration); err != nil {
		return fmt.Errorf("flush subscriptions: %w", err)
	}

	listener, err := net.Listen("tcp", s.cfg.Entry.ListenAddress)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", s.cfg.Entry.ListenAddress, err)
	}
	defer listener.Close()

	s.logger.Printf("entry listening on %s", s.cfg.Entry.ListenAddress)

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
	sessionID := s.newSessionID()

	addr, err := tunnel.AcceptSOCKS5(conn)
	if err != nil {
		s.logger.Printf("[%s] SOCKS handshake failed: %v", sessionID, err)
		_ = conn.Close()
		return
	}

	client := &clientSession{
		id:      sessionID,
		conn:    conn,
		inbound: make(chan []byte, s.cfg.Tunnel.SessionQueueDepth),
	}

	if err := s.registry.Add(sessionID, client); err != nil {
		s.logger.Printf("[%s] register session failed: %v", sessionID, err)
		_ = tunnel.FailureReply(conn)
		_ = conn.Close()
		return
	}
	defer s.unregister(sessionID)

	req := tunnel.ConnectRequest{
		CID:  sessionID,
		Host: addr.Host,
		Port: addr.Port,
	}

	payload, err := json.Marshal(req)
	if err != nil {
		s.logger.Printf("[%s] encode connect request failed: %v", sessionID, err)
		_ = tunnel.FailureReply(conn)
		return
	}

	response, err := s.nc.Request(tunnel.ConnectSubject(s.cfg.Tunnel.SubjectPrefix), payload, s.cfg.Entry.RequestTimeout.Duration)
	if err != nil {
		s.logger.Printf("[%s] exit connect request failed: %v", sessionID, err)
		_ = tunnel.FailureReply(conn)
		return
	}

	if string(response.Data) != "OK" {
		s.logger.Printf("[%s] exit refused connection to %s:%d: %s", sessionID, addr.Host, addr.Port, strings.TrimSpace(string(response.Data)))
		_ = tunnel.FailureReply(conn)
		return
	}

	if err := tunnel.SuccessReply(conn); err != nil {
		s.logger.Printf("[%s] send SOCKS success failed: %v", sessionID, err)
		return
	}

	s.logger.Printf("[%s] proxying %s:%d", sessionID, addr.Host, addr.Port)
	upSubject := tunnel.UpSubject(s.cfg.Tunnel.SubjectPrefix, sessionID)

	bridge := tunnel.Bridge{
		Conn:               conn,
		Incoming:           client.inbound,
		ChunkSize:          s.cfg.Tunnel.ChunkSizeBytes,
		ReadCoalesceDelay:  s.cfg.Tunnel.ReadCoalesceDelay.Duration,
		WriteCoalesceDelay: s.cfg.Tunnel.WriteCoalesceDelay.Duration,
		WriteBatchBytes:    s.cfg.Tunnel.WriteBatchBytes,
		Publish: func(frame []byte) error {
			return s.nc.Publish(upSubject, frame)
		},
	}

	if err := bridge.Run(ctx); err != nil {
		s.logger.Printf("[%s] bridge ended with error: %v", sessionID, err)
		return
	}

	s.logger.Printf("[%s] closed", sessionID)
}

func (s *Server) handleDownstream(msg *nats.Msg) {
	sessionID, err := tunnel.SessionIDFromSubject(msg.Subject)
	if err != nil {
		s.logger.Printf("invalid downstream subject %q: %v", msg.Subject, err)
		return
	}

	if delivered := s.registry.Deliver(sessionID, msg.Data); !delivered {
		if sink := s.registry.Remove(sessionID); sink != nil {
			sink.Close()
		}
	}
}

func (s *Server) unregister(id string) {
	if sink := s.registry.Remove(id); sink != nil {
		sink.Close()
	}
}

func (s *Server) newSessionID() string {
	return fmt.Sprintf("%016x", s.nextID.Add(1))
}

func (c *clientSession) Enqueue(payload []byte) bool {
	if c.closed.Load() {
		return false
	}

	select {
	case c.inbound <- payload:
		return true
	default:
		return false
	}
}

func (c *clientSession) Close() {
	c.closeOne.Do(func() {
		c.closed.Store(true)
		_ = c.conn.Close()
		close(c.inbound)
	})
}
