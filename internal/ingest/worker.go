// Package ingest refreshes stored data from providers so user requests never
// depend on provider polling.
package ingest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/samwilson0745/live-score/internal/domain"
	"github.com/samwilson0745/live-score/internal/metrics"
	"github.com/samwilson0745/live-score/internal/ports"
)

// ErrAlreadyRunning is returned when a sync for the same scope is in flight.
var ErrAlreadyRunning = errors.New("sync already running")

const maxRetryAfter = time.Minute

type Config struct {
	Sports      []domain.Sport
	Limit       int
	Interval    time.Duration
	Timeout     time.Duration // per provider attempt
	MaxAttempts int           // total attempts, including the first
	BaseBackoff time.Duration
	MaxBackoff  time.Duration
}

func (c *Config) defaults() {
	if c.Limit <= 0 {
		c.Limit = 50
	}
	if c.Interval <= 0 {
		c.Interval = 30 * time.Second
	}
	if c.Timeout <= 0 {
		c.Timeout = 10 * time.Second
	}
	if c.MaxAttempts <= 0 {
		c.MaxAttempts = 3
	}
	if c.BaseBackoff <= 0 {
		c.BaseBackoff = time.Second
	}
	if c.MaxBackoff <= 0 {
		c.MaxBackoff = 30 * time.Second
	}
}

type Deps struct {
	Provider    ports.SportsProvider
	Repo        ports.MatchRepository
	Syncs       ports.SyncStateStore   // optional
	Invalidator ports.CacheInvalidator // optional
	Publisher   ports.UpdatePublisher  // optional
	Logger      *slog.Logger
	Metrics     *metrics.Metrics
}

type Worker struct {
	cfg Config
	Deps

	now   func() time.Time
	sleep func(ctx context.Context, d time.Duration) error
	rand  func() float64

	mu      sync.Mutex
	running map[string]bool
}

func NewWorker(cfg Config, d Deps) *Worker {
	cfg.defaults()
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	return &Worker{
		cfg: cfg, Deps: d,
		now:     time.Now,
		sleep:   sleepCtx,
		rand:    rand.Float64,
		running: map[string]bool{},
	}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Run syncs every configured sport immediately and then on each tick, until
// ctx is cancelled.
func (w *Worker) Run(ctx context.Context) {
	w.runAll(ctx)
	t := time.NewTicker(w.cfg.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.runAll(ctx)
		}
	}
}

func (w *Worker) runAll(ctx context.Context) {
	for _, sport := range w.cfg.Sports {
		if ctx.Err() != nil {
			return
		}
		if err := w.SyncSport(ctx, sport); err != nil && !errors.Is(err, ErrAlreadyRunning) {
			w.Logger.Error("sync failed", "provider", w.Provider.Name(), "sport", sport, "error", err)
		}
	}
}

// SyncSport fetches, stores and announces one sport. It is idempotent: running
// it twice on unchanged data changes nothing.
func (w *Worker) SyncSport(ctx context.Context, sport domain.Sport) error {
	scope := string(sport)
	if !w.acquire(scope) {
		return ErrAlreadyRunning
	}
	defer w.release(scope)

	start := w.now()
	changed, err := w.sync(ctx, sport)
	dur := w.now().Sub(start)

	outcome := "success"
	if err != nil {
		outcome = "error"
	}
	w.Metrics.IngestRun(w.Provider.Name(), scope, outcome, dur, w.now())

	if w.Syncs != nil {
		rec := ports.SyncRecord{Provider: w.Provider.Name(), Scope: scope, Attempted: start, Success: err == nil, Duration: dur}
		if err != nil {
			rec.Err = err.Error()
		}
		// Record with a fresh context so shutdown still saves the outcome.
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		if rerr := w.Syncs.RecordSync(rctx, rec); rerr != nil {
			w.Logger.Warn("record sync state failed", "error", rerr)
		}
	}
	if err == nil {
		w.Logger.Info("sync complete", "provider", w.Provider.Name(), "sport", sport, "changed", changed, "duration", dur)
	}
	return err
}

func (w *Worker) sync(ctx context.Context, sport domain.Sport) (int, error) {
	res, err := w.fetchWithRetry(ctx, sport)
	if err != nil {
		return 0, err
	}

	now := w.now().UTC()
	for i := range res.Matches {
		res.Matches[i].IngestedAt = now
		if res.Matches[i].ProviderUpdatedAt.IsZero() {
			res.Matches[i].ProviderUpdatedAt = now
		}
	}

	changed, err := w.Repo.UpsertMatches(ctx, res.Matches)
	if err != nil {
		return 0, fmt.Errorf("store matches: %w", err)
	}
	if len(changed) == 0 {
		return 0, nil
	}

	if w.Invalidator != nil {
		if err := w.Invalidator.DeletePrefix(ctx, cachePrefix(sport)); err != nil {
			w.Logger.Warn("cache invalidation failed", "sport", sport, "error", err)
		}
	}
	if w.Publisher != nil {
		w.Publisher.Publish(changed)
	}
	return len(changed), nil
}

func (w *Worker) fetchWithRetry(ctx context.Context, sport domain.Sport) (*domain.MatchesResult, error) {
	var lastErr error
	for attempt := 1; attempt <= w.cfg.MaxAttempts; attempt++ {
		actx, cancel := context.WithTimeout(ctx, w.cfg.Timeout)
		callStart := time.Now()
		res, err := w.Provider.GetMatches(actx, string(sport), w.cfg.Limit)
		cancel()

		outcome := "success"
		if err != nil {
			outcome = "error"
		}
		w.Metrics.ProviderCall(w.Provider.Name(), outcome, time.Since(callStart))

		if err == nil {
			return res, nil
		}
		lastErr = err
		if ctx.Err() != nil || !ports.IsRetryable(err) || attempt == w.cfg.MaxAttempts {
			break
		}
		if serr := w.sleep(ctx, w.backoff(attempt, err)); serr != nil {
			break
		}
	}
	return nil, lastErr
}

// backoff honors Retry-After when given; otherwise it is exponential with
// jitter (between half and the full delay).
func (w *Worker) backoff(attempt int, err error) time.Duration {
	var pe *ports.ProviderError
	if errors.As(err, &pe) && pe.RetryAfter > 0 {
		return min(pe.RetryAfter, maxRetryAfter)
	}
	d := w.cfg.BaseBackoff << (attempt - 1)
	if d <= 0 || d > w.cfg.MaxBackoff {
		d = w.cfg.MaxBackoff
	}
	return d/2 + time.Duration(w.rand()*float64(d/2))
}

func (w *Worker) acquire(scope string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.running[scope] {
		return false
	}
	w.running[scope] = true
	return true
}

func (w *Worker) release(scope string) {
	w.mu.Lock()
	delete(w.running, scope)
	w.mu.Unlock()
}
