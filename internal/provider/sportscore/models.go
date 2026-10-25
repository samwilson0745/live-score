package sportscore

import "time"

type MatchesResponse struct {
	Sport   string    `json:"sport"`
	Count   int       `json:"count"`
	Matches []Match   `json:"matches"`
	Updated time.Time `json:"updated"`
}

type Match struct {
	Home            string    `json:"home"`
	Away            string    `json:"away"`
	HomeLogo        string    `json:"home_logo"`
	AwayLogo        string    `json:"away_logo"`
	HomeScore       *string   `json:"home_score"` // helps us distinguish a missing score from a score of zero.
	AwayScore       *string   `json:"away_score"` // same here
	Status          string    `json:"status"`
	StatusText      string    `json:"status_text"`
	Time            time.Time `json:"time"`
	Competition     string    `json:"competition"`
	CompetitionLogo string    `json:"competition_logo"`
	URL             string    `json:"url"`
}
