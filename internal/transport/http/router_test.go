package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/samwilson0745/live-score/internal/application"
	"github.com/samwilson0745/live-score/internal/domain"
	"github.com/samwilson0745/live-score/internal/metrics"
	"github.com/samwilson0745/live-score/internal/realtime"
	"github.com/samwilson0745/live-score/internal/storage/memory"
	"github.com/samwilson0745/live-score/internal/testutil"
	transport "github.com/samwilson0745/live-score/internal/transport/http"
	"github.com/samwilson0745/live-score/internal/transport/http/handler"
)

var t0 = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func build(t *testing.T, mutate func(*transport.Deps)) (http.Handler, *testutil.Provider) {
	t.Helper()
	p := &testutil.Provider{Result: domain.NewMatchesResult("football",
		[]domain.Match{testutil.Match("m1", testutil.Int(1), testutil.Int(0), domain.StatusLive, t0)}, t0)}
	m := metrics.New()
	svc := application.NewMatchesService(application.ProviderLoader{Provider: p, Metrics: m}, application.ServiceOptions{Logger: discard(), Metrics: m})
	d := transport.Deps{
		Matches: handler.NewMatchesHandler(svc, []domain.Sport{"football"}, discard()),
		Metrics: m, Logger: discard(),
	}
	if mutate != nil {
		mutate(&d)
	}
	return transport.NewRouter(d), p
}

func do(h http.Handler, method, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

func TestHealthAndReady(t *testing.T) {
	h, _ := build(t, nil)
	if rec := do(h, "GET", "/health"); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"status":"ok"`) {
		t.Errorf("/health = %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "GET", "/ready"); rec.Code != 200 {
		t.Errorf("/ready without checks = %d", rec.Code)
	}

	down, _ := build(t, func(d *transport.Deps) { d.Ready = func(context.Context) error { return errors.New("db down") } })
	if rec := do(down, "GET", "/ready"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("/ready with failing check = %d", rec.Code)
	}
}

func TestRoutingRestrictions(t *testing.T) {
	h, _ := build(t, nil)
	if rec := do(h, "POST", "/api/v1/matches"); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST = %d", rec.Code)
	}
	if rec := do(h, "GET", "/nope"); rec.Code != 404 {
		t.Errorf("unknown = %d", rec.Code)
	}
	// Stored/ws routes are absent without a repository.
	if rec := do(h, "GET", "/api/v1/matches/m1"); rec.Code != 404 {
		t.Errorf("stored route without repo = %d", rec.Code)
	}
}

func TestRequestIDHeader(t *testing.T) {
	h, _ := build(t, nil)
	if rec := do(h, "GET", "/health"); len(rec.Header().Get("X-Request-ID")) < 8 {
		t.Error("request ID should be generated")
	}
	req := httptest.NewRequest("GET", "/health", nil)
	req.Header.Set("X-Request-ID", "abc-123")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Header().Get("X-Request-ID") != "abc-123" {
		t.Error("incoming request ID should be propagated")
	}
	req.Header.Set("X-Request-ID", "bad id\twith space")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Header().Get("X-Request-ID") == "bad id\twith space" {
		t.Error("unsafe request IDs must be replaced")
	}
}

func TestMetricsEndpointUsesRoutePatterns(t *testing.T) {
	h, _ := build(t, nil)
	do(h, "GET", "/api/v1/matches?limit=5")
	do(h, "GET", "/definitely/not/a/route/12345")

	body := do(h, "GET", "/metrics").Body.String()
	for _, want := range []string{
		`http_requests_total{method="GET",route="GET /api/v1/matches",status="200"} 1`,
		`route="unmatched"`,
		`cache_events_total`,
		`provider_requests_total`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics missing %q", want)
		}
	}
	if strings.Contains(body, "12345") {
		t.Error("raw paths must not become label values")
	}
}

func TestPanicRecovery(t *testing.T) {
	h, _ := build(t, func(d *transport.Deps) {
		d.Ready = func(context.Context) error { panic("kaboom") }
	})
	rec := do(h, "GET", "/ready")
	if rec.Code != 500 {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

func TestLoggerRecordsStatusWithoutQuery(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	h, _ := build(t, func(d *transport.Deps) { d.Logger = log })
	do(h, "GET", "/api/v1/matches?limit=bogus&secret=hunter2")

	var line map[string]any
	for _, l := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var m map[string]any
		if json.Unmarshal([]byte(l), &m) == nil && m["msg"] == "request" {
			line = m
		}
	}
	if line == nil || line["status"] != float64(400) || line["method"] != "GET" || line["path"] != "/api/v1/matches" {
		t.Fatalf("log line = %v", line)
	}
	if strings.Contains(buf.String(), "hunter2") {
		t.Error("query values must not be logged")
	}
}

func TestStoredEndpoints(t *testing.T) {
	repo := memory.New()
	m := testutil.Match("m_1", testutil.Int(0), nil, domain.StatusLive, t0)
	_, _ = repo.UpsertMatches(context.Background(), []domain.Match{m})
	h, _ := build(t, func(d *transport.Deps) { d.Stored = handler.NewStoredHandler(repo, discard()) })

	if rec := do(h, "GET", "/api/v1/matches/m_1"); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"id":"m_1"`) {
		t.Errorf("get = %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "GET", "/api/v1/matches/zzz"); rec.Code != 404 {
		t.Errorf("missing = %d", rec.Code)
	}
	if rec := do(h, "GET", "/api/v1/competitions/c_1/matches?limit=5"); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"count":1`) {
		t.Errorf("competition = %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "GET", "/api/v1/competitions/c_1/matches?limit=0"); rec.Code != 400 {
		t.Errorf("bad limit = %d", rec.Code)
	}
}

func TestWebSocketSnapshotThenUpdates(t *testing.T) {
	repo, hub := memory.New(), realtime.NewHub()
	m := testutil.Match("m_ws", testutil.Int(0), testutil.Int(0), domain.StatusLive, t0)
	_, _ = repo.UpsertMatches(context.Background(), []domain.Match{m})

	h, _ := build(t, func(d *transport.Deps) {
		d.WS = &handler.WSHandler{Hub: hub, Repo: repo, Log: discard()}
	})
	srv := httptest.NewServer(h)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/api/v1/ws?match_id=m_ws&match_id=unknown", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()

	var msg handler.WSMessage
	if err := wsjson.Read(ctx, conn, &msg); err != nil || msg.Type != "snapshot" || msg.Match.ID != "m_ws" {
		t.Fatalf("snapshot = %+v err=%v", msg, err)
	}

	updated := m
	updated.HomeScore, updated.Version = testutil.Int(1), 2
	hub.Publish([]domain.Match{updated})
	if err := wsjson.Read(ctx, conn, &msg); err != nil || msg.Type != "update" || *msg.Match.HomeScore != 1 || msg.Match.Version != 2 {
		t.Fatalf("update = %+v err=%v", msg, err)
	}

	conn.Close(websocket.StatusNormalClosure, "")
	deadline := time.Now().Add(2 * time.Second)
	for hub.Subscribers() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if hub.Subscribers() != 0 {
		t.Error("subscription leaked after disconnect")
	}
}

func TestWebSocketValidation(t *testing.T) {
	h, _ := build(t, func(d *transport.Deps) {
		d.WS = &handler.WSHandler{Hub: realtime.NewHub(), Repo: memory.New(), Log: discard()}
	})
	if rec := do(h, "GET", "/api/v1/ws"); rec.Code != 400 {
		t.Errorf("no ids = %d", rec.Code)
	}
	long := "/api/v1/ws?match_id=" + strings.Repeat("x", 65)
	if rec := do(h, "GET", long); rec.Code != 400 {
		t.Errorf("long id = %d", rec.Code)
	}
}
