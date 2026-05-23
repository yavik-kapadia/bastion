package main

import (
	"context"
	"encoding/hex"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/yavik-kapadia/bastion/internal/api"
	"github.com/yavik-kapadia/bastion/internal/auth"
	"github.com/yavik-kapadia/bastion/internal/config"
	"github.com/yavik-kapadia/bastion/internal/db"
	bastionlog "github.com/yavik-kapadia/bastion/internal/logging"
	"github.com/yavik-kapadia/bastion/internal/metrics"
	"github.com/yavik-kapadia/bastion/internal/notify"
	"github.com/yavik-kapadia/bastion/internal/relay"
	"github.com/yavik-kapadia/bastion/internal/ws"
)

var version = "dev"

func main() {
	cfgPath := flag.String("config", "bastion.toml", "path to TOML config file")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("bastion %s\n", version)
		os.Exit(0)
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	logger := newLogger(cfg.Logging)
	slog.SetDefault(logger)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	slog.Info("Bastion starting",
		"version", version,
		"srt_addr", cfg.SRT.ListenAddr,
		"api_addr", cfg.API.ListenAddr,
		"db", cfg.Database.Path,
	)

	if err := run(ctx, cfg); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
	slog.Info("Bastion stopped")
}

func run(ctx context.Context, cfg *config.Config) error {
	// Database
	database, err := db.Open(cfg.Database.Path)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer database.Close()

	// Metrics
	prom := metrics.NewProm()

	// WebSocket hub
	hub := ws.NewHub()
	go hub.Run(ctx)

	// Tee slog into the DB so the dashboard can show a live log tail.
	// The text handler keeps writing to stdout (container logs); the DB
	// sink is advisory and drops oldest under sustained pressure.
	innerLogger := slog.Default().Handler()
	dbHandler := bastionlog.NewDBHandler(bastionlog.Options{
		Inner: innerLogger,
		Sink:  bastionlog.RepoSink{Repo: database.EventLogs},
		Broadcaster: bastionlog.HubBroadcaster{
			Send: hub.Broadcast,
		},
		MinLevel: dbLogLevel(cfg.Logging),
	})
	slog.SetDefault(slog.New(dbHandler))
	defer dbHandler.Close()

	// Housekeeping: purge logs older than configured retention every 5 minutes.
	go runLogPurge(ctx, database, cfg.Logging.EventRetention, cfg.Logging.MaxDBSize)

	// Decode optional at-rest encryption key.
	var encKey []byte
	if cfg.API.EncryptionKey != "" {
		var err error
		encKey, err = hex.DecodeString(cfg.API.EncryptionKey)
		if err != nil {
			return fmt.Errorf("invalid encryption_key: %w", err)
		}
	}

	// SRT relay — created first so we can pass it to the auth guard.
	// Auth is wired via a closure so the guard pointer is set before any
	// connection is accepted (r.Start is called after both are constructed).
	var guard *auth.Guard
	r := relay.New(cfg.SRT.ListenAddr, cfg.SRT.SubscriberBufSize, func(sid *relay.StreamID, addr net.Addr) (string, error) {
		return guard.Authorize(sid, addr)
	}, relay.Config{
		Latency:              cfg.SRT.Latency,
		MaxBW:                cfg.SRT.MaxBandwidth,
		MaxConcurrentStreams: cfg.SRT.MaxConcurrentStreams,
	})

	// Auth guard: enforces per-stream encryption, publisher ACLs, subscriber caps.
	guard = auth.NewGuard(database.Streams, r, encKey, cfg.SRT.AllowUnregistered)

	// Outbound webhook for publisher_disconnect events. Empty URL = disabled
	// (Send/Start are no-ops, no goroutine started).
	wb := notify.NewWebhook(cfg.Notify.PublisherDisconnectWebhook)
	if cfg.Notify.PublisherDisconnectWebhook != "" {
		go wb.Start(ctx)
		r.OnPublisherDisconnect = func(stream, remoteAddr string, dur time.Duration) {
			wb.Send(notify.Event{
				Type:       "publisher_disconnect",
				Stream:     stream,
				Remote:     remoteAddr,
				Duration:   dur.String(),
				OccurredAt: time.Now().UTC(),
			})
		}
	}

	// Metrics collector: polls relay stats and pushes to WS clients + Prometheus.
	collector := metrics.NewCollector(r, hub, prom, time.Second)
	go collector.Start(ctx)

	// HTTP API
	apiSrv, err := api.NewServer(database, r, prom, hub, frontendFS(), api.Options{
		EncKeyHex:  cfg.API.EncryptionKey,
		SRTAddr:    cfg.SRT.ListenAddr,
		PublicHost: cfg.API.PublicHost,

		SessionTTL:      cfg.API.SessionTTL,
		LoginRateLimit:  cfg.API.LoginRateLimit,
		LoginRateWindow: cfg.API.LoginRateWindow,
		ForceHTTPS:      cfg.API.ForceHTTPS,

		ThumbnailEnabled:     cfg.Thumbnail.Enabled,
		ThumbnailCacheTTL:    cfg.Thumbnail.CacheTTL,
		ThumbnailWidth:       cfg.Thumbnail.Width,
		ThumbnailJPEGQuality: cfg.Thumbnail.JPEGQuality,
		ThumbnailFormat:      cfg.Thumbnail.Format,
		ThumbnailWebPQuality: cfg.Thumbnail.WebPQuality,
		ThumbnailTimeout:     cfg.Thumbnail.Timeout,

		MediaInfoEnabled: cfg.MediaInfo.Enabled,

		BrandName:             cfg.Dashboard.BrandName,
		ThumbnailRefreshRate:  cfg.Dashboard.ThumbnailRefreshRate,
		ExternalPort:          cfg.SRT.ExternalPort,
		DefaultMaxSubscribers: cfg.SRT.DefaultMaxSubscribers,
	})
	if err != nil {
		return fmt.Errorf("new api server: %w", err)
	}

	// Start relay and API concurrently.
	errCh := make(chan error, 2)
	go func() { errCh <- r.Start(ctx) }()
	go func() { errCh <- apiSrv.Start(ctx, cfg.API.ListenAddr, cfg.API.CORSOrigin) }()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		return nil
	}
}

// runLogPurge deletes event_logs older than ttl every 5 minutes, and also
// trims oldest rows when the database size exceeds maxBytes (soft cap).
// Stops when ctx is cancelled. ttl<=0 disables the time-based purge;
// maxBytes<=0 disables the size cap. Both checks are independent.
func runLogPurge(ctx context.Context, database *db.DB, ttl time.Duration, maxBytes int64) {
	if ttl <= 0 && maxBytes <= 0 {
		slog.Info("event_logs: retention disabled (logging.event_retention=0, logging.max_db_size=0)")
		return
	}
	if ttl <= 0 {
		slog.Info("event_logs: time-based retention disabled (logging.event_retention=0)")
	}
	const interval = 5 * time.Minute
	t := time.NewTicker(interval)
	defer t.Stop()
	// Run once at startup so a fresh container immediately trims old data
	// (e.g. after a long crash-restart cycle).
	purgeOnce(database, ttl, maxBytes)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			purgeOnce(database, ttl, maxBytes)
		}
	}
}

// purgeOnce runs both the time-based purge and the size-based trim. Each is
// independent; a failure in one does not block the other.
func purgeOnce(database *db.DB, ttl time.Duration, maxBytes int64) {
	if ttl > 0 {
		if n, err := database.EventLogs.PurgeOlderThan(ttl); err != nil {
			slog.Warn("event_logs: purge failed", "err", err)
		} else if n > 0 {
			slog.Debug("event_logs: purged stale rows", "deleted", n)
		}
	}
	if maxBytes > 0 {
		if n, err := database.EventLogs.TrimToBytes(maxBytes); err != nil {
			slog.Warn("event_logs: size trim failed", "err", err)
		} else if n > 0 {
			slog.Info("event_logs: trimmed oldest rows for size cap", "deleted", n, "max_bytes", maxBytes)
		}
	}
}

// dbLogLevel resolves logging.event_db_level into a slog.Level. Empty falls
// back to the main logging.level.
func dbLogLevel(cfg config.LoggingConfig) slog.Level {
	name := cfg.EventDBLevel
	if name == "" {
		name = cfg.Level
	}
	switch name {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func newLogger(cfg config.LoggingConfig) *slog.Logger {
	level := slog.LevelInfo
	switch cfg.Level {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}

	opts := &slog.HandlerOptions{Level: level}
	if cfg.Format == "json" {
		return slog.New(slog.NewJSONHandler(os.Stdout, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stdout, opts))
}
