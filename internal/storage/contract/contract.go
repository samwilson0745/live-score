// Package contract holds behavior every ports.MatchRepository must satisfy;
// the memory and PostgreSQL implementations both run it.
package contract

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/samwilson0745/live-score/internal/domain"
	"github.com/samwilson0745/live-score/internal/ports"
	"github.com/samwilson0745/live-score/internal/testutil"
)

var t0 = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

// Run executes the contract against fresh repositories from newRepo.
func Run(t *testing.T, newRepo func(*testing.T) ports.MatchRepository) {
	ctx := context.Background()

	t.Run("insert, unchanged, change", func(t *testing.T) {
		r := newRepo(t)
		m := testutil.Match("m_ins", testutil.Int(0), nil, domain.StatusLive, t0)

		ch, err := r.UpsertMatches(ctx, []domain.Match{m})
		if err != nil || len(ch) != 1 || ch[0].Version != 1 {
			t.Fatalf("insert: %v %+v", err, ch)
		}

		later := m
		later.ProviderUpdatedAt = t0.Add(time.Minute)
		if ch, _ := r.UpsertMatches(ctx, []domain.Match{later}); len(ch) != 0 {
			t.Errorf("identical state must not count as changed: %+v", ch)
		}

		scored := later
		scored.HomeScore = testutil.Int(1)
		ch, _ = r.UpsertMatches(ctx, []domain.Match{scored})
		if len(ch) != 1 || ch[0].Version != 2 {
			t.Fatalf("change: %+v", ch)
		}
	})

	t.Run("stale observation never overwrites", func(t *testing.T) {
		r := newRepo(t)
		fresh := testutil.Match("m_stale", testutil.Int(2), testutil.Int(1), domain.StatusFinished, t0)
		_, _ = r.UpsertMatches(ctx, []domain.Match{fresh})

		old := testutil.Match("m_stale", testutil.Int(1), testutil.Int(1), domain.StatusLive, t0.Add(-time.Hour))
		if ch, _ := r.UpsertMatches(ctx, []domain.Match{old}); len(ch) != 0 {
			t.Fatal("stale data reported as change")
		}
		got, err := r.GetMatch(ctx, "m_stale")
		if err != nil || *got.HomeScore != 2 || got.Status != domain.StatusFinished {
			t.Fatalf("stored = %+v err=%v", got, err)
		}
	})

	t.Run("null score preserved distinct from zero", func(t *testing.T) {
		r := newRepo(t)
		_, _ = r.UpsertMatches(ctx, []domain.Match{
			testutil.Match("m_null", nil, nil, domain.StatusScheduled, t0),
			testutil.Match("m_zero", testutil.Int(0), testutil.Int(0), domain.StatusFinished, t0),
		})
		n, _ := r.GetMatch(ctx, "m_null")
		z, _ := r.GetMatch(ctx, "m_zero")
		if n.HomeScore != nil || z.HomeScore == nil || *z.HomeScore != 0 {
			t.Errorf("null=%v zero=%v", n.HomeScore, z.HomeScore)
		}
		if n.Source.Provider != "fake" || !n.StartTime.Equal(t0.Add(-time.Hour)) {
			t.Errorf("round trip lost fields: %+v", n)
		}
	})

	t.Run("not found", func(t *testing.T) {
		if _, err := newRepo(t).GetMatch(ctx, "nope"); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("filters, order and limit", func(t *testing.T) {
		r := newRepo(t)
		a := testutil.Match("m_a", nil, nil, domain.StatusLive, t0)
		b := testutil.Match("m_b", nil, nil, domain.StatusFinished, t0)
		b.StartTime = a.StartTime.Add(time.Hour) // newer
		c := testutil.Match("m_c", nil, nil, domain.StatusLive, t0)
		c.Sport = "tennis"
		c.Competition.ID = "c_other"
		_, _ = r.UpsertMatches(ctx, []domain.Match{a, b, c})

		all, _ := r.ListMatches(ctx, ports.MatchFilter{Sport: "football"})
		if len(all) != 2 || all[0].ID != "m_b" {
			t.Errorf("order/sport filter: %+v", ids(all))
		}
		live, _ := r.ListMatches(ctx, ports.MatchFilter{Sport: "football", Status: domain.StatusLive})
		if len(live) != 1 || live[0].ID != "m_a" {
			t.Errorf("status filter: %+v", ids(live))
		}
		comp, _ := r.ListMatches(ctx, ports.MatchFilter{CompetitionID: "c_other"})
		if len(comp) != 1 || comp[0].ID != "m_c" {
			t.Errorf("competition filter: %+v", ids(comp))
		}
		one, _ := r.ListMatches(ctx, ports.MatchFilter{Limit: 1})
		if len(one) != 1 {
			t.Errorf("limit: %d", len(one))
		}
	})
}

func ids(ms []domain.Match) (out []string) {
	for _, m := range ms {
		out = append(out, m.ID)
	}
	return
}
