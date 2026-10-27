// Package domain holds the canonical, provider-independent sports models.
package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// ErrNotFound is returned by repositories when an entity does not exist.
var ErrNotFound = errors.New("not found")

type Sport string

// MatchStatus is the normalized lifecycle state of a match. The provider's own
// wording is kept separately in ProviderRef.
type MatchStatus string

const (
	StatusScheduled MatchStatus = "scheduled"
	StatusLive      MatchStatus = "live"
	StatusFinished  MatchStatus = "finished"
	StatusPostponed MatchStatus = "postponed"
	StatusCancelled MatchStatus = "cancelled"
	StatusUnknown   MatchStatus = "unknown"
)

// ParseMatchStatus reports whether s is a known, queryable status.
func ParseMatchStatus(s string) (MatchStatus, bool) {
	switch st := MatchStatus(strings.ToLower(s)); st {
	case StatusScheduled, StatusLive, StatusFinished, StatusPostponed, StatusCancelled, StatusUnknown:
		return st, true
	}
	return "", false
}

type Team struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	LogoURL string `json:"logo_url,omitempty"`
}

type Competition struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	LogoURL string `json:"logo_url,omitempty"`
}

// ProviderRef points back at the upstream record a match was built from. It is
// a source reference, not a universal match ID.
type ProviderRef struct {
	Provider   string `json:"provider"`
	MatchID    string `json:"match_id"`
	URL        string `json:"url,omitempty"`
	Status     string `json:"status"`
	StatusText string `json:"status_text,omitempty"`
}

type Match struct {
	ID          string      `json:"id"`
	Sport       Sport       `json:"sport"`
	Competition Competition `json:"competition"`
	Home        Team        `json:"home"`
	Away        Team        `json:"away"`
	// Scores are nil when unknown; nil is never the same as 0.
	HomeScore *int        `json:"home_score"`
	AwayScore *int        `json:"away_score"`
	Status    MatchStatus `json:"status"`
	// ScoreDetails carries sport-specific structure (sets, innings, ...).
	ScoreDetails json.RawMessage `json:"score_details,omitempty"`
	StartTime    time.Time       `json:"start_time"`
	Source       ProviderRef     `json:"source"`
	// ProviderUpdatedAt is when the provider says its data was updated;
	// IngestedAt is when this system stored it.
	ProviderUpdatedAt time.Time `json:"provider_updated_at"`
	IngestedAt        time.Time `json:"ingested_at,omitzero"`
	// Version increases every time stored data for the match changes.
	Version int64 `json:"version,omitempty"`
}

// SameState reports whether two matches carry the same user-visible state.
func (m Match) SameState(o Match) bool {
	return m.Status == o.Status &&
		intPtrEqual(m.HomeScore, o.HomeScore) &&
		intPtrEqual(m.AwayScore, o.AwayScore) &&
		m.StartTime.Equal(o.StartTime) &&
		string(m.ScoreDetails) == string(o.ScoreDetails)
}

func intPtrEqual(a, b *int) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

type MatchesResult struct {
	Sport     Sport     `json:"sport"`
	Count     int       `json:"count"`
	Matches   []Match   `json:"matches"`
	UpdatedAt time.Time `json:"updated_at,omitzero"`
}

func NewMatchesResult(sport Sport, matches []Match, updatedAt time.Time) *MatchesResult {
	if matches == nil {
		matches = []Match{}
	}
	return &MatchesResult{Sport: sport, Count: len(matches), Matches: matches, UpdatedAt: updatedAt.UTC()}
}

// NormalizeName lower-cases and collapses whitespace for ID and matching use.
func NormalizeName(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

func stableID(prefix string, parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return prefix + hex.EncodeToString(sum[:8])
}

// NewMatchID derives our internal match ID from the first provider reference
// that introduced the match.
func NewMatchID(provider, providerMatchID string) string {
	return stableID("m_", provider, providerMatchID)
}

func NewCompetitionID(sport Sport, name string) string {
	return stableID("c_", string(sport), NormalizeName(name))
}

func NewTeamID(sport Sport, name string) string {
	return stableID("t_", string(sport), NormalizeName(name))
}
