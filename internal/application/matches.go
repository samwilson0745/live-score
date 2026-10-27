// Package application contains the use cases that sit between transport and
// the ports.
package application

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/samwilson0745/live-score/internal/domain"
	"github.com/samwilson0745/live-score/internal/metrics"
	"github.com/samwilson0745/live-score/internal/ports"
)

// ErrOverloaded is returned when too many provider calls are already in
// flight and the request is shed instead of queued.
var ErrOverloaded = errors.New("provider concurrency limit reached")

type CacheStatus string

const (
	CacheHit    CacheStatus = "HIT"
	CacheMiss   CacheStatus = "MISS"
	CacheBypass CacheStatus = "BYPASS"
)

type Query struct {
	Sport  domain.Sport
	Limit  int
	Status domain.MatchStatus // optional
}

func (q Query) CacheKey() string {
	return fmt.Sprintf("%s%s:%s:%d", CacheKeyPrefix, q.Sport, q.Status, q.Limit)
}

// CacheKeyPrefix namespaces list responses; the sport follows it so ingestion
// can invalidate one sport at a time.
const CacheKeyPrefix = "matches:v2:"

// SportCachePrefix is the prefix shared by every cached list for a sport.
func SportCachePrefix(s domain.Sport) string { return CacheKeyPrefix + string(s) + ":" }

// MatchLoader produces a result for a query (from a provider or storage).
type MatchLoader interface {
	Load(ctx context.Context, q Query) (*domain.MatchesResult, error)
}

// ProviderLoader fetches live from an upstream provider. The status filter is
// applied after fetching, so Limit bounds the upstream page, not the output.
type ProviderLoader struct {
	Provider ports.SportsProvider
	Metrics  *metrics.Metrics
}

func (p ProviderLoader) Load(ctx context.Context, q Query) (*domain.MatchesResult, error) {
	start := time.Now()
	res, err := p.Provider.GetMatches(ctx, string(q.Sport), q.Limit)
	outcome := "success"
	if err != nil {
		outcome = "error"
	}
	p.Metrics.ProviderCall(p.Provider.Name(), outcome, time.Since(start))
	if err != nil {
		return nil, err
	}
	if q.Status != "" {
		filtered := make([]domain.Match, 0, len(res.Matches))
		for _, m := range res.Matches {
			if m.Status == q.Status {
				filtered = append(filtered, m)
			}
		}
		res = domain.NewMatchesResult(res.Sport, filtered, res.UpdatedAt)
	}
	return res, nil
}

// RepositoryLoader serves the stored copy; it never calls a provider.
type RepositoryLoader struct {
	Repo ports.MatchRepository
}

func (r RepositoryLoader) Load(ctx context.Context, q Query) (*domain.MatchesResult, error) {
	ms, err := r.Repo.ListMatches(ctx, ports.MatchFilter{Sport: q.Sport, Status: q.Status, Limit: q.Limit})
	if err != nil {
		return nil, err
	}
	var updated time.Time
	for _, m := range ms {
		if m.ProviderUpdatedAt.After(updated) {
			updated = m.ProviderUpdatedAt
		}
	}
	return domain.NewMatchesResult(q.Sport, ms, updated), nil
}

type MatchesService struct {
	loader      MatchLoader
	cache       ports.MatchCache // optional
	group       singleflight.Group
	sem         chan struct{}
	loadTimeout time.Duration
	log         *slog.Logger
	metrics     *metrics.Metrics
}

type ServiceOptions struct {
	Cache         ports.MatchCache
	MaxConcurrent int           // concurrent loads before shedding; default 16
	LoadTimeout   time.Duration // default 8s
	Logger        *slog.Logger
	Metrics       *metrics.Metrics
}

func NewMatchesService(loader MatchLoader, o ServiceOptions) *MatchesService {
	if o.MaxConcurrent <= 0 {
		o.MaxConcurrent = 16
	}
	if o.LoadTimeout <= 0 {
		o.LoadTimeout = 8 * time.Second
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	return &MatchesService{
		loader:      loader,
		cache:       o.Cache,
		sem:         make(chan struct{}, o.MaxConcurrent),
		loadTimeout: o.LoadTimeout,
		log:         o.Logger,
		metrics:     o.Metrics,
	}
}

// List serves a query cache-aside. Concurrent misses for the same key share a
// single load, and loads beyond the concurrency limit are shed.
func (s *MatchesService) List(ctx context.Context, q Query) (*domain.MatchesResult, CacheStatus, error) {
	key := q.CacheKey()

	status := CacheBypass
	if s.cache != nil {
		var cached domain.MatchesResult
		hit, err := s.cache.Get(ctx, key, &cached)
		switch {
		case err != nil:
			s.metrics.CacheEvent("get_error")
			s.log.Warn("cache get failed", "key", key, "error", err)
		case hit:
			s.metrics.CacheEvent("hit")
			return &cached, CacheHit, nil
		default:
			status = CacheMiss
		}
	}
	if status == CacheMiss {
		s.metrics.CacheEvent("miss")
	} else {
		s.metrics.CacheEvent("bypass")
	}

	v, err, _ := s.group.Do(key, func() (any, error) {
		select {
		case s.sem <- struct{}{}:
			defer func() { <-s.sem }()
		default:
			s.metrics.LoadShed()
			return nil, ErrOverloaded
		}

		// Detach from any single caller so one cancelled request does not
		// fail everyone waiting on the shared load.
		lctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.loadTimeout)
		defer cancel()

		res, err := s.loader.Load(lctx, q)
		if err != nil {
			return nil, err
		}
		if s.cache != nil && status != CacheBypass {
			if err := s.cache.Set(lctx, key, res); err != nil {
				s.metrics.CacheEvent("set_error")
				s.log.Warn("cache set failed", "key", key, "error", err)
			}
		}
		return res, nil
	})
	if err != nil {
		return nil, status, err
	}
	return v.(*domain.MatchesResult), status, nil
}
