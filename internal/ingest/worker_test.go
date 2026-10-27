package ingest

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/samwilson0745/live-score/internal/domain"
	"github.com/samwilson0745/live-score/internal/ports"
	"github.com/samwilson0745/live-score/internal/storage/memory"
	"github.com/samwilson0745/live-score/internal/testutil"
)

var t0 = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

type scriptedProvider struct {
	mu      sync.Mutex
	results []any // *domain.MatchesResult or error, consumed in order; last repeats
	calls   atomic.Int32
	block   chan struct{}
}

func (p *scriptedProvider) Name() string { return "fake" }

func (p *scriptedProvider) GetMatches(ctx context.Context, _ string, _ int) (*domain.MatchesResult, error) {
	p.calls.Add(1)
	if p.block != nil {
		select {
		case <-p.block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	r := p.results[0]
	if len(p.results) > 1 {
		p.results = p.results[1:]
	}
	if err, ok := r.(error); ok {
		return nil, err
	}
	return r.(*domain.MatchesResult), nil
}

type recPublisher struct {
	mu  sync.Mutex
	got [][]domain.Match
}

func (r *recPublisher) Publish(m []domain.Match) {
	r.mu.Lock()
	r.got = append(r.got, m)
	r.mu.Unlock()
}

type recInvalidator struct{ prefixes []string }

func (r *recInvalidator) DeletePrefix(_ context.Context, p string) error {
	r.prefixes = append(r.prefixes, p)
	return nil
}

func res(updated time.Time, home int, status domain.MatchStatus) *domain.MatchesResult {
	return domain.NewMatchesResult("football", []domain.Match{
		testutil.Match("m1", &home, testutil.Int(0), status, updated),
	}, updated)
}

func newWorker(p ports.SportsProvider, repo *memory.Repository, pub ports.UpdatePublisher, inv ports.CacheInvalidator) (*Worker, *[]time.Duration) {
	w := NewWorker(Config{Sports: []domain.Sport{"football"}, MaxAttempts: 3, BaseBackoff: time.Second, MaxBackoff: 8 * time.Second},
		Deps{Provider: p, Repo: repo, Syncs: repo, Publisher: pub, Invalidator: inv, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	var sleeps []time.Duration
	w.sleep = func(_ context.Context, d time.Duration) error { sleeps = append(sleeps, d); return nil }
	w.rand = func() float64 { return 1 } // full delay, deterministic
	w.now = func() time.Time { return t0 }
	return w, &sleeps
}

func TestSyncStoresPublishesInvalidates(t *testing.T) {
	repo, pub, inv := memory.New(), &recPublisher{}, &recInvalidator{}
	p := &scriptedProvider{results: []any{res(t0, 1, domain.StatusLive)}}
	w, _ := newWorker(p, repo, pub, inv)

	if err := w.SyncSport(context.Background(), "football"); err != nil {
		t.Fatal(err)
	}
	m, err := repo.GetMatch(context.Background(), "m1")
	if err != nil || !m.IngestedAt.Equal(t0) {
		t.Fatalf("stored = %+v err=%v", m, err)
	}
	if len(pub.got) != 1 || len(inv.prefixes) != 1 || inv.prefixes[0] != "matches:v2:football:" {
		t.Errorf("published=%d invalidated=%v", len(pub.got), inv.prefixes)
	}
	st, err := repo.GetSyncState(context.Background(), "fake", "football")
	if err != nil || st.LastSuccess.IsZero() || st.ConsecutiveFailures != 0 {
		t.Errorf("sync state = %+v err=%v", st, err)
	}
}

func TestSyncIsIdempotent(t *testing.T) {
	repo, pub, inv := memory.New(), &recPublisher{}, &recInvalidator{}
	p := &scriptedProvider{results: []any{res(t0, 1, domain.StatusLive)}}
	w, _ := newWorker(p, repo, pub, inv)

	for i := 0; i < 3; i++ {
		if err := w.SyncSport(context.Background(), "football"); err != nil {
			t.Fatal(err)
		}
	}
	if len(pub.got) != 1 || len(inv.prefixes) != 1 {
		t.Errorf("unchanged data re-announced: published=%d invalidated=%d", len(pub.got), len(inv.prefixes))
	}
}

func TestOlderDataDoesNotReplaceNewer(t *testing.T) {
	repo := memory.New()
	p := &scriptedProvider{results: []any{
		res(t0, 2, domain.StatusFinished),
		res(t0.Add(-time.Hour), 1, domain.StatusLive),
	}}
	w, _ := newWorker(p, repo, nil, nil)
	_ = w.SyncSport(context.Background(), "football")
	_ = w.SyncSport(context.Background(), "football")

	m, _ := repo.GetMatch(context.Background(), "m1")
	if *m.HomeScore != 2 || m.Status != domain.StatusFinished {
		t.Errorf("stale data overwrote newer: %+v", m)
	}
}

func TestRetriesRetryableThenSucceeds(t *testing.T) {
	retryable := &ports.ProviderError{Provider: "fake", StatusCode: 500, Retryable: true}
	p := &scriptedProvider{results: []any{retryable, retryable, res(t0, 1, domain.StatusLive)}}
	w, sleeps := newWorker(p, memory.New(), nil, nil)

	if err := w.SyncSport(context.Background(), "football"); err != nil {
		t.Fatal(err)
	}
	if p.calls.Load() != 3 {
		t.Errorf("calls = %d", p.calls.Load())
	}
	if len(*sleeps) != 2 || (*sleeps)[0] != time.Second || (*sleeps)[1] != 2*time.Second {
		t.Errorf("backoff = %v, want exponential 1s,2s", *sleeps)
	}
}

func TestRetriesAreBounded(t *testing.T) {
	retryable := &ports.ProviderError{Provider: "fake", StatusCode: 503, Retryable: true}
	p := &scriptedProvider{results: []any{retryable}}
	repo := memory.New()
	w, _ := newWorker(p, repo, nil, nil)

	if err := w.SyncSport(context.Background(), "football"); err == nil {
		t.Fatal("want error")
	}
	if p.calls.Load() != 3 {
		t.Errorf("calls = %d, want MaxAttempts=3", p.calls.Load())
	}
	st, _ := repo.GetSyncState(context.Background(), "fake", "football")
	if st.ConsecutiveFailures != 1 || st.LastError == "" {
		t.Errorf("failure not recorded: %+v", st)
	}
}

func TestPermanentErrorsAreNotRetried(t *testing.T) {
	p := &scriptedProvider{results: []any{&ports.ProviderError{Provider: "fake", StatusCode: 404}}}
	w, sleeps := newWorker(p, memory.New(), nil, nil)
	_ = w.SyncSport(context.Background(), "football")
	if p.calls.Load() != 1 || len(*sleeps) != 0 {
		t.Errorf("calls=%d sleeps=%v", p.calls.Load(), *sleeps)
	}
}

func TestRetryAfterHonoredAndCapped(t *testing.T) {
	p := &scriptedProvider{results: []any{
		&ports.ProviderError{Provider: "fake", StatusCode: 429, Retryable: true, RetryAfter: 5 * time.Second},
		&ports.ProviderError{Provider: "fake", StatusCode: 429, Retryable: true, RetryAfter: time.Hour},
		res(t0, 1, domain.StatusLive),
	}}
	w, sleeps := newWorker(p, memory.New(), nil, nil)
	_ = w.SyncSport(context.Background(), "football")
	if (*sleeps)[0] != 5*time.Second || (*sleeps)[1] != time.Minute {
		t.Errorf("sleeps = %v", *sleeps)
	}
}

func TestBackoffJitterStaysWithinBounds(t *testing.T) {
	w, _ := newWorker(&scriptedProvider{results: []any{errors.New("x")}}, memory.New(), nil, nil)
	for _, r := range []float64{0, 0.5, 1} {
		w.rand = func() float64 { return r }
		d := w.backoff(3, errors.New("x")) // base 1s << 2 = 4s
		if d < 2*time.Second || d > 4*time.Second {
			t.Errorf("rand=%v -> %v, want within [2s,4s]", r, d)
		}
	}
	if d := w.backoff(30, errors.New("x")); d > 8*time.Second {
		t.Errorf("backoff must be capped at MaxBackoff, got %v", d)
	}
}

func TestOverlappingRunsAreRejected(t *testing.T) {
	p := &scriptedProvider{results: []any{res(t0, 1, domain.StatusLive)}, block: make(chan struct{})}
	w, _ := newWorker(p, memory.New(), nil, nil)

	done := make(chan error, 1)
	go func() { done <- w.SyncSport(context.Background(), "football") }()
	for p.calls.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	if err := w.SyncSport(context.Background(), "football"); !errors.Is(err, ErrAlreadyRunning) {
		t.Errorf("second run err = %v, want ErrAlreadyRunning", err)
	}
	close(p.block)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestRunStopsOnCancel(t *testing.T) {
	p := &scriptedProvider{results: []any{res(t0, 1, domain.StatusLive)}}
	w, _ := newWorker(p, memory.New(), nil, nil)
	w.cfg.Interval = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	time.Sleep(60 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop")
	}
	if p.calls.Load() < 2 {
		t.Errorf("expected repeated ticks, got %d calls", p.calls.Load())
	}
}
