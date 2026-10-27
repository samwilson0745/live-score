package application_test

import (
	"testing"
	"time"

	"github.com/samwilson0745/live-score/internal/application"
	"github.com/samwilson0745/live-score/internal/domain"
	"github.com/samwilson0745/live-score/internal/testutil"
)

func obs(provider string, home, away int, status domain.MatchStatus, at time.Time) application.Observation {
	m := testutil.Match("m1", &home, &away, status, at)
	m.Source.Provider = provider
	return application.Observation{Match: m, ObservedAt: at}
}

func TestReconcilePrefersPriorityOverRecency(t *testing.T) {
	o := application.ReconcileOptions{Priority: map[string]int{"a": 1, "b": 2}, StaleAfter: time.Minute}
	r, ok := application.Reconcile([]application.Observation{
		obs("b", 2, 1, domain.StatusLive, t0.Add(10*time.Second)),
		obs("a", 1, 1, domain.StatusLive, t0),
	}, o)
	if !ok || r.Chosen != "a" || *r.Match.HomeScore != 1 {
		t.Fatalf("chosen=%s", r.Chosen)
	}
	if len(r.Conflicts) != 1 || r.Conflicts[0].Field != "home_score" {
		t.Errorf("conflicts = %+v", r.Conflicts)
	}
}

func TestReconcileIgnoresStaleObservations(t *testing.T) {
	o := application.ReconcileOptions{Priority: map[string]int{"a": 1, "b": 2}, StaleAfter: time.Minute}
	r, _ := application.Reconcile([]application.Observation{
		obs("a", 0, 0, domain.StatusLive, t0.Add(-time.Hour)), // top priority but stale
		obs("b", 2, 1, domain.StatusFinished, t0),
	}, o)
	if r.Chosen != "b" || len(r.Conflicts) != 0 {
		t.Errorf("chosen=%s conflicts=%v", r.Chosen, r.Conflicts)
	}
}

func TestReconcileAgreementHasNoConflicts(t *testing.T) {
	r, _ := application.Reconcile([]application.Observation{
		obs("a", 1, 0, domain.StatusLive, t0), obs("b", 1, 0, domain.StatusLive, t0),
	}, application.ReconcileOptions{Priority: map[string]int{"a": 1}})
	if len(r.Conflicts) != 0 {
		t.Errorf("conflicts = %v", r.Conflicts)
	}
	if _, ok := application.Reconcile(nil, application.ReconcileOptions{}); ok {
		t.Error("empty input should not reconcile")
	}
}

func TestSameEvent(t *testing.T) {
	a := testutil.Match("a", nil, nil, domain.StatusScheduled, t0)
	b := a
	b.Home.Name, b.Away.Name = "  HOME ", "away"
	b.StartTime = a.StartTime.Add(10 * time.Minute)
	if !application.SameEvent(a, b, 15*time.Minute) {
		t.Error("should match despite case/whitespace and small time skew")
	}
	if application.SameEvent(a, b, time.Minute) {
		t.Error("outside tolerance must not match")
	}
	c := a
	c.Home, c.Away = a.Away, a.Home
	if application.SameEvent(a, c, time.Hour) {
		t.Error("swapped home/away must not match")
	}
	d := a
	d.Sport = "tennis"
	if application.SameEvent(a, d, time.Hour) {
		t.Error("different sport must not match")
	}
}
