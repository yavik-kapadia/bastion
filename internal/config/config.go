package config

import (
	"fmt"
	"os"
	"time"

	"github.com/BurntSushi/toml"
)

// Config is the top-level application configuration.
type Config struct {
	SRT       SRTConfig       `toml:"srt"`
	API       APIConfig       `toml:"api"`
	Database  DatabaseConfig  `toml:"database"`
	Logging   LoggingConfig   `toml:"logging"`
	Thumbnail ThumbnailConfig `toml:"thumbnail"`
	MediaInfo MediaInfoConfig `toml:"media_info"`
	Dashboard DashboardConfig `toml:"dashboard"`
	Notify    NotifyConfig    `toml:"notify"`
}

// SRTConfig configures the SRT relay listener.
type SRTConfig struct {
	ListenAddr            string        `toml:"listen_addr"`
	Latency               time.Duration `toml:"latency"`
	MaxBandwidth          int64         `toml:"max_bandwidth"`
	PayloadSize           int           `toml:"payload_size"`
	SubscriberBufSize     int           `toml:"subscriber_buf_size"`
	AllowUnregistered     bool          `toml:"allow_unregistered_streams"`
	ConnectTimeout        time.Duration `toml:"connect_timeout"`         // 0 = use library default
	PeerIdleTimeout       time.Duration `toml:"peer_idle_timeout"`       // 0 = use library default
	MaxConcurrentStreams  int           `toml:"max_concurrent_streams"`  // 0 = unlimited
	DefaultMaxSubscribers int           `toml:"default_max_subscribers"` // applied to streams created with no explicit max
	PublisherGracePeriod  time.Duration `toml:"publisher_grace_period"`  // hold subscribers this long after a publisher disconnect
	ExternalPort          int           `toml:"external_port"`           // host port that external clients reach (e.g. 443 if you NAT 443→9710). 0 = parsed from listen_addr.
}

// APIConfig configures the HTTP API and dashboard server.
type APIConfig struct {
	ListenAddr      string        `toml:"listen_addr"`
	CORSOrigin      string        `toml:"cors_origin"`
	JWTSecret       string        `toml:"jwt_secret"`
	EncryptionKey   string        `toml:"encryption_key"`
	PublicHost      string        `toml:"public_host"`
	SessionTTL      time.Duration `toml:"session_ttl"`       // cookie / session lifetime
	LoginRateLimit  int           `toml:"login_rate_limit"`  // attempts allowed within the window
	LoginRateWindow time.Duration `toml:"login_rate_window"` // window for rate limit
	ForceHTTPS      bool          `toml:"force_https"`       // strict-transport-security + secure cookie even behind a TLS proxy
}

// DatabaseConfig configures the SQLite database.
type DatabaseConfig struct {
	Path string `toml:"path"`
}

// LoggingConfig configures log output and the persistent event-log table.
type LoggingConfig struct {
	Level          string        `toml:"level"`
	Format         string        `toml:"format"`
	EventRetention time.Duration `toml:"event_retention"` // how long to keep entries in event_logs
	EventDBLevel   string        `toml:"event_db_level"`  // min level to persist (debug/info/warn/error); "" = same as Level
	MaxDBSize      int64         `toml:"max_db_size"`     // soft cap on event_logs table size in bytes; 0 = unlimited
}

// ThumbnailConfig controls the per-stream thumbnail capture pipeline.
type ThumbnailConfig struct {
	Enabled     bool          `toml:"enabled"`
	CacheTTL    time.Duration `toml:"cache_ttl"`     // how long a captured frame is reused
	Width       int           `toml:"width"`         // scaled output width in px (height keeps aspect)
	JPEGQuality int           `toml:"jpeg_quality"`  // 1 (best) – 31 (worst), mjpeg semantics
	Timeout     time.Duration `toml:"timeout"`       // ffmpeg invocation timeout
}

// MediaInfoConfig controls the per-publisher ffprobe.
type MediaInfoConfig struct {
	Enabled bool `toml:"enabled"`
}

// DashboardConfig controls front-end customization injected at runtime.
type DashboardConfig struct {
	BrandName             string        `toml:"brand_name"`              // shown in nav + title
	ThumbnailRefreshRate  time.Duration `toml:"thumbnail_refresh_rate"`  // how often the detail page re-requests the thumbnail
}

// NotifyConfig contains outbound notification settings.
type NotifyConfig struct {
	PublisherDisconnectWebhook string `toml:"publisher_disconnect_webhook"` // POST a JSON event when a publisher EOFs. Empty = disabled.
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
			ListenAddr:            ":9710",
			Latency:               120 * time.Millisecond,
			MaxBandwidth:          -1,
			PayloadSize:           1316,
			SubscriberBufSize:     512,
			AllowUnregistered:     true,
			ConnectTimeout:        0,
			PeerIdleTimeout:       0,
			MaxConcurrentStreams:  0,
			DefaultMaxSubscribers: 0,
			PublisherGracePeriod:  0,
			ExternalPort:          0,
		},
		API: APIConfig{
			ListenAddr:      ":8080",
			CORSOrigin:      "*",
			SessionTTL:      24 * time.Hour,
			LoginRateLimit:  10,
			LoginRateWindow: 15 * time.Minute,
			ForceHTTPS:      false,
		},
		Database: DatabaseConfig{
			Path: "./bastion.db",
		},
		Logging: LoggingConfig{
			Level:          "info",
			Format:         "text",
			EventRetention: 24 * time.Hour,
			EventDBLevel:   "",
			MaxDBSize:      0,
		},
		Thumbnail: ThumbnailConfig{
			Enabled:     true,
			CacheTTL:    10 * time.Second,
			Width:       480,
			JPEGQuality: 5,
			Timeout:     15 * time.Second,
		},
		MediaInfo: MediaInfoConfig{
			Enabled: true,
		},
		Dashboard: DashboardConfig{
			BrandName:            "Bastion",
			ThumbnailRefreshRate: 15 * time.Second,
		},
		Notify: NotifyConfig{
			PublisherDisconnectWebhook: "",
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
	if c.Thumbnail.JPEGQuality < 1 || c.Thumbnail.JPEGQuality > 31 {
		return fmt.Errorf("thumbnail.jpeg_quality must be between 1 (best) and 31 (worst)")
	}
	if c.Thumbnail.Width < 16 {
		return fmt.Errorf("thumbnail.width must be at least 16")
	}
	if c.Thumbnail.CacheTTL < 0 {
		return fmt.Errorf("thumbnail.cache_ttl must be non-negative")
	}
	if c.Logging.EventRetention < 0 {
		return fmt.Errorf("logging.event_retention must be non-negative")
	}
	if c.API.SessionTTL <= 0 {
		return fmt.Errorf("api.session_ttl must be positive")
	}
	if c.API.LoginRateLimit < 1 {
		return fmt.Errorf("api.login_rate_limit must be >= 1")
	}
	if c.API.LoginRateWindow <= 0 {
		return fmt.Errorf("api.login_rate_window must be positive")
	}
	if c.SRT.MaxConcurrentStreams < 0 {
		return fmt.Errorf("srt.max_concurrent_streams must be non-negative (0 = unlimited)")
	}
	if c.SRT.PublisherGracePeriod < 0 {
		return fmt.Errorf("srt.publisher_grace_period must be non-negative")
	}
	if c.SRT.ExternalPort < 0 || c.SRT.ExternalPort > 65535 {
		return fmt.Errorf("srt.external_port must be 0-65535")
	}
	return nil
}
