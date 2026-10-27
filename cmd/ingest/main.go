// Command ingest runs the background ingestion worker as its own process.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/samwilson0745/live-score/internal/cache"
	"github.com/samwilson0745/live-score/internal/config"
	"github.com/samwilson0745/live-score/internal/ingest"
	"github.com/samwilson0745/live-score/internal/metrics"
	"github.com/samwilson0745/live-score/internal/ports"
	"github.com/samwilson0745/live-score/internal/provider/sportscore"
	"github.com/samwilson0745/live-score/internal/storage/postgres"
	"github.com/samwilson0745/live-score/migrations"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	if cfg.DatabaseURL == "" {
		return errMissingDB
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	store, err := postgres.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer store.Close()
	if err := store.Migrate(ctx, migrations.FS); err != nil {
		return err
	}

	var opts []sportscore.Option
	if cfg.ProviderBaseURL != "" {
		opts = append(opts, sportscore.WithBaseURL(cfg.ProviderBaseURL))
	}

	var invalidator ports.CacheInvalidator
	if cfg.RedisAddr != "" {
		rc := cache.NewRedisCache(cfg.RedisAddr, cfg.CacheTTL)
		defer rc.Close()
		invalidator = rc
	}

	worker := ingest.NewWorker(ingest.Config{
		Sports:   cfg.IngestSports,
		Limit:    cfg.IngestLimit,
		Interval: cfg.IngestInterval,
		Timeout:  cfg.ProviderTimeout,
	}, ingest.Deps{
		Provider: sportscore.NewClient(opts...), Repo: store, Syncs: store,
		Invalidator: invalidator, Logger: log, Metrics: metrics.New(),
	})

	log.Info("ingest worker starting", "sports", cfg.IngestSports, "interval", cfg.IngestInterval)
	worker.Run(ctx)
	log.Info("ingest worker stopped")
	return nil
}

var errMissingDB = errString("DATABASE_URL is required for the ingest worker")

type errString string

func (e errString) Error() string { return string(e) }
