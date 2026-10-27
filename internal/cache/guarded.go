package cache

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/samwilson0745/live-score/internal/ports"
)

// ErrUnavailable is returned while the circuit is open.
var ErrUnavailable = errors.New("cache temporarily disabled")

// Guarded wraps a cache with a simple circuit breaker: after maxFailures
// consecutive errors the cache is skipped for cooldown, so a dead Redis does
// not add its timeout to every request. Misses are not failures.
type Guarded struct {
	inner       ports.MatchCache
	maxFailures int
	cooldown    time.Duration
	now         func() time.Time

	mu        sync.Mutex
	failures  int
	openUntil time.Time
}

func NewGuarded(inner ports.MatchCache, maxFailures int, cooldown time.Duration) *Guarded {
	return &Guarded{inner: inner, maxFailures: maxFailures, cooldown: cooldown, now: time.Now}
}

func (g *Guarded) allow() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return !g.now().Before(g.openUntil)
}

func (g *Guarded) record(err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err == nil {
		g.failures = 0
		return
	}
	g.failures++
	if g.failures >= g.maxFailures {
		g.openUntil = g.now().Add(g.cooldown)
		g.failures = g.maxFailures - 1 // half-open: a single failed trial reopens it
	}
}

func (g *Guarded) Get(ctx context.Context, key string, dest any) (bool, error) {
	if !g.allow() {
		return false, ErrUnavailable
	}
	ok, err := g.inner.Get(ctx, key, dest)
	g.record(err)
	return ok, err
}

func (g *Guarded) Set(ctx context.Context, key string, value any) error {
	if !g.allow() {
		return ErrUnavailable
	}
	err := g.inner.Set(ctx, key, value)
	g.record(err)
	return err
}

// Open reports whether the circuit is currently open.
func (g *Guarded) Open() bool { return !g.allow() }
