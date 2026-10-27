// Package http wires routes and middleware.
package http

import (
	"context"
	"log/slog"
	nethttp "net/http"
	"time"

	"github.com/samwilson0745/live-score/internal/metrics"
	"github.com/samwilson0745/live-score/internal/transport/http/handler"
	"github.com/samwilson0745/live-score/internal/transport/http/middleware"
)

type Deps struct {
	Matches *handler.MatchesHandler
	Stored  *handler.StoredHandler // optional: needs a repository
	WS      nethttp.Handler        // optional
	Metrics *metrics.Metrics
	Logger  *slog.Logger
	// Ready reports whether dependencies the service needs are reachable.
	Ready func(ctx context.Context) error
}

func NewRouter(d Deps) nethttp.Handler {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	mux := nethttp.NewServeMux()

	mux.HandleFunc("GET /health", func(w nethttp.ResponseWriter, r *nethttp.Request) {
		writeJSON(w, nethttp.StatusOK, map[string]string{"status": "ok", "service": "live-score"})
	})
	mux.HandleFunc("GET /ready", func(w nethttp.ResponseWriter, r *nethttp.Request) {
		if d.Ready != nil {
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			if err := d.Ready(ctx); err != nil {
				writeJSON(w, nethttp.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
				return
			}
		}
		writeJSON(w, nethttp.StatusOK, map[string]string{"status": "ready"})
	})
	if d.Metrics != nil {
		mux.Handle("GET /metrics", d.Metrics.Handler())
	}

	mux.Handle("GET /api/v1/matches", d.Matches)
	if d.Stored != nil {
		mux.HandleFunc("GET /api/v1/matches/{id}", d.Stored.GetMatch)
		mux.HandleFunc("GET /api/v1/competitions/{id}/matches", d.Stored.CompetitionMatches)
	}
	if d.WS != nil {
		mux.Handle("GET /api/v1/ws", d.WS)
	}

	var h nethttp.Handler = mux
	h = middleware.Recover(d.Logger)(h)
	h = middleware.Metrics(d.Metrics)(h)
	h = middleware.RequestLogger(d.Logger)(h)
	h = middleware.RequestID(h)
	return h
}
