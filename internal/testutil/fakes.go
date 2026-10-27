// Package testutil provides fakes shared by tests.
package testutil

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"

	"github.com/samwilson0745/live-score/internal/domain"
)

// Provider is a controllable ports.SportsProvider.
type Provider struct {
	Result *domain.MatchesResult
	Err    error
	Block  chan struct{} // if set, calls wait until closed or ctx is done

	Calls atomic.Int32
	mu    sync.Mutex
	Last  struct {
		Sport string
		Limit int
	}
}

func (p *Provider) Name() string { return "fake" }

func (p *Provider) GetMatches(ctx context.Context, sport string, limit int) (*domain.MatchesResult, error) {
	p.Calls.Add(1)
	p.mu.Lock()
	p.Last.Sport, p.Last.Limit = sport, limit
	p.mu.Unlock()
	if p.Block != nil {
		select {
		case <-p.Block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if p.Err != nil {
		return nil, p.Err
	}
	return p.Result, nil
}

// Cache is an in-memory ports.MatchCache that round-trips through JSON like
// the real one.
type Cache struct {
	mu     sync.Mutex
	Data   map[string][]byte
	GetErr error
	SetErr error
	Gets   atomic.Int32
	Sets   atomic.Int32
}

func NewCache() *Cache { return &Cache{Data: map[string][]byte{}} }

func (c *Cache) Get(_ context.Context, key string, dest any) (bool, error) {
	c.Gets.Add(1)
	if c.GetErr != nil {
		return false, c.GetErr
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	b, ok := c.Data[key]
	if !ok {
		return false, nil
	}
	return true, json.Unmarshal(b, dest)
}

func (c *Cache) Set(_ context.Context, key string, value any) error {
	c.Sets.Add(1)
	if c.SetErr != nil {
		return c.SetErr
	}
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.Data[key] = b
	c.mu.Unlock()
	return nil
}

// Match builds a match with sensible defaults for tests.
func Match(id string, home, away *int, status domain.MatchStatus, updated time.Time) domain.Match {
	return domain.Match{
		ID: id, Sport: "football",
		Competition: domain.Competition{ID: "c_1", Name: "League"},
		Home:        domain.Team{ID: "t_h", Name: "Home"},
		Away:        domain.Team{ID: "t_a", Name: "Away"},
		HomeScore:   home, AwayScore: away, Status: status,
		StartTime: updated.Add(-time.Hour).UTC(),
		Source:    domain.ProviderRef{Provider: "fake", MatchID: id, Status: string(status)},

		ProviderUpdatedAt: updated.UTC(),
		IngestedAt:        updated.UTC(),
	}
}

func Int(n int) *int { return &n }
