package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/samwilson0745/live-score/internal/domain"
	"github.com/samwilson0745/live-score/internal/ports"
)

const upsertMatchSQL = `
INSERT INTO matches (id, sport_id, competition_id, home_team_id, away_team_id, status,
    home_score, away_score, score_details, start_time, provider_updated_at, ingested_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
ON CONFLICT (id) DO UPDATE SET
    competition_id = EXCLUDED.competition_id,
    status = EXCLUDED.status,
    home_score = EXCLUDED.home_score,
    away_score = EXCLUDED.away_score,
    score_details = EXCLUDED.score_details,
    start_time = EXCLUDED.start_time,
    provider_updated_at = EXCLUDED.provider_updated_at,
    ingested_at = EXCLUDED.ingested_at,
    version = matches.version + 1,
    updated_at = now()
WHERE matches.provider_updated_at <= EXCLUDED.provider_updated_at
  AND (matches.status, matches.home_score, matches.away_score, matches.start_time, matches.score_details)
      IS DISTINCT FROM
      (EXCLUDED.status, EXCLUDED.home_score, EXCLUDED.away_score, EXCLUDED.start_time, EXCLUDED.score_details)
RETURNING version`

const upsertRefSQL = `
INSERT INTO match_provider_refs (provider, provider_match_id, match_id, url, provider_status,
    provider_status_text, provider_updated_at, observed_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
ON CONFLICT (provider, provider_match_id) DO UPDATE SET
    url = EXCLUDED.url,
    provider_status = EXCLUDED.provider_status,
    provider_status_text = EXCLUDED.provider_status_text,
    provider_updated_at = EXCLUDED.provider_updated_at,
    observed_at = EXCLUDED.observed_at
WHERE match_provider_refs.provider_updated_at <= EXCLUDED.provider_updated_at`

// UpsertMatches stores everything in one transaction and returns the matches
// that were inserted or materially changed (with their new version).
func (s *Store) UpsertMatches(ctx context.Context, matches []domain.Match) ([]domain.Match, error) {
	ctx, cancel := context.WithTimeout(ctx, 4*queryTimeout)
	defer cancel()

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var changed []domain.Match
	for _, m := range matches {
		sport := string(m.Sport)
		if _, err := tx.Exec(ctx, `INSERT INTO sports (id) VALUES ($1) ON CONFLICT DO NOTHING`, sport); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO competitions (id, sport_id, name, logo_url) VALUES ($1,$2,$3,$4)
			ON CONFLICT (id) DO UPDATE SET logo_url = EXCLUDED.logo_url`,
			m.Competition.ID, sport, m.Competition.Name, m.Competition.LogoURL); err != nil {
			return nil, err
		}
		for _, t := range []domain.Team{m.Home, m.Away} {
			if _, err := tx.Exec(ctx, `INSERT INTO teams (id, sport_id, name, logo_url) VALUES ($1,$2,$3,$4)
				ON CONFLICT (id) DO UPDATE SET logo_url = EXCLUDED.logo_url`,
				t.ID, sport, t.Name, t.LogoURL); err != nil {
				return nil, err
			}
		}

		var details []byte
		if len(m.ScoreDetails) > 0 {
			details = m.ScoreDetails
		}
		var version int64
		err := tx.QueryRow(ctx, upsertMatchSQL, m.ID, sport, m.Competition.ID, m.Home.ID, m.Away.ID,
			string(m.Status), m.HomeScore, m.AwayScore, details, m.StartTime, m.ProviderUpdatedAt, m.IngestedAt).Scan(&version)
		switch {
		case err == nil:
			m.Version = version
			changed = append(changed, m)
		case errors.Is(err, pgx.ErrNoRows): // unchanged or stale: keep existing row
		default:
			return nil, err
		}

		if _, err := tx.Exec(ctx, upsertRefSQL, m.Source.Provider, m.Source.MatchID, m.ID, m.Source.URL,
			m.Source.Status, m.Source.StatusText, m.ProviderUpdatedAt, m.IngestedAt); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return changed, nil
}

const selectMatchSQL = `
SELECT m.id, m.sport_id, c.id, c.name, c.logo_url, ht.id, ht.name, ht.logo_url, at.id, at.name, at.logo_url,
       m.status, m.home_score, m.away_score, m.score_details, m.start_time, m.provider_updated_at,
       m.ingested_at, m.version,
       COALESCE(r.provider,''), COALESCE(r.provider_match_id,''), COALESCE(r.url,''),
       COALESCE(r.provider_status,''), COALESCE(r.provider_status_text,'')
FROM matches m
JOIN competitions c ON c.id = m.competition_id
JOIN teams ht ON ht.id = m.home_team_id
JOIN teams at ON at.id = m.away_team_id
LEFT JOIN LATERAL (
    SELECT * FROM match_provider_refs r WHERE r.match_id = m.id
    ORDER BY r.provider_updated_at DESC, r.provider LIMIT 1
) r ON true`

func scanMatch(row pgx.Row) (domain.Match, error) {
	var (
		m      domain.Match
		sport  string
		status string
		det    []byte
	)
	err := row.Scan(&m.ID, &sport, &m.Competition.ID, &m.Competition.Name, &m.Competition.LogoURL,
		&m.Home.ID, &m.Home.Name, &m.Home.LogoURL, &m.Away.ID, &m.Away.Name, &m.Away.LogoURL,
		&status, &m.HomeScore, &m.AwayScore, &det, &m.StartTime, &m.ProviderUpdatedAt, &m.IngestedAt, &m.Version,
		&m.Source.Provider, &m.Source.MatchID, &m.Source.URL, &m.Source.Status, &m.Source.StatusText)
	if err != nil {
		return m, err
	}
	m.Sport = domain.Sport(sport)
	m.Status = domain.MatchStatus(status)
	if len(det) > 0 {
		m.ScoreDetails = json.RawMessage(det)
	}
	m.StartTime, m.ProviderUpdatedAt, m.IngestedAt = m.StartTime.UTC(), m.ProviderUpdatedAt.UTC(), m.IngestedAt.UTC()
	return m, nil
}

func (s *Store) ListMatches(ctx context.Context, f ports.MatchFilter) ([]domain.Match, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, selectMatchSQL+`
WHERE ($1 = '' OR m.sport_id = $1) AND ($2 = '' OR m.status = $2) AND ($3 = '' OR m.competition_id = $3)
ORDER BY m.start_time DESC, m.id
LIMIT $4`, string(f.Sport), string(f.Status), f.CompetitionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []domain.Match{}
	for rows.Next() {
		m, err := scanMatch(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) GetMatch(ctx context.Context, id string) (*domain.Match, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	m, err := scanMatch(s.pool.QueryRow(ctx, selectMatchSQL+` WHERE m.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (s *Store) RecordSync(ctx context.Context, r ports.SyncRecord) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	var lastSuccess *time.Time
	failures := 1
	if r.Success {
		t := r.Attempted.Add(r.Duration)
		lastSuccess = &t
		failures = 0
	}
	_, err := s.pool.Exec(ctx, `
INSERT INTO sync_state (provider, scope, last_attempt_at, last_success_at, last_error, consecutive_failures, last_duration_ms)
VALUES ($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT (provider, scope) DO UPDATE SET
    last_attempt_at = EXCLUDED.last_attempt_at,
    last_success_at = COALESCE(EXCLUDED.last_success_at, sync_state.last_success_at),
    last_error = EXCLUDED.last_error,
    consecutive_failures = CASE WHEN $8 THEN 0 ELSE sync_state.consecutive_failures + 1 END,
    last_duration_ms = EXCLUDED.last_duration_ms`,
		r.Provider, r.Scope, r.Attempted, lastSuccess, r.Err, failures, r.Duration.Milliseconds(), r.Success)
	return err
}

func (s *Store) GetSyncState(ctx context.Context, provider, scope string) (*ports.SyncState, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	var st ports.SyncState
	var lastSuccess *time.Time
	err := s.pool.QueryRow(ctx, `SELECT provider, scope, last_attempt_at, last_success_at, last_error, consecutive_failures
		FROM sync_state WHERE provider = $1 AND scope = $2`, provider, scope).
		Scan(&st.Provider, &st.Scope, &st.LastAttempt, &lastSuccess, &st.LastError, &st.ConsecutiveFailures)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if lastSuccess != nil {
		st.LastSuccess = *lastSuccess
	}
	return &st, nil
}
