package handler

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/samwilson0745/live-score/internal/application"
	"github.com/samwilson0745/live-score/internal/domain"
	"github.com/samwilson0745/live-score/internal/ports"
)

const (
	DefaultSport = "football"
	DefaultLimit = 5
	MaxLimit     = 100
)

type MatchesHandler struct {
	service *application.MatchesService
	sports  map[domain.Sport]struct{}
	log     *slog.Logger
}

func NewMatchesHandler(svc *application.MatchesService, supported []domain.Sport, log *slog.Logger) *MatchesHandler {
	set := make(map[domain.Sport]struct{}, len(supported))
	for _, s := range supported {
		set[s] = struct{}{}
	}
	if log == nil {
		log = slog.Default()
	}
	return &MatchesHandler{service: svc, sports: set, log: log}
}

func (h *MatchesHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	q, msg := h.parse(r)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}

	res, cacheStatus, err := h.service.List(r.Context(), q)
	if err != nil {
		h.fail(w, q, err)
		return
	}
	w.Header().Set("X-Cache", string(cacheStatus))
	writeJSON(w, http.StatusOK, res)
}

func (h *MatchesHandler) parse(r *http.Request) (application.Query, string) {
	vals := r.URL.Query()

	sport := domain.Sport(strings.ToLower(vals.Get("sport")))
	if sport == "" {
		sport = DefaultSport
	}
	if _, ok := h.sports[sport]; !ok {
		return application.Query{}, "unsupported sport"
	}

	limit := DefaultLimit
	if v := vals.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > MaxLimit {
			return application.Query{}, "limit must be between 1 and " + strconv.Itoa(MaxLimit)
		}
		limit = n
	}

	var status domain.MatchStatus
	if v := vals.Get("status"); v != "" {
		st, ok := domain.ParseMatchStatus(v)
		if !ok {
			return application.Query{}, "unknown status"
		}
		status = st
	}
	return application.Query{Sport: sport, Limit: limit, Status: status}, ""
}

func (h *MatchesHandler) fail(w http.ResponseWriter, q application.Query, err error) {
	h.log.Error("list matches failed", "sport", q.Sport, "error", err)

	var pe *ports.ProviderError
	switch {
	case errors.Is(err, application.ErrOverloaded):
		w.Header().Set("Retry-After", "1")
		writeError(w, http.StatusServiceUnavailable, "server busy, retry shortly")
	case errors.Is(err, context.DeadlineExceeded):
		writeError(w, http.StatusGatewayTimeout, "timed out loading matches")
	case errors.As(err, &pe):
		writeError(w, http.StatusBadGateway, "sports data provider unavailable")
	default:
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}
