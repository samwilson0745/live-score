package cache

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/samwilson0745/live-score/internal/testutil"
)

func TestGuardedOpensAndRecovers(t *testing.T) {
	inner := testutil.NewCache()
	inner.GetErr = errors.New("down")
	g := NewGuarded(inner, 3, 10*time.Second)
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	g.now = func() time.Time { return clock }
	ctx := context.Background()
	var v any

	for i := 0; i < 3; i++ {
		if _, err := g.Get(ctx, "k", &v); err == nil || errors.Is(err, ErrUnavailable) {
			t.Fatalf("call %d: want inner error, got %v", i, err)
		}
	}
	if !g.Open() {
		t.Fatal("circuit should be open after 3 failures")
	}
	calls := inner.Gets.Load()
	if _, err := g.Get(ctx, "k", &v); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("want ErrUnavailable, got %v", err)
	}
	if inner.Gets.Load() != calls {
		t.Error("open circuit must not touch the inner cache")
	}

	// After the cooldown a single trial goes through; success closes it.
	clock = clock.Add(11 * time.Second)
	inner.GetErr = nil
	if _, err := g.Get(ctx, "k", &v); err != nil {
		t.Fatalf("trial: %v", err)
	}
	if g.Open() {
		t.Error("circuit should be closed after a successful trial")
	}
}

func TestGuardedFailedTrialReopens(t *testing.T) {
	inner := testutil.NewCache()
	inner.SetErr = errors.New("down")
	g := NewGuarded(inner, 2, time.Second)
	clock := time.Unix(1000, 0)
	g.now = func() time.Time { return clock }

	_ = g.Set(context.Background(), "k", 1)
	_ = g.Set(context.Background(), "k", 1)
	clock = clock.Add(2 * time.Second)
	_ = g.Set(context.Background(), "k", 1) // failed trial
	if !g.Open() {
		t.Error("a failed trial should reopen the circuit immediately")
	}
}

func TestMissesAreNotFailures(t *testing.T) {
	g := NewGuarded(testutil.NewCache(), 1, time.Minute)
	var v any
	for i := 0; i < 5; i++ {
		if ok, err := g.Get(context.Background(), "absent", &v); ok || err != nil {
			t.Fatalf("ok=%v err=%v", ok, err)
		}
	}
	if g.Open() {
		t.Error("misses must not open the circuit")
	}
}
