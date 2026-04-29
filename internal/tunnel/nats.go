package tunnel

import (
	"fmt"
	"log"

	"github.com/nats-io/nats.go"

	"tuna/internal/config"
)

func ConnectNATS(cfg config.NATSConfig, logger *log.Logger) (*nats.Conn, error) {
	password, err := cfg.ResolvedPassword()
	if err != nil {
		return nil, err
	}

	options := []nats.Option{
		nats.Name(cfg.Name),
		nats.UserInfo(cfg.Username, password),
		nats.Timeout(cfg.ConnectTimeout.Duration),
		nats.MaxReconnects(cfg.MaxReconnects),
		nats.ReconnectWait(cfg.ReconnectWait.Duration),
		nats.PingInterval(cfg.PingInterval.Duration),
		nats.MaxPingsOutstanding(cfg.MaxPingsOutstanding),
		nats.ReconnectBufSize(cfg.ReconnectBufferBytes),
		nats.DisconnectErrHandler(func(_ *nats.Conn, disconnectErr error) {
			if disconnectErr != nil {
				logger.Printf("nats disconnected: %v", disconnectErr)
				return
			}
			logger.Printf("nats disconnected")
		}),
		nats.ReconnectHandler(func(conn *nats.Conn) {
			logger.Printf("nats reconnected: %s", conn.ConnectedUrl())
		}),
		nats.ClosedHandler(func(conn *nats.Conn) {
			if lastErr := conn.LastError(); lastErr != nil {
				logger.Printf("nats closed: %v", lastErr)
				return
			}
			logger.Printf("nats closed")
		}),
		nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, subErr error) {
			logger.Printf("nats async error: %v", subErr)
		}),
	}

	conn, err := nats.Connect(cfg.URL, options...)
	if err != nil {
		return nil, fmt.Errorf("connect nats: %w", err)
	}

	return conn, nil
}
