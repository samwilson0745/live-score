package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/coder/websocket"

	"github.com/samwilson0745/live-score/internal/domain"
	"github.com/samwilson0745/live-score/internal/metrics"
	"github.com/samwilson0745/live-score/internal/ports"
	"github.com/samwilson0745/live-score/internal/realtime"
)

const maxWSMatches = 50

type WSMessage struct {
	Type  string       `json:"type"` // "snapshot" or "update"
	Match domain.Match `json:"match"`
}

// WSHandler streams updates for the match IDs given as repeated ?match_id=
// parameters. It sends a snapshot per match first (so a reconnecting client
// recovers missed updates), then live updates. Compare Match.Version to drop
// stale or out-of-order messages.
type WSHandler struct {
	Hub            *realtime.Hub
	Repo           ports.MatchRepository
	OriginPatterns []string
	Metrics        *metrics.Metrics
	Log            *slog.Logger
}

func (h *WSHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ids := r.URL.Query()["match_id"]
	if len(ids) == 0 || len(ids) > maxWSMatches {
		writeError(w, http.StatusBadRequest, "provide between 1 and 50 match_id parameters")
		return
	}
	for _, id := range ids {
		if id == "" || len(id) > 64 {
			writeError(w, http.StatusBadRequest, "invalid match_id")
			return
		}
	}

	// Server-wide read/write timeouts must not kill a long-lived stream.
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Time{})
	_ = rc.SetWriteDeadline(time.Time{})

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: h.OriginPatterns})
	if err != nil {
		return // Accept already replied
	}
	defer conn.CloseNow()
	h.Metrics.WSConnections(1)
	defer h.Metrics.WSConnections(-1)

	// Subscribe before reading snapshots so no update falls in the gap.
	sub := h.Hub.Subscribe(ids, 32)
	defer sub.Close()

	ctx := conn.CloseRead(r.Context())

	for _, id := range ids {
		m, err := h.Repo.GetMatch(ctx, id)
		if err != nil {
			continue // unknown ids simply produce no snapshot
		}
		if !h.send(ctx, conn, WSMessage{Type: "snapshot", Match: *m}) {
			return
		}
	}

	ping := time.NewTicker(30 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case m, ok := <-sub.C:
			if !ok {
				if sub.Overflowed() {
					conn.Close(websocket.StatusPolicyViolation, "too slow; reconnect for a fresh snapshot")
				}
				return
			}
			if !h.send(ctx, conn, WSMessage{Type: "update", Match: m}) {
				return
			}
		case <-ping.C:
			pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := conn.Ping(pctx)
			cancel()
			if err != nil {
				return
			}
		}
	}
}

func (h *WSHandler) send(ctx context.Context, conn *websocket.Conn, msg WSMessage) bool {
	data, err := json.Marshal(msg)
	if err != nil {
		return false
	}
	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return conn.Write(wctx, websocket.MessageText, data) == nil
}
