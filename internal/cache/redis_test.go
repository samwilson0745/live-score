package cache

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

type payload struct {
	N int  `json:"n"`
	P *int `json:"p"`
}

func TestRedisRoundTripAndJitter(t *testing.T) {
	mr := miniredis.RunT(t)
	c := NewRedisCache(mr.Addr(), 100*time.Second)
	defer c.Close()
	ctx := context.Background()

	var got payload
	if ok, err := c.Get(ctx, "missing", &got); ok || err != nil {
		t.Fatalf("miss: ok=%v err=%v", ok, err)
	}
	if err := c.Set(ctx, "k", payload{N: 7}); err != nil {
		t.Fatal(err)
	}
	if ok, err := c.Get(ctx, "k", &got); !ok || err != nil || got.N != 7 || got.P != nil {
		t.Fatalf("hit: ok=%v err=%v got=%+v", ok, err, got)
	}
	if ttl := mr.TTL("k"); ttl < 90*time.Second || ttl > 110*time.Second {
		t.Errorf("ttl %v outside 100s +/-10%%", ttl)
	}

	seen := map[time.Duration]bool{}
	for i := 0; i < 20; i++ {
		seen[c.jitteredTTL()] = true
	}
	if len(seen) < 2 {
		t.Error("TTL should be jittered")
	}
}

func TestRedisDeletePrefix(t *testing.T) {
	mr := miniredis.RunT(t)
	c := NewRedisCache(mr.Addr(), time.Minute)
	defer c.Close()
	ctx := context.Background()
	for _, k := range []string{"a:1", "a:2", "b:1"} {
		_ = c.Set(ctx, k, payload{})
	}
	if err := c.DeletePrefix(ctx, "a:"); err != nil {
		t.Fatal(err)
	}
	if mr.Exists("a:1") || mr.Exists("a:2") || !mr.Exists("b:1") {
		t.Error("only keys with the prefix should be removed")
	}
}

func TestRedisUnavailable(t *testing.T) {
	mr := miniredis.RunT(t)
	c := NewRedisCache(mr.Addr(), time.Minute)
	defer c.Close()
	mr.Close()

	var p payload
	if _, err := c.Get(context.Background(), "k", &p); err == nil {
		t.Error("want error when redis is down")
	}
	if err := c.Ping(context.Background()); err == nil {
		t.Error("ping should fail")
	}
}
