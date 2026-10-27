// Package ports declares the interfaces the core depends on. Concrete
// implementations (SportScore, Redis, PostgreSQL, ...) live elsewhere.
package ports

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/samwilson0745/live-score/internal/domain"
)

type SportsProvider interface {
	Name() string
	GetMatches(ctx context.Context, sport string, limit int) (*domain.MatchesResult, error)
}

// MatchCache stores serialized values. A Get miss is (false, nil).
type MatchCache interface {
	Get(ctx context.Context, key string, dest any) (bool, error)
	Set(ctx context.Context, key string, value any) error
}

// CacheInvalidator removes every key that starts with prefix.
type CacheInvalidator interface {
	DeletePrefix(ctx context.Context, prefix string) error
}

type MatchFilter struct {
	Sport         domain.Sport
	Status        domain.MatchStatus
	CompetitionID string
	Limit         int
}

type MatchRepository interface {
	// UpsertMatches stores matches and returns only those that were inserted
	// or materially changed. A match older than the stored one (by
	// ProviderUpdatedAt) never overwrites it.
	UpsertMatches(ctx context.Context, matches []domain.Match) ([]domain.Match, error)
	ListMatches(ctx context.Context, f MatchFilter) ([]domain.Match, error)
	// GetMatch returns domain.ErrNotFound if id is unknown.
	GetMatch(ctx context.Context, id string) (*domain.Match, error)
}

type SyncRecord struct {
	Provider  string
	Scope     string
	Attempted time.Time
	Success   bool
	Duration  time.Duration
	Err       string
}

type SyncState struct {
	Provider            string
	Scope               string
	LastAttempt         time.Time
	LastSuccess         time.Time
	LastError           string
	ConsecutiveFailures int
}

type SyncStateStore interface {
	RecordSync(ctx context.Context, r SyncRecord) error
	GetSyncState(ctx context.Context, provider, scope string) (*SyncState, error)
}

// UpdatePublisher receives matches whose stored state changed.
type UpdatePublisher interface {
	Publish(matches []domain.Match)
}

// ProviderError describes a failed upstream call so callers can decide
// whether to retry.
type ProviderError struct {
	Provider   string
	StatusCode int
	Retryable  bool
	RetryAfter time.Duration
	Err        error
}

func (e *ProviderError) Error() string {
	msg := e.Provider + " request failed"
	if e.StatusCode != 0 {
		msg += ": HTTP " + strconv.Itoa(e.StatusCode)
	}
	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}
	return msg
}

func (e *ProviderError) Unwrap() error { return e.Err }

func IsRetryable(err error) bool {
	var pe *ProviderError
	if errors.As(err, &pe) {
		return pe.Retryable
	}
	return false
}
