package realtime

import (
	"testing"
	"time"

	"github.com/samwilson0745/live-score/internal/domain"
)

func m(id string, v int64) domain.Match { return domain.Match{ID: id, Version: v} }

func recv(t *testing.T, s *Subscription) domain.Match {
	t.Helper()
	select {
	case got, ok := <-s.C:
		if !ok {
			t.Fatal("channel closed")
		}
		return got
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
	return domain.Match{}
}

func TestPublishOnlyReachesSubscribers(t *testing.T) {
	h := NewHub()
	a, b := h.Subscribe([]string{"x"}, 4), h.Subscribe([]string{"y", "x"}, 4)
	h.Publish([]domain.Match{m("x", 2), m("z", 1)})

	if recv(t, a).Version != 2 || recv(t, b).Version != 2 {
		t.Error("both x subscribers should receive")
	}
	if len(a.C) != 0 || len(b.C) != 0 {
		t.Error("z has no subscribers and must be ignored")
	}
}

func TestCloseUnsubscribes(t *testing.T) {
	h := NewHub()
	s := h.Subscribe([]string{"x"}, 1)
	s.Close()
	s.Close() // idempotent
	h.Publish([]domain.Match{m("x", 1)})
	if _, ok := <-s.C; ok {
		t.Error("channel should be closed")
	}
	if h.Subscribers() != 0 {
		t.Error("subscriber leaked")
	}
}

func TestSlowSubscriberIsDropped(t *testing.T) {
	h := NewHub()
	slow, fast := h.Subscribe([]string{"x"}, 1), h.Subscribe([]string{"x"}, 10)
	for i := int64(1); i <= 3; i++ {
		h.Publish([]domain.Match{m("x", i)}) // never blocks
	}
	if !slow.Overflowed() {
		t.Error("slow subscriber should be flagged")
	}
	if recv(t, fast).Version != 1 {
		t.Error("fast subscriber unaffected")
	}
	if h.Subscribers() != 1 {
		t.Errorf("subscribers = %d, want 1", h.Subscribers())
	}
}
