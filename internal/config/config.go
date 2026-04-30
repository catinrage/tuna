package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"
)

type Duration struct {
	time.Duration
}

func (d *Duration) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("duration must be a string: %w", err)
	}

	parsed, err := time.ParseDuration(raw)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", raw, err)
	}

	d.Duration = parsed
	return nil
}

type NATSConfig struct {
	URL                  string   `json:"url"`
	Username             string   `json:"username"`
	Password             string   `json:"password"`
	Name                 string   `json:"name"`
	ConnectTimeout       Duration `json:"connect_timeout"`
	ReconnectWait        Duration `json:"reconnect_wait"`
	PingInterval         Duration `json:"ping_interval"`
	MaxPingsOutstanding  int      `json:"max_pings_outstanding"`
	MaxReconnects        int      `json:"max_reconnects"`
	ReconnectBufferBytes int      `json:"reconnect_buffer_bytes"`
}

type TunnelConfig struct {
	SubjectPrefix               string   `json:"subject_prefix"`
	DataSubjectShards           int      `json:"data_subject_shards"`
	ChunkSizeBytes              int      `json:"chunk_size_bytes"`
	WriteBatchBytes             int      `json:"write_batch_bytes"`
	ReadCoalesceMinDelay        Duration `json:"read_coalesce_min_delay"`
	ReadCoalesceMaxDelay        Duration `json:"read_coalesce_max_delay"`
	WriteCoalesceMinDelay       Duration `json:"write_coalesce_min_delay"`
	WriteCoalesceMaxDelay       Duration `json:"write_coalesce_max_delay"`
	SessionQueueDepth           int      `json:"session_queue_depth"`
	QueueBackpressureTimeout    Duration `json:"queue_backpressure_timeout"`
	SessionIdleTimeout          Duration `json:"session_idle_timeout"`
	CleanupInterval             Duration `json:"cleanup_interval"`
	SubscriptionPendingMessages int      `json:"subscription_pending_messages"`
	SubscriptionPendingBytes    int      `json:"subscription_pending_bytes"`
	TCPNoDelay                  bool     `json:"tcp_no_delay"`
	TCPKeepAlive                Duration `json:"tcp_keep_alive"`
	TCPReadBufferBytes          int      `json:"tcp_read_buffer_bytes"`
	TCPWriteBufferBytes         int      `json:"tcp_write_buffer_bytes"`
}

type EntryRoleConfig struct {
	ListenAddress  string   `json:"listen_address"`
	RequestTimeout Duration `json:"request_timeout"`
}

type ExitRoleConfig struct {
	DialTimeout Duration `json:"dial_timeout"`
}

type EntryConfig struct {
	NATS   NATSConfig      `json:"nats"`
	Tunnel TunnelConfig    `json:"tunnel"`
	Entry  EntryRoleConfig `json:"entry"`
}

type ExitConfig struct {
	NATS   NATSConfig     `json:"nats"`
	Tunnel TunnelConfig   `json:"tunnel"`
	Exit   ExitRoleConfig `json:"exit"`
}

func LoadEntry(path string) (EntryConfig, error) {
	cfg := EntryConfig{}
	applyDefaults(&cfg.NATS, &cfg.Tunnel)
	cfg.Entry.ListenAddress = "127.0.0.1:1080"
	cfg.Entry.RequestTimeout = Duration{Duration: 10 * time.Second}

	if err := loadJSON(path, &cfg); err != nil {
		return EntryConfig{}, err
	}

	if err := validateNATS(cfg.NATS); err != nil {
		return EntryConfig{}, err
	}

	if err := validateTunnel(cfg.Tunnel); err != nil {
		return EntryConfig{}, err
	}

	if cfg.Entry.ListenAddress == "" {
		return EntryConfig{}, errors.New("entry.listen_address is required")
	}

	if cfg.Entry.RequestTimeout.Duration <= 0 {
		return EntryConfig{}, errors.New("entry.request_timeout must be positive")
	}

	return cfg, nil
}

func LoadExit(path string) (ExitConfig, error) {
	cfg := ExitConfig{}
	applyDefaults(&cfg.NATS, &cfg.Tunnel)
	cfg.Exit.DialTimeout = Duration{Duration: 10 * time.Second}

	if err := loadJSON(path, &cfg); err != nil {
		return ExitConfig{}, err
	}

	if err := validateNATS(cfg.NATS); err != nil {
		return ExitConfig{}, err
	}

	if err := validateTunnel(cfg.Tunnel); err != nil {
		return ExitConfig{}, err
	}

	if cfg.Exit.DialTimeout.Duration <= 0 {
		return ExitConfig{}, errors.New("exit.dial_timeout must be positive")
	}

	return cfg, nil
}

func applyDefaults(natsCfg *NATSConfig, tunnelCfg *TunnelConfig) {
	natsCfg.URL = "nats://127.0.0.1:4222"
	natsCfg.Username = "nats_client"
	natsCfg.ConnectTimeout = Duration{Duration: 5 * time.Second}
	natsCfg.ReconnectWait = Duration{Duration: time.Second}
	natsCfg.PingInterval = Duration{Duration: 20 * time.Second}
	natsCfg.MaxPingsOutstanding = 5
	natsCfg.MaxReconnects = -1
	natsCfg.ReconnectBufferBytes = 64 << 20

	tunnelCfg.SubjectPrefix = "tuna"
	tunnelCfg.DataSubjectShards = 16
	tunnelCfg.ChunkSizeBytes = 512 << 10
	tunnelCfg.WriteBatchBytes = 1 << 20
	tunnelCfg.ReadCoalesceMinDelay = Duration{Duration: 50 * time.Microsecond}
	tunnelCfg.ReadCoalesceMaxDelay = Duration{Duration: 500 * time.Microsecond}
	tunnelCfg.WriteCoalesceMinDelay = Duration{Duration: 50 * time.Microsecond}
	tunnelCfg.WriteCoalesceMaxDelay = Duration{Duration: 500 * time.Microsecond}
	tunnelCfg.SessionQueueDepth = 2048
	tunnelCfg.QueueBackpressureTimeout = Duration{Duration: 2 * time.Second}
	tunnelCfg.SessionIdleTimeout = Duration{Duration: 2 * time.Minute}
	tunnelCfg.CleanupInterval = Duration{Duration: 15 * time.Second}
	tunnelCfg.SubscriptionPendingMessages = 256 * 1024
	tunnelCfg.SubscriptionPendingBytes = 512 << 20
	tunnelCfg.TCPNoDelay = true
	tunnelCfg.TCPKeepAlive = Duration{Duration: 30 * time.Second}
	tunnelCfg.TCPReadBufferBytes = 4 << 20
	tunnelCfg.TCPWriteBufferBytes = 4 << 20
}

func loadJSON(path string, dst any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config %s: %w", path, err)
	}

	if err := json.Unmarshal(data, dst); err != nil {
		return fmt.Errorf("parse config %s: %w", path, err)
	}

	return nil
}

func validateNATS(cfg NATSConfig) error {
	if cfg.URL == "" {
		return errors.New("nats.url is required")
	}

	if cfg.ConnectTimeout.Duration <= 0 {
		return errors.New("nats.connect_timeout must be positive")
	}

	if cfg.ReconnectWait.Duration <= 0 {
		return errors.New("nats.reconnect_wait must be positive")
	}

	if cfg.PingInterval.Duration <= 0 {
		return errors.New("nats.ping_interval must be positive")
	}

	if cfg.MaxPingsOutstanding <= 0 {
		return errors.New("nats.max_pings_outstanding must be positive")
	}

	if cfg.ReconnectBufferBytes < 0 {
		return errors.New("nats.reconnect_buffer_bytes cannot be negative")
	}

	if cfg.Password == "" {
		return errors.New("nats.password is required")
	}

	return nil
}

func validateTunnel(cfg TunnelConfig) error {
	if cfg.SubjectPrefix == "" {
		return errors.New("tunnel.subject_prefix is required")
	}

	if cfg.DataSubjectShards <= 0 {
		return errors.New("tunnel.data_subject_shards must be positive")
	}

	if cfg.ChunkSizeBytes <= 0 {
		return errors.New("tunnel.chunk_size_bytes must be positive")
	}

	if cfg.WriteBatchBytes <= 0 {
		return errors.New("tunnel.write_batch_bytes must be positive")
	}

	if cfg.WriteBatchBytes < cfg.ChunkSizeBytes {
		return errors.New("tunnel.write_batch_bytes must be greater than or equal to tunnel.chunk_size_bytes")
	}

	if cfg.ReadCoalesceMinDelay.Duration < 0 || cfg.ReadCoalesceMaxDelay.Duration < 0 {
		return errors.New("tunnel read coalesce delays cannot be negative")
	}

	if cfg.WriteCoalesceMinDelay.Duration < 0 || cfg.WriteCoalesceMaxDelay.Duration < 0 {
		return errors.New("tunnel write coalesce delays cannot be negative")
	}

	if cfg.ReadCoalesceMaxDelay.Duration < cfg.ReadCoalesceMinDelay.Duration {
		return errors.New("tunnel.read_coalesce_max_delay must be greater than or equal to tunnel.read_coalesce_min_delay")
	}

	if cfg.WriteCoalesceMaxDelay.Duration < cfg.WriteCoalesceMinDelay.Duration {
		return errors.New("tunnel.write_coalesce_max_delay must be greater than or equal to tunnel.write_coalesce_min_delay")
	}

	if cfg.SessionQueueDepth <= 0 {
		return errors.New("tunnel.session_queue_depth must be positive")
	}

	if cfg.QueueBackpressureTimeout.Duration <= 0 {
		return errors.New("tunnel.queue_backpressure_timeout must be positive")
	}

	if cfg.SessionIdleTimeout.Duration <= 0 {
		return errors.New("tunnel.session_idle_timeout must be positive")
	}

	if cfg.CleanupInterval.Duration <= 0 {
		return errors.New("tunnel.cleanup_interval must be positive")
	}

	if cfg.SubscriptionPendingMessages <= 0 {
		return errors.New("tunnel.subscription_pending_messages must be positive")
	}

	if cfg.SubscriptionPendingBytes <= 0 {
		return errors.New("tunnel.subscription_pending_bytes must be positive")
	}

	if cfg.TCPKeepAlive.Duration <= 0 {
		return errors.New("tunnel.tcp_keep_alive must be positive")
	}

	if cfg.TCPReadBufferBytes < 0 || cfg.TCPWriteBufferBytes < 0 {
		return errors.New("tunnel TCP buffer sizes cannot be negative")
	}

	return nil
}
