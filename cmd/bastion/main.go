package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"encoding/hex"

	"github.com/yavik14/bastion/internal/api"
	"github.com/yavik14/bastion/internal/auth"
	"github.com/yavik14/bastion/internal/config"
	"github.com/yavik14/bastion/internal/db"
	"github.com/yavik14/bastion/internal/metrics"
	"github.com/yavik14/bastion/internal/relay"
	"github.com/yavik14/bastion/internal/ws"
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

	// Decode optional at-rest encryption key.
	var encKey []byte
	if cfg.API.EncryptionKey != "" {
		var err error
		encKey, err = hex.DecodeString(cfg.API.EncryptionKey)
		if err != nil {
			return fmt.Errorf("invalid encryption_key: %w", err)
		}
	}

	// Auth guard: enforces per-stream encryption + publisher ACLs.
	guard := auth.NewGuard(database.Streams, encKey, cfg.SRT.AllowUnregistered)

	// SRT relay
	r := relay.New(cfg.SRT.ListenAddr, cfg.SRT.SubscriberBufSize, guard.Authorize, relay.Config{
		Latency: cfg.SRT.Latency,
		MaxBW:   cfg.SRT.MaxBandwidth,
	})

	// Metrics collector: polls relay stats and pushes to WS clients + Prometheus.
	collector := metrics.NewCollector(r, hub, prom, time.Second)
	go collector.Start(ctx)

	// HTTP API
	apiSrv, err := api.NewServer(database, r, prom, hub, frontendFS(), cfg.API.EncryptionKey)
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
