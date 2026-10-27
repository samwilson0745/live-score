package application_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/samwilson0745/live-score/internal/application"
	"github.com/samwilson0745/live-score/internal/domain"
	"github.com/samwilson0745/live-score/internal/testutil"
)

var t0 = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

func result() *domain.MatchesResult {
	return domain.NewMatchesResult("football", []domain.Match{
		testutil.Match("m1", testutil.Int(1), testutil.Int(0), domain.StatusLive, t0),
		testutil.Match("m2", nil, nil, domain.StatusScheduled, t0),
	}, t0)
}

func newSvc(p *testutil.Provider, c *testutil.Cache, opts application.ServiceOptions) *application.MatchesService {
	if c != nil {
		opts.Cache = c
	}
	return application.NewMatchesService(application.ProviderLoader{Provider: p}, opts)
}

var q = application.Query{Sport: "football", Limit: 5}

func TestMissThenHit(t *testing.T) {
	p, c := &testutil.Provider{Result: result()}, testutil.NewCache()
	svc := newSvc(p, c, application.ServiceOptions{})

	_, st, err := svc.List(context.Background(), q)
	if err != nil || st != application.CacheMiss {
		t.Fatalf("first: status=%s err=%v", st, err)
	}
	if c.Sets.Load() != 1 {
		t.Error("miss should populate the cache")
	}

	res, st, err := svc.List(context.Background(), q)
	if err != nil || st != application.CacheHit {
		t.Fatalf("second: status=%s err=%v", st, err)
	}
	if p.Calls.Load() != 1 {
		t.Errorf("provider called %d times, want 1", p.Calls.Load())
	}
	if res.Matches[1].HomeScore != nil {
		t.Error("null score must survive the cache round trip")
	}
}

func TestNilCacheBypasses(t *testing.T) {
	p := &testutil.Provider{Result: result()}
	svc := application.NewMatchesService(application.ProviderLoader{Provider: p}, application.ServiceOptions{})
	_, st, err := svc.List(context.Background(), q)
	if err != nil || st != application.CacheBypass {
		t.Fatalf("status=%s err=%v", st, err)
	}
}

func TestCacheReadErrorBypassesButServes(t *testing.T) {
	p, c := &testutil.Provider{Result: result()}, testutil.NewCache()
	c.GetErr = errors.New("redis down")
	svc := newSvc(p, c, application.ServiceOptions{})

	res, st, err := svc.List(context.Background(), q)
	if err != nil || st != application.CacheBypass || res.Count != 2 {
		t.Fatalf("status=%s err=%v", st, err)
	}
}

func TestCacheWriteErrorStillServes(t *testing.T) {
	p, c := &testutil.Provider{Result: result()}, testutil.NewCache()
	c.SetErr = errors.New("write failed")
	svc := newSvc(p, c, application.ServiceOptions{})

	_, st, err := svc.List(context.Background(), q)
	if err != nil || st != application.CacheMiss {
		t.Fatalf("status=%s err=%v", st, err)
	}
}

func TestProviderErrorPropagates(t *testing.T) {
	p, c := &testutil.Provider{Err: errors.New("boom")}, testutil.NewCache()
	svc := newSvc(p, c, application.ServiceOptions{})
	if _, _, err := svc.List(context.Background(), q); err == nil {
		t.Fatal("want error")
	}
	if c.Sets.Load() != 0 {
		t.Error("errors must not be cached")
	}
}

func TestStatusFilterAppliedAndKeyed(t *testing.T) {
	p, c := &testutil.Provider{Result: result()}, testutil.NewCache()
	svc := newSvc(p, c, application.ServiceOptions{})

	live := application.Query{Sport: "football", Limit: 5, Status: domain.StatusLive}
	res, _, err := svc.List(context.Background(), live)
	if err != nil || res.Count != 1 || res.Matches[0].ID != "m1" {
		t.Fatalf("filtered result = %+v err=%v", res, err)
	}
	// Different filter must not share the cache entry.
	if _, st, _ := svc.List(context.Background(), q); st != application.CacheMiss {
		t.Errorf("unfiltered query status = %s, want MISS", st)
	}
}

func TestConcurrentMissesAreCoalesced(t *testing.T) {
	p := &testutil.Provider{Result: result(), Block: make(chan struct{})}
	svc := newSvc(p, testutil.NewCache(), application.ServiceOptions{})

	const n = 20
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := svc.List(context.Background(), q)
			errs <- err
		}()
	}
	time.Sleep(100 * time.Millisecond) // let callers pile up on the blocked load
	close(p.Block)
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := p.Calls.Load(); got != 1 {
		t.Errorf("provider called %d times for %d concurrent misses, want 1", got, n)
	}
}

func TestLoadSheddingWhenAtConcurrencyLimit(t *testing.T) {
	p := &testutil.Provider{Result: result(), Block: make(chan struct{})}
	svc := newSvc(p, nil, application.ServiceOptions{MaxConcurrent: 1})

	done := make(chan struct{})
	go func() {
		_, _, _ = svc.List(context.Background(), application.Query{Sport: "football", Limit: 5})
		close(done)
	}()
	deadline := time.Now().Add(2 * time.Second)
	for p.Calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	// A different key cannot coalesce, so it must be shed.
	_, _, err := svc.List(context.Background(), application.Query{Sport: "football", Limit: 6})
	if !errors.Is(err, application.ErrOverloaded) {
		t.Fatalf("err = %v, want ErrOverloaded", err)
	}
	close(p.Block)
	<-done
}

func TestLoadTimeout(t *testing.T) {
	p := &testutil.Provider{Result: result(), Block: make(chan struct{})}
	defer close(p.Block)
	svc := newSvc(p, nil, application.ServiceOptions{LoadTimeout: 50 * time.Millisecond})

	_, _, err := svc.List(context.Background(), q)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want deadline exceeded", err)
	}
}

func TestCacheKeyAndPrefix(t *testing.T) {
	k := application.Query{Sport: "football", Limit: 5, Status: domain.StatusLive}.CacheKey()
	if want := application.SportCachePrefix("football"); k[:len(want)] != want {
		t.Errorf("key %q lacks prefix %q", k, want)
	}
}
