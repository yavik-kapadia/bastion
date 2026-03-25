package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/yavik14/bastion/internal/config"
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

	// Phase 2+: relay, db, api, ws components will be wired here.
	if err := run(ctx, cfg); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
	slog.Info("Bastion stopped")
}

func run(ctx context.Context, _ *config.Config) error {
	// Placeholder: block until signal.
	<-ctx.Done()
	return nil
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
