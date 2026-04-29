package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDurationUnmarshalJSON(t *testing.T) {
	var d Duration
	if err := d.UnmarshalJSON([]byte(`"1500ms"`)); err != nil {
		t.Fatalf("unmarshal duration: %v", err)
	}

	if got, want := d.Duration, 1500*time.Millisecond; got != want {
		t.Fatalf("duration = %v, want %v", got, want)
	}
}

func TestDurationUnmarshalJSONRejectsInvalid(t *testing.T) {
	var d Duration
	if err := d.UnmarshalJSON([]byte(`123`)); err == nil || !strings.Contains(err.Error(), "duration must be a string") {
		t.Fatalf("expected string duration error, got %v", err)
	}

	if err := d.UnmarshalJSON([]byte(`"later"`)); err == nil || !strings.Contains(err.Error(), "invalid duration") {
		t.Fatalf("expected invalid duration error, got %v", err)
	}
}

func TestLoadEntryAppliesDefaultsAndOverrides(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "entry.json")
	content := `{
		"nats": {
			"password": "secret"
		},
		"entry": {
			"listen_address": "127.0.0.1:2080"
		}
	}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadEntry(path)
	if err != nil {
		t.Fatalf("load entry config: %v", err)
	}

	if got, want := cfg.Entry.ListenAddress, "127.0.0.1:2080"; got != want {
		t.Fatalf("listen_address = %q, want %q", got, want)
	}
	if got, want := cfg.Entry.RequestTimeout.Duration, 10*time.Second; got != want {
		t.Fatalf("request_timeout = %v, want %v", got, want)
	}
	if got, want := cfg.Tunnel.ChunkSizeBytes, 256<<10; got != want {
		t.Fatalf("chunk_size_bytes = %d, want %d", got, want)
	}
	if got, want := cfg.NATS.URL, "nats://127.0.0.1:4222"; got != want {
		t.Fatalf("nats.url = %q, want %q", got, want)
	}
}

func TestLoadExitRejectsInvalidTunnelConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "exit.json")
	content := `{
		"nats": {
			"password": "secret"
		},
		"tunnel": {
			"chunk_size_bytes": 0
		}
	}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := LoadExit(path)
	if err == nil || !strings.Contains(err.Error(), "chunk_size_bytes") {
		t.Fatalf("expected tunnel validation error, got %v", err)
	}
}

func TestResolvedPasswordPrefersInlineValue(t *testing.T) {
	cfg := NATSConfig{Password: "inline", PasswordEnv: "IGNORED_ENV"}
	got, err := cfg.ResolvedPassword()
	if err != nil {
		t.Fatalf("resolve password: %v", err)
	}
	if got != "inline" {
		t.Fatalf("password = %q, want inline", got)
	}
}

func TestResolvedPasswordReadsEnvironment(t *testing.T) {
	t.Setenv("TUNA_TEST_PASSWORD", "from-env")
	cfg := NATSConfig{PasswordEnv: "TUNA_TEST_PASSWORD"}
	got, err := cfg.ResolvedPassword()
	if err != nil {
		t.Fatalf("resolve password: %v", err)
	}
	if got != "from-env" {
		t.Fatalf("password = %q, want from-env", got)
	}
}

func TestResolvedPasswordRejectsMissingEnvironment(t *testing.T) {
	cfg := NATSConfig{PasswordEnv: "TUNA_MISSING_PASSWORD"}
	_, err := cfg.ResolvedPassword()
	if err == nil || !strings.Contains(err.Error(), "is not set") {
		t.Fatalf("expected missing environment error, got %v", err)
	}
}
