package application

import (
	"strconv"
	"time"

	"github.com/samwilson0745/live-score/internal/domain"
)

// Observation is one provider's view of a match at a point in time.
type Observation struct {
	Match      domain.Match
	ObservedAt time.Time
}

type Conflict struct {
	Field string
	Left  string
	Right string
}

type ReconcileOptions struct {
	// Priority ranks providers; lower number wins. Unlisted providers rank last.
	Priority map[string]int
	// StaleAfter drops observations older than the freshest one by this much.
	StaleAfter time.Duration
}

type Reconciled struct {
	Match     domain.Match
	Chosen    string
	Conflicts []Conflict
}

// Reconcile picks a canonical state from several observations of the same
// match: fresh observations only, then provider priority, then recency. It
// does not vote and never lets "last response wins" decide on its own.
// Disagreements between fresh providers are reported as conflicts.
func Reconcile(obs []Observation, o ReconcileOptions) (Reconciled, bool) {
	if len(obs) == 0 {
		return Reconciled{}, false
	}
	newest := obs[0].ObservedAt
	for _, x := range obs[1:] {
		if x.ObservedAt.After(newest) {
			newest = x.ObservedAt
		}
	}
	fresh := make([]Observation, 0, len(obs))
	for _, x := range obs {
		if o.StaleAfter <= 0 || newest.Sub(x.ObservedAt) <= o.StaleAfter {
			fresh = append(fresh, x)
		}
	}

	rank := func(p string) int {
		if r, ok := o.Priority[p]; ok {
			return r
		}
		return 1 << 20
	}
	best := fresh[0]
	for _, x := range fresh[1:] {
		bp, xp := rank(best.Match.Source.Provider), rank(x.Match.Source.Provider)
		if xp < bp || (xp == bp && x.ObservedAt.After(best.ObservedAt)) {
			best = x
		}
	}

	res := Reconciled{Match: best.Match, Chosen: best.Match.Source.Provider}
	for _, x := range fresh {
		if x.Match.Source.Provider == res.Chosen {
			continue
		}
		res.Conflicts = append(res.Conflicts, diff(best.Match, x.Match)...)
	}
	return res, true
}

func diff(a, b domain.Match) []Conflict {
	var out []Conflict
	if a.Status != b.Status {
		out = append(out, Conflict{"status", string(a.Status), string(b.Status)})
	}
	if !scoreEqual(a.HomeScore, b.HomeScore) {
		out = append(out, Conflict{"home_score", scoreStr(a.HomeScore), scoreStr(b.HomeScore)})
	}
	if !scoreEqual(a.AwayScore, b.AwayScore) {
		out = append(out, Conflict{"away_score", scoreStr(a.AwayScore), scoreStr(b.AwayScore)})
	}
	return out
}

func scoreEqual(a, b *int) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func scoreStr(p *int) string {
	if p == nil {
		return "unknown"
	}
	return strconv.Itoa(*p)
}

// SameEvent decides whether two unmapped provider records describe the same
// event: same sport and competition, same participants (in either order only
// if the sides match positionally), and start times within tolerance. It is
// deliberately strict; ambiguous pairs should be left unmapped.
func SameEvent(a, b domain.Match, tolerance time.Duration) bool {
	if a.Sport != b.Sport {
		return false
	}
	if domain.NormalizeName(a.Competition.Name) != domain.NormalizeName(b.Competition.Name) {
		return false
	}
	if domain.NormalizeName(a.Home.Name) != domain.NormalizeName(b.Home.Name) ||
		domain.NormalizeName(a.Away.Name) != domain.NormalizeName(b.Away.Name) {
		return false
	}
	d := a.StartTime.Sub(b.StartTime)
	if d < 0 {
		d = -d
	}
	return d <= tolerance
}
