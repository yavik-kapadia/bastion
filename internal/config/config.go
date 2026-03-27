package config

import (
	"fmt"
	"os"
	"time"

	"github.com/BurntSushi/toml"
)

// Config is the top-level application configuration.
type Config struct {
	SRT      SRTConfig      `toml:"srt"`
	API      APIConfig      `toml:"api"`
	Database DatabaseConfig `toml:"database"`
	Logging  LoggingConfig  `toml:"logging"`
}

// SRTConfig configures the SRT relay listener.
type SRTConfig struct {
	ListenAddr          string        `toml:"listen_addr"`
	Latency             time.Duration `toml:"latency"`
	MaxBandwidth        int64         `toml:"max_bandwidth"`
	PayloadSize         int           `toml:"payload_size"`
	SubscriberBufSize   int           `toml:"subscriber_buf_size"`
	AllowUnregistered   bool          `toml:"allow_unregistered_streams"`
}

// APIConfig configures the HTTP API and dashboard server.
type APIConfig struct {
	ListenAddr    string `toml:"listen_addr"`
	CORSOrigin    string `toml:"cors_origin"`
	JWTSecret     string `toml:"jwt_secret"`
	EncryptionKey string `toml:"encryption_key"`
	PublicHost    string `toml:"public_host"`
}

// DatabaseConfig configures the SQLite database.
type DatabaseConfig struct {
	Path string `toml:"path"`
}

// LoggingConfig configures log output.
type LoggingConfig struct {
	Level  string `toml:"level"`
	Format string `toml:"format"`
}

// Load reads and parses the TOML configuration file at path.
func Load(path string) (*Config, error) {
	cfg := defaults()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}
	if _, err := toml.Decode(string(data), cfg); err != nil {
		return nil, fmt.Errorf("parse config %q: %w", path, err)
	}
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	return cfg, nil
}

func defaults() *Config {
	return &Config{
		SRT: SRTConfig{
			ListenAddr:        ":9710",
			Latency:           120 * time.Millisecond,
			MaxBandwidth:      -1,
			PayloadSize:       1316,
			SubscriberBufSize: 512,
			AllowUnregistered: true,
		},
		API: APIConfig{
			ListenAddr: ":8080",
			CORSOrigin: "*",
		},
		Database: DatabaseConfig{
			Path: "./bastion.db",
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "text",
		},
	}
}

func (c *Config) validate() error {
	if c.SRT.ListenAddr == "" {
		return fmt.Errorf("srt.listen_addr must not be empty")
	}
	if c.API.ListenAddr == "" {
		return fmt.Errorf("api.listen_addr must not be empty")
	}
	if c.Database.Path == "" {
		return fmt.Errorf("database.path must not be empty")
	}
	if c.SRT.PayloadSize < 188 {
		return fmt.Errorf("srt.payload_size must be >= 188 (one MPEG-TS packet)")
	}
	return nil
}
