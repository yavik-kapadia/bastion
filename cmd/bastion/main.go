package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/yavik14/bastion/internal/api"
	"github.com/yavik14/bastion/internal/config"
	"github.com/yavik14/bastion/internal/db"
	"github.com/yavik14/bastion/internal/metrics"
	"github.com/yavik14/bastion/internal/relay"
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

	// SRT relay
	r := relay.New(cfg.SRT.ListenAddr, cfg.SRT.SubscriberBufSize, nil)

	// HTTP API
	apiSrv, err := api.NewServer(database, r, prom, cfg.API.EncryptionKey)
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
