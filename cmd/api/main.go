package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/samwilson0745/live-score/internal/application"
	"github.com/samwilson0745/live-score/internal/cache"
	"github.com/samwilson0745/live-score/internal/config"
	"github.com/samwilson0745/live-score/internal/ingest"
	"github.com/samwilson0745/live-score/internal/metrics"
	"github.com/samwilson0745/live-score/internal/ports"
	"github.com/samwilson0745/live-score/internal/provider/sportscore"
	"github.com/samwilson0745/live-score/internal/realtime"
	"github.com/samwilson0745/live-score/internal/storage/postgres"
	transport "github.com/samwilson0745/live-score/internal/transport/http"
	"github.com/samwilson0745/live-score/internal/transport/http/handler"
	"github.com/samwilson0745/live-score/migrations"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("fatal", "error", err)
		os.Exit(1)
	}
}

// run holds all setup so deferred cleanup (Redis, database) always executes.
func run(log *slog.Logger) error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	m := metrics.New()

	var opts []sportscore.Option
	if cfg.ProviderBaseURL != "" {
		opts = append(opts, sportscore.WithBaseURL(cfg.ProviderBaseURL))
	}
	provider := sportscore.NewClient(opts...)

	// Redis is optional; the guarded wrapper degrades dynamically if it
	// goes away later, not just at startup.
	var matchCache ports.MatchCache
	var invalidator ports.CacheInvalidator
	if cfg.RedisAddr != "" {
		rc := cache.NewRedisCache(cfg.RedisAddr, cfg.CacheTTL)
		defer rc.Close()
		if err := rc.Ping(ctx); err != nil {
			log.Warn("redis unreachable at startup; will keep retrying", "error", err)
		} else {
			log.Info("redis connected", "addr", cfg.RedisAddr)
		}
		matchCache = cache.NewGuarded(rc, 3, 10*time.Second)
		invalidator = rc
	}

	// Data source: the stored copy when a database is configured, otherwise
	// live provider calls behind the cache.
	var loader application.MatchLoader = application.ProviderLoader{Provider: provider, Metrics: m}
	var repo ports.MatchRepository
	var syncs ports.SyncStateStore
	var ready func(context.Context) error
	if cfg.DatabaseURL != "" {
		store, err := postgres.Open(ctx, cfg.DatabaseURL)
		if err != nil {
			return err
		}
		defer store.Close()
		if err := store.Migrate(ctx, migrations.FS); err != nil {
			return err
		}
		repo, syncs, ready = store, store, store.Ping
		loader = application.RepositoryLoader{Repo: store}
		log.Info("postgres connected; serving stored matches")
	}

	svc := application.NewMatchesService(loader, application.ServiceOptions{
		Cache:         matchCache,
		MaxConcurrent: cfg.MaxProviderConc,
		LoadTimeout:   cfg.ProviderTimeout,
		Logger:        log,
		Metrics:       m,
	})

	deps := transport.Deps{
		Matches: handler.NewMatchesHandler(svc, cfg.SupportedSports, log),
		Metrics: m,
		Logger:  log,
		Ready:   ready,
	}

	if repo != nil {
		deps.Stored = handler.NewStoredHandler(repo, log)
		hub := realtime.NewHub()
		deps.WS = &handler.WSHandler{Hub: hub, Repo: repo, OriginPatterns: cfg.WSOrigins, Metrics: m, Log: log}

		if cfg.IngestEnabled {
			worker := ingest.NewWorker(ingest.Config{
				Sports:   cfg.IngestSports,
				Limit:    cfg.IngestLimit,
				Interval: cfg.IngestInterval,
				Timeout:  cfg.ProviderTimeout,
			}, ingest.Deps{
				Provider: provider, Repo: repo, Syncs: syncs,
				Invalidator: invalidator, Publisher: hub, Logger: log, Metrics: m,
			})
			go worker.Run(ctx)
		}
	}

	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           transport.NewRouter(deps),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errc := make(chan error, 1)
	go func() {
		log.Info("live-score server starting", "addr", cfg.HTTPAddr)
		errc <- server.ListenAndServe()
	}()

	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		log.Info("shutting down")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return server.Shutdown(shutdownCtx)
}
