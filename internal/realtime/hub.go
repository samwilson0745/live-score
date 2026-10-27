// Package realtime fans stored-match updates out to subscribers in-process.
// It is not durable: clients that reconnect must fetch a fresh snapshot, and
// should use Match.Version to discard stale or out-of-order updates.
package realtime

import (
	"sync"
	"sync/atomic"

	"github.com/samwilson0745/live-score/internal/domain"
)

type Hub struct {
	mu   sync.Mutex
	subs map[string]map[*Subscription]struct{}
}

func NewHub() *Hub { return &Hub{subs: map[string]map[*Subscription]struct{}{}} }

type Subscription struct {
	C          chan domain.Match
	ids        []string
	hub        *Hub
	closed     bool // guarded by hub.mu
	overflowed atomic.Bool
}

// Overflowed reports that the subscriber fell behind and was disconnected.
func (s *Subscription) Overflowed() bool { return s.overflowed.Load() }

// Subscribe registers for updates to the given match IDs. buffer bounds how
// far a subscriber may fall behind before it is dropped.
func (h *Hub) Subscribe(ids []string, buffer int) *Subscription {
	if buffer < 1 {
		buffer = 16
	}
	s := &Subscription{C: make(chan domain.Match, buffer), ids: append([]string(nil), ids...), hub: h}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, id := range ids {
		if h.subs[id] == nil {
			h.subs[id] = map[*Subscription]struct{}{}
		}
		h.subs[id][s] = struct{}{}
	}
	return s
}

func (s *Subscription) Close() {
	s.hub.mu.Lock()
	defer s.hub.mu.Unlock()
	s.hub.removeLocked(s)
}

func (h *Hub) removeLocked(s *Subscription) {
	if s.closed {
		return
	}
	s.closed = true
	for _, id := range s.ids {
		delete(h.subs[id], s)
		if len(h.subs[id]) == 0 {
			delete(h.subs, id)
		}
	}
	close(s.C)
}

// Publish never blocks: a subscriber whose buffer is full is dropped.
func (h *Hub) Publish(matches []domain.Match) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, m := range matches {
		for s := range h.subs[m.ID] {
			select {
			case s.C <- m:
			default:
				s.overflowed.Store(true)
				h.removeLocked(s)
			}
		}
	}
}

func (h *Hub) Subscribers() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	seen := map[*Subscription]struct{}{}
	for _, set := range h.subs {
		for s := range set {
			seen[s] = struct{}{}
		}
	}
	return len(seen)
}
