package sportscore

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/samwilson0745/live-score/internal/domain"
	"github.com/samwilson0745/live-score/internal/ports"
)

const sampleBody = `{
  "sport": "football", "count": 3, "updated": "2026-10-24T21:37:35.732648+00:00",
  "matches": [
    {"home":"A","away":"B","home_score":"2","away_score":"0","status":"finished","status_text":"Finished",
     "time":"2026-10-24T00:00:00+00:00","competition":"League","url":"/football/match/a-vs-b/abc123/"},
    {"home":"C","away":"D","home_score":"0","away_score":"0","status":"finished","status_text":"Finished",
     "time":"2026-10-24T00:00:00+00:00","competition":"League","url":"/football/match/c-vs-d/def456/"},
    {"home":"E","away":"F","home_score":null,"away_score":null,"status":"upcoming","status_text":"Postponed",
     "time":"2026-10-24T00:00:00+00:00","competition":"League","url":"/football/match/e-vs-f/ghi789/"}
  ]}`

func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return NewClient(WithBaseURL(srv.URL))
}

func TestGetMatchesSuccess(t *testing.T) {
	var gotPath, gotSport, gotLimit string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotSport, gotLimit = r.URL.Path, r.URL.Query().Get("sport"), r.URL.Query().Get("limit")
		_, _ = w.Write([]byte(sampleBody))
	})

	res, err := c.GetMatches(context.Background(), "football", 3)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/widget/matches/" || gotSport != "football" || gotLimit != "3" {
		t.Errorf("request = %s sport=%s limit=%s", gotPath, gotSport, gotLimit)
	}
	if res.Count != 3 || len(res.Matches) != 3 {
		t.Fatalf("got %d matches", len(res.Matches))
	}
	if want := time.Date(2026, 10, 24, 21, 37, 35, 732648000, time.UTC); !res.UpdatedAt.Equal(want) {
		t.Errorf("updated = %v", res.UpdatedAt)
	}
}

func TestNullScoreDistinctFromZero(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(sampleBody)) })
	res, err := c.GetMatches(context.Background(), "football", 3)
	if err != nil {
		t.Fatal(err)
	}
	zero, unknown := res.Matches[1], res.Matches[2]
	if zero.HomeScore == nil || *zero.HomeScore != 0 {
		t.Errorf("0-0 match should have non-nil zero score, got %v", zero.HomeScore)
	}
	if unknown.HomeScore != nil || unknown.AwayScore != nil {
		t.Errorf("null scores must stay nil")
	}
	if unknown.Status != domain.StatusPostponed {
		t.Errorf("status = %s, want postponed", unknown.Status)
	}
	if res.Matches[0].Source.MatchID != "abc123" || res.Matches[0].ID == "" {
		t.Errorf("provider ref not extracted: %+v", res.Matches[0].Source)
	}
}

func TestGetMatchesErrors(t *testing.T) {
	tests := []struct {
		name      string
		handler   http.HandlerFunc
		retryable bool
		retry     time.Duration
	}{
		{"server error is retryable", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }, true, 0},
		{"rate limit honors Retry-After", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Retry-After", "7")
			w.WriteHeader(429)
		}, true, 7 * time.Second},
		{"not found is permanent", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) }, false, 0},
		{"malformed JSON is permanent", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("{nope")) }, false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newTestClient(t, tt.handler)
			_, err := c.GetMatches(context.Background(), "football", 5)
			var pe *ports.ProviderError
			if !errors.As(err, &pe) {
				t.Fatalf("want ProviderError, got %v", err)
			}
			if pe.Retryable != tt.retryable || pe.RetryAfter != tt.retry {
				t.Errorf("retryable=%v retryAfter=%v", pe.Retryable, pe.RetryAfter)
			}
		})
	}
}

func TestContextCancellation(t *testing.T) {
	release := make(chan struct{})
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	defer close(release)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := c.GetMatches(ctx, "football", 5)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("want deadline exceeded, got %v", err)
	}
	if ports.IsRetryable(err) {
		t.Error("cancelled calls must not be retryable")
	}
	if time.Since(start) > 2*time.Second {
		t.Error("did not return promptly")
	}
}

func TestInputValidation(t *testing.T) {
	called := false
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { called = true })
	for _, tt := range []struct {
		sport string
		limit int
	}{{"", 5}, {"foot ball", 5}, {"../etc", 5}, {"football&x=1", 5}, {"football", 0}, {"football", 101}} {
		if _, err := c.GetMatches(context.Background(), tt.sport, tt.limit); err == nil {
			t.Errorf("sport=%q limit=%d should be rejected", tt.sport, tt.limit)
		}
	}
	if called {
		t.Error("invalid input must not reach the provider")
	}
}
