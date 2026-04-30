package tunnel

import (
	"fmt"
	"log"

	"github.com/nats-io/nats.go"

	"tuna/internal/config"
)

func ConnectNATS(cfg config.NATSConfig, logger *log.Logger, role string) (*nats.Conn, error) {
	name := cfg.Name
	if role != "" {
		name = cfg.Name + "-" + role
	}

	options := []nats.Option{
		nats.Name(name),
		nats.UserInfo(cfg.Username, cfg.Password),
		nats.Timeout(cfg.ConnectTimeout.Duration),
		nats.MaxReconnects(cfg.MaxReconnects),
		nats.ReconnectWait(cfg.ReconnectWait.Duration),
		nats.PingInterval(cfg.PingInterval.Duration),
		nats.MaxPingsOutstanding(cfg.MaxPingsOutstanding),
		nats.ReconnectBufSize(cfg.ReconnectBufferBytes),
		nats.DisconnectErrHandler(func(_ *nats.Conn, disconnectErr error) {
			if disconnectErr != nil {
				logger.Printf("nats %s disconnected: %v", role, disconnectErr)
				return
			}
			logger.Printf("nats %s disconnected", role)
		}),
		nats.ReconnectHandler(func(conn *nats.Conn) {
			logger.Printf("nats %s reconnected: %s", role, conn.ConnectedUrl())
		}),
		nats.ClosedHandler(func(conn *nats.Conn) {
			if lastErr := conn.LastError(); lastErr != nil {
				logger.Printf("nats %s closed: %v", role, lastErr)
				return
			}
			logger.Printf("nats %s closed", role)
		}),
		nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, subErr error) {
			logger.Printf("nats %s async error: %v", role, subErr)
		}),
	}

	conn, err := nats.Connect(cfg.URL, options...)
	if err != nil {
		return nil, fmt.Errorf("connect nats: %w", err)
	}

	return conn, nil
}
