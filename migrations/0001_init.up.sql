CREATE TABLE sports (
    id TEXT PRIMARY KEY
);

CREATE TABLE competitions (
    id         TEXT PRIMARY KEY,
    sport_id   TEXT NOT NULL REFERENCES sports (id),
    name       TEXT NOT NULL,
    logo_url   TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (sport_id, name)
);

CREATE TABLE teams (
    id         TEXT PRIMARY KEY,
    sport_id   TEXT NOT NULL REFERENCES sports (id),
    name       TEXT NOT NULL,
    logo_url   TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (sport_id, name)
);

CREATE TABLE matches (
    id                  TEXT PRIMARY KEY,
    sport_id            TEXT NOT NULL REFERENCES sports (id),
    competition_id      TEXT NOT NULL REFERENCES competitions (id),
    home_team_id        TEXT NOT NULL REFERENCES teams (id),
    away_team_id        TEXT NOT NULL REFERENCES teams (id),
    status              TEXT NOT NULL,
    home_score          INTEGER,
    away_score          INTEGER,
    score_details       JSONB,
    start_time          TIMESTAMPTZ NOT NULL,
    provider_updated_at TIMESTAMPTZ NOT NULL,
    ingested_at         TIMESTAMPTZ NOT NULL,
    version             BIGINT NOT NULL DEFAULT 1,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- List endpoints filter by sport (+ status) and order by start time.
CREATE INDEX matches_sport_status_start_idx ON matches (sport_id, status, start_time DESC);
CREATE INDEX matches_competition_start_idx ON matches (competition_id, start_time DESC);

-- One row per provider record that maps onto a canonical match.
CREATE TABLE match_provider_refs (
    provider            TEXT NOT NULL,
    provider_match_id   TEXT NOT NULL,
    match_id            TEXT NOT NULL REFERENCES matches (id) ON DELETE CASCADE,
    url                 TEXT NOT NULL DEFAULT '',
    provider_status     TEXT NOT NULL DEFAULT '',
    provider_status_text TEXT NOT NULL DEFAULT '',
    provider_updated_at TIMESTAMPTZ NOT NULL,
    observed_at         TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (provider, provider_match_id)
);

CREATE INDEX match_provider_refs_match_idx ON match_provider_refs (match_id);

-- Last-sync bookkeeping per provider and scope (e.g. a sport).
CREATE TABLE sync_state (
    provider             TEXT NOT NULL,
    scope                TEXT NOT NULL,
    last_attempt_at      TIMESTAMPTZ NOT NULL,
    last_success_at      TIMESTAMPTZ,
    last_error           TEXT NOT NULL DEFAULT '',
    consecutive_failures INTEGER NOT NULL DEFAULT 0,
    last_duration_ms     BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (provider, scope)
);
