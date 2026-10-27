package sportscore

import (
	"testing"

	"github.com/samwilson0745/live-score/internal/domain"
)

func TestNormalizeStatus(t *testing.T) {
	tests := []struct {
		status, text string
		want         domain.MatchStatus
	}{
		{"finished", "Finished", domain.StatusFinished},
		{"upcoming", "Not started", domain.StatusScheduled},
		{"upcoming", "Postponed", domain.StatusPostponed},
		{"upcoming", "Cancelled", domain.StatusCancelled},
		{"inprogress", "2nd half", domain.StatusLive},
		{"live", "", domain.StatusLive},
		{"weird", "???", domain.StatusUnknown},
	}
	for _, tt := range tests {
		if got := NormalizeStatus(tt.status, tt.text); got != tt.want {
			t.Errorf("NormalizeStatus(%q,%q) = %s, want %s", tt.status, tt.text, got, tt.want)
		}
	}
}

func TestProviderMatchID(t *testing.T) {
	for in, want := range map[string]string{
		"/football/match/a-vs-b/xyz/": "xyz",
		"/football/match/a-vs-b/xyz":  "xyz",
		"xyz":                         "xyz",
		"":                            "",
	} {
		if got := providerMatchID(in); got != want {
			t.Errorf("providerMatchID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseScore(t *testing.T) {
	s0, bad := "0", "x"
	if p := parseScore(&s0); p == nil || *p != 0 {
		t.Error("\"0\" must parse to non-nil 0")
	}
	if parseScore(nil) != nil || parseScore(&bad) != nil {
		t.Error("nil and unparsable scores must be nil")
	}
}

func TestMissingURLStillGetsDistinctIDs(t *testing.T) {
	r := &MatchesResponse{Matches: []Match{{Home: "A", Away: "B"}, {Home: "C", Away: "D"}}}
	res := ToDomain("football", r)
	if res.Matches[0].ID == res.Matches[1].ID {
		t.Error("matches without URLs must not collide")
	}
}
