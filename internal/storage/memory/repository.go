// Package memory is an in-process MatchRepository with the same upsert rules
// as the PostgreSQL one. It backs tests and database-less development.
package memory

import (
	"context"
	"sort"
	"sync"

	"github.com/samwilson0745/live-score/internal/domain"
	"github.com/samwilson0745/live-score/internal/ports"
)

type Repository struct {
	mu      sync.RWMutex
	matches map[string]domain.Match
	syncs   map[string]ports.SyncState
}

func New() *Repository {
	return &Repository{matches: map[string]domain.Match{}, syncs: map[string]ports.SyncState{}}
}

func (r *Repository) UpsertMatches(_ context.Context, in []domain.Match) ([]domain.Match, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var changed []domain.Match
	for _, m := range in {
		cur, exists := r.matches[m.ID]
		if exists {
			if cur.ProviderUpdatedAt.After(m.ProviderUpdatedAt) || cur.SameState(m) {
				continue
			}
			m.Version = cur.Version + 1
		} else {
			m.Version = 1
		}
		r.matches[m.ID] = m
		changed = append(changed, m)
	}
	return changed, nil
}

func (r *Repository) ListMatches(_ context.Context, f ports.MatchFilter) ([]domain.Match, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var out []domain.Match
	for _, m := range r.matches {
		if f.Sport != "" && m.Sport != f.Sport {
			continue
		}
		if f.Status != "" && m.Status != f.Status {
			continue
		}
		if f.CompetitionID != "" && m.Competition.ID != f.CompetitionID {
			continue
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].StartTime.Equal(out[j].StartTime) {
			return out[i].StartTime.After(out[j].StartTime)
		}
		return out[i].ID < out[j].ID
	})
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out, nil
}

func (r *Repository) GetMatch(_ context.Context, id string) (*domain.Match, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.matches[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return &m, nil
}

func (r *Repository) RecordSync(_ context.Context, rec ports.SyncRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := rec.Provider + "/" + rec.Scope
	st := r.syncs[key]
	st.Provider, st.Scope, st.LastAttempt = rec.Provider, rec.Scope, rec.Attempted
	if rec.Success {
		st.LastSuccess = rec.Attempted.Add(rec.Duration)
		st.LastError = ""
		st.ConsecutiveFailures = 0
	} else {
		st.LastError = rec.Err
		st.ConsecutiveFailures++
	}
	r.syncs[key] = st
	return nil
}

func (r *Repository) GetSyncState(_ context.Context, provider, scope string) (*ports.SyncState, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	st, ok := r.syncs[provider+"/"+scope]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return &st, nil
}
