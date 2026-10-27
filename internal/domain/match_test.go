package domain

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestIDsAreStableAndNamespaced(t *testing.T) {
	if NewMatchID("p", "1") != NewMatchID("p", "1") {
		t.Error("IDs must be deterministic")
	}
	if NewMatchID("p", "1") == NewMatchID("q", "1") {
		t.Error("provider must be part of the ID")
	}
	if NewTeamID("football", "  FC  Foo ") != NewTeamID("football", "fc foo") {
		t.Error("team IDs should ignore case and spacing")
	}
	if !strings.HasPrefix(NewCompetitionID("football", "x"), "c_") {
		t.Error("competition prefix")
	}
}

func TestSameStateTreatsNilAndZeroDifferently(t *testing.T) {
	zero := 0
	a := Match{HomeScore: nil, Status: StatusLive}
	b := Match{HomeScore: &zero, Status: StatusLive}
	if a.SameState(b) {
		t.Error("unknown score must differ from 0")
	}
	if !a.SameState(a) {
		t.Error("identical state")
	}
}

func TestJSONShape(t *testing.T) {
	zero := 0
	m := Match{ID: "m", HomeScore: &zero, StartTime: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
	b, _ := json.Marshal(m)
	s := string(b)
	if !strings.Contains(s, `"home_score":0`) || !strings.Contains(s, `"away_score":null`) ||
		!strings.Contains(s, `"start_time":"2026-01-02T03:04:05Z"`) {
		t.Errorf("json = %s", s)
	}
	if strings.Contains(s, "ingested_at") {
		t.Errorf("zero ingested_at must be omitted: %s", s)
	}
	if r, _ := json.Marshal(NewMatchesResult("f", nil, time.Time{})); !strings.Contains(string(r), `"matches":[]`) || strings.Contains(string(r), "updated_at") {
		t.Errorf("empty list must serialize as [] without a zero updated_at, got %s", r)
	}
}

func TestParseMatchStatus(t *testing.T) {
	if s, ok := ParseMatchStatus("LIVE"); !ok || s != StatusLive {
		t.Error("case-insensitive parse")
	}
	if _, ok := ParseMatchStatus("nope"); ok {
		t.Error("unknown must fail")
	}
}
