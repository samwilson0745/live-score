package handler_test

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/samwilson0745/live-score/internal/application"
	"github.com/samwilson0745/live-score/internal/domain"
	"github.com/samwilson0745/live-score/internal/ports"
	"github.com/samwilson0745/live-score/internal/testutil"
	"github.com/samwilson0745/live-score/internal/transport/http/handler"
)

var now = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

func fixture() *domain.MatchesResult {
	return domain.NewMatchesResult("football", []domain.Match{
		testutil.Match("m1", testutil.Int(0), testutil.Int(0), domain.StatusFinished, now),
		testutil.Match("m2", nil, nil, domain.StatusScheduled, now),
	}, now)
}

func setup(p *testutil.Provider, c *testutil.Cache) *handler.MatchesHandler {
	opts := application.ServiceOptions{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if c != nil {
		opts.Cache = c
	}
	svc := application.NewMatchesService(application.ProviderLoader{Provider: p}, opts)
	return handler.NewMatchesHandler(svc, []domain.Sport{"football", "tennis"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func get(h http.Handler, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec
}

func TestDefaults(t *testing.T) {
	p := &testutil.Provider{Result: fixture()}
	rec := get(setup(p, testutil.NewCache()), "/api/v1/matches")

	if rec.Code != 200 {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	if p.Last.Sport != "football" || p.Last.Limit != 5 {
		t.Errorf("defaults = %s/%d, want football/5", p.Last.Sport, p.Last.Limit)
	}
}

func TestInvalidInput(t *testing.T) {
	tests := []struct{ name, target string }{
		{"non-numeric limit", "/m?limit=abc"},
		{"zero limit", "/m?limit=0"},
		{"negative limit", "/m?limit=-3"},
		{"over limit", "/m?limit=101"},
		{"unsupported sport", "/m?sport=curling"},
		{"unsafe sport", "/m?sport=../../etc"},
		{"unknown status", "/m?status=bogus"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &testutil.Provider{Result: fixture()}
			rec := get(setup(p, testutil.NewCache()), tt.target)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rec.Code)
			}
			var body map[string]string
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body["error"] == "" {
				t.Errorf("want JSON error body, got %q", rec.Body)
			}
			if p.Calls.Load() != 0 {
				t.Error("provider must not be called for invalid input")
			}
		})
	}
}

func TestLimitBoundariesAccepted(t *testing.T) {
	for _, l := range []string{"1", "100"} {
		p := &testutil.Provider{Result: fixture()}
		if rec := get(setup(p, nil), "/m?limit="+l); rec.Code != 200 {
			t.Errorf("limit=%s -> %d", l, rec.Code)
		}
	}
}

func TestCacheHitDoesNotCallProvider(t *testing.T) {
	p, c := &testutil.Provider{Result: fixture()}, testutil.NewCache()
	h := setup(p, c)

	if got := get(h, "/m").Header().Get("X-Cache"); got != "MISS" {
		t.Fatalf("first X-Cache = %q", got)
	}
	rec := get(h, "/m")
	if rec.Code != 200 || rec.Header().Get("X-Cache") != "HIT" {
		t.Fatalf("second: %d X-Cache=%q", rec.Code, rec.Header().Get("X-Cache"))
	}
	if p.Calls.Load() != 1 {
		t.Errorf("provider calls = %d, want 1", p.Calls.Load())
	}
}

func TestCacheBypass(t *testing.T) {
	p := &testutil.Provider{Result: fixture()}
	rec := get(setup(p, nil), "/m")
	if rec.Code != 200 || rec.Header().Get("X-Cache") != "BYPASS" {
		t.Fatalf("%d X-Cache=%q", rec.Code, rec.Header().Get("X-Cache"))
	}
}

func TestCacheErrorsDoNotBreakResponse(t *testing.T) {
	c := testutil.NewCache()
	c.GetErr, c.SetErr = errors.New("get"), errors.New("set")
	rec := get(setup(&testutil.Provider{Result: fixture()}, c), "/m")
	if rec.Code != 200 || rec.Header().Get("X-Cache") != "BYPASS" {
		t.Fatalf("%d X-Cache=%q", rec.Code, rec.Header().Get("X-Cache"))
	}
}

func TestCacheWriteErrorOnMiss(t *testing.T) {
	c := testutil.NewCache()
	c.SetErr = errors.New("set")
	rec := get(setup(&testutil.Provider{Result: fixture()}, c), "/m")
	if rec.Code != 200 || rec.Header().Get("X-Cache") != "MISS" {
		t.Fatalf("%d X-Cache=%q", rec.Code, rec.Header().Get("X-Cache"))
	}
}

func TestProviderErrorMapping(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"provider error", &ports.ProviderError{Provider: "x", StatusCode: 500}, http.StatusBadGateway},
		{"plain provider failure", errors.New("boom"), http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := get(setup(&testutil.Provider{Err: tt.err}, testutil.NewCache()), "/m")
			if rec.Code != tt.want {
				t.Errorf("status = %d, want %d", rec.Code, tt.want)
			}
			if rec.Header().Get("X-Cache") != "" {
				t.Error("failed responses carry no X-Cache")
			}
		})
	}
}

func TestResponseShape(t *testing.T) {
	rec := get(setup(&testutil.Provider{Result: fixture()}, nil), "/m")
	var body struct {
		Sport   string `json:"sport"`
		Count   int    `json:"count"`
		Matches []struct {
			HomeScore *int   `json:"home_score"`
			Status    string `json:"status"`
			StartTime string `json:"start_time"`
		} `json:"matches"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Count != 2 || body.Matches[0].HomeScore == nil || *body.Matches[0].HomeScore != 0 || body.Matches[1].HomeScore != nil {
		t.Errorf("scores not preserved: %+v", body)
	}
	if _, err := time.Parse(time.RFC3339, body.Matches[0].StartTime); err != nil {
		t.Errorf("start_time not RFC 3339: %v", err)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content type = %q", ct)
	}
}
